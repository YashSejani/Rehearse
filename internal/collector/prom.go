package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Prom is a tiny client for Prometheus's HTTP API (instant queries only).
type Prom struct {
	G   Getter
	Now func() time.Time
}

// Sample is one series of an instant-query result.
type Sample struct {
	Labels map[string]string
	Value  float64
}

type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// Query runs an instant PromQL query.
func (p *Prom) Query(ctx context.Context, q string) ([]Sample, error) {
	params := url.Values{"query": {q}}
	if p.Now != nil {
		params.Set("time", strconv.FormatInt(p.Now().Unix(), 10))
	}
	body, err := p.G.Get(ctx, "/api/v1/query", params)
	if err != nil {
		return nil, fmt.Errorf("prometheus query: %w", err)
	}
	var r promResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("prometheus response: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", r.Error)
	}
	out := make([]Sample, 0, len(r.Data.Result))
	for _, s := range r.Data.Result {
		str, _ := s.Value[1].(string)
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			continue
		}
		out = append(out, Sample{Labels: s.Metric, Value: v})
	}
	return out, nil
}

// Scalar returns the first sample's value, or (0, false) when the result is empty.
func (p *Prom) Scalar(ctx context.Context, q string) (float64, bool, error) {
	s, err := p.Query(ctx, q)
	if err != nil || len(s) == 0 {
		return 0, false, err
	}
	return s[0].Value, true, nil
}
