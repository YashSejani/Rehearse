// Package actions is the Safety Gate's action catalogue. Each typed action from the agent
// becomes a JSON Patch on one Deployment PLUS its inverse, both built from the current
// object before anything changes. Unknown action types are rejected: there is no way to
// turn agent output into an arbitrary command or object.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Action is the typed action from the agent's answer.
type Action struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// Op is one JSON Patch operation (RFC 6902).
type Op struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// Plan is everything needed to apply an action and to undo it.
type Plan struct {
	Action      Action `json:"action"`
	Namespace   string `json:"namespace"`
	Deployment  string `json:"deployment"`
	Description string `json:"description"`
	Patch       []Op   `json:"patch"`
	Inverse     []Op   `json:"inverse"`
	// Invertible is false when undo cannot restore the exact old state (a restart).
	Invertible bool `json:"invertible"`
}

// PatchJSON / InverseJSON are the bodies sent to the API server.
func (p *Plan) PatchJSON() []byte   { b, _ := json.Marshal(p.Patch); return b }
func (p *Plan) InverseJSON() []byte { b, _ := json.Marshal(p.Inverse); return b }

// Catalogue lists the action types the gate accepts.
var Catalogue = []string{"RollbackDeploy", "RestartDeployment", "ScaleReplicas", "PatchMemoryLimit", "FixImage"}

var (
	nameRx   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	memoryRx = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi)$`)
	imageRx  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9._-]+)?(@sha256:[a-f0-9]{64})?$`)
)

