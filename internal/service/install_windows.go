//go:build windows

package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/paths"
	"github.com/burnersen/TimeSync/internal/winsys"
)

// InstallOptions tune Install.
type InstallOptions struct {
	// IntervalMinutes > 0 overrides the interval in the settings.
	IntervalMinutes int
	Out             io.Writer
}

// Install sets up (or updates) the service. It is safe to run again: an
// existing installation is updated in place and keeps its settings.
//
// Steps:
//  1. remove a version 1.x service ("ntp-sync")
//  2. stop a running TimeSync service (update case)
//  3. copy this program to C:\Program Files\TimeSync, where only
//     administrators can replace it
//  4. create C:\ProgramData\TimeSync with restricted permissions and the
//     settings file
//  5. create or update the service: "Local Service" account, own service
//     SID, delayed automatic start, restart after a crash
//  6. let the service (and only it) write its log folder
//  7. register the event log source
//  8. take over the Windows Time service if the settings say so
//  9. start the service
func Install(opt InstallOptions) error {
	out := opt.Out
	if out == nil {
		out = io.Discard
	}
	if !winsys.IsElevated() {
		return winsys.ErrNoPrivilege
	}
	layout := paths.Default()

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()

	if removed, err := removeService(m, LegacyName); err != nil {
		return fmt.Errorf("remove the old %s service: %w", LegacyName, err)
	} else if removed {
		fmt.Fprintf(out, "[OK] Old service %q (version 1.x) removed.\n", LegacyName)
	}

	if s, err := m.OpenService(Name); err == nil {
		stopAndWait(s)
		s.Close()
	}

	self, err := executable()
	if err != nil {
		return err
	}
	target := layout.ServiceExe()
	if !samePath(self, target) {
		if err := copyFile(self, target); err != nil {
			return fmt.Errorf("copy the program to %s: %w", layout.InstallDir, err)
		}
		fmt.Fprintf(out, "[OK] Program copied to %s\n", target)
	}

	if err := winsys.SecureDir(layout.DataDir, winsys.DataDirSDDL); err != nil {
		return fmt.Errorf("prepare %s: %w", layout.DataDir, err)
	}
	cfg, err := config.Load(layout.Config())
	if err != nil {
		fmt.Fprintf(out, "[WARN] %v\n", err)
	}
	if opt.IntervalMinutes > 0 {
		cfg.IntervalMinutes = max(min(opt.IntervalMinutes, config.MaxIntervalMinutes), config.MinIntervalMinutes)
		if cfg.IntervalMinutes != opt.IntervalMinutes {
			fmt.Fprintf(out, "[WARN] Interval adjusted to %d minutes (allowed: %d to %d).\n", cfg.IntervalMinutes, config.MinIntervalMinutes, config.MaxIntervalMinutes)
		}
	}
	if err := config.Save(layout.Config(), cfg); err != nil {
		return fmt.Errorf("save the settings: %w", err)
	}

	scfg := mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		ErrorControl:     mgr.ErrorNormal,
		DisplayName:      DisplayName,
		Description:      Description,
		ServiceStartName: `NT AUTHORITY\LocalService`,
		SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	s, err := m.OpenService(Name)
	if err == nil {
		old, _ := s.Config()
		scfg.BinaryPathName = windows.EscapeArg(target) + " service run"
		scfg.Password = ""
		if old.ServiceStartName == scfg.ServiceStartName {
			scfg.ServiceStartName = "" // unchanged; avoids a password prompt
		}
		if err := s.UpdateConfig(scfg); err != nil {
			s.Close()
			return fmt.Errorf("update the service: %w", err)
		}
	} else {
		s, err = m.CreateService(Name, target, scfg, "service", "run")
		if err != nil {
			return fmt.Errorf("create the service: %w", err)
		}
	}
	defer s.Close()
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Minute},
	}, 24*60*60)

	sid, err := winsys.ServiceSID(Name)
	if err != nil {
		return fmt.Errorf("look up the service identity: %w", err)
	}
	if err := winsys.SecureDir(layout.StateDir(), winsys.StateDirSDDL(sid)); err != nil {
		return fmt.Errorf("prepare %s: %w", layout.StateDir(), err)
	}
	if err := winsys.InstallEventSource(Name); err != nil {
		fmt.Fprintf(out, "[WARN] Event log source not registered: %v\n", err)
	}

	if msg, err := ApplyW32TimeMode(cfg); err != nil {
		fmt.Fprintf(out, "[WARN] %v\n", err)
	} else if msg != "" {
		fmt.Fprintf(out, "[OK] %s\n", msg)
	}

	if err := startAndWait(s); err != nil {
		return fmt.Errorf("start the service: %w", err)
	}
	fmt.Fprintf(out, "[OK] Service %q installed and running (every %d minutes).\n", Name, cfg.IntervalMinutes)
	return nil
}

