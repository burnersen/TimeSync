//go:build windows

package diag

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/history"
	"github.com/burnersen/TimeSync/internal/ipc"
	"github.com/burnersen/TimeSync/internal/paths"
	"github.com/burnersen/TimeSync/internal/syncer"
	"github.com/burnersen/TimeSync/internal/version"
	"github.com/burnersen/TimeSync/internal/winsys"
)

// Report collects everything useful for a support request. It contains no
// user names; the domain name is not included either.
func Report(ctx context.Context) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	section := func(title string) { p("\n== %s ==", title) }
	layout := paths.Default()
	now := time.Now()

	p("%s diagnostics", version.Product)
	p("Created: %s (UTC %s)", now.Format("2006-01-02 15:04:05 -07:00"), now.UTC().Format(time.RFC3339))

	section("Program")
	exe, _ := os.Executable()
	p("Version:       %s (%s, %s/%s)", version.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	p("Path:          %s", winsys.RedactUser(exe))
	p("Administrator: %t", winsys.IsElevated())

	section("System")
	p("Windows:       %s", winsys.OSVersion())
	p("Language:      %s", winsys.UILanguage())
	p("Up since:      %s (%s)", winsys.BootTime().Format("2006-01-02 15:04"), winsys.Uptime().Round(time.Minute))
	joined, _ := winsys.Domain()
	p("Domain member: %t", joined)

	section("Time zone")
	tz := winsys.CurrentTimeZone()
	p("Zone:          %s (%s)", tz.KeyName, tz.StandardName)
	p("UTC offset:    %+d min, daylight saving now: %t", tz.UTCOffsetMinutes, tz.InDST)
	p("Auto DST off:  %t", tz.AutoDSTDisabled)
	p("Auto zone:     %t", tz.AutoTimeZone)

	section("Windows Time service (w32time)")
	w := winsys.ReadW32Time(layout.W32TimeBackup())
	p("State:         %s", w.Summary())
	p("Taken over:    %t (backup present: %t)", w.TakenOver, w.BackupPresent)

	section("TimeSync service")
	for _, name := range []string{"TimeSync", "ntp-sync"} {
		s := winsys.QueryService(name)
		if !s.Installed {
			p("%-13s  not installed", name+":")
			continue
		}
		p("%-13s  %s, start %s, account %s", name+":", s.State, s.StartType, s.Account)
		p("               %s", winsys.RedactUser(s.BinPath))
	}
	if resp, err := ipc.Call(ipc.CmdStatus, 5*time.Second); err == nil && resp.Status != nil {
		st := resp.Status
		p("Service ver.:  %s, since %s, interval %d min, next %s", st.Version, st.StartedAt.Format("2006-01-02 15:04"), st.IntervalMinutes, st.NextSync.Format("15:04:05"))
		p("Monitor only:  %t", st.MonitorOnly)
		if st.ConfigError != "" {
			p("Config error:  %s", st.ConfigError)
		}
	} else {
		p("Status pipe:   %v", err)
	}

	section("Settings")
	cfg, cerr := config.Load(layout.Config())
	if cerr != nil {
		p("Problem: %v", cerr)
	}
	if _, err := os.Stat(layout.Config()); err != nil {
		p("(no settings file, defaults)")
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	p("%s", data)

	section("Statistics")
	entries, _ := history.Read(layout.History(), 500)
	st := history.Analyze(entries, now)
	if st.DriftSamples >= 3 {
		p("Drift:         %+.2f s per day (from %d measurements)", st.LagSecondsPerDay, st.DriftSamples)
	} else {
		p("Drift:         not enough data yet")
	}
	p("After boot:    %s (CMOS suspect: %t)", FormatOffset(time.Duration(st.LastBootOffsetMs*float64(time.Millisecond))), st.CMOSSuspect)
	p("Last 7 days:   %d ok, %d failed, %d failures in a row now", st.Successes7d, st.Failures7d, st.ConsecutiveFailures)

	section("Last 20 synchronizations")
	if len(entries) == 0 {
		p("(none)")
	}
	for _, e := range entries[max(0, len(entries)-20):] {
		p("%s", FormatEntry(e))
	}

	section("Check right now")
	_, off := now.Zone()
	res := syncer.Run(ctx, syncer.Request{Config: cfg, Trigger: history.TriggerManual, UTCOffset: time.Duration(off) * time.Second}, nil, syncer.DefaultQueries)
	for _, s := range res.Samples {
		p("%s", FormatSample(s))
	}
	v := res.Verdict
	p("Verdict:       %s (%s), offset %s, %d of %d agree, hints %v", v.Action, v.Reason, FormatOffset(v.Offset), v.Agreeing, v.Asked, v.Hints)

	section("Log (last 40 lines)")
	for _, line := range tail(layout.Log(), 40) {
		p("%s", winsys.RedactUser(line))
	}
	return b.String()
}

func tail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{"(no log: " + err.Error() + ")"}
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}
