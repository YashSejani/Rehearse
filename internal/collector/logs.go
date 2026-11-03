package collector

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LogLine is one distinct log message and how often it appeared.
type LogLine struct {
	Text   string    `json:"text"`
	Count  int       `json:"count"`
	Source string    `json:"source"` // "app" (container stdout) or "event" (Kubernetes event)
	First  time.Time `json:"first"`
	Last   time.Time `json:"last"`
}

// LogLines is the result of Logs: deduplicated error lines for one service.
type LogLines struct {
	Service string    `json:"service"`
	Window  string    `json:"window"`
	Raw     int       `json:"raw_lines"` // lines read from Loki before dedup
	Lines   []LogLine `json:"lines"`     // capped at limits.log_lines, newest first
}

// errorFilter keeps lines that look like problems. Case-insensitive.
const errorFilter = `(?i)(err|fail|fatal|panic|refused|timeout|timed out|denied|unauthori|exception|oom|killed|back-off|unable|cannot|could not)`

// eventNoise are routine Kubernetes event reasons that never explain a failure.
const eventNoise = `reason=(Scheduled|Pulled|Pulling|Created|Started|SuccessfulCreate|SuccessfulDelete)\b`

// Logs returns the most recent distinct error lines of one service from Loki, plus the
// Kubernetes events about its pods and deployment (a service that is down cannot log).
func (c *Collector) Logs(ctx context.Context, service string, lines int, window time.Duration) (*LogLines, error) {
	if lines <= 0 || lines > c.Cfg.Limits.LogLines {
		lines = c.Cfg.Limits.LogLines
	}
	if !validName.MatchString(service) {
		return nil, fmt.Errorf("invalid service name %q", service)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	end := c.Now()
	start := end.Add(-window)
	const readLimit = 1000 // raw lines read; dedup shrinks them to `lines`

	appQ := fmt.Sprintf(`{namespace=%q, app=%q} |~ %q != "fsnotify" != "unable to retrieve container logs"`, c.ns(), service, errorFilter)
	app, err := c.Loki.QueryRange(ctx, appQ, start, end, readLimit)
	if err != nil {
		return nil, fmt.Errorf("app logs: %w", err)
	}
	evSel := strings.TrimSuffix(strings.TrimSpace(c.Cfg.Loki.EventsSelector), "}") + fmt.Sprintf(`, namespace=%q}`, c.ns())
	// Events about pods (name=geo-7d9...-x2b), replica sets and the deployment itself (name=geo ).
	evQ := fmt.Sprintf(`%s |~ %q !~ %q`, evSel, `name=`+regexp.QuoteMeta(service)+`(-[a-z0-9]+)* `, eventNoise)
	events, err := c.Loki.QueryRange(ctx, evQ, start, end, readLimit)
	if err != nil {
		return nil, fmt.Errorf("event logs: %w", err)
	}

	for i := range events {
		events[i].Line = CompactEvent(events[i].Line)
	}
	merged := append(Dedup(app, "app"), Dedup(events, "event")...)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Last.After(merged[j].Last) })
	if len(merged) > lines {
		merged = merged[:lines]
	}
	return &LogLines{Service: service, Window: window.String(), Raw: len(app) + len(events), Lines: merged}, nil
}

var (
	ansiRx = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	// Timestamps in the formats we meet: ISO 8601, Go's log "2026/11/03 20:57:50", time only.
	tsRx = regexp.MustCompile(`\d{4}[-/]\d{2}[-/]\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?|\b\d{2}:\d{2}:\d{2}(\.\d+)?\b`)
	// Things that differ between otherwise identical lines.
	uuidRx  = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	rvRx    = regexp.MustCompile(`\b(objectRV|eventRV|count)=\S+`)
	spaceRx = regexp.MustCompile(`\s+`)

	validName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// Normalize strips what makes identical messages look different: colour codes,
// timestamps, UUIDs and event counters. Idea from AIOpsLab's log_deduplication.py.
func Normalize(line string) string {
	s := ansiRx.ReplaceAllString(line, "")
	s = tsRx.ReplaceAllString(s, "")
	s = uuidRx.ReplaceAllString(s, "<id>")
	s = rvRx.ReplaceAllString(s, "")
	s = spaceRx.ReplaceAllString(s, " ")
	return truncate(s, 300)
}

// CompactEvent shortens a logfmt Kubernetes event line from Alloy to the fields that
// matter: "Warning BackOff Pod/geo-7d9-x2b: Back-off pulling image ...".
func CompactEvent(line string) string {
	f := parseLogfmt(line)
	if f["reason"] == "" {
		return line
	}
	return fmt.Sprintf("%s %s %s/%s: %s", nonEmpty(f["type"], "Normal"), f["reason"], f["kind"], f["name"], f["msg"])
}

// parseLogfmt reads key=value pairs; values may be "quoted" with \" escapes.
func parseLogfmt(s string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(s); {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := s[i : i+eq]
		i += eq + 1
		var val strings.Builder
		if i < len(s) && s[i] == '"' {
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				val.WriteByte(s[i])
				i++
			}
			i++ // closing quote
		} else {
			for i < len(s) && s[i] != ' ' {
				val.WriteByte(s[i])
				i++
			}
		}
		out[key] = val.String()
	}
	return out
}

// Dedup collapses entries with the same normalized text into one LogLine with a count.
// Entries may come in any order; First/Last record the time range of each message.
func Dedup(entries []LogEntry, source string) []LogLine {
	index := map[string]int{}
	var out []LogLine
	for _, e := range entries {
		text := Normalize(e.Line)
		if text == "" {
			continue
		}
		if i, ok := index[text]; ok {
			out[i].Count++
			if e.Time.Before(out[i].First) {
				out[i].First = e.Time
			}
			if e.Time.After(out[i].Last) {
				out[i].Last = e.Time
			}
			continue
		}
		index[text] = len(out)
		out = append(out, LogLine{Text: text, Count: 1, Source: source, First: e.Time, Last: e.Time})
	}
	return out
}
