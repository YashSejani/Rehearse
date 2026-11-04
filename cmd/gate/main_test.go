package main

import (
	"testing"

	"github.com/YashSejani/Rehearse/internal/knowledge"
)

func TestBaselineErrorRatio(t *testing.T) {
	m := &knowledge.ServiceMap{Baselines: []knowledge.Baseline{
		{Service: "user", Metric: "endpoint:user:error_ratio", Value: 0.01},
		{Service: "user", Metric: "endpoint:reservation:error_ratio", Value: 0.03},
		{Service: "user", Metric: "endpoint:user:p95_ms", Value: 12},
		{Service: "geo", Metric: "endpoint:hotels:error_ratio", Value: 0.5},
	}}
	if got := baselineErrorRatio(m, "user"); got != 0.03 {
		t.Errorf("got %v, want the worst endpoint of user (0.03)", got)
	}
	if got := baselineErrorRatio(m, "nope"); got != 0 {
		t.Errorf("unknown service: %v", got)
	}
}
