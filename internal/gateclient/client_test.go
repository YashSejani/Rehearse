package gateclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/YashSejani/Rehearse/internal/actions"
	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/gate"
)

type okProber struct{}

func (okProber) Probe(context.Context, string) (gate.Health, error) {
	return gate.Health{Available: true}, nil
}

func TestClientAgainstGate(t *testing.T) {
	two := int32(2)
	kube := fake.NewClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "geo", Namespace: "hotel"},
		Spec: appsv1.DeploymentSpec{Replicas: &two, Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "app:v1"}}}}},
	})
	g := &gate.Gate{Kube: kube, Store: gate.NewMemStore(), Prober: okProber{}, AllowedNamespaces: []string{"hotel"},
		Watch: gate.WatchConfig{Interval: time.Second, Duration: time.Second, Consecutive: 1}, Sleep: func(time.Duration) {}}
	srv := httptest.NewServer(g.Handler("tok"))
	defer srv.Close()

	bad, _ := New(config.Backend{URL: srv.URL}, nil, "wrong")
	if _, err := bad.List(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token: %v", err)
	}
	c, _ := New(config.Backend{URL: srv.URL}, nil, "tok")
	// Scale-to-0 is blocked; the client reports the proposal, not an HTTP error.
	p, err := c.Propose(context.Background(), gate.Request{Namespace: "hotel",
		Action: actions.Action{Type: "ScaleReplicas", Params: map[string]any{"deployment": "geo", "replicas": float64(0)}}})
	if err == nil && p.Status == gate.StatusChecking {
		p, err = c.WaitChecked(context.Background(), p.ID, 10*time.Millisecond)
	}
	if err != nil || p.Status != gate.StatusBlocked {
		t.Fatalf("propose scale-to-0: %v %+v", err, p)
	}
	if _, err := c.Approve(context.Background(), p.ID, "yash"); err == nil {
		t.Error("approving a blocked proposal must fail")
	}
	ps, err := c.List(context.Background())
	if err != nil || len(ps) != 1 {
		t.Errorf("list: %v %d", err, len(ps))
	}
	if !Final(gate.StatusApplied) || Final(gate.StatusWatching) {
		t.Error("Final()")
	}
	_ = http.StatusOK
}
