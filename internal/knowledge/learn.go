package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/YashSejani/Rehearse/internal/collector"
)

// Service is one deployment of the app as Rehearse understands it.
type Service struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"` // app, datastore, cache, infra
	Image    string  `json:"image"`
	Replicas int32   `json:"replicas"`
	Ports    []int32 `json:"ports,omitempty"`
}

// Edge says From depends on (calls) To.
type Edge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Source string `json:"source"` // trace, env, name
	Calls  int64  `json:"calls,omitempty"`
}

// Baseline is one normal value measured while the app was healthy.
type Baseline struct {
	Service string  `json:"service"`
	Metric  string  `json:"metric"`
	Value   float64 `json:"value"`
	Window  string  `json:"window"`
}

// ServiceMap is the result of `rehearse learn`.
type ServiceMap struct {
	Namespace string     `json:"namespace"`
	Services  []Service  `json:"services"`
	Edges     []Edge     `json:"edges"`
	Baselines []Baseline `json:"baselines"`
	Notes     []string   `json:"notes,omitempty"`
}

// Learner discovers the app from the cluster. Every source is optional; what fails is noted.
type Learner struct {
	Kube         kubernetes.Interface
	Jaeger       collector.Getter // may be nil
	Prom         *collector.Prom  // may be nil
	Namespace    string
	ServiceLabel string
	Endpoints    map[string][]string // service -> k6 endpoints (from config)
	Now          func() time.Time
}

// kindOf classifies a deployment by its image. Generic: works for any app using these
// common building blocks.
func kindOf(image string) string {
	// Compare whole name parts, not substrings: "hotelreservation" must not match "otel".
	parts := map[string]bool{}
	for _, p := range strings.FieldsFunc(strings.ToLower(image), func(r rune) bool {
		return r == '/' || r == ':' || r == '@' || r == '-' || r == '.' || r == '_'
	}) {
		parts[p] = true
	}
	has := func(names ...string) bool {
		for _, n := range names {
			if parts[n] {
				return true
			}
		}
		return false
	}
	switch {
	case has("mongo", "mongodb", "postgres", "postgresql", "pgvector", "mysql", "mariadb", "cassandra", "elasticsearch", "etcd"):
		return "datastore"
	case has("memcached", "redis", "valkey"):
		return "cache"
	case has("consul", "jaeger", "jaegertracing", "prometheus", "grafana", "otel", "opentelemetry", "zipkin"):
		return "infra"
	default:
		return "app"
	}
}

