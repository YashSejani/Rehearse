package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Incident is one investigation and what happened to it.
type Incident struct {
	ID         int64           `json:"id"`
	Namespace  string          `json:"namespace"`
	Target     string          `json:"target"`
	Source     string          `json:"source"`
	Summary    string          `json:"summary"`
	Answer     json.RawMessage `json:"answer"`
	Status     string          `json:"status"`
	ProposalID int64           `json:"proposal_id,omitempty"`
	Alert      string          `json:"alert,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// UpdateIncident stores the diagnosis and the gate proposal of an incident.
func (s *Store) UpdateIncident(ctx context.Context, in *Incident) error {
	if len(in.Answer) == 0 {
		in.Answer = json.RawMessage("{}")
	}
	_, err := s.Pool.Exec(ctx, `UPDATE incidents SET summary=$2, answer=$3, status=$4, proposal_id=NULLIF($5,0), updated_at=now() WHERE id=$1`,
		in.ID, in.Summary, in.Answer, in.Status, in.ProposalID)
	return err
}

// ListIncidents returns the newest incidents first.
func (s *Store) ListIncidents(ctx context.Context, limit int) ([]*Incident, error) {
	return s.queryIncidents(ctx, `ORDER BY id DESC LIMIT $1`, limit)
}

// GetIncident returns one incident.
func (s *Store) GetIncident(ctx context.Context, id int64) (*Incident, error) {
	l, err := s.queryIncidents(ctx, `WHERE id=$1`, id)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, fmt.Errorf("incident %d not found", id)
	}
	return l[0], nil
}

// OpenIncidentFor returns an unfinished incident for the same target created in the last
// `within`, so a repeating alert does not start a new investigation every minute.
func (s *Store) OpenIncidentFor(ctx context.Context, ns, target string, within time.Duration) (*Incident, error) {
	l, err := s.queryIncidents(ctx, `WHERE namespace=$1 AND target=$2 AND status IN ('open','investigating','proposed','approved')
		AND created_at > now() - make_interval(secs => $3) ORDER BY id DESC LIMIT 1`, ns, target, within.Seconds())
	if err != nil || len(l) == 0 {
		return nil, err
	}
	return l[0], nil
}

func (s *Store) queryIncidents(ctx context.Context, where string, args ...any) ([]*Incident, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, namespace, target, source, summary, answer, status, COALESCE(proposal_id,0), alert, created_at, updated_at FROM incidents `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Incident
	for rows.Next() {
		in := &Incident{}
		var ans []byte
		if err := rows.Scan(&in.ID, &in.Namespace, &in.Target, &in.Source, &in.Summary, &ans, &in.Status, &in.ProposalID, &in.Alert, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		in.Answer = ans
		out = append(out, in)
	}
	return out, rows.Err()
}

// CreateIncident stores a new investigation and returns its id.
func (s *Store) CreateIncident(ctx context.Context, in *Incident) (int64, error) {
	if len(in.Answer) == 0 {
		in.Answer = json.RawMessage("{}")
	}
	var id int64
	err := s.Pool.QueryRow(ctx, `INSERT INTO incidents(namespace, target, source, summary, answer, status, alert)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, in.Namespace, in.Target, nonEmpty(in.Source, "cli"), in.Summary, in.Answer, nonEmpty(in.Status, "open"), in.Alert).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create incident: %w", err)
	}
	return id, nil
}

// SetIncidentStatus moves an incident along (proposed, applied, resolved, ...).
func (s *Store) SetIncidentStatus(ctx context.Context, id int64, status string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE incidents SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

// ResolvedIncidentChunks turns every resolved incident into a document, so the next similar
// problem can find "last time this happened, the cause was X and Y fixed it".
func (s *Store) ResolvedIncidentChunks(ctx context.Context) ([]Chunk, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, namespace, target, summary, answer FROM incidents WHERE status='resolved' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var id int64
		var ns, target, summary string
		var raw []byte
		if err := rows.Scan(&id, &ns, &target, &summary, &raw); err != nil {
			return nil, err
		}
		var a struct {
			RootCause string   `json:"root_cause"`
			Category  string   `json:"category"`
			Evidence  []string `json:"evidence"`
			Action    struct {
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"action"`
		}
		_ = json.Unmarshal(raw, &a)
		params, _ := json.Marshal(a.Action.Params)
		text := fmt.Sprintf("Resolved incident on %s/%s. Root cause: %s (category %s). Fixed by %s %s. Evidence: %s. %s",
			ns, target, a.RootCause, a.Category, a.Action.Type, params, strings.Join(a.Evidence, " | "), summary)
		out = append(out, Chunk{Kind: "incident", Source: fmt.Sprintf("incident/%d", id), Title: "incident " + target, Text: text})
	}
	return out, rows.Err()
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
