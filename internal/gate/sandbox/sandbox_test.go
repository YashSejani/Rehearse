package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/YashSejani/Rehearse/internal/actions"
)

func dep(name, image string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "hotel", UID: types.UID("uid-" + name),
			Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{}"}},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}}}},
	}
}

// newKube simulates the deployment controller: a deployment is available unless it has
// 0 replicas or an image containing "bad".
func newKube(objs ...runtime.Object) *fake.Clientset {
	cs := fake.NewClientset(objs...)
	cs.PrependReactor("list", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
		obj, err := cs.Tracker().List(appsv1.SchemeGroupVersion.WithResource("deployments"), appsv1.SchemeGroupVersion.WithKind("Deployment"), a.GetNamespace())
		if err != nil {
			return true, nil, err
		}
		list := obj.(*appsv1.DeploymentList)
		for i := range list.Items {
			d := &list.Items[i]
			if *d.Spec.Replicas > 0 && !strings.Contains(d.Spec.Template.Spec.Containers[0].Image, "bad") {
				d.Status.AvailableReplicas = *d.Spec.Replicas
			}
		}
		return true, list, nil
	})
	return cs
}

func sb(kube *fake.Clientset) *Sandbox {
	return &Sandbox{Kube: kube, SourceNS: "hotel", GateSA: "rehearse/rehearse-gate", Timeout: 2 * time.Second, ReproduceWait: 200 * time.Millisecond,
		Poll: 10 * time.Millisecond, Stable: 30 * time.Millisecond, newSuffix: func() string { return "test1" }}
}

func plan(t *testing.T, kube *fake.Clientset, a actions.Action) *actions.Plan {
	p, err := actions.Build(context.Background(), kube, "hotel", a, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func noSandboxLeft(t *testing.T, kube *fake.Clientset) {
	t.Helper()
	ns, _ := kube.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	for _, n := range ns.Items {
		if strings.HasPrefix(n.Name, Prefix) {
			t.Errorf("sandbox %s left behind", n.Name)
		}
	}
}

func objects(bad string) []runtime.Object {
	return []runtime.Object{
		dep("geo", bad, 1), dep("frontend", "app:v1", 1),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "frontend", Namespace: "hotel"},
			Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, ClusterIP: "10.0.0.1", ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyCluster,
				Ports: []corev1.ServicePort{{Port: 5000, NodePort: 30001}}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "mongo-geo-script", Namespace: "hotel"}, Data: map[string]string{"a.sh": "x"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kube-root-ca.crt", Namespace: "hotel"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "geo-pvc", Namespace: "hotel"},
			Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "pv-real-data"}},
	}
}

func TestGoodFixRehearsedHealthy(t *testing.T) {
	kube := newKube(objects("app:bad")...)
	summary, ok, err := sb(kube).Rehearse(context.Background(),
		plan(t, kube, actions.Action{Type: "FixImage", Params: map[string]any{"deployment": "geo", "image": "app:v1"}}))
	if err != nil || !ok || !strings.HasPrefix(summary, "rehearsed: healthy in") {
		t.Fatalf("summary %q ok %v err %v", summary, ok, err)
	}
	noSandboxLeft(t, kube)
	// The real app was not touched.
	d, _ := kube.AppsV1().Deployments("hotel").Get(context.Background(), "geo", metav1.GetOptions{})
	if d.Spec.Template.Spec.Containers[0].Image != "app:bad" {
		t.Error("rehearsal changed the real deployment")
	}
}

func TestWrongFixCaught(t *testing.T) {
	kube := newKube(objects("app:bad")...)
	wrong := actions.Action{Type: "ScaleReplicas", Params: map[string]any{"deployment": "geo", "replicas": float64(2)}}
	summary, ok, err := sb(kube).Rehearse(context.Background(), plan(t, kube, wrong))
	if err != nil || ok || !strings.HasPrefix(summary, "rehearsed: still failing") || !strings.Contains(summary, "geo") {
		t.Fatalf("summary %q ok %v err %v", summary, ok, err)
	}
	noSandboxLeft(t, kube)
}

func TestFaultNotReproduced(t *testing.T) {
	kube := newKube(objects("app:v1")...) // nothing is broken in what we can copy
	_, _, err := sb(kube).Rehearse(context.Background(),
		plan(t, kube, actions.Action{Type: "RestartDeployment", Params: map[string]any{"deployment": "geo"}}))
	if err == nil || !strings.Contains(err.Error(), "did not reproduce") {
		t.Fatalf("want 'did not reproduce', got %v", err)
	}
	noSandboxLeft(t, kube)
}

