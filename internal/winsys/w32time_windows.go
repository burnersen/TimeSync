//go:build windows

package winsys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/version"
)

// The Windows Time service reads its time source from this key. Type
// "NoSync" keeps the service alive (other parts of Windows rely on it) but
// stops it from fetching time and adjusting the clock. That is exactly what
// "w32tm /config /syncfromflags:NO" does.
const w32timeParams = `SYSTEM\CurrentControlSet\Services\W32Time\Parameters`

// W32TimeState describes the Windows Time service.
type W32TimeState struct {
	Installed bool   `json:"installed"`
	StartType string `json:"startType"`
	Running   bool   `json:"running"`
	Type      string `json:"type"`
	NtpServer string `json:"ntpServer"`
	// TakenOver: TimeSync has switched its time source off.
	TakenOver bool `json:"takenOver"`
	// BackupPresent: the original settings are saved and can be restored.
	BackupPresent bool `json:"backupPresent"`
}

// Summary is a short human readable description for logs.
func (s W32TimeState) Summary() string {
	if !s.Installed {
		return "not installed"
	}
	state := "stopped"
	if s.Running {
		state = "running"
	}
	return fmt.Sprintf("%s, start %s, source %s (%s)", state, s.StartType, s.Type, s.NtpServer)
}

type w32timeBackup struct {
	Type        string    `json:"type"`
	TypeExisted bool      `json:"typeExisted"`
	SavedAt     time.Time `json:"savedAt"`
	SavedBy     string    `json:"savedBy"`
}

// ReadW32Time returns the current state. It needs no special rights.
func ReadW32Time(backupPath string) W32TimeState {
	var st W32TimeState
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, w32timeParams, registry.QUERY_VALUE); err == nil {
		st.Installed = true
		st.Type, _, _ = k.GetStringValue("Type")
		st.NtpServer, _, _ = k.GetStringValue("NtpServer")
		k.Close()
	}
	_, err := os.Stat(backupPath)
	st.BackupPresent = err == nil
	st.TakenOver = st.BackupPresent && st.Type == "NoSync"

	if m, err := connectReadOnly(); err == nil {
		defer m.Disconnect()
		if s, err := openServiceReadOnly(m, "w32time"); err == nil {
			if cfg, err := s.Config(); err == nil {
				st.StartType = startTypeName(cfg.StartType, cfg.DelayedAutoStart)
			}
			if q, err := s.Query(); err == nil {
				st.Running = q.State == svc.Running
			}
			s.Close()
		}
	}
	return st
}

// TakeOverW32Time saves the original time source and switches it off.
// Calling it twice keeps the first, true original.
func TakeOverW32Time(backupPath string) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, w32timeParams, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Windows Time settings: %w", err)
	}
	defer k.Close()

	if _, err := os.Stat(backupPath); errors.Is(err, os.ErrNotExist) {
		typ, _, terr := k.GetStringValue("Type")
		b := w32timeBackup{Type: typ, TypeExisted: terr == nil, SavedAt: time.Now(), SavedBy: version.Product + " " + version.Version}
		data, _ := json.MarshalIndent(b, "", "  ")
		if err := config.WriteFileAtomic(backupPath, data); err != nil {
			return fmt.Errorf("save the Windows Time settings: %w", err)
		}
	}
	if err := k.SetStringValue("Type", "NoSync"); err != nil {
		return fmt.Errorf("change the Windows Time settings: %w", err)
	}
	notifyW32Time()
	return nil
}

// RestoreW32Time puts the saved original back. Without a backup it does
// nothing.
func RestoreW32Time(backupPath string) (restored bool, err error) {
	data, err := os.ReadFile(backupPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var b w32timeBackup
	if err := json.Unmarshal(data, &b); err != nil {
		return false, fmt.Errorf("the backup %s is damaged: %w", backupPath, err)
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, w32timeParams, registry.SET_VALUE)
	if err != nil {
		return false, fmt.Errorf("open Windows Time settings: %w", err)
	}
	defer k.Close()
	if b.TypeExisted {
		err = k.SetStringValue("Type", b.Type)
	} else {
		err = k.DeleteValue("Type")
		if errors.Is(err, registry.ErrNotExist) {
			err = nil
		}
	}
	if err != nil {
		return false, fmt.Errorf("restore the Windows Time settings: %w", err)
	}
	notifyW32Time()
	return true, os.Remove(backupPath)
}

// notifyW32Time asks a running Windows Time service to re-read its
// settings. If it is not running it reads them on its next start anyway.
func notifyW32Time() {
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()
	s, err := m.OpenService("w32time")
	if err != nil {
		return
	}
	defer s.Close()
	_, _ = s.Control(svc.ParamChange)
}

func startTypeName(t uint32, delayed bool) string {
	switch t {
	case mgr.StartAutomatic:
		if delayed {
			return "automatic (delayed)"
		}
		return "automatic"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "disabled"
	}
	return fmt.Sprint(t)
}
