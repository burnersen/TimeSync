package engine

import (
	"slices"
	"testing"
	"time"

	"github.com/burnersen/TimeSync/internal/source"
)

const ms = time.Millisecond

var policy = Policy{
	MinCorrection: 2 * ms,
	SlewMax:       500 * ms,
	SlewAllowed:   true,
	BigJump:       15 * time.Minute,
	UTCOffset:     time.Hour, // Berlin, winter time
}

func ntpOK(name string, off time.Duration) source.Sample {
	return source.Sample{Server: name, Kind: source.KindNTP, Offset: off, RTT: 20 * ms}
}

func ntpErr(name string) source.Sample {
	return source.Sample{Server: name, Kind: source.KindNTP, Err: "timeout", ErrKind: source.ErrTimeout}
}

func httpsOK(name string, off time.Duration) source.Sample {
	return source.Sample{Server: name, Kind: source.KindHTTPS, Offset: off, RTT: 200 * ms}
}

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name     string
		samples  []source.Sample
		policy   Policy
		action   Action
		reason   string
		offset   time.Duration
		hints    []string
		outliers int
	}{
		{"all agree, tiny offset",
			[]source.Sample{ntpOK("a", 1*ms), ntpOK("b", 0), ntpOK("c", -1*ms), ntpOK("d", 1*ms)},
			policy, ActionNone, ReasonInSync, 500 * time.Microsecond, nil, 0},
		{"small offset is slewed",
			[]source.Sample{ntpOK("a", 120*ms), ntpOK("b", 118*ms), ntpOK("c", 121*ms), ntpOK("d", 119*ms)},
			policy, ActionSlew, ReasonSlew, 119500 * time.Microsecond, nil, 0},
		{"small offset is stepped when slewing is not allowed",
			[]source.Sample{ntpOK("a", 120*ms), ntpOK("b", 120*ms), ntpOK("c", 120*ms)},
			Policy{MinCorrection: 2 * ms, SlewMax: 500 * ms, BigJump: 15 * time.Minute}, ActionStep, ReasonStep, 120 * ms, nil, 0},
		{"medium offset is stepped",
			[]source.Sample{ntpOK("a", 3*time.Second), ntpOK("b", 3*time.Second), ntpOK("c", 3*time.Second)},
			policy, ActionStep, ReasonStep, 3 * time.Second, nil, 0},
		{"one liar is outvoted (the 2036 server)",
			[]source.Sample{ntpOK("a", 5*ms), ntpOK("b", 6*ms), ntpOK("liar", 81822*time.Hour), ntpOK("d", 4*ms)},
			policy, ActionSlew, ReasonSlew, 5 * ms, []string{HintOutliers}, 1},
		{"two against two: nobody is trusted",
			[]source.Sample{ntpOK("a", 0), ntpOK("b", 0), ntpOK("c", time.Hour*5), ntpOK("d", time.Hour*5)},
			policy, ActionRefuse, ReasonNoMajority, 0, []string{HintOutliers}, 0},
		{"big jump with three agreeing servers is allowed (dead CMOS battery)",
			[]source.Sample{ntpOK("a", 400*24*time.Hour), ntpOK("b", 400*24*time.Hour), ntpOK("c", 400*24*time.Hour), ntpErr("d")},
			policy, ActionStep, ReasonStep, 400 * 24 * time.Hour, nil, 0},
		{"big jump with only two answers out of four is refused",
			[]source.Sample{ntpOK("a", 2*time.Hour+7*time.Minute), ntpOK("b", 2*time.Hour+7*time.Minute), ntpErr("c"), ntpErr("d")},
			policy, ActionRefuse, ReasonBigJump, 2*time.Hour + 7*time.Minute, nil, 0},
		{"single configured server may jump (user's choice) but is flagged",
			[]source.Sample{ntpOK("only", 2*time.Hour+7*time.Minute)},
			policy, ActionStep, ReasonStep, 2*time.Hour + 7*time.Minute, []string{HintSingleServer}, 0},
		{"no answers at all",
			[]source.Sample{ntpErr("a"), ntpErr("b")},
			policy, ActionRefuse, ReasonNoAnswers, 0, nil, 0},
		{"dual boot: exactly the UTC offset",
			[]source.Sample{ntpOK("a", time.Hour+20*ms), ntpOK("b", time.Hour+21*ms), ntpOK("c", time.Hour+19*ms)},
			policy, ActionStep, ReasonStep, time.Hour + 20*ms, []string{HintDualBoot}, 0},
		{"dual boot the other way round",
			[]source.Sample{ntpOK("a", -time.Hour), ntpOK("b", -time.Hour), ntpOK("c", -time.Hour)},
			policy, ActionStep, ReasonStep, -time.Hour, []string{HintDualBoot}, 0},
		{"whole hours but not the UTC offset: time zone",
			[]source.Sample{ntpOK("a", -6*time.Hour), ntpOK("b", -6*time.Hour), ntpOK("c", -6*time.Hour)},
			policy, ActionStep, ReasonStep, -6 * time.Hour, []string{HintTimeZone}, 0},
		{"NTP blocked, HTTPS answers within a second: leave it",
			[]source.Sample{ntpErr("a"), ntpErr("b"), httpsOK("w1", 600*ms), httpsOK("w2", -200*ms), httpsOK("w3", -150*ms)},
			policy, ActionNone, ReasonLowPrecisionOK, -150 * ms, []string{HintNTPBlocked}, 0},
		{"NTP blocked, HTTPS shows 40 s: step, never slew",
			[]source.Sample{ntpErr("a"), httpsOK("w1", 40*time.Second), httpsOK("w2", 40*time.Second+300*ms), httpsOK("w3", 39*time.Second+900*ms)},
			policy, ActionStep, ReasonStep, 40 * time.Second, []string{HintNTPBlocked}, 0},
		{"NTP answers win over HTTPS",
			[]source.Sample{ntpOK("a", 10*ms), ntpOK("b", 10*ms), ntpOK("c", 10*ms), httpsOK("w", time.Minute)},
			policy, ActionSlew, ReasonSlew, 10 * ms, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Evaluate(c.samples, c.policy)
			if v.Action != c.action || v.Reason != c.reason {
				t.Fatalf("got %s/%s, want %s/%s (%+v)", v.Action, v.Reason, c.action, c.reason, v)
			}
			if v.Trusted() && v.Offset != c.offset {
				t.Errorf("offset %v, want %v", v.Offset, c.offset)
			}
			for _, h := range c.hints {
				if !slices.Contains(v.Hints, h) {
					t.Errorf("hint %q missing in %v", h, v.Hints)
				}
			}
			if c.outliers > 0 && len(v.Outliers) != c.outliers {
				t.Errorf("outliers %v, want %d", v.Outliers, c.outliers)
			}
		})
	}
}

func TestPlanSlew(t *testing.T) {
	p, ok := PlanSlew(100*ms, time.Hour)
	if !ok || p.RatePPM != MaxSlewPPM || p.Duration != 200*time.Second {
		t.Fatalf("100 ms: %+v %v", p, ok)
	}
	p, ok = PlanSlew(-50*ms, time.Hour)
	if !ok || p.RatePPM != -MaxSlewPPM || p.Duration != 100*time.Second {
		t.Fatalf("-50 ms: %+v %v", p, ok)
	}
	if _, ok := PlanSlew(500*ms, 5*time.Minute); ok {
		t.Fatal("500 ms takes 1000 s and must not fit into 5 minutes")
	}
	if _, ok := PlanSlew(0, time.Hour); ok {
		t.Fatal("zero offset needs no plan")
	}
}
