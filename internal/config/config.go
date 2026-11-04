// Package config loads config.yaml: the cluster context, the target app, and where
// Prometheus and Loki live. Nothing app-specific is hard-coded in Go.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

type Config struct {
	Kube       Kube      `json:"kube"`
	App        App       `json:"app"`
	Prometheus Backend   `json:"prometheus"`
	Loki       Loki      `json:"loki"`
	Limits     Limits    `json:"limits"`
	Agent      Agent     `json:"agent"`
	Jaeger     Backend   `json:"jaeger"`
	Knowledge  Knowledge `json:"knowledge"`
	Gate       Gate      `json:"gate"`
}

// Gate is where the Safety Gate (Phase 6) runs and how to authenticate to it.
type Gate struct {
	Backend     `json:",inline"`
	TokenSecret string `json:"token_secret"` // namespace/name of the Secret with key "token"
	// Sandbox settings (Phase 7).
	Sandbox Sandbox `json:"sandbox"`
}

type Sandbox struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`    // namespace (vCluster needs more RAM; see decisions.md)
	Timeout int    `json:"timeout"` // seconds to wait for the copy to become healthy
	// One HTTP request that must succeed against the copy after the fix.
	SmokeService string `json:"smoke_service"`
	SmokePort    int    `json:"smoke_port"`
	SmokePath    string `json:"smoke_path"`
}

// Knowledge is App Knowledge (Phase 5): Postgres + pgvector and the RAG settings.
type Knowledge struct {
	// DSN connects directly (e.g. inside the cluster). If empty, the CLI port-forwards to
	// Service and reads the password from Secret ("namespace/name", key "password").
	DSN        string   `json:"dsn"`
	Service    string   `json:"service"`
	Secret     string   `json:"secret"`
	EmbedModel string   `json:"embed_model"`
	TopK       int      `json:"top_k"`
	RAG        bool     `json:"rag"`
	Sources    []string `json:"sources"` // files/dirs ingested by `rehearse knowledge ingest`
}

type Kube struct {
	Context string `json:"context"`
}

type App struct {
	Namespace    string              `json:"namespace"`
	ServiceLabel string              `json:"service_label"`
	Endpoints    map[string][]string `json:"endpoints"`
}

// Backend is an HTTP service reached either through the Kubernetes API service proxy
// (Service = "namespace/name:port") or directly (URL).
type Backend struct {
	Service string `json:"service"`
	URL     string `json:"url"`
}

type Loki struct {
	Backend        `json:",inline"`
	EventsSelector string `json:"events_selector"`
}

type Limits struct {
	LogLines       int `json:"log_lines"`
	Issues         int `json:"issues"`
	Changes        int `json:"changes"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

// Agent settings are used from Phase 3 on.
type Agent struct {
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	MaxSteps    int     `json:"max_steps"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"` // per reply; stops a model that loops
	// AlwaysAskModel turns off triage: the model is asked even when the deterministic
	// health check says everything is fine (used to measure false alarms).
	AlwaysAskModel bool   `json:"always_ask_model"`
	TraceDir       string `json:"trace_dir"`
}

// Timeout is the per-call timeout for Kubernetes, Prometheus and Loki requests.
func (l Limits) Timeout() time.Duration { return time.Duration(l.TimeoutSeconds) * time.Second }

// Load reads a YAML file and fills in defaults for anything left out.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	c := Default()
	if err := yaml.UnmarshalStrict(data, c); err != nil {
		// Unknown keys (e.g. a section added by a newer version) are a warning, not a
		// failure, so a running older binary keeps working. Real syntax errors still fail.
		c = Default()
		if err2 := yaml.Unmarshal(data, c); err2 != nil {
			return nil, fmt.Errorf("parse config: %w", err2)
		}
		fmt.Fprintf(os.Stderr, "warning: config: %v\n", err)
	}
	c.fillDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func Default() *Config {
	c := &Config{}
	c.fillDefaults()
	return c
}

func (c *Config) fillDefaults() {
	if c.App.Namespace == "" {
		c.App.Namespace = "default"
	}
	if c.App.ServiceLabel == "" {
		c.App.ServiceLabel = "app.kubernetes.io/name"
	}
	if c.Loki.EventsSelector == "" {
		c.Loki.EventsSelector = `{job="kubernetes-events"}`
	}
	if c.Limits.LogLines <= 0 {
		c.Limits.LogLines = 20
	}
	if c.Limits.Issues <= 0 {
		c.Limits.Issues = 20
	}
	if c.Limits.Changes <= 0 {
		c.Limits.Changes = 15
	}
	if c.Limits.TimeoutSeconds <= 0 {
		c.Limits.TimeoutSeconds = 15
	}
	if c.Agent.BaseURL == "" {
		c.Agent.BaseURL = "http://127.0.0.1:11434/v1"
	}
	if c.Agent.Model == "" {
		c.Agent.Model = "qwen2.5:3b"
	}
	if c.Agent.MaxSteps <= 0 {
		c.Agent.MaxSteps = 8
	}
	if c.Agent.MaxTokens <= 0 {
		c.Agent.MaxTokens = 768
	}
	if c.Knowledge.EmbedModel == "" {
		c.Knowledge.EmbedModel = "nomic-embed-text"
	}
	if c.Knowledge.TopK <= 0 {
		c.Knowledge.TopK = 5
	}
	if c.Knowledge.Service == "" {
		c.Knowledge.Service = "rehearse/postgres:5432"
	}
	if c.Knowledge.Secret == "" {
		c.Knowledge.Secret = "rehearse/rehearse-db"
	}
	if c.Gate.Service == "" && c.Gate.URL == "" {
		c.Gate.Service = "rehearse/gate:8080"
	}
	if c.Gate.TokenSecret == "" {
		c.Gate.TokenSecret = "rehearse/rehearse-gate"
	}
	if c.Gate.Sandbox.Mode == "" {
		c.Gate.Sandbox.Mode = "namespace"
	}
	if c.Gate.Sandbox.Timeout <= 0 {
		c.Gate.Sandbox.Timeout = 180
	}
	if c.Agent.TraceDir == "" {
		c.Agent.TraceDir = "traces"
	}
}

func (c *Config) Validate() error {
	for name, b := range map[string]Backend{"prometheus": c.Prometheus, "loki": c.Loki.Backend} {
		if b.URL == "" && b.Service == "" {
			return fmt.Errorf("config: %s needs `service` or `url`", name)
		}
		if b.URL == "" {
			if _, err := ParseServiceRef(b.Service); err != nil {
				return fmt.Errorf("config: %s: %w", name, err)
			}
		}
	}
	return nil
}

// ServiceRef is a parsed "namespace/name:port".
type ServiceRef struct {
	Namespace string
	Name      string
	Port      int
}

func ParseServiceRef(s string) (ServiceRef, error) {
	ns, rest, ok := strings.Cut(s, "/")
	if !ok || ns == "" {
		return ServiceRef{}, fmt.Errorf("service %q: want namespace/name:port", s)
	}
	name, portStr, ok := strings.Cut(rest, ":")
	if !ok || name == "" {
		return ServiceRef{}, fmt.Errorf("service %q: want namespace/name:port", s)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return ServiceRef{}, fmt.Errorf("service %q: bad port", s)
	}
	return ServiceRef{Namespace: ns, Name: name, Port: port}, nil
}
