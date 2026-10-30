//go:build windows

// ntp-sync is the command line tool and the Windows service of TimeSync.
//
// Run "ntp-sync help" for the commands. Every command line of version 1.x
// still works.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/burnersen/TimeSync/internal/cli"
	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/diag"
	"github.com/burnersen/TimeSync/internal/engine"
	"github.com/burnersen/TimeSync/internal/history"
	"github.com/burnersen/TimeSync/internal/ipc"
	"github.com/burnersen/TimeSync/internal/paths"
	"github.com/burnersen/TimeSync/internal/service"
	"github.com/burnersen/TimeSync/internal/syncer"
	"github.com/burnersen/TimeSync/internal/version"
	"github.com/burnersen/TimeSync/internal/winsys"
)

// Exit codes, so scripts can react.
const (
	exitOK        = 0
	exitError     = 1
	exitUsage     = 2
	exitUntrusted = 3 // no trustworthy time available
	exitNoAdmin   = 4
)

var layout = paths.Default()

func main() {
	cmd, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n\n", strings.TrimPrefix(err.Error(), "usage: "))
		printUsage()
		os.Exit(exitUsage)
	}
	if cmd.Name == cli.CmdServiceRun {
		if err := service.Run(cmd.Interval); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(exitError)
		}
		return
	}

	// Started by double-click (no arguments, a console window of its own):
	// the window would vanish the moment we are done, taking every message
	// with it. The wait ends by itself, so a scheduled task that runs the
	// program the same way never hangs.
	pause := cmd.Banner && winsys.LaunchedFromExplorer()
	code := run(cmd)
	if pause {
		fmt.Print("\nPress Enter to close this window (closes by itself in one minute)...")
		done := make(chan struct{})
		go func() {
			bufio.NewReader(os.Stdin).ReadString('\n')
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Minute):
		}
	}
	os.Exit(code)
}

func run(cmd cli.Command) int {
	switch cmd.Name {
	case cli.CmdVersion:
		fmt.Printf("ntp-sync %s (%s)\n", version.Version, version.Product)
		return exitOK
	case cli.CmdHelp:
		printUsage()
		return exitOK
	case cli.CmdCheck:
		return doCheck(cmd)
	case cli.CmdSync:
		if cmd.Banner {
			printBanner()
		}
		return doSync(cmd)
	case cli.CmdRun:
		return doLoop(cmd)
	case cli.CmdStatus:
		return doStatus(cmd)
	case cli.CmdHistory:
		return doHistory(cmd)
	case cli.CmdServiceInstall:
		return report(cmd, needAdmin(func() error {
			if cmd.Interval > 0 && cmd.Interval < config.MinIntervalMinutes {
				fmt.Printf("[INFO] The service syncs at most every %d minutes; using %d.\n", config.MinIntervalMinutes, config.MinIntervalMinutes)
			}
			fmt.Println("[...] Installing the TimeSync service...")
			return service.Install(service.InstallOptions{IntervalMinutes: cmd.Interval, Out: os.Stdout})
		}))
	case cli.CmdServiceUninstall:
		return report(cmd, needAdmin(func() error {
			fmt.Println("[...] Removing the TimeSync service...")
			return service.Uninstall(service.UninstallOptions{Purge: cmd.Purge, KeepBinary: cmd.KeepBinary, Out: os.Stdout})
		}))
	case cli.CmdServiceStart:
		return report(cmd, needAdmin(service.Start))
	case cli.CmdServiceStop:
		return report(cmd, needAdmin(service.Stop))
	case cli.CmdServiceRestart:
		return report(cmd, needAdmin(func() error {
			if err := service.Stop(); err != nil {
				return err
			}
			return service.Start()
		}))
	case cli.CmdConfigShow:
		return doConfigShow(cmd)
	case cli.CmdConfigPath:
		fmt.Println(layout.Config())
		return exitOK
	case cli.CmdConfigSet, cli.CmdConfigReset, cli.CmdConfigImport:
		return report(cmd, needAdmin(func() error { return doConfigChange(cmd) }))
	case cli.CmdW32TimeStatus:
		st := winsys.ReadW32Time(layout.W32TimeBackup())
		fmt.Printf("Windows Time: %s\nTaken over by TimeSync: %t\nOriginal settings saved: %t\n", st.Summary(), st.TakenOver, st.BackupPresent)
		return exitOK
	case cli.CmdW32TimeRestore:
		return report(cmd, needAdmin(func() error {
			restored, err := winsys.RestoreW32Time(layout.W32TimeBackup())
			if err == nil && restored {
				fmt.Println("[OK] Windows Time settings restored.")
			} else if err == nil {
				fmt.Println("[INFO] Nothing to restore.")
			}
			return err
		}))
	case cli.CmdDiagnose:
		text := diag.Report(context.Background())
		if cmd.Out != "" {
			if err := os.WriteFile(cmd.Out, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
				return exitError
			}
			fmt.Printf("[OK] Diagnostics written to %s\n", cmd.Out)
			return exitOK
		}
		fmt.Print(text)
		return exitOK
	}
	printUsage()
	return exitUsage
}