// Build validates the action and builds the patch and inverse from the live Deployment.
// `now` is used for the restart annotation.
func Build(ctx context.Context, kube kubernetes.Interface, ns string, a Action, now time.Time) (*Plan, error) {
	name, _ := a.Params["deployment"].(string)
	if !nameRx.MatchString(name) {
		return nil, fmt.Errorf("params.deployment %q is not a valid name", name)
	}
	if a.Type == "None" {
		return nil, fmt.Errorf("action None: nothing to do")
	}
	known := false
	for _, t := range Catalogue {
		known = known || t == a.Type
	}
	if !known {
		return nil, fmt.Errorf("action type %q is not in the catalogue %v", a.Type, Catalogue)
	}
	d, err := kube.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get deployment %s/%s: %w", ns, name, err)
	}
	p := &Plan{Action: a, Namespace: ns, Deployment: name, Invertible: true}
	switch a.Type {
	case "ScaleReplicas":
		err = scale(p, d, a.Params)
	case "PatchMemoryLimit":
		err = memory(p, d, a.Params)
	case "FixImage":
		err = image(p, d, a.Params)
	case "RestartDeployment":
		restart(p, d, now)
	case "RollbackDeploy":
		err = rollback(ctx, kube, p, d, a.Params)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func scale(p *Plan, d *appsv1.Deployment, params map[string]any) error {
	n, ok := params["replicas"].(float64)
	if !ok || n != float64(int32(n)) || n < 1 || n > 10 {
		return fmt.Errorf("params.replicas must be a whole number from 1 to 10")
	}
	old := int32(1)
	if d.Spec.Replicas != nil {
		old = *d.Spec.Replicas
	}
	p.Description = fmt.Sprintf("scale deployment %s from %d to %d replicas", d.Name, old, int32(n))
	p.Patch = []Op{{"test", "/spec/replicas", old}, {"replace", "/spec/replicas", int32(n)}}
	p.Inverse = []Op{{"test", "/spec/replicas", int32(n)}, {"replace", "/spec/replicas", old}}
	return nil
}

func containerIndex(d *appsv1.Deployment, params map[string]any) (int, error) {
	cs := d.Spec.Template.Spec.Containers
	want, _ := params["container"].(string)
	if want == "" {
		if len(cs) == 0 {
			return 0, fmt.Errorf("deployment %s has no containers", d.Name)
		}
		return 0, nil
	}
	for i, c := range cs {
		if c.Name == want {
			return i, nil
		}
	}
	return 0, fmt.Errorf("deployment %s has no container %q", d.Name, want)
}

func memory(p *Plan, d *appsv1.Deployment, params map[string]any) error {
	m, _ := params["memory"].(string)
	if !memoryRx.MatchString(m) {
		return fmt.Errorf("params.memory %q must look like 256Mi or 1Gi", m)
	}
	q := resource.MustParse(m)
	if q.Cmp(resource.MustParse("16Mi")) < 0 {
		return fmt.Errorf("params.memory %s is too small", m)
	}
	i, err := containerIndex(d, params)
	if err != nil {
		return err
	}
	c := d.Spec.Template.Spec.Containers[i]
	oldRes := c.Resources.DeepCopy()
	newRes := c.Resources.DeepCopy()
	if newRes.Limits == nil {
		newRes.Limits = corev1.ResourceList{}
	}
	newRes.Limits[corev1.ResourceMemory] = q
	// A request above the new limit is invalid; lower it to the limit.
	if r, ok := newRes.Requests[corev1.ResourceMemory]; ok && r.Cmp(q) > 0 {
		newRes.Requests[corev1.ResourceMemory] = q
	}
	path := fmt.Sprintf("/spec/template/spec/containers/%d/resources", i)
	p.Description = fmt.Sprintf("set memory limit of %s/%s to %s (was %s)", d.Name, c.Name, m, limitString(oldRes))
	p.Patch = []Op{{"test", fmt.Sprintf("/spec/template/spec/containers/%d/name", i), c.Name}, {"replace", path, newRes}}
	p.Inverse = []Op{{"test", fmt.Sprintf("/spec/template/spec/containers/%d/name", i), c.Name}, {"replace", path, oldRes}}
	return nil
}

func limitString(r *corev1.ResourceRequirements) string {
	if l, ok := r.Limits[corev1.ResourceMemory]; ok {
		return l.String()
	}
	return "no limit"
}

func image(p *Plan, d *appsv1.Deployment, params map[string]any) error {
	img, _ := params["image"].(string)
	if !imageRx.MatchString(img) || len(img) > 300 {
		return fmt.Errorf("params.image %q is not a valid image reference", img)
	}
	i, err := containerIndex(d, params)
	if err != nil {
		return err
	}
	c := d.Spec.Template.Spec.Containers[i]
	path := fmt.Sprintf("/spec/template/spec/containers/%d/image", i)
	p.Description = fmt.Sprintf("change image of %s/%s from %s to %s", d.Name, c.Name, c.Image, img)
	p.Patch = []Op{{"test", path, c.Image}, {"replace", path, img}}
	p.Inverse = []Op{{"test", path, img}, {"replace", path, c.Image}}
	return nil
}

const restartAnnotation = "kubectl.kubernetes.io/restartedAt"

func restart(p *Plan, d *appsv1.Deployment, now time.Time) {
	// "~1" escapes "/" in a JSON Pointer.
	path := "/spec/template/metadata/annotations/kubectl.kubernetes.io~1restartedAt"
	old, had := d.Spec.Template.Annotations[restartAnnotation]
	stamp := now.UTC().Format(time.RFC3339)
	p.Description = fmt.Sprintf("restart all pods of deployment %s (rolling the pod template)", d.Name)
	p.Invertible = false // pods were already restarted; undo only restores the annotation
	if d.Spec.Template.Annotations == nil {
		p.Patch = []Op{{"add", "/spec/template/metadata/annotations", map[string]string{restartAnnotation: stamp}}}
		p.Inverse = []Op{{Op: "remove", Path: "/spec/template/metadata/annotations"}}
		return
	}
	p.Patch = []Op{{"add", path, stamp}}
	if had {
		p.Inverse = []Op{{"replace", path, old}}
	} else {
		p.Inverse = []Op{{Op: "remove", Path: path}}
	}
}

func rollback(ctx context.Context, kube kubernetes.Interface, p *Plan, d *appsv1.Deployment, params map[string]any) error {
	rsList, err := kube.AppsV1().ReplicaSets(d.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list replicasets: %w", err)
	}
	type rev struct {
		n  int
		rs *appsv1.ReplicaSet
	}
	var revs []rev
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		for _, o := range rs.OwnerReferences {
			if o.UID == d.UID {
				n, _ := strconv.Atoi(rs.Annotations["deployment.kubernetes.io/revision"])
				revs = append(revs, rev{n, rs})
			}
		}
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].n > revs[j].n })
	if len(revs) < 2 {
		return fmt.Errorf("deployment %s has no previous revision to roll back to", d.Name)
	}
	target := revs[1] // the revision before the current one
	if want, ok := params["to_revision"].(float64); ok {
		found := false
		for _, r := range revs {
			if float64(r.n) == want {
				target, found = r, true
			}
		}
		if !found || target.n == revs[0].n {
			return fmt.Errorf("to_revision %v is not a previous revision", want)
		}
	}
	tmpl := target.rs.Spec.Template.DeepCopy()
	delete(tmpl.Labels, "pod-template-hash") // the deployment controller adds it itself
	cur := d.Spec.Template.DeepCopy()
	p.Description = fmt.Sprintf("roll back deployment %s from revision %d to revision %d", d.Name, revs[0].n, target.n)
	p.Patch = []Op{{"replace", "/spec/template", tmpl}}
	p.Inverse = []Op{{"replace", "/spec/template", cur}}
	return nil
}
