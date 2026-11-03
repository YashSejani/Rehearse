package collector

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func rs(name string, rev string, created time.Duration, image, memLimit string) *appsv1.ReplicaSet {
	ct := corev1.Container{Name: "hotel-reserv-geo", Image: image}
	if memLimit != "" {
		ct.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(memLimit)}
	}
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "hotel", CreationTimestamp: at(created),
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": rev},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "geo", UID: types.UID("geo-uid")}},
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{ct}}}},
	}
}

func TestChanges(t *testing.T) {
	d := deploy("geo", 1, 0)
	d.UID = "geo-uid"
	d.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType
	d.Spec.Template.Spec.Containers = []corev1.Container{{Name: "hotel-reserv-geo", Image: "app:bad"}}

	other := rs("search-1", "1", time.Hour, "app:ok", "")
	other.OwnerReferences[0].UID = "search-uid"

	warn := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "e1", Namespace: "hotel"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "geo-2-x"},
		Type:           corev1.EventTypeWarning, Reason: "Failed", Message: "Failed to pull image app:bad", Count: 5,
		LastTimestamp: at(10 * time.Second),
	}
	routine := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "e2", Namespace: "hotel"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "geo-2-x"},
		Type:           corev1.EventTypeNormal, Reason: "Pulled", LastTimestamp: at(5 * time.Second),
	}
	otherSvc := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "e3", Namespace: "hotel"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "mongodb-geo-1-a"},
		Type:           corev1.EventTypeWarning, Reason: "BackOff", LastTimestamp: at(5 * time.Second),
	}

	c := newTestCollector(t, &fakeBackend{}, d,
		rs("geo-1", "1", time.Hour, "app:ok", ""),
		rs("geo-2", "2", time.Minute, "app:bad", "24Mi"),
		other, warn, routine, otherSvc)

	ch, err := c.Changes(context.Background(), "geo")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var all strings.Builder
	for _, e := range ch {
		kinds = append(kinds, e.Kind)
		all.WriteString(e.Summary + "\n")
	}
	text := all.String()
	if !strings.Contains(text, "image app:ok -> app:bad") || !strings.Contains(text, "memory limit none -> 24Mi") {
		t.Errorf("rollout diff missing:\n%s", text)
	}
	if !strings.Contains(text, "Failed to pull image") {
		t.Errorf("warning event missing:\n%s", text)
	}
	if strings.Contains(text, "Pulled") || strings.Contains(text, "mongodb-geo") || strings.Contains(text, "search") {
		t.Errorf("noise or other service included:\n%s", text)
	}
	if kinds[0] != "Current" || !strings.Contains(ch[0].Summary, "strategy Recreate") {
		t.Errorf("current state should be first: %+v", ch[0])
	}
	if len(ch) != 4 { // current, 2 rollouts, 1 warning
		t.Errorf("got %d changes: %v", len(ch), kinds)
	}
}

func TestChangesMissingDeployment(t *testing.T) {
	c := newTestCollector(t, &fakeBackend{})
	if _, err := c.Changes(context.Background(), "nope"); err == nil {
		t.Error("expected error")
	}
}
