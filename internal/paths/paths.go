// Package paths knows where TimeSync keeps its files.
//
//	C:\Program Files\TimeSync\ntp-sync.exe       the service binary (admins only)
//	C:\ProgramData\TimeSync\config.json          settings (admins write, all read)
//	C:\ProgramData\TimeSync\w32time-backup.json  original Windows Time settings
//	C:\ProgramData\TimeSync\state\               log and history (written by the service)
package paths

import "path/filepath"

const (
	dirName        = "TimeSync"
	configName     = "config.json"
	backupName     = "w32time-backup.json"
	stateDirName   = "state"
	logName        = "timesync.log"
	historyName    = "history.jsonl"
	ServiceExeName = "ntp-sync.exe"
	GUIExeName     = "TimeSync.exe"
)

// Layout resolves all paths below a ProgramData and a Program Files root.
type Layout struct {
	DataDir    string
	InstallDir string
}

// Default returns the standard locations of this machine.
func Default() Layout {
	return Layout{
		DataDir:    filepath.Join(programData(), dirName),
		InstallDir: filepath.Join(programFiles(), dirName),
	}
}

func (l Layout) Config() string        { return filepath.Join(l.DataDir, configName) }
func (l Layout) W32TimeBackup() string { return filepath.Join(l.DataDir, backupName) }
func (l Layout) StateDir() string      { return filepath.Join(l.DataDir, stateDirName) }
func (l Layout) Log() string           { return filepath.Join(l.StateDir(), logName) }
func (l Layout) History() string       { return filepath.Join(l.StateDir(), historyName) }
func (l Layout) ServiceExe() string    { return filepath.Join(l.InstallDir, ServiceExeName) }