// errNoAdmin is reported with its own exit code.
var errNoAdmin = errors.New("this needs administrator rights: right-click the command prompt and choose \"Run as administrator\"")

func needAdmin(f func() error) error {
	if !winsys.IsElevated() {
		return errNoAdmin
	}
	return f()
}

// outcome is written with -out, for the window: an elevated process cannot
// hand its output back to the window any other way.
type outcome struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Result *syncer.Result `json:"result,omitempty"`
}

func writeOutcome(path string, o outcome) {
	if path == "" {
		return
	}
	data, _ := json.MarshalIndent(o, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}

func report(cmd cli.Command, err error) int {
	writeOutcome(cmd.Out, outcome{OK: err == nil, Error: errString(err)})
	switch {
	case err == nil:
		if !strings.HasPrefix(cmd.Name, "service install") && !strings.HasPrefix(cmd.Name, "service uninstall") {
			fmt.Println("[OK] Done.")
		}
		return exitOK
	case errors.Is(err, errNoAdmin), errors.Is(err, winsys.ErrNoPrivilege):
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", errNoAdmin)
		return exitNoAdmin
	default:
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		return exitError
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func loadConfig(cmd cli.Command) config.Config {
	cfg, err := config.Load(layout.Config())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] %v\n", err)
	}
	if len(cmd.Servers) > 0 {
		for _, s := range cmd.Servers {
			if err := config.ValidateServer(s); err != nil {
				fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
				os.Exit(exitUsage)
			}
		}
		cfg.Servers = cmd.Servers
	}
	return cfg
}

func baseRequest(cfg config.Config, trigger string) syncer.Request {
	_, off := time.Now().Zone()
	return syncer.Request{
		Config:    cfg,
		Trigger:   trigger,
		BootTime:  winsys.BootTime(),
		UTCOffset: time.Duration(off) * time.Second,
	}
}

func doCheck(cmd cli.Command) int {
	cfg := loadConfig(cmd)
	if !cmd.JSON {
		fmt.Printf("[%s] Asking %d time servers (nothing will be changed)...\n", time.Now().Format("15:04:05"), len(cfg.Servers))
	}
	res := syncer.Run(context.Background(), baseRequest(cfg, history.TriggerManual), nil, syncer.DefaultQueries)
	writeOutcome(cmd.Out, outcome{OK: res.Verdict.Trusted(), Result: &res})
	if cmd.JSON {
		printJSON(res)
	} else {
		printResult(res)
	}
	if !res.Verdict.Trusted() {
		return exitUntrusted
	}
	return exitOK
}

func doSync(cmd cli.Command) int {
	// If the service runs, it does the work: no administrator rights
	// needed, and the result lands in the shared history.
	if !cmd.Local && len(cmd.Servers) == 0 && !cmd.Force {
		if winsys.QueryService(service.Name).Running {
			if !cmd.JSON {
				fmt.Printf("[%s] Asking the TimeSync service to synchronize now...\n", time.Now().Format("15:04:05"))
			}
			resp, err := ipc.Call(ipc.CmdSync, ipc.SyncTimeout+5*time.Second)
			if err == nil && resp.Result != nil {
				writeOutcome(cmd.Out, outcome{OK: true, Result: resp.Result})
				if cmd.JSON {
					printJSON(resp.Result)
				} else {
					printResult(*resp.Result)
				}
				return resultCode(*resp.Result)
			}
			if !cmd.JSON {
				fmt.Printf("    The service did not answer (%v); synchronizing directly.\n", err)
			}
		}
	}

	if !winsys.IsElevated() {
		writeOutcome(cmd.Out, outcome{Error: errNoAdmin.Error()})
		fmt.Fprintf(os.Stderr, "[ERROR] Setting the clock %v.\n", strings.TrimPrefix(errNoAdmin.Error(), "this "))
		fmt.Fprintln(os.Stderr, "        Tip: \"ntp-sync check\" only measures and works without.")
		return exitNoAdmin
	}
	cfg := loadConfig(cmd)
	req := baseRequest(cfg, history.TriggerManual)
	req.Apply = true
	if joined, _ := winsys.Domain(); joined && !cfg.AllowOnDomain && !cmd.Force {
		req.MonitorOnly = true
		if !cmd.JSON {
			fmt.Println("[INFO] This computer belongs to a company domain, which provides the time.")
			fmt.Println("       Measuring only. Use -force to set the clock anyway.")
		}
	}
	if !cmd.JSON {
		fmt.Printf("[%s] Asking %d time servers...\n", time.Now().Format("15:04:05"), len(cfg.Servers))
	}
	res := syncer.Run(context.Background(), req, winsys.Clock{}, syncer.DefaultQueries)
	_ = history.NewStore(layout.History()).Append(res.Entry)
	writeOutcome(cmd.Out, outcome{OK: res.Applied != history.ActionFailed && res.Verdict.Trusted(), Result: &res, Error: res.Error})
	if cmd.JSON {
		printJSON(res)
	} else {
		printResult(res)
	}
	return resultCode(res)
}

func resultCode(res syncer.Result) int {
	switch {
	case res.Applied == history.ActionFailed:
		return exitError
	case !res.Verdict.Trusted():
		return exitUntrusted
	}
	return exitOK
}

func doLoop(cmd cli.Command) int {
	if !winsys.IsElevated() {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", errNoAdmin)
		return exitNoAdmin
	}
	interval := time.Duration(max(cmd.Interval, 1)) * time.Minute
	fmt.Printf("[*] Synchronizing every %v, press Ctrl+C to stop...\n", interval)
	for {
		cfg := loadConfig(cmd)
		req := baseRequest(cfg, history.TriggerInterval)
		req.Apply = true
		res := syncer.Run(context.Background(), req, winsys.Clock{}, syncer.DefaultQueries)
		_ = history.NewStore(layout.History()).Append(res.Entry)
		printResult(res)
		time.Sleep(interval)
	}
}

func doStatus(cmd cli.Command) int {
	info := winsys.QueryService(service.Name)
	resp, perr := ipc.Call(ipc.CmdStatus, 5*time.Second)
	if cmd.JSON {
		printJSON(map[string]any{"service": info, "status": resp.Status, "w32time": winsys.ReadW32Time(layout.W32TimeBackup())})
		return exitOK
	}
	switch {
	case !info.Installed:
		fmt.Println("Service:      not installed (install with: ntp-sync service install)")
		if legacy := winsys.QueryService(service.LegacyName); legacy.Installed {
			fmt.Println("              An old version 1.x service is installed; \"ntp-sync service install\" upgrades it.")
		}
	case perr != nil || resp.Status == nil:
		fmt.Printf("Service:      %s (%s)\n", info.State, info.StartType)
	default:
		st := resp.Status
		fmt.Printf("Service:      running, version %s, every %d minutes\n", st.Version, st.IntervalMinutes)
		fmt.Printf("Next sync:    %s\n", st.NextSync.Format("15:04:05"))
		if st.MonitorOnly {
			fmt.Println("Mode:         measuring only (domain computer)")
		}
		if st.ConfigError != "" {
			fmt.Printf("Settings:     %s\n", st.ConfigError)
		}
		if st.Last != nil {
			fmt.Printf("Last:         %s\n", diag.FormatEntry(*st.Last))
		}
		if st.Stats.DriftSamples >= 3 {
			fmt.Printf("Drift:        %s\n", driftText(st.Stats.LagSecondsPerDay))
		}
	}
	fmt.Printf("Windows Time: %s\n", winsys.ReadW32Time(layout.W32TimeBackup()).Summary())
	return exitOK
}

func driftText(lag float64) string {
	if lag >= 0 {
		return fmt.Sprintf("the clock loses about %.2f s per day", lag)
	}
	return fmt.Sprintf("the clock gains about %.2f s per day", -lag)
}

func doHistory(cmd cli.Command) int {
	entries, err := history.Read(layout.History(), cmd.N)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		return exitError
	}
	all, _ := history.Read(layout.History(), 0)
	stats := history.Analyze(all, time.Now())
	if cmd.JSON {
		printJSON(map[string]any{"entries": entries, "stats": stats})
		return exitOK
	}
	if len(entries) == 0 {
		fmt.Println("No synchronizations recorded yet.")
		return exitOK
	}
	for _, e := range entries {
		fmt.Println(diag.FormatEntry(e))
	}
	if stats.DriftSamples >= 3 {
		fmt.Printf("\nDrift: %s.\n", driftText(stats.LagSecondsPerDay))
	}
	if stats.CMOSSuspect {
		fmt.Println("Note: right after switching on, the clock was far off. If that happens often, the CMOS battery may be weak.")
	}
	return exitOK
}

