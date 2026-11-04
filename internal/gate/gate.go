// Package gate is the Safety Gate: the only part of Rehearse that writes to the cluster.
// It turns one typed action into a patch and its inverse, dry-runs it on the API server
// (where Kyverno checks it), rehearses it in a sandbox (Phase 7), waits for a human to
// approve, applies it, watches the app, undoes it if things got worse, and records every
// step in the audit log.
package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/YashSejani/Rehearse/internal/actions"
)

// Rehearser tests a plan on a copy of the app before approval (Phase 7).
type Rehearser interface {
	// Rehearse returns a human summary ("rehearsed: healthy in 41s") and whether the copy
	// became healthy. ok=false with err=nil means "rehearsed: still failing".
	Rehearse(ctx context.Context, p *actions.Plan) (summary string, ok bool, err error)
}

// Request is what the agent (through the CLI, alert handler or MCP server) sends.
type Request struct {
	IncidentID int64           `json:"incident_id,omitempty"`
	Namespace  string          `json:"namespace"`
	Action     actions.Action  `json:"action"`
	Answer     json.RawMessage `json:"answer,omitempty"` // the diagnosis, for the approver and audit only
}

type Gate struct {
	Kube      kubernetes.Interface // the gate's own, write-capable ServiceAccount
	Store     Store
	Prober    Prober
	Rehearser Rehearser // nil = "not rehearsed (sandbox disabled)"
	Watch     WatchConfig
	// AllowedNamespaces limits where the gate may act at all.
	AllowedNamespaces []string
	// Baseline returns the normal error ratio of a deployment (Phase 5); nil = 0.
	Baseline func(ctx context.Context, ns, dep string) float64
	Now      func() time.Time
	Sleep    func(time.Duration)

	mu       sync.Mutex
	inflight map[string]bool // one change per deployment at a time
	wg       sync.WaitGroup
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Gate) event(ctx context.Context, p *Proposal, step string, ok bool, detail string) {
	e := Event{At: g.now(), Step: step, OK: ok, Detail: detail}
	p.Events = append(p.Events, e)
	_ = g.Store.AddEvent(ctx, p.ID, e)
}

func (g *Gate) finish(ctx context.Context, p *Proposal, status, reason string) {
	p.Status, p.Reason, p.UpdatedAt = status, reason, g.now()
	_ = g.Store.Update(ctx, p)
}

// Propose runs everything up to approval: validate, build patch + inverse, server-side
// dry-run (Kyverno), policy re-check, diff, rehearsal. The result is "blocked" or
// "awaiting_approval". Nothing in the cluster changes here.
func (g *Gate) Propose(ctx context.Context, req Request) (*Proposal, error) {
	p, err := g.create(ctx, req)
	if err != nil {
		return nil, err
	}
	return g.check(ctx, p, req), nil
}

// ProposeAsync stores the proposal as "checking" and runs the checks in the background,
// because a rehearsal takes minutes and HTTP proxies cut long requests off. Callers poll.
func (g *Gate) ProposeAsync(ctx context.Context, req Request) (*Proposal, error) {
	p, err := g.create(ctx, req)
	if err != nil {
		return nil, err
	}
	snapshot := *p
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.check(context.WithoutCancel(ctx), p, req)
	}()
	return &snapshot, nil
}

func (g *Gate) create(ctx context.Context, req Request) (*Proposal, error) {
	dep, _ := req.Action.Params["deployment"].(string)
	p := &Proposal{IncidentID: req.IncidentID, Namespace: req.Namespace, Deployment: dep, Action: req.Action,
		Answer: req.Answer, Status: StatusChecking, CreatedAt: g.now(), UpdatedAt: g.now()}
	if err := g.Store.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("store proposal: %w", err)
	}
	return p, nil
}

