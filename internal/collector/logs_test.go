package collector

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"\x1b[90m2026-11-03T21:00:04Z\x1b[0m \x1b[31mERR\x1b[0m cmd/geo/db.go:30 > auth failed": "ERR cmd/geo/db.go:30 > auth failed",
		"2026/11/03 20:57:50 grpc/lb/consul: error retrieving instances":                        "grpc/lb/consul: error retrieving instances",
		"id 7ebb5d1b-0ec1-450e-b082-a3ee4415bf0f failed at 12:01:02.123":                        "id <id> failed at",
		"   lots    of\tspace  ": "lots of space",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDedup(t *testing.T) {
	var entries []LogEntry
	for i := range 347 {
		entries = append(entries, LogEntry{
			Time: testNow.Add(time.Duration(i) * time.Second),
			Line: fmt.Sprintf("2026/11/03 20:57:%02d grpc/lb/consul: connect: connection refused", i%60),
		})
	}
	entries = append(entries, LogEntry{Time: testNow, Line: "ERR auth failed"})
	got := Dedup(entries, "app")
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(got), got)
	}
	if got[0].Count != 347 || got[0].Source != "app" {
		t.Errorf("first line count %d, want 347", got[0].Count)
	}
	if !got[0].First.Equal(testNow) || !got[0].Last.Equal(testNow.Add(346*time.Second)) {
		t.Errorf("time range %v..%v", got[0].First, got[0].Last)
	}
	if got[1].Count != 1 {
		t.Errorf("second line count %d", got[1].Count)
	}
	if len(Dedup(nil, "app")) != 0 {
		t.Error("empty input should give empty output")
	}
}

func TestCompactEvent(t *testing.T) {
	in := `name=geo-b67c9dff5-7hpcg kind=Pod objectAPIversion=v1 objectRV=161557 eventRV=161706 reportingcontroller=kubelet reason=Failed type=Warning count=3 msg="Failed to pull image \"yinfangchen/hotelreservation:does-not-exist\": not found" `
	want := `Warning Failed Pod/geo-b67c9dff5-7hpcg: Failed to pull image "yinfangchen/hotelreservation:does-not-exist": not found`
	if got := CompactEvent(in); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := CompactEvent("plain text"); got != "plain text" {
		t.Errorf("non-event line changed: %q", got)
	}
}

func TestLogs(t *testing.T) {
	ts := func(d time.Duration) string { return fmt.Sprint(testNow.Add(-d).UnixNano()) }
	app := fmt.Sprintf(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"app":"geo"},"values":[
		["%s","2026-11-04T11:59:00Z ERR auth failed"],["%s","2026-11-04T11:58:00Z ERR auth failed"]]}]}}`, ts(time.Minute), ts(2*time.Minute))
	ev := fmt.Sprintf(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"job":"kubernetes-events"},"values":[
		["%s","name=geo-1-a kind=Pod reason=BackOff type=Normal msg=\"Back-off pulling image\""]]}]}}`, ts(10*time.Second))
	fb := &fakeBackend{rules: []rule{{"kubernetes-events", ev}, {`app="geo"`, app}}}
	c := newTestCollector(t, fb)

	ll, err := c.Logs(context.Background(), "geo", 5, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ll.Raw != 3 || len(ll.Lines) != 2 {
		t.Fatalf("raw %d lines %d: %+v", ll.Raw, len(ll.Lines), ll.Lines)
	}
	if ll.Lines[0].Source != "event" || !strings.HasPrefix(ll.Lines[0].Text, "Normal BackOff Pod/geo-1-a") {
		t.Errorf("newest first should be the event: %+v", ll.Lines[0])
	}
	if ll.Lines[1].Count != 2 {
		t.Errorf("app line count %d, want 2", ll.Lines[1].Count)
	}
	// The queries must be scoped to our namespace and service.
	for _, q := range fb.queries {
		if !strings.Contains(q, `namespace="hotel"`) || !strings.Contains(q, "geo") {
			t.Errorf("query not scoped: %s", q)
		}
	}
	if _, err := c.Logs(context.Background(), `geo"} or {x="`, 5, time.Minute); err == nil {
		t.Error("expected error for a service name that could inject LogQL")
	}
}

func TestCrashedSince(t *testing.T) {
	since := testNow.Add(-10 * time.Minute)
	ended := func(at time.Time, restarts int32) corev1.ContainerStatus {
		return corev1.ContainerStatus{RestartCount: restarts, LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(at)}}}
	}
	cases := []struct {
		name string
		cs   corev1.ContainerStatus
		want bool
	}{
		{"crash inside the window", ended(testNow.Add(-time.Minute), 1), true},
		{"old crash or node reboot", ended(testNow.Add(-time.Hour), 7), false},
		{"never restarted", ended(testNow, 0), false},
		{"no termination recorded", corev1.ContainerStatus{RestartCount: 1}, false},
	}
	for _, c := range cases {
		if got := crashedSince(c.cs, since); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCrashLines(t *testing.T) {
	raw := "\x1b[32mINF\x1b[0m cmd/geo/db.go:31 > Generating test data...\n" +
		"\x1b[31mFTL\x1b[0m cmd/geo/db.go:35 > not authorized on geo-db to execute command\n" +
		"unable to retrieve container logs for containerd://0229\n" +
		"failed to try resolving symlinks in path \"/var/log/pods/hotel_geo/0.log\": no such file\n"
	got := crashLines(raw)
	if len(got) != 1 || !strings.Contains(got[0], "not authorized on geo-db") {
		t.Errorf("crashLines = %q, want only the FTL line", got)
	}
}

func TestErrorFilterFindsFatalWithoutTheWordError(t *testing.T) {
	for _, line := range []string{"FTL cmd/x.go:1 > bad", "not authorized on geo-db", "Unauthorized"} {
		if !errLineRx.MatchString(line) {
			t.Errorf("errorFilter missed %q", line)
		}
	}
	if errLineRx.MatchString("INF cmd/geo/main.go:23 > Reading config...") {
		t.Error("errorFilter matched a normal line")
	}
}

func TestNewLines(t *testing.T) {
	have := []LogLine{{Text: "same"}}
	got := newLines([]LogLine{{Text: "same"}, {Text: "other"}}, have)
	if len(got) != 1 || got[0].Text != "other" {
		t.Errorf("newLines = %v", got)
	}
}
