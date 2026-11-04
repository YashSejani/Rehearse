// Package sandbox rehearses a proposed fix on a copy of the app before a human approves it.
// The copy is a temporary namespace rehearse-sbx-<id> (a vCluster needs more RAM than the
// laptop has; see decisions.md). It is always deleted afterwards, even on errors.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/YashSejani/Rehearse/internal/actions"
)

const (
	Prefix      = "rehearse-sbx-"
	LabelKey    = "rehearse.dev/sandbox"
	sandboxRole = "rehearse-sandbox-admin" // ClusterRole the gate binds to itself inside a sandbox only
)

type Sandbox struct {
	Kube     kubernetes.Interface
	SourceNS string
	GateSA   string // "namespace/name" of the gate's ServiceAccount
	Timeout  time.Duration
	// ReproduceWait: how long the broken copy gets to (unexpectedly) become healthy
	// before the fix is applied. Default 90s.
	ReproduceWait time.Duration
	// Stable: how long everything must stay available to count as healthy. A container with a
	// too-low memory limit starts and dies seconds later. Default 30s.
	Stable    time.Duration
	Smoke     Smoke
	HTTP      *http.Client
	Poll      time.Duration
	Log       func(format string, args ...any)
	newSuffix func() string
}

// Smoke is one HTTP request that must succeed against the copy, e.g. frontend:5000/hotels?...
type Smoke struct {
	Service string
	Port    int
	Path    string
}

func (s *Sandbox) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

