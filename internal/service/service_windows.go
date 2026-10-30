//go:build windows

// Package service is the Windows service: it keeps the clock in sync in
// the background, with or without anyone signed in.
package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/history"
	"github.com/burnersen/TimeSync/internal/ipc"
	"github.com/burnersen/TimeSync/internal/paths"
	"github.com/burnersen/TimeSync/internal/syncer"
	"github.com/burnersen/TimeSync/internal/version"
	"github.com/burnersen/TimeSync/internal/winsys"
)

const (
	// Name is the service name; LegacyName is the one version 1.x used.
	Name        = "TimeSync"
	LegacyName  = "ntp-sync"
	DisplayName = "TimeSync"
	Description = "Keeps the Windows clock accurate using several NTP servers. https://github.com/burnersen/TimeSync"

	// After waking from sleep the network needs a moment.
	resumeDelay = 10 * time.Second
	// Network changes come in bursts (address, then DNS, then VPN...).
	networkDebounce = 15 * time.Second
	// Don't resync on a network change right after a successful sync.
	networkMinGap = 2 * time.Minute
	// How often the settings file is checked for changes.
	configPoll = 30 * time.Second
	// A manual sync from the window or the command line is rate limited
	// so nobody can turn the service into a load generator for the pool.
	manualMinGap = 10 * time.Second
	// Retry delays after failures double from retryBase up to retryMax.
	// Most failures mean "no network yet", which costs the servers nothing,
	// but the delays still grow quickly so a real outage is not hammered.
	retryBase = 30 * time.Second
	retryMax  = 20 * time.Minute
	// After this many failures in a row the event log gets a warning.
	failureWarnAfter = 3
	// Kiss-of-death penalties.
	kodRateSkip = time.Hour
	kodDenySkip = 24 * time.Hour
)

// PBT_* power events (winuser.h).
const (
	pbtResumeSuspend   = 0x7
	pbtResumeAutomatic = 0x12
)

type job struct {
	trigger string
	reply   chan syncer.Result
}

type daemon struct {
	layout paths.Layout
	logger *log.Logger
	store  *history.Store
	events *winsys.EventLog
	clock  winsys.Clock
	jobs   chan job

	mu            sync.Mutex
	cfg           config.Config
	cfgErr        string
	cfgMod        time.Time
	startedAt     time.Time
	nextSync      time.Time
	syncing       bool
	slewUntil     time.Time
	slewTimer     *time.Timer
	monitorOnly   bool
	domain        string
	last          *history.Entry
	lastSuccess   time.Time
	lastManual    time.Time
	failures      int
	bootMeasured  bool
	kod           map[string]time.Time
	legacyMinutes int
}

// Run is called when Windows starts the service. legacyInterval is set when
// the service was installed by version 1.x ("-service -interval N").
func Run(legacyInterval int) error {
	d := newDaemon(legacyInterval)
	return svc.Run(Name, d)
}

func newDaemon(legacyInterval int) *daemon {
	layout := paths.Default()
	os.MkdirAll(layout.StateDir(), 0o755)
	logger := log.New(&history.LogFile{Path: layout.Log(), MaxSize: 1 << 20}, "", log.LstdFlags)
	return &daemon{
		layout:        layout,
		logger:        logger,
		store:         history.NewStore(layout.History()),
		jobs:          make(chan job, 8),
		kod:           map[string]time.Time{},
		legacyMinutes: legacyInterval,
	}
}