// ApplyW32TimeMode takes over or releases the Windows Time service as the
// settings say. Domain computers are left alone unless explicitly allowed.
func ApplyW32TimeMode(cfg config.Config) (string, error) {
	layout := paths.Default()
	joined, _ := winsys.Domain()
	if cfg.W32TimeMode == config.W32TimeTakeover && (!joined || cfg.AllowOnDomain) {
		st := winsys.ReadW32Time(layout.W32TimeBackup())
		if st.TakenOver {
			return "", nil
		}
		if err := winsys.TakeOverW32Time(layout.W32TimeBackup()); err != nil {
			return "", err
		}
		return "Windows Time no longer sets the clock (original settings saved).", nil
	}
	restored, err := winsys.RestoreW32Time(layout.W32TimeBackup())
	if err != nil {
		return "", err
	}
	if restored {
		return "Windows Time settings restored.", nil
	}
	return "", nil
}

// UninstallOptions tune Uninstall.
type UninstallOptions struct {
	// Purge also deletes settings, log and history.
	Purge bool
	// KeepBinary leaves C:\Program Files\TimeSync alone (the installer
	// removes its own files).
	KeepBinary bool
	Out        io.Writer
}

// Uninstall removes the service and restores the Windows Time service.
func Uninstall(opt UninstallOptions) error {
	out := opt.Out
	if out == nil {
		out = io.Discard
	}
	if !winsys.IsElevated() {
		return winsys.ErrNoPrivilege
	}
	layout := paths.Default()
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()

	found := false
	for _, name := range []string{Name, LegacyName} {
		removed, err := removeService(m, name)
		if err != nil {
			return fmt.Errorf("remove the service %q: %w", name, err)
		}
		if removed {
			found = true
			fmt.Fprintf(out, "[OK] Service %q removed.\n", name)
		}
	}
	if !found {
		fmt.Fprintln(out, "[INFO] No TimeSync service was installed.")
	}

	if restored, err := winsys.RestoreW32Time(layout.W32TimeBackup()); err != nil {
		fmt.Fprintf(out, "[WARN] %v\n", err)
	} else if restored {
		fmt.Fprintln(out, "[OK] Windows Time settings restored.")
	}
	_ = winsys.RemoveEventSource(Name)

	if !opt.KeepBinary {
		target := layout.ServiceExe()
		if self, err := executable(); err == nil && samePath(self, target) {
			// A running program cannot delete itself; Windows does it at the
			// next restart.
			_ = windows.MoveFileEx(windows.StringToUTF16Ptr(target), nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
			_ = windows.MoveFileEx(windows.StringToUTF16Ptr(layout.InstallDir), nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		} else if err := os.Remove(target); err == nil {
			os.Remove(layout.InstallDir) // only succeeds if empty
		}
	}
	if opt.Purge {
		if err := os.RemoveAll(layout.DataDir); err != nil {
			fmt.Fprintf(out, "[WARN] %v\n", err)
		} else {
			fmt.Fprintf(out, "[OK] %s deleted.\n", layout.DataDir)
		}
	} else {
		fmt.Fprintf(out, "[INFO] Settings and history kept in %s (delete with -purge).\n", layout.DataDir)
	}
	return nil
}

// Start starts the installed service.
func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return errors.New("the service is not installed")
	}
	defer s.Close()
	return startAndWait(s)
}

// Stop stops the installed service.
func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return errors.New("the service is not installed")
	}
	defer s.Close()
	stopAndWait(s)
	return nil
}

func removeService(m *mgr.Mgr, name string) (bool, error) {
	s, err := m.OpenService(name)
	if err != nil {
		return false, nil
	}
	defer s.Close()
	stopAndWait(s)
	if err := s.Delete(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return false, err
	}
	return true, nil
}

func stopAndWait(s *mgr.Service) {
	st, err := s.Control(svc.Stop)
	if err != nil {
		return
	}
	deadline := time.Now().Add(20 * time.Second)
	for st.State != svc.Stopped && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return
		}
	}
}

func startAndWait(s *mgr.Service) error {
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return err
		}
		switch st.State {
		case svc.Running:
			return nil
		case svc.Stopped:
			return errors.New("the service stopped right after starting; see the event log")
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("the service did not report running within 20 seconds")
}

func executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Abs(p)
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// copyFile copies via a temporary file and a rename, so an interrupted copy
// never leaves a broken program behind.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return config.WriteFileAtomic(dst, data)
}
