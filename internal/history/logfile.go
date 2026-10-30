package history

import (
	"os"
	"path/filepath"
	"sync"
)

// LogFile is an io.Writer for the text log. Unlike version 1.0.0, it checks
// the size on every write, so a service that runs for months still rotates.
type LogFile struct {
	Path    string
	MaxSize int64
	mu      sync.Mutex
}

func (l *LogFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if info, err := os.Stat(l.Path); err == nil && info.Size()+int64(len(p)) > l.MaxSize {
		old := l.Path + ".old"
		os.Remove(old) // Windows cannot rename over an existing file
		os.Rename(l.Path, old)
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(p)
}
