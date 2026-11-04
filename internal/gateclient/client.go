// Package gateclient talks to the Safety Gate's HTTP API, either directly (URL) or
// through the Kubernetes API service proxy (like the collector does for Prometheus).
package gateclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/gate"
)

type Client struct {
	do func(ctx context.Context, method, path string, body []byte) ([]byte, int, error)
}

// New builds a client for the configured gate. token is sent as X-Rehearse-Token.
func New(b config.Backend, kube kubernetes.Interface, token string) (*Client, error) {
	if b.URL != "" {
		base := strings.TrimRight(b.URL, "/")
		hc := &http.Client{Timeout: 60 * time.Second}
		return &Client{do: func(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
			req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
			if err != nil {
				return nil, 0, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Rehearse-Token", token)
			resp, err := hc.Do(req)
			if err != nil {
				return nil, 0, err
			}
			defer resp.Body.Close()
			out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			return out, resp.StatusCode, err
		}}, nil
	}
	ref, err := config.ParseServiceRef(b.Service)
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("/api/v1/namespaces/%s/services/%s:%d/proxy", ref.Namespace, ref.Name, ref.Port)
	return &Client{do: func(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
		res := kube.CoreV1().RESTClient().Verb(method).AbsPath(prefix+path).
			SetHeader("Content-Type", "application/json").SetHeader("X-Rehearse-Token", token).
			Body(body).Do(ctx)
		var code int
		res.StatusCode(&code)
		raw, err := res.Raw()
		if err != nil && code == 0 {
			return nil, 0, fmt.Errorf("gate via API proxy: %w", err)
		}
		return raw, code, nil
	}}, nil
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	raw, code, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if code >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("gate HTTP %d: %s", code, e.Error)
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) Propose(ctx context.Context, req gate.Request) (*gate.Proposal, error) {
	var p gate.Proposal
	return &p, c.call(ctx, http.MethodPost, "/actions", req, &p)
}

func (c *Client) Get(ctx context.Context, id int64) (*gate.Proposal, error) {
	var p gate.Proposal
	return &p, c.call(ctx, http.MethodGet, fmt.Sprintf("/proposals/%d", id), nil, &p)
}

func (c *Client) List(ctx context.Context) ([]*gate.Proposal, error) {
	var ps []*gate.Proposal
	return ps, c.call(ctx, http.MethodGet, "/proposals", nil, &ps)
}

func (c *Client) Approve(ctx context.Context, id int64, by string) (*gate.Proposal, error) {
	var p gate.Proposal
	return &p, c.call(ctx, http.MethodPost, fmt.Sprintf("/proposals/%d/approve", id), map[string]any{"by": by}, &p)
}

func (c *Client) Reject(ctx context.Context, id int64, by, reason string) (*gate.Proposal, error) {
	var p gate.Proposal
	return &p, c.call(ctx, http.MethodPost, fmt.Sprintf("/proposals/%d/reject", id), map[string]any{"by": by, "reason": reason}, &p)
}

// Final reports whether a proposal reached an end state.
func Final(status string) bool {
	switch status {
	case gate.StatusBlocked, gate.StatusRejected, gate.StatusApplied, gate.StatusUndone, gate.StatusFailed:
		return true
	}
	return false
}

// WaitChecked polls until the gate has finished checking (awaiting approval or blocked).
func (c *Client) WaitChecked(ctx context.Context, id int64, every time.Duration) (*gate.Proposal, error) {
	for {
		p, err := c.Get(ctx, id)
		if err != nil || p.Status != gate.StatusChecking {
			return p, err
		}
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case <-time.After(every):
		}
	}
}

// WaitFinal polls until the proposal is final, calling progress on every status change.
func (c *Client) WaitFinal(ctx context.Context, id int64, every time.Duration, progress func(*gate.Proposal)) (*gate.Proposal, error) {
	last := ""
	for {
		p, err := c.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if p.Status != last && progress != nil {
			progress(p)
			last = p.Status
		}
		if Final(p.Status) {
			return p, nil
		}
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case <-time.After(every):
		}
	}
}
