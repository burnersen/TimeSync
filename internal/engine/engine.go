// Package engine turns a handful of server answers into one decision: how
// far off is the clock, can we trust that, and what should be done about it.
// It is pure logic without any system access, so every rule is unit tested.
package engine

import (
	"math"
	"sort"
	"time"

	"github.com/burnersen/TimeSync/internal/source"
)

// Action is what should happen to the clock.
type Action string

const (
	ActionNone   Action = "none"   // close enough, leave it alone
	ActionSlew   Action = "slew"   // run the clock slightly faster/slower
	ActionStep   Action = "step"   // set the clock directly
	ActionRefuse Action = "refuse" // answers are not trustworthy enough
)

// Reasons, as stable codes for the log, the window and the tests.
const (
	ReasonInSync         = "in-sync"
	ReasonSlew           = "slew"
	ReasonStep           = "step"
	ReasonNoAnswers      = "no-valid-answers"
	ReasonNoMajority     = "servers-disagree"
	ReasonBigJump        = "big-jump-needs-agreement"
	ReasonLowPrecisionOK = "low-precision-in-sync"
)

// Hint codes: things worth telling the user beyond the plain offset.
const (
	// The deviation equals the local UTC offset: the hardware clock is kept
	// in UTC by another operating system (dual boot with Linux) or vice versa.
	HintDualBoot = "dual-boot"
	// The deviation is a whole number of hours (or quarter hours) but does
	// not match the UTC offset: most likely a wrong time zone setting.
	HintTimeZone = "time-zone"
	// Only web servers answered: UDP port 123 seems blocked.
	HintNTPBlocked = "ntp-blocked"
	// Some servers disagreed with the rest and were ignored.
	HintOutliers = "outliers-ignored"
	// Only a single server is configured: there is no cross-check.
	HintSingleServer = "single-server"
)

// Policy holds the thresholds from the settings.
type Policy struct {
	MinCorrection time.Duration
	SlewMax       time.Duration // 0 disables slewing
	SlewAllowed   bool          // false for one-shot runs and when w32time may interfere
	BigJump       time.Duration
	// UTCOffset is the local time zone's current offset from UTC, used to
	// recognize the dual-boot pattern.
	UTCOffset time.Duration
}

// Verdict is the outcome of Evaluate.
type Verdict struct {
	Action       Action        `json:"action"`
	Reason       string        `json:"reason"`
	Offset       time.Duration `json:"offset"`
	Uncertainty  time.Duration `json:"uncertainty"`
	LowPrecision bool          `json:"lowPrecision"`
	Agreeing     int           `json:"agreeing"`
	Valid        int           `json:"valid"`
	Asked        int           `json:"asked"`
	Outliers     []string      `json:"outliers,omitempty"`
	Hints        []string      `json:"hints,omitempty"`
}

// Trusted reports whether Offset is a usable measurement.
func (v Verdict) Trusted() bool {
	return v.Action != ActionRefuse
}

// Agreement windows: answers further from the median than this (plus half
// their own round trip) are treated as broken.
const (
	ntpWindow   = 100 * time.Millisecond
	httpsWindow = 2 * time.Second
	// HTTPS dates have one second resolution; smaller offsets are noise.
	httpsMinCorrection = 2 * time.Second
)

