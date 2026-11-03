package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Answer is the agent's final diagnosis. It is the only thing the model's output turns
// into; anything else it writes is ignored.
type Answer struct {
	RootCause  string   `json:"root_cause"`
	Category   string   `json:"category"`
	Target     string   `json:"target"`
	Evidence   []string `json:"evidence"`
	Confidence float64  `json:"confidence"`
	Action     Action   `json:"action"`
}

// Action is one typed fix from a fixed catalogue. The Safety Gate (Phase 6) is the only
// thing that turns it into a cluster change.
type Action struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

var Categories = []string{"image", "resources", "config", "scaling", "network", "dependency", "unknown"}

// ActionTypes maps each action to its required parameters.
var ActionTypes = map[string][]string{
	"RollbackDeploy":    {"deployment"},
	"RestartDeployment": {"deployment"},
	"ScaleReplicas":     {"deployment", "replicas"},
	"PatchMemoryLimit":  {"deployment", "memory"},
	"FixImage":          {"deployment", "image"},
	"None":              {},
}

// optionalParams are allowed besides the required ones; anything else is dropped.
var optionalParams = map[string][]string{
	"RollbackDeploy":   {"to_revision"},
	"PatchMemoryLimit": {"container"},
	"FixImage":         {"container"},
}

var memoryRx = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi)$`)

// ParseAnswer extracts the JSON object from the model's text (models sometimes wrap it in
// ```json fences or add a sentence) and decodes it.
func ParseAnswer(text string) (*Answer, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in the answer")
	}
	var a Answer
	dec := json.NewDecoder(strings.NewReader(text[start : end+1]))
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("answer is not valid JSON: %w", err)
	}
	if a.Action.Params == nil {
		a.Action.Params = map[string]any{}
	}
	return &a, nil
}

// Validate checks the answer against the schema and the facts. evidenceSource is all text
// the model was shown (facts + tool results); every evidence line must be found in it,
// so the model cannot invent evidence.
func (a *Answer) Validate(evidenceSource string) error {
	var problems []string
	a.Target = normalizeTarget(a.Target)
	if d, ok := a.Action.Params["deployment"].(string); ok {
		a.Action.Params["deployment"] = normalizeTarget(d)
	}
	if len(strings.Fields(a.RootCause)) < 3 {
		problems = append(problems, "root_cause must be a sentence that explains the cause")
	}
	if !contains(Categories, a.Category) {
		problems = append(problems, fmt.Sprintf("category %q is not one of %v", a.Category, Categories))
	}
	if a.Confidence < 0 || a.Confidence > 1 {
		problems = append(problems, "confidence must be between 0 and 1")
	}
	required, ok := ActionTypes[a.Action.Type]
	if !ok {
		problems = append(problems, fmt.Sprintf("action.type %q is not one of RollbackDeploy, RestartDeployment, ScaleReplicas, PatchMemoryLimit, FixImage, None", a.Action.Type))
	}
	if ok { // drop parameters the action does not define (e.g. an image on a rollback)
		for k := range a.Action.Params {
			if !contains(required, k) && !contains(optionalParams[a.Action.Type], k) {
				delete(a.Action.Params, k)
			}
		}
	}
	for _, p := range required {
		if _, ok := a.Action.Params[p]; !ok {
			problems = append(problems, fmt.Sprintf("action %s needs params.%s", a.Action.Type, p))
		}
	}
	if m, ok := a.Action.Params["memory"].(string); ok && a.Action.Type == "PatchMemoryLimit" && !memoryRx.MatchString(m) {
		problems = append(problems, fmt.Sprintf("params.memory %q must look like 256Mi or 1Gi", m))
	}
	if img, ok := a.Action.Params["image"].(string); ok && a.Action.Type == "FixImage" {
		// Small models often write a digest as repo:sha256:… instead of repo@sha256:….
		img = strings.Replace(img, ":sha256:", "@sha256:", 1)
		a.Action.Params["image"] = img
		// The model must not invent images: only one seen in the facts (e.g. the previous
		// revision), never a floating :latest tag.
		switch {
		case strings.HasSuffix(img, ":latest") || !strings.ContainsAny(img, ":@"):
			problems = append(problems, fmt.Sprintf("params.image %q must have a pinned tag or digest, not latest", img))
		case !strings.Contains(evidenceSource, img):
			problems = append(problems, fmt.Sprintf("params.image %q does not appear in the facts; use an image from a previous revision, or RollbackDeploy", img))
		}
	}
	if a.Action.Type == "ScaleReplicas" {
		if n, ok := a.Action.Params["replicas"].(float64); !ok || n < 1 || n != float64(int(n)) {
			problems = append(problems, "params.replicas must be a whole number >= 1")
		}
	}
	if a.Action.Type != "None" && a.Target == "" {
		problems = append(problems, "target is empty")
	}
	if a.Action.Type != "None" && len(a.Evidence) == 0 {
		problems = append(problems, "evidence is empty: quote lines from the facts")
	}
	src := normalizeEvidence(evidenceSource)
	for _, e := range a.Evidence {
		if !evidenceFound(e, src) {
			problems = append(problems, fmt.Sprintf("evidence %q does not appear in the facts; quote it exactly", truncate(e, 80)))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid answer: %s", strings.Join(problems, "; "))
	}
	return nil
}

// normalizeTarget turns "deployment geo", "deploy/geo" or "Deployment/geo" into "geo".
func normalizeTarget(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	for _, p := range []string{"deployment/", "deploy/", "deployment ", "deploy "} {
		s = strings.TrimPrefix(s, p)
	}
	return strings.TrimSpace(s)
}

// Healthy reports whether the answer says nothing is wrong.
func (a *Answer) Healthy() bool { return a.Action.Type == "None" && a.Category == "unknown" }

// evidenceFound accepts an evidence line when it, or a long enough piece of it, appears in
// the facts. Models often trim or slightly reformat quotes; exact matching would reject
// honest answers, while requiring 70% of the words still rejects invented lines.
func evidenceFound(e, normalizedSource string) bool {
	ne := normalizeEvidence(e)
	if ne == "" {
		return false
	}
	if strings.Contains(normalizedSource, ne) {
		return true
	}
	words := strings.Fields(ne)
	if len(words) < 3 {
		return false
	}
	found := 0
	for _, w := range words {
		if strings.Contains(normalizedSource, w) {
			found++
		}
	}
	return float64(found)/float64(len(words)) >= 0.7
}

var nonWord = regexp.MustCompile(`[^a-z0-9./:_-]+`)

func normalizeEvidence(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