func (s *Sandbox) suffix() string {
	if s.newSuffix != nil {
		return s.newSuffix()
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Rehearse implements gate.Rehearser. ok=true: the copy became healthy after the fix.
// err != nil: the rehearsal could not be done (shown as "not rehearsed (reason)").
func (s *Sandbox) Rehearse(ctx context.Context, p *actions.Plan) (string, bool, error) {
	start := time.Now()
	ns := Prefix + s.suffix()
	ctx, cancel := context.WithTimeout(ctx, s.Timeout+3*time.Minute)
	defer cancel()
	defer s.cleanup(ns) // always, even after errors or a cancelled context

	if err := s.create(ctx, ns); err != nil {
		return "", false, fmt.Errorf("create sandbox: %w", err)
	}
	created := time.Since(start).Round(time.Second)
	s.logf("sandbox %s: copied %s in %s", ns, s.SourceNS, created)

	// The copy has the current (broken) spec. If it is healthy anyway, the fault lives
	// outside what we copy (data, network, an external system) and the rehearsal proves nothing.
	wait := s.ReproduceWait
	if wait == 0 {
		wait = 90 * time.Second
	}
	if healthy, _ := s.waitHealthy(ctx, ns, p.Deployment, wait); healthy {
		return "", false, fmt.Errorf("fault did not reproduce in the copy (sandbox ready in %s)", created)
	}
	if _, err := s.Kube.AppsV1().Deployments(ns).Patch(ctx, p.Deployment, types.JSONPatchType, p.PatchJSON(),
		metav1.PatchOptions{FieldManager: "rehearse-gate-sandbox"}); err != nil {
		return "", false, fmt.Errorf("apply fix in sandbox: %w", err)
	}
	fixAt := time.Now()
	healthy, why := s.waitHealthy(ctx, ns, p.Deployment, s.Timeout)
	if healthy {
		if err := s.smoke(ctx, ns); err != nil {
			healthy, why = false, "smoke request failed: "+err.Error()
		}
	}
	took := time.Since(fixAt).Round(time.Second)
	if !healthy {
		return fmt.Sprintf("rehearsed: still failing after %s (%s)", took, why), false, nil
	}
	return fmt.Sprintf("rehearsed: healthy in %s (sandbox %s, created in %s)", took, ns, created), true, nil
}

// Create copies the source namespace into a new sandbox namespace (used by the Phase 8
// data generator, which runs as the user, not as the gate).
func (s *Sandbox) Create(ctx context.Context, ns string) error {
	if !strings.HasPrefix(ns, Prefix) {
		return fmt.Errorf("sandbox namespace must start with %s", Prefix)
	}
	return s.create(ctx, ns)
}

// Sync makes a sandbox Deployment and Service (if any) match the source again: the
// generic "undo whatever fault was injected into the copy".
func (s *Sandbox) Sync(ctx context.Context, ns, name string) error {
	src, err := s.Kube.AppsV1().Deployments(s.SourceNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	cur, err := s.Kube.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	cur.Spec = *src.Spec.DeepCopy()
	if _, err := s.Kube.AppsV1().Deployments(ns).Update(ctx, cur, metav1.UpdateOptions{}); err != nil {
		return err
	}
	if ssvc, err := s.Kube.CoreV1().Services(s.SourceNS).Get(ctx, name, metav1.GetOptions{}); err == nil {
		if csvc, err := s.Kube.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			csvc.Spec.Ports = ssvc.Spec.Ports
			for i := range csvc.Spec.Ports {
				csvc.Spec.Ports[i].NodePort = 0
			}
			csvc.Spec.Selector = ssvc.Spec.Selector
			if _, err := s.Kube.CoreV1().Services(ns).Update(ctx, csvc, metav1.UpdateOptions{}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Delete removes one sandbox namespace.
func (s *Sandbox) Delete(ns string) { s.cleanup(ns) }

// create makes the namespace, gives the gate rights inside it only, and copies the app.
func (s *Sandbox) create(ctx context.Context, ns string) error {
	_, err := s.Kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: ns, Labels: map[string]string{LabelKey: "true"}}}, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	saNS, saName, _ := strings.Cut(s.GateSA, "/")
	if saName != "" {
		if _, err := s.Kube.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rehearse-gate", Namespace: ns},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: saName, Namespace: saNS}},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: sandboxRole},
		}, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("sandbox rolebinding: %w", err)
		}
	}
	// The RoleBinding just created takes a moment to reach the authorizer: retry on Forbidden.
	if err := retryForbidden(ctx, func() error {
		_, err := s.Kube.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
		return err
	}); err != nil {
		return fmt.Errorf("sandbox access: %w", err)
	}
	cms, err := s.Kube.CoreV1().ConfigMaps(s.SourceNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, cm := range cms.Items {
		if cm.Name == "kube-root-ca.crt" {
			continue
		}
		cp := &corev1.ConfigMap{ObjectMeta: clean(cm.ObjectMeta, ns), Data: cm.Data, BinaryData: cm.BinaryData}
		if _, err := s.Kube.CoreV1().ConfigMaps(ns).Create(ctx, cp, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("copy configmap %s: %w", cm.Name, err)
		}
	}
	pvcs, err := s.Kube.CoreV1().PersistentVolumeClaims(s.SourceNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, c := range pvcs.Items {
		spec := *c.Spec.DeepCopy()
		spec.VolumeName = "" // a new, empty volume: data is NOT copied (see docs/07-sandbox.md)
		cp := &corev1.PersistentVolumeClaim{ObjectMeta: clean(c.ObjectMeta, ns), Spec: spec}
		delete(cp.Annotations, "pv.kubernetes.io/bind-completed")
		delete(cp.Annotations, "pv.kubernetes.io/bound-by-controller")
		delete(cp.Annotations, "volume.kubernetes.io/selected-node")
		if _, err := s.Kube.CoreV1().PersistentVolumeClaims(ns).Create(ctx, cp, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("copy pvc %s: %w", c.Name, err)
		}
	}
	svcs, err := s.Kube.CoreV1().Services(s.SourceNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, sv := range svcs.Items {
		spec := *sv.Spec.DeepCopy()
		spec.ClusterIP, spec.ClusterIPs = "", nil
		if spec.Type == corev1.ServiceTypeNodePort || spec.Type == corev1.ServiceTypeLoadBalancer {
			// Never expose a sandbox outside the cluster. Fields that only exist for
			// externally reachable Services must go too, or the API server rejects it.
			spec.Type = corev1.ServiceTypeClusterIP
			spec.ExternalTrafficPolicy = ""
			spec.HealthCheckNodePort = 0
			spec.LoadBalancerSourceRanges = nil
			spec.AllocateLoadBalancerNodePorts = nil
		}
		for i := range spec.Ports {
			spec.Ports[i].NodePort = 0
		}
		cp := &corev1.Service{ObjectMeta: clean(sv.ObjectMeta, ns), Spec: spec}
		if _, err := s.Kube.CoreV1().Services(ns).Create(ctx, cp, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("copy service %s: %w", sv.Name, err)
		}
	}
	deps, err := s.Kube.AppsV1().Deployments(s.SourceNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, d := range deps.Items {
		cp := &appsv1.Deployment{ObjectMeta: clean(d.ObjectMeta, ns), Spec: *d.Spec.DeepCopy()}
		delete(cp.Annotations, "deployment.kubernetes.io/revision")
		if _, err := s.Kube.AppsV1().Deployments(ns).Create(ctx, cp, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("copy deployment %s: %w", d.Name, err)
		}
	}
	return nil
}

func retryForbidden(ctx context.Context, f func() error) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = f(); err == nil || !apierrors.IsForbidden(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return err
}

// clean keeps name, labels and annotations; drops identity fields of the original object.
func clean(m metav1.ObjectMeta, ns string) metav1.ObjectMeta {
	ann := map[string]string{}
	for k, v := range m.Annotations {
		if k != "kubectl.kubernetes.io/last-applied-configuration" {
			ann[k] = v
		}
	}
	return metav1.ObjectMeta{Name: m.Name, Namespace: ns, Labels: m.Labels, Annotations: ann}
}

// waitHealthy waits until every deployment in the sandbox has had all replicas available for
// the whole Stable period.
func (s *Sandbox) waitHealthy(ctx context.Context, ns, target string, timeout time.Duration) (bool, string) {
	deadline := time.Now().Add(timeout)
	poll := s.Poll
	if poll == 0 {
		poll = 5 * time.Second
	}
	stable := s.Stable
	if stable == 0 {
		stable = 30 * time.Second
	}
	why := "timeout"
	var since time.Time
	for {
		deps, err := s.Kube.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			var missing []string
			for _, d := range deps.Items {
				want := int32(1)
				if d.Spec.Replicas != nil {
					want = *d.Spec.Replicas
				}
				if want == 0 || d.Status.AvailableReplicas < want {
					missing = append(missing, d.Name)
				}
			}
			if len(missing) == 0 && len(deps.Items) > 0 {
				if since.IsZero() {
					since = time.Now()
				}
				if time.Since(since) >= stable {
					return true, ""
				}
				why = "available, but not yet for " + stable.String()
			} else {
				since = time.Time{}
				why = "not ready: " + strings.Join(missing, ", ")
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false, why
		}
		select {
		case <-ctx.Done():
			return false, why
		case <-time.After(poll):
		}
	}
}

func (s *Sandbox) smoke(ctx context.Context, ns string) error {
	if s.Smoke.Service == "" {
		return nil
	}
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	url := fmt.Sprintf("http://%s.%s.svc:%d%s", s.Smoke.Service, ns, s.Smoke.Port, s.Smoke.Path)
	var last error
	for i := 0; i < 6; i++ { // services register in consul a little after becoming Ready
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := hc.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 400 {
				return nil
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return last
}

// cleanup deletes the sandbox namespace with a fresh context (the caller's may be done).
func (s *Sandbox) cleanup(ns string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.Kube.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		s.logf("sandbox %s: cleanup failed: %v (run `rehearse sandbox cleanup`)", ns, err)
		return
	}
	s.logf("sandbox %s: deleted", ns)
}

// CleanupAll deletes every leftover sandbox namespace (label rehearse.dev/sandbox=true).
func CleanupAll(ctx context.Context, kube kubernetes.Interface) ([]string, error) {
	list, err := kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: LabelKey + "=true"})
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, n := range list.Items {
		if !strings.HasPrefix(n.Name, Prefix) || n.Status.Phase == corev1.NamespaceTerminating {
			continue
		}
		if err := kube.CoreV1().Namespaces().Delete(ctx, n.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return deleted, err
		}
		deleted = append(deleted, n.Name)
	}
	return deleted, nil
}
