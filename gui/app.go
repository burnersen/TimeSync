//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/diag"
	"github.com/burnersen/TimeSync/internal/history"
	"github.com/burnersen/TimeSync/internal/ipc"
	"github.com/burnersen/TimeSync/internal/paths"
	"github.com/burnersen/TimeSync/internal/syncer"
	"github.com/burnersen/TimeSync/internal/version"
	"github.com/burnersen/TimeSync/internal/winsys"
)

const serviceName = "TimeSync"

// App holds what the page calls. Every exported method is reachable from
// JavaScript as window.go.main.App.<Name>.
type App struct {
	ctx    context.Context
	layout paths.Layout
	prefs  Prefs
}

// NewApp loads the window preferences before the window opens, so it can
// open in the right colours.
func NewApp() *App {
	return &App{layout: paths.Default(), prefs: loadPrefs()}
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

func (a *App) onSecondInstance(options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

// StartupInfo is what the page needs before anything else.
type StartupInfo struct {
	Version  string `json:"version"`
	Language string `json:"language"`
	Theme    string `json:"theme"`
	Elevated bool   `json:"elevated"`
	CLIFound bool   `json:"cliFound"`
	Portable bool   `json:"portable"`
	Domain   bool   `json:"domain"`
	Repo     string `json:"repo"`
}

func (a *App) Startup() StartupInfo {
	lang := a.prefs.Language
	if lang != "de" && lang != "en" {
		lang = "en"
		if strings.HasPrefix(strings.ToLower(winsys.UILanguage()), "de") {
			lang = "de"
		}
	}
	_, err := os.Stat(cliPath())
	joined, _ := winsys.Domain()
	return StartupInfo{
		Version:  version.Version,
		Language: lang,
		Theme:    a.prefs.Theme,
		Elevated: winsys.IsElevated(),
		CLIFound: err == nil,
		Portable: portable(),
		Domain:   joined,
		Repo:     version.Repository,
	}
}

// CheckView is the result of measuring without changing anything.
type CheckView struct {
	Result    *syncer.Result  `json:"result,omitempty"`
	CheckedAt int64           `json:"checkedAt"`
	TimeZone  winsys.TimeZone `json:"timeZone"`
	Servers   []string        `json:"servers"`
	Error     string          `json:"error,omitempty"`
}

// Check asks the configured servers how far off the clock is. It needs no
// administrator rights.
func (a *App) Check() CheckView {
	cfg, err := config.Load(a.layout.Config())
	view := CheckView{TimeZone: winsys.CurrentTimeZone(), Servers: cfg.Servers}
	if err != nil {
		view.Error = err.Error()
	}
	_, off := time.Now().Zone()
	res := syncer.Run(context.Background(), syncer.Request{
		Config:    cfg,
		Trigger:   history.TriggerManual,
		UTCOffset: time.Duration(off) * time.Second,
	}, nil, syncer.DefaultQueries)
	view.Result = &res
	view.CheckedAt = time.Now().UnixMilli()
	return view
}

// ServiceView describes the background service.
type ServiceView struct {
	Installed bool                `json:"installed"`
	Running   bool                `json:"running"`
	State     string              `json:"state"`
	Legacy    bool                `json:"legacy"`
	Status    *ipc.Status         `json:"status,omitempty"`
	W32Time   winsys.W32TimeState `json:"w32time"`
}

func (a *App) Service() ServiceView {
	info := winsys.QueryService(serviceName)
	v := ServiceView{
		Installed: info.Installed,
		Running:   info.Running,
		State:     info.State,
		Legacy:    winsys.QueryService("ntp-sync").Installed,
		W32Time:   winsys.ReadW32Time(a.layout.W32TimeBackup()),
	}
	if info.Running {
		if resp, err := ipc.Call(ipc.CmdStatus, 3*time.Second); err == nil {
			v.Status = resp.Status
		}
	}
	return v
}

// ActionResult is the outcome of anything that changes something.
type ActionResult struct {
	OK        bool           `json:"ok"`
	Cancelled bool           `json:"cancelled"`
	Error     string         `json:"error,omitempty"`
	Result    *syncer.Result `json:"result,omitempty"`
}

// SyncNow sets the clock: through the service if it runs (no prompt),
// otherwise through ntp-sync.exe with administrator rights. force also sets
// the clock on a domain computer.
func (a *App) SyncNow(force bool) ActionResult {
	if !force && winsys.QueryService(serviceName).Running {
		resp, err := ipc.Call(ipc.CmdSync, ipc.SyncTimeout+5*time.Second)
		if err == nil && resp.Result != nil {
			return ActionResult{OK: true, Result: resp.Result}
		}
		if err != nil && strings.Contains(err.Error(), "wait a few seconds") {
			return ActionResult{Error: err.Error()}
		}
	}
	args := []string{"sync", "-local"}
	if force {
		args = append(args, "-force")
	}
	return runElevated(args)
}

// SettingsView is the settings file as the page edits it.
type SettingsView struct {
	Config   config.Config `json:"config"`
	Defaults config.Config `json:"defaults"`
	Path     string        `json:"path"`
	Exists   bool          `json:"exists"`
	Error    string        `json:"error,omitempty"`
}

func (a *App) GetSettings() SettingsView {
	cfg, err := config.Load(a.layout.Config())
	_, statErr := os.Stat(a.layout.Config())
	v := SettingsView{Config: cfg, Defaults: config.Default(), Path: a.layout.Config(), Exists: statErr == nil}
	if err != nil {
		v.Error = err.Error()
	}
	return v
}

// ValidateSettings checks settings without saving them.
func (a *App) ValidateSettings(cfg config.Config) string {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err.Error()
	}
	return ""
}

// SaveSettings writes the settings through ntp-sync.exe (administrator
// prompt), which also tells the service.
func (a *App) SaveSettings(cfg config.Config) ActionResult {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return ActionResult{Error: err.Error()}
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	f, err := os.CreateTemp("", "timesync-settings-*.json")
	if err != nil {
		return ActionResult{Error: err.Error()}
	}
	f.Write(data)
	f.Close()
	defer os.Remove(f.Name())
	return runElevated([]string{"config", "import", f.Name()})
}

func (a *App) InstallService(intervalMinutes int) ActionResult {
	args := []string{"service", "install"}
	if intervalMinutes > 0 {
		args = append(args, "-interval", strconv.Itoa(intervalMinutes))
	}
	return runElevated(args)
}

func (a *App) UninstallService(purge bool) ActionResult {
	args := []string{"service", "uninstall"}
	if purge {
		args = append(args, "-purge")
	}
	return runElevated(args)
}

// HistoryView holds the recorded synchronizations, newest last.
type HistoryView struct {
	Entries []history.Entry `json:"entries"`
	Stats   history.Stats   `json:"stats"`
}

func (a *App) History(n int) HistoryView {
	all, _ := history.Read(a.layout.History(), 0)
	v := HistoryView{Stats: history.Analyze(all, time.Now())}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	v.Entries = all
	return v
}

// SaveDiagnostics asks where to save the report and writes it.
func (a *App) SaveDiagnostics() ActionResult {
	name := "TimeSync-diagnostics-" + time.Now().Format("2006-01-02-1504") + ".txt"
	home, _ := os.UserHomeDir()
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            "TimeSync",
		DefaultFilename:  name,
		DefaultDirectory: filepath.Join(home, "Desktop"),
		Filters:          []runtime.FileFilter{{DisplayName: "Text (*.txt)", Pattern: "*.txt"}},
	})
	if err != nil {
		return ActionResult{Error: err.Error()}
	}
	if path == "" {
		return ActionResult{Cancelled: true}
	}
	text := diag.Report(context.Background())
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644); err != nil {
		return ActionResult{Error: err.Error()}
	}
	return ActionResult{OK: true, Error: ""}
}