func (l *Learner) Learn(ctx context.Context) (*ServiceMap, error) {
	m := &ServiceMap{Namespace: l.Namespace}
	deps, err := l.Kube.AppsV1().Deployments(l.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	byName := map[string]*Service{}
	for _, d := range deps.Items {
		s := serviceFromDeployment(&d)
		m.Services = append(m.Services, s)
	}
	sort.Slice(m.Services, func(i, j int) bool { return m.Services[i].Name < m.Services[j].Name })
	for i := range m.Services {
		byName[m.Services[i].Name] = &m.Services[i]
	}

	edges := map[[2]string]*Edge{}
	add := func(from, to, src string, calls int64) {
		if from == to || byName[from] == nil || byName[to] == nil {
			return
		}
		k := [2]string{from, to}
		if e, ok := edges[k]; ok {
			if src == "trace" { // trace evidence is the strongest; keep it and its count
				e.Source, e.Calls = src, calls
			}
			return
		}
		edges[k] = &Edge{From: from, To: to, Source: src, Calls: calls}
	}

	// 1. Traces: Jaeger's dependency graph (who called whom in the last hour).
	if l.Jaeger != nil {
		n, err := l.traceEdges(ctx, add)
		if err != nil {
			m.Notes = append(m.Notes, "traces: "+err.Error())
		} else {
			m.Notes = append(m.Notes, fmt.Sprintf("traces: %d caller->callee pairs from Jaeger", n))
		}
	}
	// 2. Configuration: env vars and args that mention another service's host name.
	for _, d := range deps.Items {
		for _, ref := range hostRefs(&d, byName, l.Namespace) {
			add(d.Name, ref, "env", 0)
		}
	}
	// 3. Naming convention for data stores: mongodb-geo / memcached-rate belong to geo / rate.
	for _, s := range m.Services {
		if s.Kind != "datastore" && s.Kind != "cache" {
			continue
		}
		_, suffix, ok := strings.Cut(s.Name, "-")
		if !ok {
			continue
		}
		for _, app := range m.Services {
			if app.Kind == "app" && sameStem(app.Name, suffix) {
				add(app.Name, s.Name, "name", 0)
			}
		}
	}
	for _, e := range edges {
		m.Edges = append(m.Edges, *e)
	}
	sort.Slice(m.Edges, func(i, j int) bool {
		if m.Edges[i].From != m.Edges[j].From {
			return m.Edges[i].From < m.Edges[j].From
		}
		return m.Edges[i].To < m.Edges[j].To
	})

	if l.Prom != nil {
		b, err := l.baselines(ctx, m.Services)
		if err != nil {
			m.Notes = append(m.Notes, "baselines: "+err.Error())
		}
		m.Baselines = b
	}
	return m, nil
}

func serviceFromDeployment(d *appsv1.Deployment) Service {
	s := Service{Name: d.Name, Kind: "app"}
	if d.Spec.Replicas != nil {
		s.Replicas = *d.Spec.Replicas
	}
	if cs := d.Spec.Template.Spec.Containers; len(cs) > 0 {
		s.Image = cs[0].Image
		s.Kind = kindOf(cs[0].Image)
		for _, p := range cs[0].Ports {
			s.Ports = append(s.Ports, p.ContainerPort)
		}
	}
	return s
}

// hostRefs finds other services named as hosts in env values and args, e.g.
// "mongodb-geo:27017", "http://user.hotel.svc:8086".
func hostRefs(d *appsv1.Deployment, known map[string]*Service, ns string) []string {
	var texts []string
	for _, c := range d.Spec.Template.Spec.Containers {
		texts = append(texts, c.Args...)
		texts = append(texts, c.Command...)
		for _, e := range c.Env {
			texts = append(texts, e.Value)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, m := range hostRx.FindAllStringSubmatch(t, -1) {
			h := m[1]
			h = strings.TrimSuffix(strings.TrimSuffix(h, ".svc.cluster.local"), ".svc")
			h = strings.TrimSuffix(h, "."+ns)
			if known[h] != nil && !seen[h] && h != d.Name {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	return out
}

var hostRx = regexp.MustCompile(`(?:^|[/@\s=,])([a-z0-9][a-z0-9-]*(?:\.[a-z0-9-]+)*)(?::\d+|/|$)`)

func (l *Learner) traceEdges(ctx context.Context, add func(from, to, src string, calls int64)) (int, error) {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	q := url.Values{
		"endTs":    {strconv.FormatInt(now.UnixMilli(), 10)},
		"lookback": {strconv.FormatInt(time.Hour.Milliseconds(), 10)},
	}
	body, err := l.Jaeger.Get(ctx, "/api/dependencies", q)
	if err != nil {
		return 0, err
	}
	var r struct {
		Data []struct {
			Parent    string `json:"parent"`
			Child     string `json:"child"`
			CallCount int64  `json:"callCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, fmt.Errorf("jaeger dependencies: %w", err)
	}
	for _, d := range r.Data {
		add(d.Parent, d.Child, "trace", d.CallCount)
	}
	return len(r.Data), nil
}

// baselines measures normal behaviour over the last hour: endpoint error ratio, p95 and
// rate, and per-service memory.
func (l *Learner) baselines(ctx context.Context, services []Service) ([]Baseline, error) {
	var out []Baseline
	const w = "1h"
	ns := l.Namespace
	seen := map[string]bool{}
	for svc, eps := range l.Endpoints {
		for _, ep := range eps {
			queries := map[string]string{
				"error_ratio": fmt.Sprintf(`sum(rate(k6_http_reqs_total{endpoint=%q, target_ns=%q, expected_response="false"}[%s])) / sum(rate(k6_http_reqs_total{endpoint=%q, target_ns=%q}[%s]))`, ep, ns, w, ep, ns, w),
				"p95_ms":      fmt.Sprintf(`histogram_quantile(0.95, sum(rate(k6_http_req_duration_seconds{endpoint=%q, target_ns=%q}[%s]))) * 1000`, ep, ns, w),
				"rate":        fmt.Sprintf(`sum(rate(k6_http_reqs_total{endpoint=%q, target_ns=%q}[%s]))`, ep, ns, w),
			}
			for name, q := range queries {
				key := svc + "/" + ep + "/" + name
				if seen[key] {
					continue
				}
				seen[key] = true
				v, ok, err := l.Prom.Scalar(ctx, q)
				if err != nil {
					return out, err
				}
				if ok {
					out = append(out, Baseline{Service: svc, Metric: "endpoint:" + ep + ":" + name, Value: round3(v), Window: w})
				}
			}
		}
	}
	for _, s := range services {
		q := fmt.Sprintf(`avg_over_time(sum(container_memory_working_set_bytes{namespace=%q, pod=~"%s-[a-z0-9]+-[a-z0-9]+", container!="", container!="POD"})[%s:1m]) / 1048576`, ns, s.Name, w)
		v, ok, err := l.Prom.Scalar(ctx, q)
		if err != nil {
			return out, err
		}
		if ok {
			out = append(out, Baseline{Service: s.Name, Metric: "memory_mib", Value: round3(v), Window: w})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Metric < out[j].Metric
	})
	return out, nil
}

// Save replaces the stored map of this namespace with m.
func (s *Store) SaveMap(ctx context.Context, m *ServiceMap) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{`DELETE FROM services WHERE namespace=$1`, `DELETE FROM edges WHERE namespace=$1`, `DELETE FROM baselines WHERE namespace=$1`} {
		if _, err := tx.Exec(ctx, q, m.Namespace); err != nil {
			return err
		}
	}
	for _, sv := range m.Services {
		ports, _ := json.Marshal(sv.Ports)
		if _, err := tx.Exec(ctx, `INSERT INTO services(namespace,name,kind,image,replicas,ports) VALUES ($1,$2,$3,$4,$5,$6)`,
			m.Namespace, sv.Name, sv.Kind, sv.Image, sv.Replicas, ports); err != nil {
			return fmt.Errorf("save service: %w", err)
		}
	}
	for _, e := range m.Edges {
		if _, err := tx.Exec(ctx, `INSERT INTO edges(namespace,from_svc,to_svc,source,calls) VALUES ($1,$2,$3,$4,$5)`,
			m.Namespace, e.From, e.To, e.Source, e.Calls); err != nil {
			return fmt.Errorf("save edge: %w", err)
		}
	}
	for _, b := range m.Baselines {
		if _, err := tx.Exec(ctx, `INSERT INTO baselines(namespace,service,metric,value,window_text) VALUES ($1,$2,$3,$4,$5)`,
			m.Namespace, b.Service, b.Metric, b.Value, b.Window); err != nil {
			return fmt.Errorf("save baseline: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// LoadMap reads the stored map of a namespace.
func (s *Store) LoadMap(ctx context.Context, ns string) (*ServiceMap, error) {
	m := &ServiceMap{Namespace: ns}
	rows, err := s.Pool.Query(ctx, `SELECT name, kind, image, replicas FROM services WHERE namespace=$1 ORDER BY name`, ns)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sv Service
		if err := rows.Scan(&sv.Name, &sv.Kind, &sv.Image, &sv.Replicas); err != nil {
			rows.Close()
			return nil, err
		}
		m.Services = append(m.Services, sv)
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT from_svc, to_svc, source, calls FROM edges WHERE namespace=$1 ORDER BY from_svc, to_svc`, ns)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.From, &e.To, &e.Source, &e.Calls); err != nil {
			rows.Close()
			return nil, err
		}
		m.Edges = append(m.Edges, e)
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT service, metric, value, window_text FROM baselines WHERE namespace=$1 ORDER BY service, metric`, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b Baseline
		if err := rows.Scan(&b.Service, &b.Metric, &b.Value, &b.Window); err != nil {
			return nil, err
		}
		m.Baselines = append(m.Baselines, b)
	}
	return m, rows.Err()
}

// Neighbours returns what svc depends on and what depends on svc.
func (m *ServiceMap) Neighbours(svc string) (dependsOn, usedBy []string) {
	for _, e := range m.Edges {
		if e.From == svc {
			dependsOn = append(dependsOn, e.To)
		}
		if e.To == svc {
			usedBy = append(usedBy, e.From)
		}
	}
	return
}

// sameStem: "reservation" and "reserve" share a stem; "geo" and "geo" are equal.
// At least 5 shared leading letters, covering 80% of the shorter name.
func sameStem(a, b string) bool {
	if a == b {
		return true
	}
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n >= 5 && float64(n) >= 0.8*float64(min(len(a), len(b)))
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

func round3(v float64) float64 { return float64(int64(v*1000+0.5)) / 1000 }
