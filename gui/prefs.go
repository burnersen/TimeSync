//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/burnersen/TimeSync/internal/config"
)

// Prefs are the window's own settings. They belong to the user, not to the
// machine, and need no administrator rights.
type Prefs struct {
	Language string `json:"language"` // "de", "en" or "" (follow Windows)
	Theme    string `json:"theme"`    // "light", "dark" or "system"
}

func normaliseTheme(t string) string {
	if t == "light" || t == "dark" {
		return t
	}
	return "system"
}

// portable: a file "portable.txt" next to TimeSync.exe keeps the window's
// preferences next to the program instead of in the user profile.
func portable() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(exe), "portable.txt"))
	return err == nil
}

func prefsPath() string {
	if portable() {
		exe, _ := os.Executable()
		return filepath.Join(filepath.Dir(exe), "TimeSync.prefs.json")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "TimeSync", "window.json")
}

func loadPrefs() Prefs {
	p := Prefs{Theme: "system"}
	if data, err := os.ReadFile(prefsPath()); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	p.Theme = normaliseTheme(p.Theme)
	return p
}

func savePrefs(p Prefs) error {
	data, _ := json.MarshalIndent(p, "", "  ")
	return config.WriteFileAtomic(prefsPath(), data)
}
