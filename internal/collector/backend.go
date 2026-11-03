// Package collector gathers evidence about a Kubernetes app: unhealthy pods, error logs,
// request metrics and recent changes. Every function returns a small typed struct, not
// text, because these functions become the agent's tools (Phase 3) and a small model
// reads short structured data much better than raw dumps.
package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"k8s.io/client-go/kubernetes"

	"github.com/YashSejani/Rehearse/internal/config"
)

// Getter does one HTTP GET against a backend (Prometheus or Loki) and returns the body.
// It is an interface so the code does not care HOW the backend is reached (Kubernetes
// API proxy or a plain URL), and tests can plug in an httptest server.
type Getter interface {
	Get(ctx context.Context, path string, query url.Values) ([]byte, error)
}

// NewGetter picks the right Getter for a configured backend.
func NewGetter(b config.Backend, kube kubernetes.Interface) (Getter, error) {
	if b.URL != "" {
		return &URLGetter{Base: strings.TrimRight(b.URL, "/"), Client: http.DefaultClient}, nil
	}
	ref, err := config.ParseServiceRef(b.Service)
	if err != nil {
		return nil, err
	}
	if kube == nil {
		return nil, fmt.Errorf("backend %s needs a Kubernetes client", b.Service)
	}
	return &ProxyGetter{Kube: kube, Ref: ref}, nil
}

// URLGetter calls a backend directly, e.g. http://localhost:9090.
type URLGetter struct {
	Base   string
	Client *http.Client
}

func (g *URLGetter) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := g.Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

// ProxyGetter reaches an in-cluster Service through the Kubernetes API server
// (/api/v1/namespaces/<ns>/services/<name>:<port>/proxy/<path>), using the same
// kubeconfig as kubectl. Only GET is ever used.
type ProxyGetter struct {
	Kube kubernetes.Interface
	Ref  config.ServiceRef
}

func (g *ProxyGetter) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	params := make(map[string]string, len(query))
	for k, v := range query {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	body, err := g.Kube.CoreV1().Services(g.Ref.Namespace).
		ProxyGet("http", g.Ref.Name, strconv.Itoa(g.Ref.Port), path, params).
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("proxy GET %s/%s%s: %w", g.Ref.Namespace, g.Ref.Name, path, err)
	}
	return body, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