// Evaluate combines the samples into a verdict.
//
// The rules, in order:
//  1. Only valid answers count. NTP answers win over HTTPS answers; HTTPS is
//     used only when no NTP server answered at all.
//  2. The median of the valid answers is the reference. Answers too far
//     from it are outliers.
//  3. More than half of the valid answers must agree, otherwise nothing is
//     trusted ("servers disagree").
//  4. The result is the median of the agreeing answers.
//  5. A correction larger than BigJump needs at least three agreeing
//     servers, or all of them if fewer than three were asked.
func Evaluate(samples []source.Sample, p Policy) Verdict {
	var ntpOK, httpsOK []source.Sample
	asked := 0
	for _, s := range samples {
		if s.Kind != source.KindHTTPS {
			asked++
		}
		if !s.OK() {
			continue
		}
		if s.Kind == source.KindHTTPS {
			httpsOK = append(httpsOK, s)
		} else {
			ntpOK = append(ntpOK, s)
		}
	}

	v := Verdict{Asked: asked}
	valid, window := ntpOK, ntpWindow
	if len(valid) == 0 && len(httpsOK) > 0 {
		valid, window = httpsOK, httpsWindow
		v.LowPrecision = true
		v.Asked = 0
		for _, s := range samples {
			if s.Kind == source.KindHTTPS {
				v.Asked++
			}
		}
		if asked > 0 {
			v.Hints = append(v.Hints, HintNTPBlocked)
		}
	}
	v.Valid = len(valid)
	if v.Valid == 0 {
		v.Action, v.Reason = ActionRefuse, ReasonNoAnswers
		return v
	}

	ref := median(offsets(valid))
	var agree []source.Sample
	for _, s := range valid {
		if abs(s.Offset-ref) <= window+s.RTT/2 {
			agree = append(agree, s)
		} else {
			v.Outliers = append(v.Outliers, s.Server)
		}
	}
	v.Agreeing = len(agree)
	if len(v.Outliers) > 0 {
		v.Hints = append(v.Hints, HintOutliers)
	}
	if v.Asked == 1 {
		v.Hints = append(v.Hints, HintSingleServer)
	}
	if v.Agreeing*2 <= v.Valid {
		v.Action, v.Reason = ActionRefuse, ReasonNoMajority
		return v
	}

	v.Offset = median(offsets(agree))
	v.Uncertainty = uncertainty(agree, v.Offset)
	v.Hints = append(v.Hints, patternHints(v.Offset, p.UTCOffset)...)

	required := min(3, v.Asked)
	if abs(v.Offset) > p.BigJump && v.Agreeing < required {
		v.Action, v.Reason = ActionRefuse, ReasonBigJump
		return v
	}

	minCorr := p.MinCorrection
	if v.LowPrecision && minCorr < httpsMinCorrection {
		minCorr = httpsMinCorrection
	}
	switch {
	case abs(v.Offset) < minCorr:
		v.Action, v.Reason = ActionNone, ReasonInSync
		if v.LowPrecision {
			v.Reason = ReasonLowPrecisionOK
		}
	case p.SlewAllowed && !v.LowPrecision && p.SlewMax > 0 && abs(v.Offset) <= p.SlewMax:
		v.Action, v.Reason = ActionSlew, ReasonSlew
	default:
		v.Action, v.Reason = ActionStep, ReasonStep
	}
	return v
}

// patternHints recognizes offsets that are not clock drift at all.
func patternHints(offset, utcOffset time.Duration) []string {
	const tolerance = 90 * time.Second
	a := abs(offset)
	if a < 25*time.Minute {
		return nil
	}
	if utcOffset != 0 && (abs(offset-utcOffset) <= tolerance || abs(offset+utcOffset) <= tolerance) {
		return []string{HintDualBoot}
	}
	quarter := 15 * time.Minute
	rest := a % quarter
	if rest <= tolerance || quarter-rest <= tolerance {
		return []string{HintTimeZone}
	}
	return nil
}

func uncertainty(agree []source.Sample, center time.Duration) time.Duration {
	var u time.Duration
	for _, s := range agree {
		e := s.RTT/2 + abs(s.Offset-center)
		if s.Kind == source.KindHTTPS {
			e += 500 * time.Millisecond
		}
		u = max(u, e)
	}
	return u
}

func offsets(ss []source.Sample) []time.Duration {
	out := make([]time.Duration, len(ss))
	for i, s := range ss {
		out[i] = s.Offset
	}
	return out
}

func median(d []time.Duration) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// SlewPlan says how to correct an offset gently.
type SlewPlan struct {
	// RatePPM is the speed change in parts per million: +500 means the clock
	// runs 0.05 % fast until the offset is gone.
	RatePPM  float64
	Duration time.Duration
}

// MaxSlewPPM is the speed change used for gentle corrections. 500 ppm is
// what the reference NTP implementation allows; nobody notices it.
const MaxSlewPPM = 500.0

// PlanSlew computes a gentle correction. ok is false when it would take
// longer than maxDuration; the caller should step instead.
func PlanSlew(offset, maxDuration time.Duration) (plan SlewPlan, ok bool) {
	if offset == 0 {
		return SlewPlan{}, false
	}
	seconds := math.Abs(offset.Seconds()) / (MaxSlewPPM * 1e-6)
	d := time.Duration(seconds * float64(time.Second))
	if d > maxDuration {
		return SlewPlan{}, false
	}
	rate := MaxSlewPPM
	if offset < 0 {
		rate = -rate
	}
	return SlewPlan{RatePPM: rate, Duration: d}, true
}
