package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/engine"
	"github.com/burnersen/TimeSync/internal/source"
)

type fakeClock struct {
	steps   []time.Duration
	slews   []engine.SlewPlan
	stepErr error
	slewErr error
}

func (c *fakeClock) Step(d time.Duration) error {
	if c.stepErr != nil {
		return c.stepErr
	}
	c.steps = append(c.steps, d)
	return nil
}

func (c *fakeClock) Slew(p engine.SlewPlan) error {
	if c.slewErr != nil {
		return c.slewErr
	}
	c.slews = append(c.slews, p)
	return nil
}

func answers(offset time.Duration, n int) Queries {
	return Queries{
		NTP: func(_ context.Context, servers []string, _ source.Options) []source.Sample {
			var out []source.Sample
			for i, s := range servers {
				if i < n {
					out = append(out, source.Sample{Server: s, Kind: source.KindNTP, Offset: offset, RTT: 10 * time.Millisecond})
				} else {
					out = append(out, source.Sample{Server: s, Kind: source.KindNTP, Err: "timeout", ErrKind: source.ErrTimeout})
				}
			}
			return out
		},
		HTTPS: func(_ context.Context, urls []string, _ source.Options) []source.Sample {
			var out []source.Sample
			for _, u := range urls {
				out = append(out, source.Sample{Server: u, Kind: source.KindHTTPS, Offset: offset, RTT: 100 * time.Millisecond})
			}
			return out
		},
	}
}

func request() Request {
	return Request{Config: config.Default(), Trigger: "manual", Apply: true, SlewAllowed: true, MaxSlewDuration: time.Hour}
}

func TestStepAndSlew(t *testing.T) {
	c := &fakeClock{}
	r := Run(context.Background(), request(), c, answers(3*time.Second, 4))
	if r.Applied != "step" || len(c.steps) != 1 || c.steps[0] != 3*time.Second {
		t.Fatalf("step: %+v %+v", r, c)
	}
	c = &fakeClock{}
	r = Run(context.Background(), request(), c, answers(100*time.Millisecond, 4))
	if r.Applied != "slew" || len(c.slews) != 1 || r.Entry.Action != "slew" {
		t.Fatalf("slew: %+v", r)
	}
}

func TestSlewFallsBackToStep(t *testing.T) {
	c := &fakeClock{slewErr: ErrSlewUnsupported}
	r := Run(context.Background(), request(), c, answers(100*time.Millisecond, 4))
	if r.Applied != "step" || len(c.steps) != 1 {
		t.Fatalf("%+v", r)
	}
	req := request()
	req.MaxSlewDuration = time.Minute // 100 ms needs 200 s
	c = &fakeClock{}
	if r := Run(context.Background(), req, c, answers(100*time.Millisecond, 4)); r.Applied != "step" {
		t.Fatalf("too long a slew must step: %+v", r)
	}
}

func TestCheckAndMonitorNeverTouchTheClock(t *testing.T) {
	c := &fakeClock{}
	req := request()
	req.Apply = false
	if r := Run(context.Background(), req, c, answers(time.Minute, 4)); r.Applied != "check" {
		t.Fatalf("%+v", r)
	}
	req = request()
	req.MonitorOnly = true
	if r := Run(context.Background(), req, c, answers(time.Minute, 4)); r.Applied != "monitor" {
		t.Fatalf("%+v", r)
	}
	if len(c.steps)+len(c.slews) != 0 {
		t.Fatal("clock touched")
	}
}

func TestFailuresAreRecorded(t *testing.T) {
	c := &fakeClock{stepErr: errors.New("A required privilege is not held by the client.")}
	r := Run(context.Background(), request(), c, answers(5*time.Second, 4))
	if r.Applied != "failed" || r.Entry.Error == "" {
		t.Fatalf("%+v", r)
	}
	cfg := config.Default()
	cfg.HTTPSFallback = false
	req := request()
	req.Config = cfg
	r = Run(context.Background(), req, c, answers(0, 0))
	if r.Applied != "refuse" || r.Entry.Error == "" {
		t.Fatalf("no answers: %+v", r)
	}
}

func TestHTTPSFallbackOnlyWhenNTPIsSilent(t *testing.T) {
	c := &fakeClock{}
	r := Run(context.Background(), request(), c, answers(10*time.Second, 0))
	if !r.Verdict.LowPrecision || r.Applied != "step" {
		t.Fatalf("%+v", r.Verdict)
	}
	r = Run(context.Background(), request(), c, answers(10*time.Millisecond, 4))
	for _, s := range r.Samples {
		if s.Kind == source.KindHTTPS {
			t.Fatal("HTTPS asked although NTP answered")
		}
	}
}

func TestSkippedServers(t *testing.T) {
	req := request()
	req.Skip = map[string]bool{config.DefaultServers[0]: true}
	r := Run(context.Background(), req, &fakeClock{}, answers(0, 4))
	if len(r.Samples) != 3 {
		t.Fatalf("skipped server was asked: %d samples", len(r.Samples))
	}
}
