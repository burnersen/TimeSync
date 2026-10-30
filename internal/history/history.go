// Package history records every synchronization attempt and derives the
// statistics shown to the user: how fast the clock drifts, and whether the
// clock was far off after switching on (a hint to a weak CMOS battery).
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/burnersen/TimeSync/internal/config"
)

// Triggers: why a synchronization ran.
const (
	TriggerStart    = "start"
	TriggerInterval = "interval"
	TriggerRetry    = "retry"
	TriggerResume   = "resume"
	TriggerNetwork  = "network"
	TriggerManual   = "manual"
)

// Actions recorded in addition to the engine's actions.
const (
	ActionFailed  = "failed"  // the clock could not be set
	ActionMonitor = "monitor" // measured only (domain computer)
)

// Entry is one line of history.jsonl.
type Entry struct {
	Time           time.Time `json:"time"`
	Trigger        string    `json:"trigger"`
	Action         string    `json:"action"`
	Reason         string    `json:"reason,omitempty"`
	OffsetMs       float64   `json:"offsetMs"`
	UncertaintyMs  float64   `json:"uncertaintyMs,omitempty"`
	Agreeing       int       `json:"agreeing"`
	Valid          int       `json:"valid"`
	Asked          int       `json:"asked"`
	LowPrecision   bool      `json:"lowPrecision,omitempty"`
	Hints          []string  `json:"hints,omitempty"`
	Error          string    `json:"error,omitempty"`
	BootTime       time.Time `json:"bootTime"`
	FirstAfterBoot bool      `json:"firstAfterBoot,omitempty"`
}

// Measured reports whether the entry holds a trusted offset.
func (e Entry) Measured() bool {
	switch e.Action {
	case "none", "slew", "step", ActionMonitor, ActionFailed:
		return true
	}
	return false
}

// Corrected reports whether the clock was adjusted after this measurement.
func (e Entry) Corrected() bool { return e.Action == "slew" || e.Action == "step" }

// The file is compacted to half its limit once it exceeds maxBytes, so
// compaction happens rarely (every few thousand entries).
const maxBytes = 1 << 20

// Store appends entries to a file. It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a store writing to path.
func NewStore(path string) *Store { return &Store{path: path} }

// Append adds one entry.
func (s *Store) Append(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	info, _ := f.Stat()
	f.Close()
	if werr != nil {
		return werr
	}
	if info != nil && info.Size() > maxBytes {
		return s.compact()
	}
	return nil
}

func (s *Store) compact() error {
	entries, err := Read(s.path, 0)
	if err != nil {
		return err
	}
	var lines [][]byte
	total := 0
	for i := len(entries) - 1; i >= 0; i-- {
		line, _ := json.Marshal(entries[i])
		if total+len(line)+1 > maxBytes/2 {
			break
		}
		total += len(line) + 1
		lines = append(lines, line)
	}
	var buf bytes.Buffer
	for i := len(lines) - 1; i >= 0; i-- {
		buf.Write(lines[i])
		buf.WriteByte('\n')
	}
	return config.WriteFileAtomic(s.path, buf.Bytes())
}

// Read returns the last n entries (all if n <= 0), oldest first. A missing
// file is not an error. Damaged lines are skipped.
func Read(path string, n int) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && !e.Time.IsZero() {
			out = append(out, e)
		}
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out, sc.Err()
}

// Stats summarizes the history for the window and the diagnostics.
type Stats struct {
	// LagSecondsPerDay: positive means the clock falls behind (runs slow),
	// negative means it runs fast. Valid only if DriftSamples >= 3.
	LagSecondsPerDay float64 `json:"lagSecondsPerDay"`
	DriftSamples     int     `json:"driftSamples"`

	// The most recent first measurement after switching on.
	LastBootOffsetMs float64   `json:"lastBootOffsetMs"`
	LastBootTime     time.Time `json:"lastBootTime,omitempty"`
	// CMOSSuspect: the clock was far off right after switching on, and not
	// in a way explained by dual boot or a time zone mix-up.
	CMOSSuspect bool `json:"cmosSuspect"`

	LastSuccess         time.Time `json:"lastSuccess,omitempty"`
	LastAttempt         time.Time `json:"lastAttempt,omitempty"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	Successes7d         int       `json:"successes7d"`
	Failures7d          int       `json:"failures7d"`
}

// CMOS threshold: switched off for a day, a healthy real-time clock is
// rarely off by more than a few seconds.
const cmosThreshold = 2 * time.Minute

// Analyze computes Stats from entries (oldest first) as of now.
func Analyze(entries []Entry, now time.Time) Stats {
	var st Stats
	weekAgo := now.Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		st.LastAttempt = e.Time
		ok := e.Measured() && e.Action != ActionFailed
		if ok {
			st.LastSuccess = e.Time
			st.ConsecutiveFailures = 0
		} else {
			st.ConsecutiveFailures++
		}
		if e.Time.After(weekAgo) {
			if ok {
				st.Successes7d++
			} else {
				st.Failures7d++
			}
		}
		if e.FirstAfterBoot && ok {
			st.LastBootOffsetMs = e.OffsetMs
			st.LastBootTime = e.BootTime
			st.CMOSSuspect = abs(e.OffsetMs) > float64(cmosThreshold/time.Millisecond) && !patternHint(e.Hints)
		}
	}

	var rates []float64
	for i := 1; i < len(entries); i++ {
		a, b := entries[i-1], entries[i]
		if !a.Measured() || !b.Measured() || a.Action == ActionFailed || b.Action == ActionFailed ||
			a.LowPrecision || b.LowPrecision || b.Trigger == TriggerResume || b.FirstAfterBoot {
			continue
		}
		if a.BootTime.Sub(b.BootTime).Abs() > 2*time.Minute {
			continue
		}
		elapsed := b.Time.Sub(a.Time)
		if elapsed < 20*time.Minute || elapsed > 48*time.Hour {
			continue
		}
		residual := a.OffsetMs
		if a.Corrected() {
			residual = 0
		}
		rates = append(rates, (b.OffsetMs-residual)/1000/elapsed.Seconds())
	}
	if len(rates) > 30 {
		rates = rates[len(rates)-30:]
	}
	st.DriftSamples = len(rates)
	if len(rates) > 0 {
		sort.Float64s(rates)
		st.LagSecondsPerDay = rates[len(rates)/2] * 86400
	}
	return st
}

func patternHint(h []string) bool {
	for _, x := range h {
		if x == "dual-boot" || x == "time-zone" {
			return true
		}
	}
	return false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
