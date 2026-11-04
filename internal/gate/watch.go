package gate

import (
	"context"
	"fmt"
	"time"

	"github.com/YashSejani/Rehearse/internal/collector"
)

// Health is one look at the target after a change.
type Health struct {
	Available  bool    // all desired replicas available
	ErrorRatio float64 // worst error ratio over the target's endpoints
	Issues     int     // unhealthy objects in the namespace
}

// Prober measures the target's health (collector-based in production, fake in tests).
type Prober interface {
	Probe(ctx context.Context, deployment string) (Health, error)
}

// CollectorProber uses the read-only collector: status + endpoint metrics over 1 minute.
type CollectorProber struct{ C *collector.Collector }

func (p CollectorProber) Probe(ctx context.Context, dep string) (Health, error) {
	m, err := p.C.Metrics(ctx, dep, time.Minute)
	if err != nil {
		return Health{}, err
	}
	issues, err := p.C.Status(ctx)
	if err != nil {
		return Health{}, err
	}
	h := Health{Available: m.DesiredReplicas > 0 && m.AvailableReplicas >= m.DesiredReplicas, Issues: len(issues)}
	for _, e := range m.Endpoints {
		if e.ErrorRatio > h.ErrorRatio {
			h.ErrorRatio = e.ErrorRatio
		}
	}
	return h, nil
}

// WatchConfig: how long and how strictly to watch after applying.
type WatchConfig struct {
	Settle   time.Duration // ignore the first part: pods restart, CrashLoopBackOff back-off
	Duration time.Duration // total watch time after the settle window
	Interval time.Duration
	// Worse means: target unavailable, or error ratio above baseline + Margin,
	// for Consecutive checks in a row (one bad sample is not enough).
	Baseline    float64
	Margin      float64
	Consecutive int
}

func DefaultWatch() WatchConfig {
	return WatchConfig{Settle: 45 * time.Second, Duration: 2 * time.Minute, Interval: 10 * time.Second, Margin: 0.05, Consecutive: 3}
}

// Watch returns nil if the target stayed healthy enough, or an error saying why it is
// worse. Like AIOpsLab's mitigation check, it judges the live cluster after a bounded
// settle window rather than one instant check.
func Watch(ctx context.Context, p Prober, dep string, cfg WatchConfig, sleep func(time.Duration)) error {
	if sleep == nil {
		sleep = time.Sleep
	}
	sleep(cfg.Settle)
	bad, checks := 0, int(cfg.Duration/cfg.Interval)
	if checks < cfg.Consecutive {
		checks = cfg.Consecutive
	}
	var last string
	for i := 0; i < checks; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, err := p.Probe(ctx, dep)
		switch {
		case err != nil:
			bad, last = bad+1, "probe failed: "+err.Error()
		case !h.Available:
			bad, last = bad+1, "deployment not available"
		case h.ErrorRatio > cfg.Baseline+cfg.Margin:
			bad, last = bad+1, fmt.Sprintf("error ratio %.0f%% above baseline %.0f%%", h.ErrorRatio*100, cfg.Baseline*100)
		default:
			bad = 0
		}
		if bad >= cfg.Consecutive {
			return fmt.Errorf("worse than baseline for %d checks in a row: %s", bad, last)
		}
		if i < checks-1 {
			sleep(cfg.Interval)
		}
	}
	return nil
}
