package collector

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pod(name, svc string, cs ...corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "hotel", Labels: map[string]string{"io.kompose.service": svc}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: cs},
	}
}

func deploy(name string, replicas, available int32, conds ...appsv1.DeploymentCondition) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "hotel", Labels: map[string]string{"io.kompose.service": name}},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: available, UpdatedReplicas: replicas, Conditions: conds},
	}
}

func at(d time.Duration) metav1.Time { return metav1.NewTime(testNow.Add(-d)) }

func TestStatus(t *testing.T) {
	healthy := pod("frontend-1-a", "frontend", corev1.ContainerStatus{Name: "c", Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: at(time.Hour)}}})
	oomLoop := pod("mongodb-profile-1-a", "mongodb-profile", corev1.ContainerStatus{Name: "c", RestartCount: 4,
		State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, FinishedAt: at(20 * time.Second)}}})
	oomRunning := pod("rate-1-a", "rate", corev1.ContainerStatus{Name: "c", RestartCount: 1, Ready: true,
		State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: at(5 * time.Second)}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, FinishedAt: at(6 * time.Second)}}})
	pull := pod("geo-1-a", "geo", corev1.ContainerStatus{Name: "c",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: `Back-off pulling image "x:nope"`}}})
	notReady := pod("search-1-a", "search", corev1.ContainerStatus{Name: "c", Ready: false,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: at(5 * time.Minute)}}})
	starting := pod("search-2-b", "search", corev1.ContainerStatus{Name: "c", Ready: false,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: at(10 * time.Second)}}})
	pending := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "big-1-a", Namespace: "hotel"},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
			{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Message: "0/2 nodes: Insufficient memory"}}}}

	objs := []runtimeObject{healthy, oomLoop, oomRunning, pull, notReady, starting, pending,
		deploy("frontend", 1, 1),
		deploy("user", 0, 0),
		deploy("geo", 1, 0),
		deploy("reservation", 1, 0, appsv1.DeploymentCondition{Type: appsv1.DeploymentProgressing,
			Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded", Message: "timed out"}),
	}
	c := newTestCollector(t, &fakeBackend{}, objs...)
	issues, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, is := range issues {
		got[is.Name] = is.Reason
	}
	want := map[string]string{
		"mongodb-profile-1-a": "OOMKilled",
		"rate-1-a":            "OOMKilled",
		"geo-1-a":             "ImagePullBackOff",
		"search-1-a":          "NotReady",
		"big-1-a":             "Unschedulable",
		"user":                "ScaledToZero",
		"geo":                 "Unavailable",
		"reservation":         "StuckRollout",
	}
	for name, reason := range want {
		if got[name] != reason {
			t.Errorf("%s: reason %q, want %q", name, got[name], reason)
		}
	}
	for _, healthyName := range []string{"frontend-1-a", "search-2-b", "frontend"} {
		if _, ok := got[healthyName]; ok {
			t.Errorf("%s reported but is healthy (or just starting)", healthyName)
		}
	}
	if issues[0].Reason != "OOMKilled" && issues[0].Reason != "ImagePullBackOff" {
		t.Errorf("most severe first: got %q first", issues[0].Reason)
	}
	if len(issues) != len(want) {
		t.Errorf("got %d issues, want %d: %+v", len(issues), len(want), issues)
	}
}

func TestStatusCap(t *testing.T) {
	var objs []runtimeObject
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		objs = append(objs, deploy(n, 0, 0))
	}
	c := newTestCollector(t, &fakeBackend{}, objs...)
	c.Cfg.Limits.Issues = 3
	issues, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 {
		t.Errorf("cap: got %d issues", len(issues))
	}
}

func TestLastReason(t *testing.T) {
	cases := []struct {
		t    corev1.ContainerStateTerminated
		want string
	}{
		{corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}, "OOMKilled"},
		{corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 137}, "Killed"},
		{corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1}, "Error"},
		{corev1.ContainerStateTerminated{ExitCode: 2}, "Error"},
	}
	for _, tc := range cases {
		if got := lastReason(&tc.t); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.t, got, tc.want)
		}
	}
}
