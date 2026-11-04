// Package server is the agent service (Phase 10): it receives Alertmanager webhooks, starts
// an investigation per alert, stores incidents, sends the proposed action to the Safety
// Gate, notifies Slack, and serves the small API the dashboard uses. It never writes to the
// cluster itself; approvals are forwarded to the gate.
package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YashSejani/Rehearse/internal/actions"
	"github.com/YashSejani/Rehearse/internal/agent"
	"github.com/YashSejani/Rehearse/internal/gate"
	"github.com/YashSejani/Rehearse/internal/knowledge"
)

// Incidents is the incident store (Postgres in production, memory in tests).
type Incidents interface {
	CreateIncident(ctx context.Context, in *knowledge.Incident) (int64, error)
	UpdateIncident(ctx context.Context, in *knowledge.Incident) error
	GetIncident(ctx context.Context, id int64) (*knowledge.Incident, error)
	ListIncidents(ctx context.Context, limit int) ([]*knowledge.Incident, error)
	OpenIncidentFor(ctx context.Context, ns, target string, within time.Duration) (*knowledge.Incident, error)
}

// Gate is the part of the gate client the server uses.
type Gate interface {
	Propose(ctx context.Context, req gate.Request) (*gate.Proposal, error)
	Get(ctx context.Context, id int64) (*gate.Proposal, error)
	List(ctx context.Context) ([]*gate.Proposal, error)
	Approve(ctx context.Context, id int64, by string) (*gate.Proposal, error)
	Reject(ctx context.Context, id int64, by, reason string) (*gate.Proposal, error)
}

// Diagnoser runs one investigation (the agent, or a fake in tests).
type Diagnoser func(ctx context.Context, target string) *agent.Result

type Server struct {
	Namespace    string
	Incidents    Incidents
	Gate         Gate
	Diagnose     Diagnoser
	Slack        *Slack // may be nil
	DashboardURL string // e.g. http://localhost:3001
	Token        string // for the dashboard API
	Dedupe       time.Duration
	// OnResolved runs after an incident is resolved (saves it into the RAG memory). May be nil.
	OnResolved func(context.Context) error

	wg sync.WaitGroup
	mu sync.Mutex
	// one investigation per deployment at a time (the model is slow on CPU)
	busy map[string]bool
}

