package config

import (
	"os"
	"testing"
)

func TestParseRepoConfig(t *testing.T) {
	data, err := os.ReadFile("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data)
	if err != nil {
		t.Fatalf("repo config.yaml does not parse: %v", err)
	}
	if c.App.Namespace != "hotel" || c.App.ServiceLabel != "io.kompose.service" {
		t.Errorf("app = %+v", c.App)
	}
	if got := c.App.Endpoints["user"]; len(got) != 2 {
		t.Errorf("user endpoints = %v", got)
	}
	ref, err := ParseServiceRef(c.Prometheus.Service)
	if err != nil || ref.Namespace != "monitoring" || ref.Port != 9090 {
		t.Errorf("prometheus ref = %+v, %v", ref, err)
	}
	if c.Loki.EventsSelector == "" {
		t.Error("loki events selector empty")
	}
}

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte("prometheus: {url: http://p:9090}\nloki: {url: http://l:3100}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.LogLines != 20 || c.Agent.Model != "qwen2.5:3b" || c.Agent.MaxSteps != 8 {
		t.Errorf("defaults not applied: %+v %+v", c.Limits, c.Agent)
	}
}

func TestUnknownFieldIsWarning(t *testing.T) {
	if _, err := Parse([]byte("prometheus: {url: http://p}\nloki: {url: http://l}\nfuture_section: {a: 1}\n")); err != nil {
		t.Errorf("unknown section should only warn: %v", err)
	}
	if _, err := Parse([]byte("prometheus: [not, a, map\n")); err == nil {
		t.Error("broken YAML must still fail")
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]string{
		"missing backend": "loki: {url: http://l}\n",
		"bad ref":         "prometheus: {service: monitoring-prom}\nloki: {url: http://l}\n",
		"bad port":        "prometheus: {service: monitoring/prom:abc}\nloki: {url: http://l}\n",
	}
	for name, yml := range cases {
		if _, err := Parse([]byte(yml)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
