package collector

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// EndpointMetrics is what users experience on one HTTP endpoint (measured by the load
// generator, because the app has no metrics of its own).
type EndpointMetrics struct {
	Endpoint   string  `json:"endpoint"`
	RatePerSec float64 `json:"rate_per_sec"`
	ErrorRatio float64 `json:"error_ratio"` // 0..1
	P95Ms      float64 `json:"p95_ms"`
}

// ServiceMetrics summarises one service over a time window.
type ServiceMetrics struct {
	Service           string            `json:"service"`
	Window            string            `json:"window"`
	Endpoints         []EndpointMetrics `json:"endpoints,omitempty"`
	DesiredReplicas   float64           `json:"desired_replicas"`
	AvailableReplicas float64           `json:"available_replicas"`
	Restarts          float64           `json:"restarts"` // container restarts in the window
	LastTerminated    string            `json:"last_terminated,omitempty"`
	MemoryMiB         float64           `json:"memory_mib"`
	MemoryLimitMiB    float64           `json:"memory_limit_mib,omitempty"` // 0 = no limit
	CPUCores          float64           `json:"cpu_cores"`
}

// Metrics reads request, error and latency numbers for the endpoints a service serves,
// and pod health numbers (replicas, restarts, memory, CPU) from Prometheus.
func (c *Collector) Metrics(ctx context.Context, service string, window time.Duration) (*ServiceMetrics, error) {
	if !validName.MatchString(service) {
		return nil, fmt.Errorf("invalid service name %q", service)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	w := promDuration(window)
	ns := c.ns()
	// Pods of a Deployment are named <deployment>-<replicaset hash>-<pod hash>.
	pod := fmt.Sprintf(`namespace=%q, pod=~"%s-[a-z0-9]+-[a-z0-9]+"`, ns, service)
	m := &ServiceMetrics{Service: service, Window: window.String()}

	if eps := c.Cfg.App.Endpoints[service]; len(eps) > 0 {
		sel := fmt.Sprintf(`endpoint=~%q`, strings.Join(eps, "|"))
		rates, err := c.byEndpoint(ctx, fmt.Sprintf(`sum by (endpoint) (rate(k6_http_reqs_total{%s}[%s]))`, sel, w))
		if err != nil {
			return nil, err
		}
		errs, err := c.byEndpoint(ctx, fmt.Sprintf(`sum by (endpoint) (rate(k6_http_reqs_total{%s, expected_response="false"}[%s]))`, sel, w))
		if err != nil {
			return nil, err
		}
		p95, err := c.byEndpoint(ctx, fmt.Sprintf(`max by (endpoint) (max_over_time(k6_http_req_duration_p95{%s}[%s]))`, sel, w))
		if err != nil {
			return nil, err
		}
		for _, ep := range eps {
			em := EndpointMetrics{Endpoint: ep, RatePerSec: round(rates[ep], 3), P95Ms: round(p95[ep]*1000, 1)}
			if rates[ep] > 0 {
				em.ErrorRatio = round(errs[ep]/rates[ep], 3)
			}
			m.Endpoints = append(m.Endpoints, em)
		}
	}

	scalars := []struct {
		dst *float64
		q   string
	}{
		{&m.DesiredReplicas, fmt.Sprintf(`kube_deployment_spec_replicas{namespace=%q, deployment=%q}`, ns, service)},
		{&m.AvailableReplicas, fmt.Sprintf(`kube_deployment_status_replicas_available{namespace=%q, deployment=%q}`, ns, service)},
		{&m.Restarts, fmt.Sprintf(`sum(increase(kube_pod_container_status_restarts_total{%s}[%s]))`, pod, w)},
		{&m.MemoryMiB, fmt.Sprintf(`sum(container_memory_working_set_bytes{%s, container!="", container!="POD"}) / 1048576`, pod)},
		{&m.MemoryLimitMiB, fmt.Sprintf(`sum(kube_pod_container_resource_limits{%s, resource="memory"}) / 1048576`, pod)},
		{&m.CPUCores, fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{%s, container!="", container!="POD"}[%s]))`, pod, w)},
	}
	for _, s := range scalars {
		v, _, err := c.Prom.Scalar(ctx, s.q)
		if err != nil {
			return nil, err
		}
		*s.dst = round(v, 2)
	}
	last, err := c.Prom.Query(ctx, fmt.Sprintf(`kube_pod_container_status_last_terminated_reason{%s} == 1`, pod))
	if err != nil {
		return nil, err
	}
	if len(last) > 0 {
		m.LastTerminated = last[0].Labels["reason"]
	}
	return m, nil
}

func (c *Collector) byEndpoint(ctx context.Context, q string) (map[string]float64, error) {
	s, err := c.Prom.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, x := range s {
		if !math.IsNaN(x.Value) {
			out[x.Labels["endpoint"]] = x.Value
		}
	}
	return out, nil
}

// promDuration renders a Go duration in PromQL syntax (minimum 1m so rate() has 2+ samples).
func promDuration(d time.Duration) string {
	if d < time.Minute {
		d = time.Minute
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dm", d/time.Minute)
}

func round(v float64, digits int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}
