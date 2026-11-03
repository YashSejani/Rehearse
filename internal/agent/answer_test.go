package agent

import (
	"strings"
	"testing"
)

const facts = `CURRENT STATE: PROBLEM
- Pod geo-1-a (service geo): ImagePullBackOff, Back-off pulling image "app:bad"
- [Rollout] revision 2: hotel-reserv-geo image app@sha256:abc -> app:bad`

func ans(action, params string) string {
	return `{"root_cause": "geo runs an image tag that does not exist", "category": "image", "target": "deployment geo",
	"evidence": ["Pod geo-1-a (service geo): ImagePullBackOff"], "confidence": 0.8,
	"action": {"type": "` + action + `", "params": ` + params + `}}`
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name, json, wantErr string
	}{
		{"rollback ok", ans("RollbackDeploy", `{"deployment": "geo"}`), ""},
		{"fix image from facts", ans("FixImage", `{"deployment": "geo", "image": "app@sha256:abc"}`), ""},
		{"invented image", ans("FixImage", `{"deployment": "geo", "image": "other/app:v2"}`), "does not appear"},
		{"digest typo repaired", ans("FixImage", `{"deployment": "geo", "image": "app:sha256:abc"}`), ""},
		{"latest image", ans("FixImage", `{"deployment": "geo", "image": "app:latest"}`), "latest"},
		{"unknown action", ans("DeleteNamespace", `{}`), "not one of"},
		{"missing param", ans("ScaleReplicas", `{"deployment": "geo"}`), "params.replicas"},
		{"scale to zero", ans("ScaleReplicas", `{"deployment": "geo", "replicas": 0}`), "replicas must be"},
		{"bad memory", ans("PatchMemoryLimit", `{"deployment": "geo", "memory": "lots"}`), "256Mi"},
		{"memory ok", ans("PatchMemoryLimit", `{"deployment": "geo", "memory": "512Mi"}`), ""},
	}
	for _, tc := range cases {
		a, err := ParseAnswer(tc.json)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		err = a.Validate(facts)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.wantErr, err)
		}
		if tc.wantErr == "" && a.Target != "geo" {
			t.Errorf("%s: target not normalized: %q", tc.name, a.Target)
		}
	}
}

func TestUnknownParamsDropped(t *testing.T) {
	a, _ := ParseAnswer(ans("RollbackDeploy", `{"deployment": "geo", "image": "app:latest", "to_revision": 3}`))
	if err := a.Validate(facts); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Action.Params["image"]; ok || a.Action.Params["to_revision"] == nil {
		t.Errorf("params %v", a.Action.Params)
	}
}

func TestHealthyAnswer(t *testing.T) {
	a, err := ParseAnswer(`Sure! {"root_cause": "nothing is wrong right now", "category": "unknown", "target": "",
		"evidence": [], "confidence": 0.2, "action": {"type": "None"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Validate("anything"); err != nil {
		t.Errorf("healthy answer rejected: %v", err)
	}
	if !a.Healthy() || a.Action.Params == nil {
		t.Errorf("healthy %v params %v", a.Healthy(), a.Action.Params)
	}
}

func TestEvidenceFuzzyMatch(t *testing.T) {
	src := normalizeEvidence(facts)
	if !evidenceFound(`geo-1-a: ImagePullBackOff, Back-off pulling image app:bad`, src) {
		t.Error("slightly reformatted quote should match")
	}
	if evidenceFound("the database password was rotated yesterday", src) {
		t.Error("invented evidence should not match")
	}
}

func TestParseAnswerErrors(t *testing.T) {
	for _, s := range []string{"no json here", "{broken", `{"root_cause": 5}`} {
		if _, err := ParseAnswer(s); err == nil {
			t.Errorf("%q: expected error", s)
		}
	}
}
