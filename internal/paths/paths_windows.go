//go:build windows

package paths

import (
	"os"

	"golang.org/x/sys/windows"
)

func programData() string {
	if p, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0); err == nil {
		return p
	}
	if p := os.Getenv("ProgramData"); p != "" {
		return p
	}
	return `C:\ProgramData`
}

// programFiles returns the native Program Files folder (not "Program Files
// (x86)"), which is where a 64-bit service binary belongs.
func programFiles() string {
	if p, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0); err == nil {
		return p
	}
	if p := os.Getenv("ProgramW6432"); p != "" {
		return p
	}
	return `C:\Program Files`
}
