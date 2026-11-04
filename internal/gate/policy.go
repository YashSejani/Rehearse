package gate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// CheckPolicy re-checks the dry-run result against the same rules as the Kyverno policies
// in deploy/policies/. Kyverno is the real enforcement (it runs inside the API server's
// dry-run); this second check means a missing or misconfigured Kyverno cannot silently
// let a forbidden change through (defence in depth).
func CheckPolicy(d *appsv1.Deployment) []string {
	var v []string
	if d.Spec.Replicas != nil && *d.Spec.Replicas < 1 {
		v = append(v, "no-scale-to-zero: replicas must be at least 1")
	}
	max := resource.MustParse("2Gi")
	for _, c := range d.Spec.Template.Spec.Containers {
		img := c.Image
		name := img
		if i := strings.LastIndex(img, "/"); i >= 0 {
			name = img[i+1:]
		}
		if strings.HasSuffix(img, ":latest") || (!strings.Contains(name, ":") && !strings.Contains(img, "@sha256:")) {
			v = append(v, fmt.Sprintf("disallow-latest-tag: container %s image %q must have a pinned tag or digest", c.Name, img))
		}
		if l, ok := c.Resources.Limits["memory"]; ok && l.Cmp(max) > 0 {
			v = append(v, fmt.Sprintf("max-memory-limit: container %s memory limit %s is above 2Gi", c.Name, l.String()))
		}
	}
	return v
}

// Diff lists what changes between two objects' specs, one "path: old -> new" per line,
// so the approver sees the raw change, not just the agent's description of it.
func Diff(before, after any) string {
	var a, b any
	ja, _ := json.Marshal(before)
	jb, _ := json.Marshal(after)
	_ = json.Unmarshal(ja, &a)
	_ = json.Unmarshal(jb, &b)
	var lines []string
	diffValue("", a, b, &lines)
	sort.Strings(lines)
	if len(lines) > 40 {
		lines = append(lines[:40], fmt.Sprintf("... and %d more changes", len(lines)-40))
	}
	return strings.Join(lines, "\n")
}

func diffValue(path string, a, b any, out *[]string) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for k := range keys {
			diffValue(path+"."+k, am[k], bm[k], out)
		}
		return
	}
	al, aok := a.([]any)
	bl, bok := b.([]any)
	if aok && bok && len(al) == len(bl) {
		for i := range al {
			diffValue(fmt.Sprintf("%s[%d]", path, i), al[i], bl[i], out)
		}
		return
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		*out = append(*out, fmt.Sprintf("%s: %s -> %s", strings.TrimPrefix(path, "."), short(string(ja)), short(string(jb))))
	}
}

func short(s string) string {
	if s == "null" {
		return "(none)"
	}
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
