package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YashSejani/Rehearse/internal/agent"
	"github.com/YashSejani/Rehearse/internal/gate"
	"github.com/YashSejani/Rehearse/internal/knowledge"
)

type memIncidents struct {
	mu  sync.Mutex
	all []*knowledge.Incident
}

func (m *memIncidents) CreateIncident(_ context.Context, in *knowledge.Incident) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *in
	cp.ID = int64(len(m.all) + 1)
	cp.CreatedAt = time.Now()
	m.all = append(m.all, &cp)
	return cp.ID, nil
}
func (m *memIncidents) UpdateIncident(_ context.Context, in *knowledge.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *in
	m.all[in.ID-1] = &cp
	return nil
}
func (m *memIncidents) GetIncident(_ context.Context, id int64) (*knowledge.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < 1 || int(id) > len(m.all) {
		return nil, fmt.Errorf("not found")
	}
	return m.all[id-1], nil
}
func (m *memIncidents) ListIncidents(context.Context, int) ([]*knowledge.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*knowledge.Incident(nil), m.all...), nil
}
func (m *memIncidents) OpenIncidentFor(_ context.Context, ns, target string, within time.Duration) (*knowledge.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, in := range m.all {
		if in.Target == target && time.Since(in.CreatedAt) < within {
			return in, nil
		}
	}
	return nil, nil
}

type fakeGate struct {
	mu       sync.Mutex
	proposed []gate.Request
	approved []int64
}

func (g *fakeGate) Propose(_ context.Context, r gate.Request) (*gate.Proposal, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.proposed = append(g.proposed, r)
	return &gate.Proposal{ID: 7, Status: gate.StatusAwaiting, Rehearsal: "rehearsed: healthy in 40s"}, nil
}
func (g *fakeGate) Get(context.Context, int64) (*gate.Proposal, error) {
	return &gate.Proposal{ID: 7, Status: gate.StatusAwaiting, Diff: "spec.replicas: 0 -> 1"}, nil
}
func (g *fakeGate) List(context.Context) ([]*gate.Proposal, error) {
	return []*gate.Proposal{{ID: 7}}, nil
}
func (g *fakeGate) Approve(_ context.Context, id int64, by string) (*gate.Proposal, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.approved = append(g.approved, id)
	return &gate.Proposal{ID: id, Status: gate.StatusApplying, ApprovedBy: by}, nil
}
func (g *fakeGate) Reject(context.Context, int64, string, string) (*gate.Proposal, error) {
	return &gate.Proposal{Status: gate.StatusRejected}, nil
}

func TestTargetOf(t *testing.T) {
	cases := map[string]map[string]string{
		"mongodb-profile": {"pod": "mongodb-profile-68d77b5685-tlpp4", "container": "hotel-reserv-profile-mongo"},
		"user":            {"deployment": "user"},
		"geo":             {"container": "hotel-reserv-geo"},
		"":                {"pod": "x"},
	}
	for want, labels := range cases {
		if got := TargetOf(labels); got != want {
			t.Errorf("%v: got %q want %q", labels, got, want)
		}
	}
}

func TestAlertToSlackAndApproval(t *testing.T) {
	var slackMsgs []string
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		_ = json.NewDecoder(r.Body).Decode(&m)
		slackMsgs = append(slackMsgs, m["text"])
	}))
	defer slack.Close()
	g := &fakeGate{}
	s := &Server{Namespace: "hotel", Incidents: &memIncidents{}, Gate: g, Slack: &Slack{URL: slack.URL},
		DashboardURL: "http://localhost:3001", Token: "tok", Dedupe: 10 * time.Minute,
		Diagnose: func(_ context.Context, target string) *agent.Result {
			return &agent.Result{Valid: true, Answer: &agent.Answer{RootCause: target + " is OOMKilled", Category: "resources",
				Target: target, Confidence: 0.8, Evidence: []string{"Pod x: OOMKilled"},
				Action: agent.Action{Type: "PatchMemoryLimit", Params: map[string]any{"deployment": target, "memory": "256Mi"}}}}
		}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	alert := `{"alerts":[
		{"status":"firing","labels":{"alertname":"RehearseCrashLooping","namespace":"hotel","pod":"mongodb-profile-68d77b5685-tlpp4"}},
		{"status":"firing","labels":{"alertname":"RehearseCrashLooping","namespace":"hotel","pod":"mongodb-profile-68d77b5685-tlpp4"}},
		{"status":"resolved","labels":{"namespace":"hotel","deployment":"geo"}},
		{"status":"firing","labels":{"namespace":"kube-system","deployment":"coredns"}}]}`
	resp, err := http.Post(srv.URL+"/alerts", "application/json", strings.NewReader(alert))
	if err != nil {
		t.Fatal(err)
	}
	var started map[string][]string
	_ = json.NewDecoder(resp.Body).Decode(&started)
	s.Wait()
	if len(started["investigating"]) != 1 || started["investigating"][0] != "mongodb-profile" {
		t.Fatalf("one investigation expected (dedupe, resolved, other namespace): %v", started)
	}
	if len(g.proposed) != 1 || g.proposed[0].Action.Type != "PatchMemoryLimit" || g.proposed[0].IncidentID != 1 {
		t.Errorf("gate proposals %+v", g.proposed)
	}
	if len(slackMsgs) != 1 || !strings.Contains(slackMsgs[0], "OOMKilled") || !strings.Contains(slackMsgs[0], "localhost:3001/incidents/1") ||
		!strings.Contains(slackMsgs[0], "rehearsed: healthy") {
		t.Errorf("slack: %v", slackMsgs)
	}

	do := func(method, path, token string) int {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader([]byte(`{"by":"yash"}`)))
		req.Header.Set("X-Rehearse-Token", token)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := do("GET", "/api/incidents", "bad"); code != http.StatusUnauthorized {
		t.Errorf("api without token: %d", code)
	}
	if code := do("GET", "/api/incidents/1", "tok"); code != http.StatusOK {
		t.Errorf("incident detail: %d", code)
	}
	if code := do("POST", "/api/proposals/7/approve", "tok"); code != http.StatusOK || len(g.approved) != 1 {
		t.Errorf("approve via dashboard API: %d %v", code, g.approved)
	}
}

type statusGate struct {
	fakeGate
	status string
}

func (g *statusGate) Get(context.Context, int64) (*gate.Proposal, error) {
	return &gate.Proposal{ID: 7, Status: g.status}, nil
}

func TestSyncIncidentsFollowsTheProposal(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		incident string
		saved    int
	}{
		gate.StatusAwaiting: {"proposed", 0},
		gate.StatusWatching: {"proposed", 0},
		gate.StatusApplied:  {"resolved", 1},
		gate.StatusUndone:   {"rolled_back", 0},
		gate.StatusRejected: {"rejected", 0},
		gate.StatusFailed:   {"failed", 0},
	}
	for status, want := range cases {
		inc := &memIncidents{}
		id, err := inc.CreateIncident(ctx, &knowledge.Incident{Target: "geo", Status: "proposed", ProposalID: 7})
		if err != nil {
			t.Fatal(err)
		}
		saved := 0
		s := &Server{Incidents: inc, Gate: &statusGate{status: status}, OnResolved: func(context.Context) error { saved++; return nil }}
		if err := s.SyncIncidents(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := inc.GetIncident(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want.incident || saved != want.saved {
			t.Errorf("proposal %s: incident %s, saved %d; want %s, %d", status, got.Status, saved, want.incident, want.saved)
		}
	}
}
