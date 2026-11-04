package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YashSejani/Rehearse/internal/actions"
)

// Proposal is one action moving through the gate.
type Proposal struct {
	ID          int64           `json:"id"`
	IncidentID  int64           `json:"incident_id,omitempty"`
	Namespace   string          `json:"namespace"`
	Deployment  string          `json:"deployment"`
	Action      actions.Action  `json:"action"`
	Answer      json.RawMessage `json:"answer,omitempty"`
	Plan        *actions.Plan   `json:"plan,omitempty"`
	Diff        string          `json:"diff"`
	Rehearsal   string          `json:"rehearsal"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
	ApprovedBy  string          `json:"approved_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Events      []Event         `json:"events,omitempty"`
	Rehearsable bool            `json:"-"`
}

// Event is one audit entry: which pipeline step ran, whether it passed, and why.
type Event struct {
	At     time.Time `json:"at"`
	Step   string    `json:"step"`
	OK     bool      `json:"ok"`
	Detail string    `json:"detail"`
}

const (
	StatusChecking = "checking" // dry-run, policy and rehearsal still running
	StatusBlocked  = "blocked"
	StatusAwaiting = "awaiting_approval"
	StatusRejected = "rejected"
	StatusApplying = "applying"
	StatusWatching = "watching"
	StatusApplied  = "applied"
	StatusUndone   = "rolled_back"
	StatusFailed   = "failed"
)

// Store keeps proposals and their audit events.
type Store interface {
	Create(ctx context.Context, p *Proposal) error
	Update(ctx context.Context, p *Proposal) error
	AddEvent(ctx context.Context, id int64, e Event) error
	Get(ctx context.Context, id int64) (*Proposal, error)
	List(ctx context.Context, limit int) ([]*Proposal, error)
}

// MemStore is an in-memory Store (tests, or a gate without Postgres).
type MemStore struct {
	mu     sync.Mutex
	next   int64
	byID   map[int64]*Proposal
	events map[int64][]Event
}

func NewMemStore() *MemStore {
	return &MemStore{byID: map[int64]*Proposal{}, events: map[int64][]Event{}}
}

func (m *MemStore) Create(_ context.Context, p *Proposal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	p.ID = m.next
	cp := *p
	m.byID[p.ID] = &cp
	return nil
}

func (m *MemStore) Update(_ context.Context, p *Proposal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[p.ID]; !ok {
		return fmt.Errorf("proposal %d not found", p.ID)
	}
	cp := *p
	cp.Events = nil
	m.byID[p.ID] = &cp
	return nil
}

func (m *MemStore) AddEvent(_ context.Context, id int64, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[id] = append(m.events[id], e)
	return nil
}

func (m *MemStore) Get(_ context.Context, id int64) (*Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.byID[id]
	if !ok {
		return nil, fmt.Errorf("proposal %d not found", id)
	}
	cp := *p
	cp.Events = append([]Event(nil), m.events[id]...)
	return &cp, nil
}

func (m *MemStore) List(_ context.Context, limit int) ([]*Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Proposal
	for _, p := range m.byID {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PGStore keeps proposals in Postgres (tables from migration 002_gate.sql).
type PGStore struct{ Pool *pgxpool.Pool }

func (s *PGStore) Create(ctx context.Context, p *Proposal) error {
	action, _ := json.Marshal(p.Action)
	plan, _ := json.Marshal(p.Plan)
	if len(p.Answer) == 0 {
		p.Answer = json.RawMessage("{}")
	}
	return s.Pool.QueryRow(ctx, `INSERT INTO proposals(incident_id, namespace, deployment, action, answer, plan, diff, rehearsal, status, reason)
		VALUES (NULLIF($1,0),$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, created_at`,
		p.IncidentID, p.Namespace, p.Deployment, action, []byte(p.Answer), plan, p.Diff, p.Rehearsal, p.Status, p.Reason).
		Scan(&p.ID, &p.CreatedAt)
}

func (s *PGStore) Update(ctx context.Context, p *Proposal) error {
	plan, _ := json.Marshal(p.Plan)
	_, err := s.Pool.Exec(ctx, `UPDATE proposals SET plan=$2, diff=$3, rehearsal=$4, status=$5, reason=$6, approved_by=$7, updated_at=now() WHERE id=$1`,
		p.ID, plan, p.Diff, p.Rehearsal, p.Status, p.Reason, p.ApprovedBy)
	return err
}

func (s *PGStore) AddEvent(ctx context.Context, id int64, e Event) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO audit_events(proposal_id, at, step, ok, detail) VALUES ($1,$2,$3,$4,$5)`, id, e.At, e.Step, e.OK, e.Detail)
	return err
}

func (s *PGStore) Get(ctx context.Context, id int64) (*Proposal, error) {
	ps, err := s.query(ctx, `WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("proposal %d not found", id)
	}
	p := ps[0]
	rows, err := s.Pool.Query(ctx, `SELECT at, step, ok, detail FROM audit_events WHERE proposal_id=$1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.At, &e.Step, &e.OK, &e.Detail); err != nil {
			return nil, err
		}
		p.Events = append(p.Events, e)
	}
	return p, rows.Err()
}

func (s *PGStore) List(ctx context.Context, limit int) ([]*Proposal, error) {
	return s.query(ctx, `ORDER BY id DESC LIMIT $1`, limit)
}

func (s *PGStore) query(ctx context.Context, where string, arg any) ([]*Proposal, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, COALESCE(incident_id,0), namespace, deployment, action, answer, plan, diff, rehearsal,
		status, reason, approved_by, created_at, updated_at FROM proposals `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Proposal
	for rows.Next() {
		p := &Proposal{}
		var action, answer, plan []byte
		if err := rows.Scan(&p.ID, &p.IncidentID, &p.Namespace, &p.Deployment, &action, &answer, &plan, &p.Diff, &p.Rehearsal,
			&p.Status, &p.Reason, &p.ApprovedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(action, &p.Action)
		p.Answer = answer
		if len(plan) > 0 && string(plan) != "null" {
			p.Plan = &actions.Plan{}
			_ = json.Unmarshal(plan, p.Plan)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