func (g *Gate) check(ctx context.Context, p *Proposal, req Request) *Proposal {
	dep := p.Deployment

	// 0. Scope: only namespaces the gate is configured for.
	if !contains(g.AllowedNamespaces, req.Namespace) {
		g.event(ctx, p, "validate", false, fmt.Sprintf("namespace %q is not managed by the gate", req.Namespace))
		g.finish(ctx, p, StatusBlocked, "namespace not allowed")
		return p
	}
	// 1. Catalogue: build the patch and its inverse from the live object.
	plan, err := actions.Build(ctx, g.Kube, req.Namespace, req.Action, g.now())
	if err != nil {
		g.event(ctx, p, "validate", false, err.Error())
		g.finish(ctx, p, StatusBlocked, err.Error())
		return p
	}
	p.Plan = plan
	g.event(ctx, p, "validate", true, plan.Description)

	// 2. Server-side dry-run: the API server runs admission (Kyverno) and validation but
	// stores nothing. A Kyverno denial comes back as an error here.
	before, err := g.Kube.AppsV1().Deployments(req.Namespace).Get(ctx, dep, metav1.GetOptions{})
	if err != nil {
		g.event(ctx, p, "dry-run", false, err.Error())
		g.finish(ctx, p, StatusBlocked, "cannot read deployment")
		return p
	}
	after, err := g.Kube.AppsV1().Deployments(req.Namespace).Patch(ctx, dep, types.JSONPatchType, plan.PatchJSON(),
		metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "rehearse-gate"})
	if err != nil {
		g.event(ctx, p, "dry-run", false, err.Error())
		g.finish(ctx, p, StatusBlocked, "dry-run rejected: "+err.Error())
		return p
	}
	p.Diff = Diff(before.Spec, after.Spec)
	g.event(ctx, p, "dry-run", true, fmt.Sprintf("accepted by the API server and admission policies; %d field(s) change", countLines(p.Diff)))

	// 3. Policy re-check on the would-be object (defence in depth if Kyverno is absent).
	if v := CheckPolicy(after); len(v) > 0 {
		g.event(ctx, p, "policy", false, fmt.Sprint(v))
		g.finish(ctx, p, StatusBlocked, "policy: "+v[0])
		return p
	}
	g.event(ctx, p, "policy", true, "no-scale-to-zero, disallow-latest-tag, max-memory-limit: pass")

	// 4. Rehearsal on a copy (Phase 7). Never skipped silently.
	if g.Rehearser == nil {
		p.Rehearsal = "not rehearsed (sandbox disabled)"
		g.event(ctx, p, "rehearsal", true, p.Rehearsal)
	} else {
		summary, ok, err := g.Rehearser.Rehearse(ctx, plan)
		switch {
		case err != nil:
			p.Rehearsal = "not rehearsed (" + err.Error() + ")"
			g.event(ctx, p, "rehearsal", true, p.Rehearsal)
		case !ok:
			p.Rehearsal = summary
			g.event(ctx, p, "rehearsal", false, summary)
			g.finish(ctx, p, StatusBlocked, "rehearsal failed: "+summary)
			return p
		default:
			p.Rehearsal = summary
			g.event(ctx, p, "rehearsal", true, summary)
		}
	}
	g.finish(ctx, p, StatusAwaiting, "waiting for a human")
	return p
}

// Approve applies an awaiting proposal and watches it in the background.
// `wait` makes it synchronous (CLI and tests).
func (g *Gate) Approve(ctx context.Context, id int64, who string, wait bool) (*Proposal, error) {
	p, err := g.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Status != StatusAwaiting {
		return p, fmt.Errorf("proposal %d is %s, not awaiting approval", id, p.Status)
	}
	if who == "" {
		return p, errors.New("approval needs a name")
	}
	key := p.Namespace + "/" + p.Deployment
	g.mu.Lock()
	if g.inflight == nil {
		g.inflight = map[string]bool{}
	}
	if g.inflight[key] {
		g.mu.Unlock()
		return p, fmt.Errorf("another change to %s is in progress", key)
	}
	g.inflight[key] = true
	g.mu.Unlock()

	p.ApprovedBy = who
	g.event(ctx, p, "approval", true, "approved by "+who)
	g.finish(ctx, p, StatusApplying, "")
	run := func() {
		defer func() { g.mu.Lock(); delete(g.inflight, key); g.mu.Unlock() }()
		g.applyAndWatch(context.WithoutCancel(ctx), p)
	}
	if wait {
		run()
		return g.Store.Get(ctx, id)
	}
	g.wg.Add(1)
	go func() { defer g.wg.Done(); run() }()
	return p, nil
}

// Reject closes a proposal without changing anything.
func (g *Gate) Reject(ctx context.Context, id int64, who, why string) (*Proposal, error) {
	p, err := g.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Status != StatusAwaiting {
		return p, fmt.Errorf("proposal %d is %s, not awaiting approval", id, p.Status)
	}
	g.event(ctx, p, "approval", false, fmt.Sprintf("rejected by %s: %s", who, why))
	g.finish(ctx, p, StatusRejected, why)
	return p, nil
}

// Wait blocks until background watches finish (tests, shutdown).
func (g *Gate) Wait() { g.wg.Wait() }

func (g *Gate) applyAndWatch(ctx context.Context, p *Proposal) {
	deps := g.Kube.AppsV1().Deployments(p.Namespace)
	if _, err := deps.Patch(ctx, p.Deployment, types.JSONPatchType, p.Plan.PatchJSON(), metav1.PatchOptions{FieldManager: "rehearse-gate"}); err != nil {
		g.event(ctx, p, "apply", false, err.Error())
		g.finish(ctx, p, StatusFailed, "apply failed: "+err.Error())
		return
	}
	g.event(ctx, p, "apply", true, p.Plan.Description)
	g.finish(ctx, p, StatusWatching, "")

	cfg := g.Watch
	if g.Baseline != nil {
		cfg.Baseline = g.Baseline(ctx, p.Namespace, p.Deployment)
	}
	if err := Watch(ctx, g.Prober, p.Deployment, cfg, g.Sleep); err != nil {
		g.event(ctx, p, "watch", false, err.Error())
		if _, uerr := deps.Patch(ctx, p.Deployment, types.JSONPatchType, p.Plan.InverseJSON(), metav1.PatchOptions{FieldManager: "rehearse-gate"}); uerr != nil {
			g.event(ctx, p, "undo", false, uerr.Error())
			g.finish(ctx, p, StatusFailed, "worse after change and undo failed: "+uerr.Error())
			return
		}
		g.event(ctx, p, "undo", true, "inverse patch applied")
		g.finish(ctx, p, StatusUndone, err.Error())
		return
	}
	g.event(ctx, p, "watch", true, fmt.Sprintf("healthy for %s after a %s settle window", cfg.Duration, cfg.Settle))
	g.finish(ctx, p, StatusApplied, "healthy after change")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}
