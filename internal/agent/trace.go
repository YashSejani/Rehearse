package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Trace appends one JSON object per line (JSONL) for every step of an investigation:
// facts, each model call (messages, tool calls, tokens, latency), each tool result, the
// answer. Like AIOpsLab's session.py. traces/ is git-ignored: it contains cluster data.
type Trace struct {
	Path string
	mu   sync.Mutex
	f    *os.File
}

// NewTrace creates traces/<timestamp>-<target>.jsonl.
func NewTrace(dir, target string) (*Trace, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("trace dir: %w", err)
	}
	name := fmt.Sprintf("%s-%s.jsonl", time.Now().Format("20060102-150405"), target)
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("trace file: %w", err)
	}
	return &Trace{Path: path, f: f}, nil
}

// Write records one event. Safe on a nil Trace (tracing off).
func (t *Trace) Write(kind string, data map[string]any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := map[string]any{"time": time.Now().Format(time.RFC3339Nano), "kind": kind}
	for k, v := range data {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"kind": kind, "marshal_error": err.Error()})
	}
	_, _ = t.f.Write(append(b, '\n'))
}

func (t *Trace) Close() error {
	if t == nil || t.f == nil {
		return nil
	}
	return t.f.Close()
}
