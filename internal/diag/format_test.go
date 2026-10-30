package diag

import (
	"testing"
	"time"
)

func TestFormatOffset(t *testing.T) {
	cases := map[time.Duration]string{
		12 * time.Millisecond:            "+12 ms",
		-3400 * time.Millisecond:         "-3.4 s",
		2*time.Hour + 5*time.Minute:      "+2 h 05 min",
		-(4*time.Minute + 7*time.Second): "-4 min 07 s",
		400 * 24 * time.Hour:             "+400.0 days",
		0:                                "+0 ms",
	}
	for d, want := range cases {
		if got := FormatOffset(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
