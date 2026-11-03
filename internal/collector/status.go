package collector

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodIssue is one problem found in the namespace: a pod that is not healthy, or a
// deployment that does not have the pods it wants.
type PodIssue struct {
	Kind     string    `json:"kind"`              // Pod or Deployment
	Name     string    `json:"name"`              // pod or deployment name
	Service  string    `json:"service"`           // service label, e.g. geo
	Reason   string    `json:"reason"`            // CrashLoopBackOff, OOMKilled, ImagePullBackOff, ...
	Message  string    `json:"message,omitempty"` // short detail, truncated
	Restarts int32     `json:"restarts,omitempty"`
	Since    time.Time `json:"since,omitempty"`
}

// Reasons we report for pods stuck before or between runs.
var badWaiting = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"RunContainerError":          true,
}

// notReadyGrace: a running pod that is not Ready for longer than this is reported
// (a failing readiness probe). Shorter gaps are just a normal start.
const notReadyGrace = 60 * time.Second

// Status lists unhealthy pods and deployments in the app namespace, most serious first.
// It uses a single List call per kind (a snapshot), not a Watch: we want "what is wrong now".
func (c *Collector) Status(ctx context.Context) ([]PodIssue, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	pods, err := c.Kube.CoreV1().Pods(c.ns()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	deps, err := c.Kube.AppsV1().Deployments(c.ns()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}

	var issues []PodIssue
	for i := range pods.Items {
		issues = append(issues, c.podIssues(&pods.Items[i])...)
	}
	for i := range deps.Items {
		if is, ok := c.deploymentIssue(&deps.Items[i]); ok {
			issues = append(issues, is)
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if severity(issues[i].Reason) != severity(issues[j].Reason) {
			return severity(issues[i].Reason) > severity(issues[j].Reason)
		}
		return issues[i].Name < issues[j].Name
	})
	if len(issues) > c.Cfg.Limits.Issues {
		issues = issues[:c.Cfg.Limits.Issues]
	}
	return issues, nil
}

func (c *Collector) podIssues(p *corev1.Pod) []PodIssue {
	now := c.Now()
	base := PodIssue{Kind: "Pod", Name: p.Name, Service: c.serviceOf(p.Labels)}

	if p.Status.Phase == corev1.PodPending {
		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				is := base
				is.Reason, is.Message, is.Since = "Unschedulable", truncate(cond.Message, 200), cond.LastTransitionTime.Time
				return []PodIssue{is}
			}
		}
	}
	if p.Status.Phase == corev1.PodFailed {
		is := base
		is.Reason, is.Message = nonEmpty(p.Status.Reason, "Failed"), truncate(p.Status.Message, 200)
		return []PodIssue{is}
	}

	var out []PodIssue
	for _, cs := range p.Status.ContainerStatuses {
		is := base
		is.Restarts = cs.RestartCount
		last := cs.LastTerminationState.Terminated
		switch {
		case cs.State.Waiting != nil && badWaiting[cs.State.Waiting.Reason]:
			is.Reason = cs.State.Waiting.Reason
			is.Message = truncate(cs.State.Waiting.Message, 200)
			// CrashLoopBackOff hides WHY it crashed; the last termination tells (OOMKilled, Error).
			if cs.State.Waiting.Reason == "CrashLoopBackOff" && last != nil {
				is.Reason = lastReason(last)
				is.Message = fmt.Sprintf("CrashLoopBackOff, last exit %d (%s)", last.ExitCode, last.Reason)
				is.Since = last.FinishedAt.Time
			}
		case cs.State.Terminated != nil:
			is.Reason = lastReason(cs.State.Terminated)
			is.Message = fmt.Sprintf("exit %d", cs.State.Terminated.ExitCode)
			is.Since = cs.State.Terminated.FinishedAt.Time
		case cs.State.Running != nil && last != nil && now.Sub(last.FinishedAt.Time) < 10*time.Minute:
			// Running again, but it died recently: report the crash (e.g. OOMKilled 30 s ago).
			is.Reason = lastReason(last)
			is.Message = fmt.Sprintf("restarted, last exit %d (%s)", last.ExitCode, last.Reason)
			is.Since = last.FinishedAt.Time
		case cs.State.Running != nil && !cs.Ready && now.Sub(cs.State.Running.StartedAt.Time) > notReadyGrace:
			is.Reason = "NotReady"
			is.Message = "running but readiness probe failing"
			is.Since = cs.State.Running.StartedAt.Time
		default:
			continue
		}
		out = append(out, is)
	}
	return out
}

// lastReason turns a termination into our reason; exit 137 without a reason is almost
// always an OOM kill that the runtime could not label.
func lastReason(t *corev1.ContainerStateTerminated) string {
	switch {
	case t.Reason == "OOMKilled":
		return "OOMKilled"
	case t.Reason == "Completed" && t.ExitCode == 0:
		return "Completed"
	case t.ExitCode == 137:
		return "Killed"
	default:
		return nonEmpty(t.Reason, "Error")
	}
}

func (c *Collector) deploymentIssue(d *appsv1.Deployment) (PodIssue, bool) {
	is := PodIssue{Kind: "Deployment", Name: d.Name, Service: nonEmpty(c.serviceOf(d.Labels), d.Name)}
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	if want == 0 {
		is.Reason, is.Message = "ScaledToZero", "spec.replicas is 0: no pods will run"
		return is, true
	}
	for _, cond := range d.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse &&
			cond.Reason == "ProgressDeadlineExceeded" {
			is.Reason, is.Message, is.Since = "StuckRollout", truncate(cond.Message, 200), cond.LastUpdateTime.Time
			return is, true
		}
	}
	if d.Status.AvailableReplicas < want {
		is.Reason = "Unavailable"
		is.Message = fmt.Sprintf("%d/%d replicas available", d.Status.AvailableReplicas, want)
		// A rollout that started but whose new pods are not up yet is also "stuck" in the making.
		if d.Status.UpdatedReplicas < want || d.Status.ObservedGeneration < d.Generation {
			is.Message += fmt.Sprintf(", rollout in progress (%d/%d updated)", d.Status.UpdatedReplicas, want)
		}
		return is, true
	}
	return is, false
}

// severity orders issues so the root-cause-looking ones come first.
func severity(reason string) int {
	switch reason {
	case "OOMKilled", "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "CreateContainerConfigError":
		return 5
	case "CrashLoopBackOff", "Killed", "Error", "RunContainerError", "CreateContainerError", "ScaledToZero", "StuckRollout":
		return 4
	case "Unschedulable", "NotReady", "Failed":
		return 3
	case "Unavailable":
		return 2
	default:
		return 1
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
