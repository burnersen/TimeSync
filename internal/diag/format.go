// Package diag builds texts for people: history lines and the diagnostics
// report users can attach to a support request.
package diag

import (
	"fmt"
	"strings"
	"time"

	"github.com/burnersen/TimeSync/internal/history"
	"github.com/burnersen/TimeSync/internal/source"
)

// FormatOffset prints an offset the way people read it: "+12 ms",
// "-3.4 s", "+2 h 05 min".
func FormatOffset(d time.Duration) string {
	sign := "+"
	if d < 0 {
		sign, d = "-", -d
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%s%d ms", sign, d.Round(time.Millisecond)/time.Millisecond)
	case d < time.Minute:
		return fmt.Sprintf("%s%.1f s", sign, d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%s%d min %02d s", sign, int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%s%d h %02d min", sign, int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%s%.1f days", sign, d.Hours()/24)
}

// FormatEntry renders one history entry as a single line.
func FormatEntry(e history.Entry) string {
	off := FormatOffset(time.Duration(e.OffsetMs * float64(time.Millisecond)))
	line := fmt.Sprintf("%s  %-8s %-8s %12s  %d/%d", e.Time.Local().Format("2006-01-02 15:04:05"), e.Trigger, e.Action, off, e.Agreeing, e.Asked)
	if e.LowPrecision {
		line += "  web-time"
	}
	if len(e.Hints) > 0 {
		line += "  hints:" + strings.Join(e.Hints, ",")
	}
	if e.Error != "" {
		line += "  " + e.Error
	}
	return line
}

// FormatSample renders one server answer.
func FormatSample(s source.Sample) string {
	if !s.OK() {
		return fmt.Sprintf("%-28s %-5s %-22s FAILED (%s) %s", s.Server, s.Kind, s.Address, s.ErrKind, s.Err)
	}
	if s.Kind == source.KindHTTPS {
		return fmt.Sprintf("%-28s %-5s %-22s %12s  rtt %4d ms  (web time)", s.Server, s.Kind, "", FormatOffset(s.Offset), s.RTT.Milliseconds())
	}
	return fmt.Sprintf("%-28s %-5s %-22s %12s  rtt %4d ms  stratum %d  ref %s",
		s.Server, s.Kind, s.Address, FormatOffset(s.Offset), s.RTT.Milliseconds(), s.Stratum, s.RefID)
}
