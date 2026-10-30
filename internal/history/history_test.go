package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var boot = time.Date(2026, 10, 30, 7, 0, 0, 0, time.UTC)

func entry(at time.Duration, action string, offsetMs float64) Entry {
	return Entry{Time: boot.Add(at), Trigger: TriggerInterval, Action: action, OffsetMs: offsetMs, BootTime: boot, Agreeing: 4, Valid: 4, Asked: 4}
}

func TestStoreAppendRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history.jsonl")
	s := NewStore(path)
	for i := range 5 {
		if err := s.Append(entry(time.Duration(i)*time.Hour, "none", float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("garbage line\n")
	f.Close()
	got, err := Read(path, 3)
	if err != nil || len(got) != 3 || got[2].OffsetMs != 4 {
		t.Fatalf("%v %v", got, err)
	}
	none, err := Read(filepath.Join(t.TempDir(), "missing"), 0)
	if err != nil || none != nil {
		t.Fatal("missing file must be empty, not an error")
	}
}

func TestStoreCompacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s := NewStore(path)
	e := entry(0, "none", 1)
	e.Error = strings.Repeat("x", 600)
	for range 2100 {
		s.Append(e)
	}
	info, _ := os.Stat(path)
	if info.Size() > maxBytes {
		t.Fatalf("file not compacted: %d bytes", info.Size())
	}
}

func TestDrift(t *testing.T) {
	// The clock loses 1 ms per minute = 1.44 s per day. Every hour the
	// service measures about 60 ms and steps it away.
	var es []Entry
	es = append(es, entry(0, "step", 3000))
	es[0].FirstAfterBoot = true
	es[0].Trigger = TriggerStart
	for h := 1; h <= 6; h++ {
		es = append(es, entry(time.Duration(h)*time.Hour, "slew", 60))
	}
	st := Analyze(es, boot.Add(7*time.Hour))
	if st.DriftSamples != 6 {
		t.Fatalf("samples %d", st.DriftSamples)
	}
	if fmt.Sprintf("%.2f", st.LagSecondsPerDay) != "1.44" {
		t.Fatalf("lag %.4f s/day", st.LagSecondsPerDay)
	}
	if st.CMOSSuspect {
		t.Fatal("3 s after boot is no CMOS problem")
	}
}

func TestDriftIgnoresSleepAndUncorrected(t *testing.T) {
	es := []Entry{
		entry(0, "none", 1),
		entry(time.Hour, "none", 31), // not corrected before: 30 ms per hour
	}
	resume := entry(10*time.Hour, "step", 5000)
	resume.Trigger = TriggerResume
	es = append(es, resume)
	st := Analyze(es, boot.Add(11*time.Hour))
	if st.DriftSamples != 1 || fmt.Sprintf("%.2f", st.LagSecondsPerDay) != "0.72" {
		t.Fatalf("%+v", st)
	}
}

func TestCMOS(t *testing.T) {
	first := entry(0, "step", -3*60*1000)
	first.FirstAfterBoot = true
	st := Analyze([]Entry{first}, boot.Add(time.Minute))
	if !st.CMOSSuspect {
		t.Fatal("3 minutes off after switching on should raise the CMOS hint")
	}
	first.Hints = []string{"dual-boot"}
	first.OffsetMs = 3600 * 1000
	if Analyze([]Entry{first}, boot).CMOSSuspect {
		t.Fatal("a dual-boot hour is not a battery problem")
	}
}

func TestFailureCounting(t *testing.T) {
	fail := entry(time.Hour, "refuse", 0)
	fail.Reason = "no-valid-answers"
	es := []Entry{entry(0, "none", 1), fail, fail}
	st := Analyze(es, boot.Add(2*time.Hour))
	if st.ConsecutiveFailures != 2 || st.Successes7d != 1 || st.Failures7d != 2 || !st.LastSuccess.Equal(boot) {
		t.Fatalf("%+v", st)
	}
}

func TestLogFileRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timesync.log")
	l := &LogFile{Path: path, MaxSize: 100}
	for range 10 {
		l.Write([]byte(strings.Repeat("a", 30) + "\n"))
	}
	info, _ := os.Stat(path)
	if info.Size() > 100 {
		t.Fatalf("log not rotated: %d", info.Size())
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatal("no .old file")
	}
}
