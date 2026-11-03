package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/YashSejani/Rehearse/internal/collector"
)

// Tools is what the agent may call: the read-only collector functions. An interface so
// tests can use canned results instead of a cluster.
type Tools interface {
	Status(ctx context.Context) ([]collector.PodIssue, error)
	Logs(ctx context.Context, service string, lines int, window time.Duration) (*collector.LogLines, error)
	Metrics(ctx context.Context, service string, window time.Duration) (*collector.ServiceMetrics, error)
	Changes(ctx context.Context, service string) ([]collector.ChangeEvent, error)
}

// Facts is the deterministic evidence collected before the model is asked anything:
// cheap, reliable, always the same shape.
type Facts struct {
	Target      string                    `json:"target"`
	CollectedAt time.Time                 `json:"collected_at"`
	Issues      []collector.PodIssue      `json:"issues"` // whole namespace: the cause may be a dependency
	Logs        *collector.LogLines       `json:"logs,omitempty"`
	Metrics     *collector.ServiceMetrics `json:"metrics,omitempty"`
	Changes     []collector.ChangeEvent   `json:"changes,omitempty"`
	Errors      []string                  `json:"errors,omitempty"` // tools that failed (shown to the model)
}

// GatherFacts runs every collector tool for the target deployment. A failing tool does not
// stop the others; its error is recorded so the model knows the evidence is incomplete.
func GatherFacts(ctx context.Context, t Tools, target string) *Facts {
	f := &Facts{Target: target, CollectedAt: time.Now()}
	var err error
	if f.Issues, err = t.Status(ctx); err != nil {
		f.Errors = append(f.Errors, "status: "+err.Error())
	}
	if f.Logs, err = t.Logs(ctx, target, 15, RecentWindow); err != nil {
		f.Errors = append(f.Errors, "logs: "+err.Error())
	}
	if f.Metrics, err = t.Metrics(ctx, target, MetricsWindow); err != nil {
		f.Errors = append(f.Errors, "metrics: "+err.Error())
	}
	var changes []collector.ChangeEvent
	if changes, err = t.Changes(ctx, target); err != nil {
		f.Errors = append(f.Errors, "changes: "+err.Error())
	}
	f.Changes = recentChanges(changes, f.CollectedAt)
	return f
}

// Facts look at the recent past only. Older events (an incident that was already fixed)
// would make the model raise a false alarm on a healthy deployment.
const (
	RecentWindow  = 10 * time.Minute
	MetricsWindow = 2 * time.Minute
	maxRollouts   = 3
)

