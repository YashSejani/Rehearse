package collector

import (
	"context"
	"strings"
	"testing"
	"time"
)

func vec(labels, value string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[{"metric":{` + labels + `},"value":[1730700000,"` + value + `"]}]}}`
}

func vec2(a, av, b, bv string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` +
		`{"metric":{"endpoint":"` + a + `"},"value":[1,"` + av + `"]},` +
		`{"metric":{"endpoint":"` + b + `"},"value":[1,"` + bv + `"]}]}}`
}

func TestMetrics(t *testing.T) {
	fb := &fakeBackend{rules: []rule{
		{`expected_response="false"`, vec2("user", "0.8", "reservation", "0.2")},
		{"rate(k6_http_reqs_total", vec2("user", "0.8", "reservation", "0.8")},
		{"k6_http_req_duration_p95", vec2("user", "0.0042", "reservation", "0.0071")},
		{"kube_deployment_spec_replicas", vec(`"deployment":"user"`, "1")},
		{"kube_deployment_status_replicas_available", vec(`"deployment":"user"`, "0")},
		{"restarts_total", vec("", "3")},
		{"working_set", vec("", "6.5")},
		{"last_terminated_reason", vec(`"reason":"OOMKilled"`, "1")},
	}}
	c := newTestCollector(t, fb)
	m, err := c.Metrics(context.Background(), "user", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Endpoints) != 2 || m.Endpoints[0].Endpoint != "user" {
		t.Fatalf("endpoints %+v", m.Endpoints)
	}
	if m.Endpoints[0].ErrorRatio != 1 || m.Endpoints[1].ErrorRatio != 0.25 {
		t.Errorf("error ratios %v / %v", m.Endpoints[0].ErrorRatio, m.Endpoints[1].ErrorRatio)
	}
	if m.Endpoints[0].P95Ms != 4.2 {
		t.Errorf("p95 %v ms", m.Endpoints[0].P95Ms)
	}
	if m.DesiredReplicas != 1 || m.AvailableReplicas != 0 || m.Restarts != 3 || m.MemoryMiB != 6.5 {
		t.Errorf("pod numbers %+v", m)
	}
	if m.LastTerminated != "OOMKilled" || m.MemoryLimitMiB != 0 {
		t.Errorf("last terminated %q, limit %v", m.LastTerminated, m.MemoryLimitMiB)
	}
	for _, q := range fb.queries {
		if strings.Contains(q, "pod=~") && !strings.Contains(q, `pod=~"user-[a-z0-9]+-[a-z0-9]+"`) {
			t.Errorf("pod selector would also match other services: %s", q)
		}
		if strings.Contains(q, "[") && !strings.Contains(q, "[5m]") && strings.Contains(q, "rate(") {
			t.Errorf("window not applied: %s", q)
		}
	}
}

func TestMetricsNoEndpoints(t *testing.T) {
	c := newTestCollector(t, &fakeBackend{})
	m, err := c.Metrics(context.Background(), "mongodb-geo", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Endpoints) != 0 || m.MemoryMiB != 0 {
		t.Errorf("expected empty metrics, got %+v", m)
	}
}

func TestPromDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{10 * time.Second: "1m", 15 * time.Minute: "15m", 2 * time.Hour: "2h", 90 * time.Minute: "90m"} {
		if got := promDuration(d); got != want {
			t.Errorf("%v: %s, want %s", d, got, want)
		}
	}
}

func TestPromError(t *testing.T) {
	fb := &fakeBackend{rules: []rule{{"", `{"status":"error","error":"parse error"}`}}}
	c := newTestCollector(t, fb)
	if _, err := c.Metrics(context.Background(), "user", time.Minute); err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Errorf("want prometheus error, got %v", err)
	}
}