func doConfigShow(cmd cli.Command) int {
	cfg, err := config.Load(layout.Config())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] %v\n", err)
	}
	if !cmd.JSON {
		fmt.Printf("# %s\n", layout.Config())
	}
	printJSON(cfg)
	return exitOK
}

func doConfigChange(cmd cli.Command) error {
	cfg, err := config.Load(layout.Config())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] %v\n", err)
	}
	switch cmd.Name {
	case cli.CmdConfigReset:
		cfg = config.Default()
	case cli.CmdConfigImport:
		data, err := os.ReadFile(cmd.Args[0])
		if err != nil {
			return err
		}
		if cfg, err = config.Parse(data); err != nil {
			return err
		}
	case cli.CmdConfigSet:
		for _, kv := range cmd.Args {
			k, v, _ := strings.Cut(kv, "=")
			if err := cfg.Set(k, v); err != nil {
				return err
			}
		}
	}
	if err := winsys.SecureDir(layout.DataDir, winsys.DataDirSDDL); err != nil {
		return err
	}
	if err := config.Save(layout.Config(), cfg); err != nil {
		return err
	}
	fmt.Printf("[OK] Settings saved to %s\n", layout.Config())
	if winsys.QueryService(service.Name).Installed {
		if msg, err := service.ApplyW32TimeMode(cfg); err != nil {
			fmt.Printf("[WARN] %v\n", err)
		} else if msg != "" {
			fmt.Printf("[OK] %s\n", msg)
		}
		if _, err := ipc.Call(ipc.CmdReload, 5*time.Second); err == nil {
			fmt.Println("[OK] The service uses the new settings.")
		}
	}
	return nil
}

func printJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

// printResult explains one round in plain words.
func printResult(res syncer.Result) {
	for _, s := range res.Samples {
		fmt.Printf("    %s\n", diag.FormatSample(s))
	}
	v := res.Verdict
	off := diag.FormatOffset(v.Offset)
	switch {
	case v.Reason == engine.ReasonNoAnswers:
		fmt.Println("    -> No time server answered.")
		fmt.Println("       If you are in a company network, UDP port 123 is probably blocked.")
	case v.Reason == engine.ReasonNoMajority:
		fmt.Println("    -> The servers disagree too much; nothing was changed to be safe.")
	case v.Reason == engine.ReasonBigJump:
		fmt.Printf("    -> Your clock seems to be %s off, but that big a jump needs at least three\n", off)
		fmt.Println("       agreeing servers. Nothing was changed. Add more servers or try again.")
	default:
		fmt.Printf("    -> %d of %d servers agree: your clock is %s off", v.Agreeing, v.Asked, off)
		if v.LowPrecision {
			fmt.Print(" (web time, about 1 s accurate)")
		}
		fmt.Println(".")
	}
	switch res.Applied {
	case "none":
		fmt.Println("    [OK] No correction needed.")
	case "step":
		fmt.Printf("    [OK] Clock set. It is now %s.\n", time.Now().Format("2006-01-02 15:04:05"))
	case "slew":
		fmt.Printf("    [OK] Correcting gently over %v.\n", res.Slew.Duration.Round(time.Second))
	case history.ActionMonitor:
		fmt.Println("    [INFO] Measured only, the clock was not changed.")
	case history.ActionFailed:
		fmt.Printf("    [ERROR] The clock could not be set: %s\n", res.Error)
	}
	for _, h := range v.Hints {
		switch h {
		case engine.HintDualBoot:
			fmt.Println("    Hint: the difference equals your time zone offset. This typically happens with")
			fmt.Println("          dual boot (Linux keeps the hardware clock in UTC, Windows in local time).")
		case engine.HintTimeZone:
			fmt.Println("    Hint: the difference is a whole number of hours. Check the time zone setting.")
		case engine.HintNTPBlocked:
			fmt.Println("    Hint: NTP (UDP port 123) seems blocked; web servers were used instead.")
		case engine.HintOutliers:
			fmt.Printf("    Hint: ignored servers that disagreed with the rest: %s\n", strings.Join(v.Outliers, ", "))
		case engine.HintSingleServer:
			fmt.Println("    Hint: only one server is configured, so there is no cross-check.")
		}
	}
}