// recentChanges keeps the current spec, the last few rollouts, and events of the last
// RecentWindow.
func recentChanges(all []collector.ChangeEvent, now time.Time) []collector.ChangeEvent {
	var out []collector.ChangeEvent
	rollouts := 0
	for _, c := range all {
		switch c.Kind {
		case "Current":
		case "Rollout":
			if rollouts++; rollouts > maxRollouts {
				continue
			}
		default:
			if now.Sub(c.Time) > RecentWindow {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// Render writes the facts as compact text for the prompt. Short lines with stable labels
// cost few tokens and are easy to quote as evidence.
func (f *Facts) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "TARGET deployment: %s\n", f.Target)
	fmt.Fprintf(&b, "CURRENT STATE: %s\n", f.HealthLine())

	b.WriteString("\n## Unhealthy pods and deployments in the namespace\n")
	if len(f.Issues) == 0 {
		b.WriteString("none: every pod is Running and Ready, every deployment has all replicas\n")
	}
	for _, is := range f.Issues {
		fmt.Fprintf(&b, "- %s %s (service %s): %s", is.Kind, is.Name, is.Service, is.Reason)
		if is.Restarts > 0 {
			fmt.Fprintf(&b, ", restarts %d", is.Restarts)
		}
		if is.Message != "" {
			fmt.Fprintf(&b, ", %s", is.Message)
		}
		b.WriteString("\n")
	}

	if m := f.Metrics; m != nil {
		fmt.Fprintf(&b, "\n## Metrics of %s (last %s)\n", m.Service, m.Window)
		fmt.Fprintf(&b, "replicas %.0f desired, %.0f available; restarts %.0f; memory %.1f MiB", m.DesiredReplicas, m.AvailableReplicas, m.Restarts, m.MemoryMiB)
		if m.MemoryLimitMiB > 0 {
			fmt.Fprintf(&b, " of limit %.0f MiB", m.MemoryLimitMiB)
		} else {
			b.WriteString(" (no memory limit)")
		}
		if m.LastTerminated != "" {
			fmt.Fprintf(&b, "; last terminated reason %s", m.LastTerminated)
		}
		b.WriteString("\n")
		for _, e := range m.Endpoints {
			fmt.Fprintf(&b, "endpoint /%s: %.2f req/s, error ratio %.0f%%, p95 %.1f ms\n", e.Endpoint, e.RatePerSec, e.ErrorRatio*100, e.P95Ms)
		}
	}

	if len(f.Changes) > 0 {
		fmt.Fprintf(&b, "\n## Recent changes to %s (newest first; events are history and may already be resolved)\n", f.Target)
		for _, c := range f.Changes {
			fmt.Fprintf(&b, "- [%s] %s", c.Kind, c.Summary)
			if c.Count > 1 {
				fmt.Fprintf(&b, " (x%d)", c.Count)
			}
			if c.Kind != "Current" {
				fmt.Fprintf(&b, " (%s ago)", f.CollectedAt.Sub(c.Time).Round(time.Second))
			}
			b.WriteString("\n")
		}
	}

	if f.Logs != nil {
		fmt.Fprintf(&b, "\n## Error logs and events of %s (last %s, deduplicated)\n", f.Logs.Service, f.Logs.Window)
		shown := 0
		for _, l := range f.Logs.Lines {
			if l.Source == "event" && f.inChanges(l.Text) {
				continue // already listed under changes; don't spend tokens twice
			}
			fmt.Fprintf(&b, "- [%s x%d] %s\n", l.Source, l.Count, l.Text)
			shown++
		}
		if shown == 0 {
			b.WriteString("no error lines (other than the events above)\n")
		}
	}
	for _, e := range f.Errors {
		fmt.Fprintf(&b, "\n(tool error: %s)\n", e)
	}
	return b.String()
}

func (f *Facts) inChanges(text string) bool {
	key := text
	if len(key) > 60 {
		key = key[:60]
	}
	for _, c := range f.Changes {
		if strings.Contains(c.Summary, key) {
			return true
		}
	}
	return false
}

// Healthy is a deterministic check: no unhealthy objects in the namespace and no
// errors on the target's endpoints right now.
func (f *Facts) Healthy() bool {
	if len(f.Issues) > 0 || len(f.Errors) > 0 {
		return false
	}
	if m := f.Metrics; m != nil {
		if m.AvailableReplicas < m.DesiredReplicas || m.DesiredReplicas == 0 {
			return false
		}
		for _, e := range m.Endpoints {
			if e.ErrorRatio >= 0.01 {
				return false
			}
		}
	}
	return true
}

// HealthLine summarises the present in one line, so old events are not mistaken for a
// current problem.
func (f *Facts) HealthLine() string {
	if f.Healthy() {
		return "HEALTHY now: no unhealthy pods or deployments, the target has all replicas, endpoint error ratio below 1%"
	}
	var why []string
	if n := len(f.Issues); n > 0 {
		why = append(why, fmt.Sprintf("%d unhealthy pods/deployments", n))
	}
	if m := f.Metrics; m != nil {
		if m.AvailableReplicas < m.DesiredReplicas || m.DesiredReplicas == 0 {
			why = append(why, fmt.Sprintf("target has %.0f/%.0f replicas available", m.AvailableReplicas, m.DesiredReplicas))
		}
		for _, e := range m.Endpoints {
			if e.ErrorRatio >= 0.01 {
				why = append(why, fmt.Sprintf("/%s error ratio %.0f%%", e.Endpoint, e.ErrorRatio*100))
			}
		}
	}
	if len(f.Errors) > 0 {
		why = append(why, "some evidence could not be collected")
	}
	return "PROBLEM: " + strings.Join(why, ", ")
}

// JSON is used by --dump-facts and the Phase 8 data generator.
func (f *Facts) JSON() string {
	b, _ := json.MarshalIndent(f, "", "  ")
	return string(b)
}