func TestCopyIsSafe(t *testing.T) {
	kube := newKube(objects("app:bad")...)
	s := sb(kube)
	if err := s.create(context.Background(), "rehearse-sbx-x"); err != nil {
		t.Fatal(err)
	}
	svc, _ := kube.CoreV1().Services("rehearse-sbx-x").Get(context.Background(), "frontend", metav1.GetOptions{})
	if svc.Spec.Type != corev1.ServiceTypeClusterIP || svc.Spec.Ports[0].NodePort != 0 || svc.Spec.ClusterIP != "" || svc.Spec.ExternalTrafficPolicy != "" {
		t.Errorf("sandbox service must not be exposed: %+v", svc.Spec)
	}
	pvc, _ := kube.CoreV1().PersistentVolumeClaims("rehearse-sbx-x").Get(context.Background(), "geo-pvc", metav1.GetOptions{})
	if pvc.Spec.VolumeName != "" {
		t.Error("sandbox must not mount the real volume")
	}
	if _, err := kube.CoreV1().ConfigMaps("rehearse-sbx-x").Get(context.Background(), "kube-root-ca.crt", metav1.GetOptions{}); err == nil {
		t.Error("kube-root-ca.crt must not be copied")
	}
	d, _ := kube.AppsV1().Deployments("rehearse-sbx-x").Get(context.Background(), "geo", metav1.GetOptions{})
	if _, ok := d.Annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok || d.UID != "" {
		t.Error("copy kept identity fields")
	}
	rb, err := kube.RbacV1().RoleBindings("rehearse-sbx-x").Get(context.Background(), "rehearse-gate", metav1.GetOptions{})
	if err != nil || rb.RoleRef.Name != sandboxRole {
		t.Errorf("rolebinding %+v %v", rb, err)
	}
	left, err := CleanupAll(context.Background(), kube)
	if err != nil || len(left) != 1 {
		t.Errorf("cleanup all: %v %v", left, err)
	}
}

func TestSync(t *testing.T) {
	kube := newKube(objects("app:v1")...)
	s := sb(kube)
	if err := s.Create(context.Background(), "rehearse-sbx-y"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d, _ := kube.AppsV1().Deployments("rehearse-sbx-y").Get(ctx, "geo", metav1.GetOptions{})
	d.Spec.Template.Spec.Containers[0].Image = "app:bad"
	_, _ = kube.AppsV1().Deployments("rehearse-sbx-y").Update(ctx, d, metav1.UpdateOptions{})
	if err := s.Sync(ctx, "rehearse-sbx-y", "geo"); err != nil {
		t.Fatal(err)
	}
	d, _ = kube.AppsV1().Deployments("rehearse-sbx-y").Get(ctx, "geo", metav1.GetOptions{})
	if d.Spec.Template.Spec.Containers[0].Image != "app:v1" {
		t.Error("sync did not restore the image")
	}
	if err := s.Create(ctx, "hotel"); err == nil {
		t.Error("Create must refuse a non-sandbox name")
	}
}

// A container that starts and dies again (too-low memory limit) must not count as healthy.
func TestWaitHealthyRejectsFlapping(t *testing.T) {
	replicas := int32(1)
	polls := 0
	cs := fake.NewClientset()
	cs.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		polls++
		d := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "db"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}}
		if polls%2 == 0 {
			d.Status.AvailableReplicas = 1
		}
		return true, &appsv1.DeploymentList{Items: []appsv1.Deployment{d}}, nil
	})
	s := &Sandbox{Kube: cs, Poll: 5 * time.Millisecond, Stable: 30 * time.Millisecond}
	if healthy, why := s.waitHealthy(context.Background(), "ns", "db", 200*time.Millisecond); healthy {
		t.Errorf("flapping deployment counted as healthy (%s)", why)
	}
}

func TestWaitHealthyAcceptsStable(t *testing.T) {
	kube := newKube(dep("db", "good", 1))
	s := &Sandbox{Kube: kube, Poll: 5 * time.Millisecond, Stable: 30 * time.Millisecond}
	if healthy, why := s.waitHealthy(context.Background(), "hotel", "db", time.Second); !healthy {
		t.Errorf("stable deployment not healthy: %s", why)
	}
}