func printBanner() {
	fmt.Println("==================================================================")
	fmt.Printf("  %s %s (ntp-sync)\n", version.Product, version.Version)
	fmt.Println("==================================================================")
	fmt.Printf("  Local time       : %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("  Administrator    : %t\n", winsys.IsElevated())
	svcInfo := winsys.QueryService(service.Name)
	state := "not installed"
	if svcInfo.Installed {
		state = svcInfo.State
	}
	fmt.Printf("  Service          : %s\n", state)
	fmt.Println("  Help             : ntp-sync help")
	fmt.Println("------------------------------------------------------------------")
}

func printUsage() {
	fmt.Printf(`%s %s - keeps the Windows clock accurate.

USAGE
  ntp-sync                      synchronize once (like "sync", with an overview)
  ntp-sync check                only measure how far off the clock is (no admin needed)
  ntp-sync sync                 synchronize now (via the service if it runs, else admin needed)
  ntp-sync status               show the service status
  ntp-sync history [-n 20]      show the last synchronizations and the clock's drift
  ntp-sync run [-interval 60]   keep synchronizing in this window (admin)
  ntp-sync diagnose [-out FILE] write a diagnostics report for support requests

  ntp-sync service install [-interval 60]   install the background service (admin)
  ntp-sync service uninstall [-purge]       remove it and restore Windows Time (admin)
  ntp-sync service start|stop|restart       (admin)

  ntp-sync config show [-json]  show the settings
  ntp-sync config set KEY=VALUE change settings, e.g. servers=ptbtime1.ptb.de,ptbtime2.ptb.de (admin)
  ntp-sync config reset         back to defaults (admin)
  ntp-sync config import FILE   load settings from a JSON file (admin)
  ntp-sync w32time status|restore

OPTIONS
  -server a,b,c   use these servers instead of the configured ones (check, sync, run)
  -json           machine readable output
  -force          sync, check: also set the clock on a domain computer
  -local          sync: do it in this process even if the service runs

Version 1.x commands (-install N, -uninstall, -interval N, -version) still work.
Settings: %s
More:     %s
`, version.Product, version.Version, layout.Config(), version.Repository)
}