// Alertmanager webhook payload (only the fields we use).
type webhook struct {
	Alerts []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"alerts"`
}

var (
	podHash  = regexp.MustCompile(`-[a-z0-9]{8,10}-[a-z0-9]{5}$`)
	validDep = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// TargetOf maps an alert's labels to a deployment name.
func TargetOf(labels map[string]string) string {
	for _, k := range []string{"deployment", "io_kompose_service", "service_name"} {
		if v := labels[k]; validDep.MatchString(v) {
			return v
		}
	}
	if p := labels["pod"]; p != "" {
		if d := podHash.ReplaceAllString(p, ""); validDep.MatchString(d) && d != p {
			return d
		}
	}
	if c := strings.TrimPrefix(labels["container"], "hotel-reserv-"); c != labels["container"] && validDep.MatchString(c) {
		return c
	}
	return ""
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	// Alertmanager runs inside the cluster and cannot hold our token; the endpoint only
	// starts read-only investigations, and proposals still need a human to approve.
	mux.HandleFunc("POST /alerts", s.alerts)
	api := http.NewServeMux()
	api.HandleFunc("GET /api/incidents", func(w http.ResponseWriter, r *http.Request) {
		l, err := s.Incidents.ListIncidents(r.Context(), 100)
		reply(w, l, err)
	})
	api.HandleFunc("GET /api/incidents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		in, err := s.Incidents.GetIncident(r.Context(), id)
		if err != nil {
			reply(w, nil, err)
			return
		}
		out := map[string]any{"incident": in}
		if in.ProposalID != 0 {
			if p, err := s.Gate.Get(r.Context(), in.ProposalID); err == nil {
				out["proposal"] = p
			}
		}
		reply(w, out, nil)
	})
	api.HandleFunc("GET /api/audit", func(w http.ResponseWriter, r *http.Request) {
		l, err := s.Gate.List(r.Context())
		reply(w, l, err)
	})
	decide := func(approve bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			var body struct {
				By     string `json:"by"`
				Reason string `json:"reason"`
			}
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
			if body.By == "" {
				body.By = "dashboard"
			}
			var p *gate.Proposal
			var err error
			if approve {
				p, err = s.Gate.Approve(r.Context(), id, body.By)
			} else {
				p, err = s.Gate.Reject(r.Context(), id, body.By, nonEmpty(body.Reason, "rejected in dashboard"))
			}
			reply(w, p, err)
		}
	}
	api.HandleFunc("POST /api/proposals/{id}/approve", decide(true))
	api.HandleFunc("POST /api/proposals/{id}/reject", decide(false))
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*") // local dashboard on another port
		w.Header().Set("Access-Control-Allow-Headers", "X-Rehearse-Token, Content-Type")
		if r.Method == http.MethodOptions {
			return
		}
		if s.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Rehearse-Token")), []byte(s.Token)) != 1 {
			reply(w, nil, httpErr{http.StatusUnauthorized, "missing or wrong token"})
			return
		}
		api.ServeHTTP(w, r)
	}))
	return mux
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	var wh webhook
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&wh); err != nil {
		reply(w, nil, httpErr{http.StatusBadRequest, "bad webhook: " + err.Error()})
		return
	}
	var started []string
	for _, a := range wh.Alerts {
		if a.Status != "firing" {
			continue
		}
		if ns := a.Labels["namespace"]; ns != "" && ns != s.Namespace {
			continue
		}
		target := TargetOf(a.Labels)
		if target == "" {
			continue
		}
		if s.Start(context.WithoutCancel(r.Context()), target, a.Labels["alertname"]) {
			started = append(started, target)
		}
	}
	reply(w, map[string]any{"investigating": started}, nil)
}

// Start begins an investigation unless one for the target is running or recent.
func (s *Server) Start(ctx context.Context, target, alert string) bool {
	s.mu.Lock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	if s.busy[target] {
		s.mu.Unlock()
		return false
	}
	if in, _ := s.Incidents.OpenIncidentFor(ctx, s.Namespace, target, s.Dedupe); in != nil {
		s.mu.Unlock()
		return false
	}
	s.busy[target] = true
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); delete(s.busy, target); s.mu.Unlock() }()
		s.investigate(ctx, target, alert)
	}()
	return true
}

// Wait blocks until running investigations finish (tests, shutdown).
func (s *Server) Wait() { s.wg.Wait() }

func (s *Server) investigate(ctx context.Context, target, alert string) {
	in := &knowledge.Incident{Namespace: s.Namespace, Target: target, Source: "alert", Alert: alert, Status: "investigating"}
	id, err := s.Incidents.CreateIncident(ctx, in)
	if err != nil {
		log.Printf("incident for %s: %v", target, err)
		return
	}
	in.ID = id
	res := s.Diagnose(ctx, target)
	answer, _ := json.Marshal(res.Answer)
	in.Answer, in.Summary = answer, res.Answer.RootCause
	in.Status = "diagnosed"
	var p *gate.Proposal
	if res.Answer.Action.Type != "None" {
		p, err = s.Gate.Propose(ctx, gate.Request{IncidentID: id, Namespace: s.Namespace, Answer: answer,
			Action: actions.Action{Type: res.Answer.Action.Type, Params: res.Answer.Action.Params}})
		if err == nil && p.Status == gate.StatusChecking {
			p, err = waitChecked(ctx, s.Gate, p.ID)
		}
		if err != nil {
			in.Summary += " (gate error: " + err.Error() + ")"
		} else {
			in.ProposalID = p.ID
			in.Status = "proposed"
			if p.Status == gate.StatusBlocked {
				in.Status = "blocked"
			}
		}
	}
	if err := s.Incidents.UpdateIncident(ctx, in); err != nil {
		log.Printf("update incident %d: %v", id, err)
	}
	if s.Slack != nil {
		if err := s.Slack.Notify(ctx, s.message(in, res, p)); err != nil {
			log.Printf("slack: %v", err)
		}
	}
}

func (s *Server) message(in *knowledge.Incident, res *agent.Result, p *gate.Proposal) string {
	a := res.Answer
	var b strings.Builder
	fmt.Fprintf(&b, ":rotating_light: *Rehearse incident #%d* on `%s/%s` (alert %s)\n", in.ID, in.Namespace, in.Target, nonEmpty(in.Alert, "-"))
	fmt.Fprintf(&b, "*Cause:* %s (confidence %.2f, category %s)\n", a.RootCause, a.Confidence, a.Category)
	for i, e := range a.Evidence {
		if i == 3 {
			break
		}
		fmt.Fprintf(&b, "> %s\n", truncate(e, 160))
	}
	params, _ := json.Marshal(a.Action.Params)
	fmt.Fprintf(&b, "*Proposed action:* %s %s\n", a.Action.Type, params)
	if p != nil {
		fmt.Fprintf(&b, "*Gate:* %s; %s\n", p.Status, nonEmpty(p.Rehearsal, "-"))
		if p.Status == gate.StatusBlocked {
			fmt.Fprintf(&b, "*Blocked:* %s\n", p.Reason)
		}
	}
	if s.DashboardURL != "" {
		fmt.Fprintf(&b, "<%s/incidents/%d|Review and approve in the dashboard>", strings.TrimRight(s.DashboardURL, "/"), in.ID)
	}
	return b.String()
}

// incidentOutcome is the incident status for a gate proposal that has finished.
var incidentOutcome = map[string]string{
	gate.StatusApplied:  "resolved",
	gate.StatusUndone:   "rolled_back",
	gate.StatusRejected: "rejected",
	gate.StatusFailed:   "failed",
}

// SyncIncidents moves incidents along when their proposal has finished, however it was
// approved (dashboard or CLI).
func (s *Server) SyncIncidents(ctx context.Context) error {
	list, err := s.Incidents.ListIncidents(ctx, 50)
	if err != nil {
		return fmt.Errorf("list incidents: %w", err)
	}
	resolved := false
	for _, in := range list {
		if in.Status != "proposed" || in.ProposalID == 0 {
			continue
		}
		p, err := s.Gate.Get(ctx, in.ProposalID)
		if err != nil {
			return fmt.Errorf("get proposal %d: %w", in.ProposalID, err)
		}
		status, done := incidentOutcome[p.Status]
		if !done {
			continue
		}
		in.Status = status
		if err := s.Incidents.UpdateIncident(ctx, in); err != nil {
			return fmt.Errorf("update incident %d: %w", in.ID, err)
		}
		resolved = resolved || status == "resolved"
	}
	if resolved && s.OnResolved != nil {
		return s.OnResolved(ctx)
	}
	return nil
}

func waitChecked(ctx context.Context, g Gate, id int64) (*gate.Proposal, error) {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		p, err := g.Get(ctx, id)
		if err != nil || p.Status != gate.StatusChecking || time.Now().After(deadline) {
			return p, err
		}
		time.Sleep(5 * time.Second)
	}
}

// Slack sends messages to an incoming webhook (URL from the environment, never committed).
type Slack struct {
	URL  string
	HTTP *http.Client
}

func (s *Slack) Notify(ctx context.Context, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook HTTP %d", resp.StatusCode)
	}
	return nil
}

type httpErr struct {
	code int
	msg  string
}

func (e httpErr) Error() string { return e.msg }

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		code := http.StatusBadGateway
		if he, ok := err.(httpErr); ok {
			code = he.code
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