// Execute is the service main loop (svc.Handler).
func (d *daemon) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	d.startedAt = time.Now()
	d.loadConfig(true)
	d.events = nil
	if d.config().EventLog {
		d.events = winsys.OpenEventLog(Name)
	}
	defer d.events.Close()

	if joined, name := winsys.Domain(); joined {
		d.domain = name
	}
	d.updateMonitorOnly()

	if d.slewPossible() {
		// A crash during a gentle correction would leave the clock running
		// fast or slow; start from normal speed.
		winsys.StopSlew()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := ipc.Serve(ctx, d.handleIPC); err != nil {
			d.logger.Printf("[ERROR] Status channel unavailable: %v", err)
		}
	}()
	netChanged := make(chan struct{}, 1)
	if d.config().SyncOnNetworkChange {
		winsys.WatchNetwork(netChanged)
	}

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent}
	cfg := d.config()
	d.logger.Printf("[INFO] %s %s started. Interval %d min, servers %v", version.Product, version.Version, cfg.IntervalMinutes, cfg.Servers)
	if d.legacyMinutes > 0 {
		d.logger.Printf("[INFO] Running with the settings of version 1.x. Run \"ntp-sync service install\" as administrator to upgrade.")
	}
	d.events.Info(winsys.EventStarted, fmt.Sprintf("%s %s started.", version.Product, version.Version))
	if d.monitorOnly {
		msg := fmt.Sprintf("This computer belongs to the domain %q. TimeSync only measures and does not set the clock (setting \"allowOnDomain\").", d.domain)
		d.logger.Printf("[INFO] %s", msg)
		d.events.Info(winsys.EventMonitoring, msg)
	}

	go d.scheduler(ctx)

	configTick := time.NewTicker(configPoll)
	defer configTick.Stop()
	var netTimer <-chan time.Time
	lastResume := time.Time{}

	for {
		select {
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				if d.slewPossible() {
					winsys.StopSlew()
				}
				d.logger.Println("[INFO] Service stopping.")
				d.events.Info(winsys.EventStopped, version.Product+" stopped.")
				return false, 0
			case svc.PowerEvent:
				if (r.EventType == pbtResumeAutomatic || r.EventType == pbtResumeSuspend) &&
					d.config().SyncOnResume && time.Since(lastResume) > time.Minute {
					lastResume = time.Now()
					time.AfterFunc(resumeDelay, func() { d.enqueue(history.TriggerResume) })
				}
				status <- r.CurrentStatus
			default:
				status <- r.CurrentStatus
			}
		case <-netChanged:
			netTimer = time.After(networkDebounce)
		case <-netTimer:
			netTimer = nil
			d.mu.Lock()
			recent := time.Since(d.lastSuccess) < networkMinGap
			d.mu.Unlock()
			if !recent {
				d.enqueue(history.TriggerNetwork)
			}
		case <-configTick.C:
			d.loadConfig(false)
		}
	}
}

// enqueue asks for a sync without waiting for it. Requests are dropped when
// the queue is full; one sync satisfies them all anyway.
func (d *daemon) enqueue(trigger string) {
	select {
	case d.jobs <- job{trigger: trigger}:
	default:
	}
}

// scheduler runs synchronizations one at a time: on its own timer and on
// request.
func (d *daemon) scheduler(ctx context.Context) {
	cfg := d.config()
	first := history.TriggerStart
	delay := time.Duration(0)
	if !cfg.SyncOnStart {
		first = history.TriggerInterval
		delay = d.interval()
	}
	timer := time.NewTimer(delay)
	d.setNext(delay)
	trigger := first
	for {
		var j job
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			j = job{trigger: trigger}
		case j = <-d.jobs:
			timer.Stop()
		}
		res := d.runOnce(ctx, j.trigger)
		if j.reply != nil {
			j.reply <- res
		}
		next, nextTrigger := d.nextDelay(res)
		trigger = nextTrigger
		timer.Reset(next)
		d.setNext(next)
	}
}

func (d *daemon) nextDelay(res syncer.Result) (time.Duration, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	interval := d.intervalLocked()
	if succeeded(res) {
		return interval, history.TriggerInterval
	}
	shift := min(d.failures-1, 10)
	delay := min(retryBase<<shift, retryMax, interval)
	return delay, history.TriggerRetry
}

func succeeded(res syncer.Result) bool {
	switch res.Applied {
	case "none", "slew", "step", history.ActionMonitor:
		return true
	}
	return false
}

func (d *daemon) runOnce(ctx context.Context, trigger string) syncer.Result {
	d.mu.Lock()
	d.syncing = true
	cfg := d.cfg
	monitor := d.monitorOnly
	firstAfterBoot := !d.bootMeasured && winsys.Uptime() < 30*time.Minute
	skip := map[string]bool{}
	for s, until := range d.kod {
		if time.Now().Before(until) {
			skip[s] = true
		} else {
			delete(d.kod, s)
		}
	}
	d.mu.Unlock()

	d.endSlew()
	_, utcOffset := time.Now().Zone()
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	req := syncer.Request{
		Config:          cfg,
		Trigger:         trigger,
		Apply:           true,
		MonitorOnly:     monitor,
		SlewAllowed:     d.slewPossible(),
		MaxSlewDuration: min(interval*4/5, 30*time.Minute),
		Skip:            skip,
		BootTime:        winsys.BootTime(),
		FirstAfterBoot:  firstAfterBoot,
		UTCOffset:       time.Duration(utcOffset) * time.Second,
	}
	res := syncer.Run(ctx, req, d.clock, syncer.DefaultQueries)
	d.record(res, cfg)
	return res
}

