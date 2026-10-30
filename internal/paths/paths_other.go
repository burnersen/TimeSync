//go:build !windows

package paths

import (
	"os"
	"path/filepath"
)

// Non-Windows builds exist only for tests and development.
func programData() string  { return filepath.Join(os.TempDir(), "timesync-programdata") }
func programFiles() string { return filepath.Join(os.TempDir(), "timesync-programfiles") }
