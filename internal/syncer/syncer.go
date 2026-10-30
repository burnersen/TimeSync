// Package syncer runs one synchronization round: ask the servers, let the
// engine decide, adjust the clock, and describe what happened. The service,
// the command line tool and the window all use this same code path.
package syncer

import (
	"context"
	"errors"
	"time"

	"github.com/burnersen/TimeSync/internal/config"
	"github.com/burnersen/TimeSync/internal/engine"
	"github.com/burnersen/TimeSync/internal/history"
	"github.com/burnersen/TimeSync/internal/source"
)

// Clock changes the system time. The Windows implementation lives in
// package winsys; tests use a fake.
type Clock interface {
	Step(offset time.Duration) error
	Slew(plan engine.SlewPlan) error
}

// ErrSlewUnsupported is returned by Clock.Slew when gentle correction is not
// possible; Run then steps instead.
var ErrSlewUnsupported = errors.New("gentle correction not supported")

// Queries are the network functions; tests replace them.
type Queries struct {
	NTP   func(ctx context.Context, servers []string, opt source.Options) []source.Sample
	HTTPS func(ctx context.Context, urls []string, opt source.Options) []source.Sample
}

// DefaultQueries asks real servers.
var DefaultQueries = Queries{NTP: source.QueryAll, HTTPS: source.QueryHTTPS}

// Request describes one round.
type Request struct {
	Config  config.Config
	Trigger string
	// Apply false only measures (no admin rights needed).
	Apply bool
	// MonitorOnly measures and records, but never touches the clock
	// (domain computers unless explicitly allowed).
	MonitorOnly bool
	// SlewAllowed: only the service can slew, because the correction has to
	// be ended later by the same process.
	SlewAllowed     bool
	MaxSlewDuration time.Duration
	// Skip lists servers that recently sent a kiss-of-death.
	Skip map[string]bool

	BootTime       time.Time
	FirstAfterBoot bool
	UTCOffset      time.Duration
	Now            func() time.Time
	Timeout        time.Duration
}

// Result is what happened, for the log, the window and "--json".
type Result struct {
	Samples []source.Sample `json:"samples"`
	Verdict engine.Verdict  `json:"verdict"`
	// Applied is "none", "slew", "step", "refuse", "failed", "monitor" or
	// "check" (measured only).
	Applied string           `json:"applied"`
	Slew    *engine.SlewPlan `json:"slew,omitempty"`
	Error   string           `json:"error,omitempty"`
	Entry   history.Entry    `json:"entry"`
	// KissCodes maps servers that asked to be left alone to their code.
	KissCodes map[string]string `json:"kissCodes,omitempty"`
}

// Run performs one round.
func Run(ctx context.Context, req Request, clock Clock, q Queries) Result {
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	cfg := req.Config
	servers := make([]string, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if !req.Skip[s] {
			servers = append(servers, s)
		}
	}
	if len(servers) == 0 {
		servers = cfg.Servers
	}

	opt := source.Options{Timeout: req.Timeout}
	samples := q.NTP(ctx, servers, opt)
	anyNTP := false
	for _, s := range samples {
		anyNTP = anyNTP || s.OK()
	}
	if !anyNTP && cfg.HTTPSFallback && len(cfg.HTTPSFallbackURLs) > 0 && q.HTTPS != nil {
		samples = append(samples, q.HTTPS(ctx, cfg.HTTPSFallbackURLs, opt)...)
	}

	policy := engine.Policy{
		MinCorrection: time.Duration(cfg.MinCorrectionMs) * time.Millisecond,
		SlewMax:       time.Duration(cfg.SlewMaxMs) * time.Millisecond,
		SlewAllowed:   req.SlewAllowed,
		BigJump:       time.Duration(cfg.BigJumpMinutes) * time.Minute,
		UTCOffset:     req.UTCOffset,
	}
	v := engine.Evaluate(samples, policy)

	res := Result{Samples: samples, Verdict: v}
	for _, s := range samples {
		if s.KissCode != "" {
			if res.KissCodes == nil {
				res.KissCodes = map[string]string{}
			}
			res.KissCodes[s.Server] = s.KissCode
		}
	}

	switch {
	case !req.Apply:
		res.Applied = "check"
	case v.Action == engine.ActionRefuse:
		res.Applied = string(engine.ActionRefuse)
	case req.MonitorOnly:
		res.Applied = history.ActionMonitor
	case v.Action == engine.ActionNone:
		res.Applied = string(engine.ActionNone)
	case v.Action == engine.ActionSlew:
		plan, ok := engine.PlanSlew(v.Offset, req.MaxSlewDuration)
		if ok {
			if err := clock.Slew(plan); err == nil {
				res.Applied, res.Slew = "slew", &plan
				break
			}
		}
		res.Applied, res.Error = step(clock, v.Offset)
	case v.Action == engine.ActionStep:
		res.Applied, res.Error = step(clock, v.Offset)
	}

	res.Entry = history.Entry{
		Time:           now(),
		Trigger:        req.Trigger,
		Action:         res.Applied,
		Reason:         v.Reason,
		OffsetMs:       float64(v.Offset) / float64(time.Millisecond),
		UncertaintyMs:  float64(v.Uncertainty) / float64(time.Millisecond),
		Agreeing:       v.Agreeing,
		Valid:          v.Valid,
		Asked:          v.Asked,
		LowPrecision:   v.LowPrecision,
		Hints:          v.Hints,
		Error:          res.Error,
		BootTime:       req.BootTime,
		FirstAfterBoot: req.FirstAfterBoot,
	}
	if res.Entry.Action == string(engine.ActionRefuse) && res.Entry.Error == "" {
		res.Entry.Error = FirstError(samples)
	}
	return res
}

func step(clock Clock, offset time.Duration) (string, string) {
	if err := clock.Step(offset); err != nil {
		return history.ActionFailed, err.Error()
	}
	return "step", ""
}

// FirstError returns the most telling error among the samples, preferring
// the most common error class.
func FirstError(samples []source.Sample) string {
	count := map[string]int{}
	first := map[string]string{}
	best := ""
	for _, s := range samples {
		if s.OK() {
			continue
		}
		count[s.ErrKind]++
		if _, ok := first[s.ErrKind]; !ok {
			first[s.ErrKind] = s.Server + ": " + s.Err
		}
		if best == "" || count[s.ErrKind] > count[best] {
			best = s.ErrKind
		}
	}
	return first[best]
}