// record updates the state, the history, the log and the event log.
func (d *daemon) record(res syncer.Result, cfg config.Config) {
	e := res.Entry
	_ = d.store.Append(e)

	d.mu.Lock()
	d.syncing = false
	d.last = &e
	for server, code := range res.KissCodes {
		penalty := kodRateSkip
		if code == "DENY" || code == "RSTR" {
			penalty = kodDenySkip
		}
		d.kod[server] = time.Now().Add(penalty)
	}
	ok := succeeded(res)
	wasFailing := d.failures >= failureWarnAfter
	if ok {
		d.failures = 0
		d.lastSuccess = time.Now()
		if res.Verdict.Trusted() {
			d.bootMeasured = true
		}
	} else {
		d.failures++
	}
	failures := d.failures
	if res.Slew != nil {
		d.slewUntil = time.Now().Add(res.Slew.Duration)
		d.slewTimer = time.AfterFunc(res.Slew.Duration, d.endSlew)
	}
	d.mu.Unlock()

	off := res.Verdict.Offset.Round(time.Millisecond)
	src := fmt.Sprintf("%d/%d servers agree", res.Verdict.Agreeing, res.Verdict.Asked)
	if res.Verdict.LowPrecision {
		src += ", web servers only (NTP blocked?)"
	}
	switch res.Applied {
	case "none":
		d.logger.Printf("[NTP] %s: clock is %v off, no correction needed (%s)", e.Trigger, off, src)
	case "slew":
		d.logger.Printf("[NTP] %s: clock is %v off, correcting gently over %v (%s)", e.Trigger, off, res.Slew.Duration.Round(time.Second), src)
	case "step":
		d.logger.Printf("[NTP] %s: clock was %v off and has been set (%s)", e.Trigger, off, src)
		if engineAbs(res.Verdict.Offset) > time.Duration(cfg.BigJumpMinutes)*time.Minute {
			d.events.Warning(winsys.EventBigJump, fmt.Sprintf("The clock was %v off and has been set (%s). Hints: %v", off, src, e.Hints))
		} else if engineAbs(res.Verdict.Offset) > time.Second {
			d.events.Info(winsys.EventCorrected, fmt.Sprintf("The clock was %v off and has been set (%s).", off, src))
		}
	case history.ActionMonitor:
		d.logger.Printf("[NTP] %s: clock is %v off (measured only, domain computer) (%s)", e.Trigger, off, src)
	case history.ActionFailed:
		d.logger.Printf("[ERROR] %s: clock is %v off but could not be set: %s", e.Trigger, off, e.Error)
	default:
		d.logger.Printf("[ERROR] %s: no trustworthy time (%s): %s", e.Trigger, e.Reason, e.Error)
	}
	for _, h := range e.Hints {
		d.logger.Printf("[HINT] %s", h)
	}
	if !ok && failures == failureWarnAfter {
		d.events.Warning(winsys.EventFailing, fmt.Sprintf("The clock could not be synchronized %d times in a row. Last error: %s (%s)", failures, e.Error, e.Reason))
	}
	if ok && wasFailing {
		d.events.Info(winsys.EventRecovered, "Synchronization works again.")
	}
}

func engineAbs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// endSlew returns the clock to normal speed if a gentle correction is
// running.
func (d *daemon) endSlew() {
	d.mu.Lock()
	active := !d.slewUntil.IsZero()
	d.slewUntil = time.Time{}
	if d.slewTimer != nil {
		d.slewTimer.Stop()
		d.slewTimer = nil
	}
	d.mu.Unlock()
	if active {
		if err := winsys.StopSlew(); err != nil {
			d.logger.Printf("[ERROR] Could not end the gentle correction: %v", err)
		}
	}
}

// slewPossible: gentle correction only works if nobody else adjusts the
// clock speed, i.e. the Windows Time service has been taken over.
func (d *daemon) slewPossible() bool {
	cfg := d.config()
	if cfg.SlewMaxMs <= 0 || cfg.W32TimeMode != config.W32TimeTakeover {
		return false
	}
	st := winsys.ReadW32Time(d.layout.W32TimeBackup())
	return !st.Installed || st.Type == "NoSync" || !st.Running
}

