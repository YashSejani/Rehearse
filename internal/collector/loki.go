package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Loki is a tiny client for Loki's query_range API.
type Loki struct {
	G Getter
}

// LogEntry is one raw log line from Loki.
type LogEntry struct {
	Time   time.Time
	Labels map[string]string
	Line   string
}

type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// QueryRange returns up to limit log lines matching a LogQL query, newest first.
func (l *Loki) QueryRange(ctx context.Context, q string, start, end time.Time, limit int) ([]LogEntry, error) {
	params := url.Values{
		"query":     {q},
		"start":     {strconv.FormatInt(start.UnixNano(), 10)},
		"end":       {strconv.FormatInt(end.UnixNano(), 10)},
		"limit":     {strconv.Itoa(limit)},
		"direction": {"backward"},
	}
	body, err := l.G.Get(ctx, "/loki/api/v1/query_range", params)
	if err != nil {
		return nil, fmt.Errorf("loki query: %w", err)
	}
	var r lokiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("loki response: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("loki: status %q", r.Status)
	}
	var out []LogEntry
	for _, s := range r.Data.Result {
		for _, v := range s.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			out = append(out, LogEntry{Time: time.Unix(0, ns), Labels: s.Stream, Line: v[1]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}
