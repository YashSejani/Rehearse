package collector

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ChangeEvent is one thing that changed around a service: a rollout (new ReplicaSet
// revision), a scale change, or a Kubernetes event.
type ChangeEvent struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"` // Rollout, Current, Event
	Summary string    `json:"summary"`
	Count   int32     `json:"count,omitempty"` // for repeated events
}

// Changes lists recent rollouts of the service's Deployment (with what differs from the
// previous revision), its current replicas/limits, and recent warning events, newest first.
func (c *Collector) Changes(ctx context.Context, service string) ([]ChangeEvent, error) {
	if !validName.MatchString(service) {
		return nil, fmt.Errorf("invalid service name %q", service)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	dep, err := c.Kube.AppsV1().Deployments(c.ns()).Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", service, err)
	}
	rsList, err := c.Kube.AppsV1().ReplicaSets(c.ns()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list replicasets: %w", err)
	}

	var revs []*appsv1.ReplicaSet
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		for _, o := range rs.OwnerReferences {
			if o.Kind == "Deployment" && o.UID == dep.UID {
				revs = append(revs, rs)
			}
		}
	}
	sort.Slice(revs, func(i, j int) bool { return revision(revs[i]) < revision(revs[j]) })

	evs, err := c.Kube.CoreV1().Events(c.ns()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	// A rollback re-uses an old ReplicaSet, so its creation time is misleading. A revision
	// became active when the deployment last scaled it up; use that time when we have it.
	activated := map[string]time.Time{}
	for _, e := range evs.Items {
		if e.Reason == "ScalingReplicaSet" && strings.HasPrefix(e.Message, "Scaled up replica set ") {
			name, _, _ := strings.Cut(strings.TrimPrefix(e.Message, "Scaled up replica set "), " ")
			if t := eventTime(&e); t.After(activated[name]) {
				activated[name] = t
			}
		}
	}

	var out []ChangeEvent
	out = append(out, ChangeEvent{Time: c.Now(), Kind: "Current", Summary: currentSummary(dep)})
	for i, rs := range revs {
		summary := fmt.Sprintf("revision %d: %s", revision(rs), podSpecSummary(&rs.Spec.Template.Spec))
		if i > 0 {
			if d := podSpecDiff(&revs[i-1].Spec.Template.Spec, &rs.Spec.Template.Spec); d != "" {
				summary = fmt.Sprintf("revision %d: %s", revision(rs), d)
			}
		}
		t := rs.CreationTimestamp.Time
		if a, ok := activated[rs.Name]; ok && a.After(t) {
			t = a
		}
		out = append(out, ChangeEvent{Time: t, Kind: "Rollout", Summary: summary})
	}

	for _, e := range evs.Items {
		name := e.InvolvedObject.Name
		if name != service && !strings.HasPrefix(name, service+"-") {
			continue
		}
		// Only events that explain change or trouble; routine pod start events are noise.
		if e.Type != corev1.EventTypeWarning && e.Reason != "ScalingReplicaSet" && e.Reason != "Killing" {
			continue
		}
		out = append(out, ChangeEvent{
			Time:    eventTime(&e),
			Kind:    "Event",
			Summary: fmt.Sprintf("%s %s %s/%s: %s", e.Type, e.Reason, e.InvolvedObject.Kind, name, truncate(e.Message, 160)),
			Count:   e.Count,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > c.Cfg.Limits.Changes {
		out = out[:c.Cfg.Limits.Changes]
	}
	return out, nil
}

func revision(rs *appsv1.ReplicaSet) int {
	n, _ := strconv.Atoi(rs.Annotations["deployment.kubernetes.io/revision"])
	return n
}

func eventTime(e *corev1.Event) time.Time {
	for _, t := range []time.Time{e.LastTimestamp.Time, e.EventTime.Time, e.FirstTimestamp.Time} {
		if !t.IsZero() {
			return t
		}
	}
	return e.CreationTimestamp.Time
}

func currentSummary(d *appsv1.Deployment) string {
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	return fmt.Sprintf("now: replicas %d desired / %d available, strategy %s, %s",
		want, d.Status.AvailableReplicas, d.Spec.Strategy.Type, podSpecSummary(&d.Spec.Template.Spec))
}

// podSpecSummary describes the parts of a pod spec that faults usually touch.
func podSpecSummary(s *corev1.PodSpec) string {
	var parts []string
	for _, ct := range s.Containers {
		p := fmt.Sprintf("%s image=%s", ct.Name, ct.Image)
		if l, ok := ct.Resources.Limits[corev1.ResourceMemory]; ok {
			p += " mem-limit=" + l.String()
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "; ")
}

// podSpecDiff says what changed between two revisions: image, memory, env, ports, probes.
func podSpecDiff(old, cur *corev1.PodSpec) string {
	var d []string
	for i, ct := range cur.Containers {
		if i >= len(old.Containers) {
			d = append(d, "added container "+ct.Name)
			continue
		}
		o := old.Containers[i]
		if o.Image != ct.Image {
			d = append(d, fmt.Sprintf("%s image %s -> %s", ct.Name, o.Image, ct.Image))
		}
		ol, cl := o.Resources.Limits[corev1.ResourceMemory], ct.Resources.Limits[corev1.ResourceMemory]
		if ol.String() != cl.String() {
			d = append(d, fmt.Sprintf("%s memory limit %s -> %s", ct.Name, qty(ol.String()), qty(cl.String())))
		}
		if fmt.Sprint(o.Env) != fmt.Sprint(ct.Env) || fmt.Sprint(o.EnvFrom) != fmt.Sprint(ct.EnvFrom) {
			d = append(d, ct.Name+" env changed")
		}
		if fmt.Sprint(o.Ports) != fmt.Sprint(ct.Ports) {
			d = append(d, ct.Name+" ports changed")
		}
		if fmt.Sprint(o.ReadinessProbe) != fmt.Sprint(ct.ReadinessProbe) || fmt.Sprint(o.LivenessProbe) != fmt.Sprint(ct.LivenessProbe) {
			d = append(d, ct.Name+" probes changed")
		}
		if fmt.Sprint(o.Command) != fmt.Sprint(ct.Command) || fmt.Sprint(o.Args) != fmt.Sprint(ct.Args) {
			d = append(d, ct.Name+" command/args changed")
		}
	}
	return strings.Join(d, "; ")
}

func qty(s string) string {
	if s == "0" {
		return "none"
	}
	return s
}