func (d *daemon) handleIPC(ctx context.Context, req ipc.Request) ipc.Response {
	switch req.Cmd {
	case ipc.CmdStatus:
		st := d.statusSnapshot()
		return ipc.Response{OK: true, Status: &st}
	case ipc.CmdReload:
		d.loadConfig(false)
		st := d.statusSnapshot()
		return ipc.Response{OK: true, Status: &st}
	case ipc.CmdSync:
		d.mu.Lock()
		if time.Since(d.lastManual) < manualMinGap {
			d.mu.Unlock()
			return ipc.Response{Error: "please wait a few seconds between manual synchronizations"}
		}
		d.lastManual = time.Now()
		d.mu.Unlock()
		reply := make(chan syncer.Result, 1)
		select {
		case d.jobs <- job{trigger: history.TriggerManual, reply: reply}:
		case <-time.After(5 * time.Second):
			return ipc.Response{Error: "the service is busy, please try again"}
		}
		select {
		case res := <-reply:
			st := d.statusSnapshot()
			return ipc.Response{OK: true, Result: &res, Status: &st}
		case <-time.After(ipc.SyncTimeout):
			return ipc.Response{Error: "the synchronization takes unusually long"}
		case <-ctx.Done():
			return ipc.Response{Error: "the service is stopping"}
		}
	}
	return ipc.Response{Error: "unknown command"}
}

func (d *daemon) statusSnapshot() ipc.Status {
	entries, _ := history.Read(d.layout.History(), 500)
	stats := history.Analyze(entries, time.Now())
	w32 := winsys.ReadW32Time(d.layout.W32TimeBackup())
	d.mu.Lock()
	defer d.mu.Unlock()
	return ipc.Status{
		Version:         version.Version,
		StartedAt:       d.startedAt,
		IntervalMinutes: int(d.intervalLocked() / time.Minute),
		NextSync:        d.nextSync,
		Syncing:         d.syncing,
		SlewUntil:       d.slewUntil,
		MonitorOnly:     d.monitorOnly,
		Domain:          d.domain,
		W32Time:         w32.Summary(),
		ConfigError:     d.cfgErr,
		Last:            d.last,
		Stats:           stats,
	}
}

func (d *daemon) setNext(delay time.Duration) {
	d.mu.Lock()
	d.nextSync = time.Now().Add(delay)
	d.mu.Unlock()
}

func (d *daemon) config() config.Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg
}

func (d *daemon) interval() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.intervalLocked()
}

func (d *daemon) intervalLocked() time.Duration {
	return time.Duration(d.cfg.IntervalMinutes) * time.Minute
}

func (d *daemon) updateMonitorOnly() {
	d.mu.Lock()
	d.monitorOnly = d.domain != "" && !d.cfg.AllowOnDomain
	d.mu.Unlock()
}

// loadConfig reads the settings file if it changed. A broken file keeps
// the previous settings running and is reported.
func (d *daemon) loadConfig(initial bool) {
	path := d.layout.Config()
	info, statErr := os.Stat(path)
	d.mu.Lock()
	unchanged := !initial && statErr == nil && info.ModTime().Equal(d.cfgMod)
	d.mu.Unlock()
	if unchanged {
		return
	}
	cfg, err := config.Load(path)
	if d.legacyMinutes > 0 && os.IsNotExist(statErr) {
		cfg.IntervalMinutes = max(min(d.legacyMinutes, config.MaxIntervalMinutes), config.MinIntervalMinutes)
	}
	d.mu.Lock()
	if statErr == nil {
		d.cfgMod = info.ModTime()
	}
	if err != nil {
		d.cfgErr = err.Error()
		if initial {
			d.cfg = cfg // defaults
		}
		d.mu.Unlock()
		d.logger.Printf("[ERROR] Settings: %v", err)
		d.events.Warning(winsys.EventConfigBad, "Settings could not be read: "+err.Error())
		return
	}
	// A new interval or new servers take effect with an immediate sync,
	// which also shows right away whether the new servers answer.
	changed := !initial && (d.cfg.IntervalMinutes != cfg.IntervalMinutes || fmt.Sprint(d.cfg.Servers) != fmt.Sprint(cfg.Servers))
	d.cfg = cfg
	d.cfgErr = ""
	d.mu.Unlock()
	d.updateMonitorOnly()
	if !initial {
		d.logger.Printf("[INFO] Settings reloaded. Interval %d min, servers %v", cfg.IntervalMinutes, cfg.Servers)
		if changed {
			d.enqueue(history.TriggerInterval)
		}
	}
}