// OpenURL opens a web page or a Windows settings page.
func (a *App) OpenURL(url string) string {
	if err := winsys.OpenURL(url); err != nil {
		return err.Error()
	}
	return ""
}

// SavePrefs remembers language and theme.
func (a *App) SavePrefs(language, theme string) string {
	a.prefs = Prefs{Language: language, Theme: normaliseTheme(theme)}
	if a.ctx != nil {
		switch a.prefs.Theme {
		case "light":
			runtime.WindowSetLightTheme(a.ctx)
		case "dark":
			runtime.WindowSetDarkTheme(a.ctx)
		default:
			runtime.WindowSetSystemDefaultTheme(a.ctx)
		}
	}
	if err := savePrefs(a.prefs); err != nil {
		return err.Error()
	}
	return ""
}

// cliPath is ntp-sync.exe next to this program.
func cliPath() string {
	exe, err := os.Executable()
	if err != nil {
		return paths.ServiceExeName
	}
	return filepath.Join(filepath.Dir(exe), paths.ServiceExeName)
}

// runElevated runs ntp-sync.exe with administrator rights and reads its
// result file: an elevated process cannot hand its output back otherwise.
func runElevated(args []string) ActionResult {
	cli := cliPath()
	if _, err := os.Stat(cli); err != nil {
		return ActionResult{Error: fmt.Sprintf("%s is missing. It must be in the same folder as TimeSync.exe.", paths.ServiceExeName)}
	}
	out, err := os.CreateTemp("", "timesync-result-*.json")
	if err != nil {
		return ActionResult{Error: err.Error()}
	}
	out.Close()
	defer os.Remove(out.Name())
	args = append(args, "-out", out.Name(), "-nopause")
	code, err := winsys.RunElevated(cli, args)
	if errors.Is(err, winsys.ErrCancelled) {
		return ActionResult{Cancelled: true}
	}
	if err != nil {
		return ActionResult{Error: err.Error()}
	}
	var o struct {
		OK     bool           `json:"ok"`
		Error  string         `json:"error"`
		Result *syncer.Result `json:"result"`
	}
	data, _ := os.ReadFile(out.Name())
	if json.Unmarshal(data, &o) != nil {
		if code == 0 {
			return ActionResult{OK: true}
		}
		return ActionResult{Error: fmt.Sprintf("ntp-sync.exe ended with code %d", code)}
	}
	if o.Error == "" && !o.OK && o.Result == nil && code != 0 {
		o.Error = fmt.Sprintf("ntp-sync.exe ended with code %d", code)
	}
	return ActionResult{OK: o.OK, Error: o.Error, Result: o.Result}
}
