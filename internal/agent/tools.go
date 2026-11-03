package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/YashSejani/Rehearse/internal/llm"
)

// toolDefs are the functions the model may call. All are read-only. There is deliberately
// no shell, kubectl or write tool: the agent can only look, and propose one typed action.
var toolDefs = []llm.Tool{
	fn("get_status", "List unhealthy pods and deployments in the namespace (CrashLoopBackOff, OOMKilled, ImagePullBackOff, ScaledToZero, ...).", nil),
	fn("get_logs", "Recent error log lines and Kubernetes events of one service, deduplicated with counts.", map[string]any{
		"service": prop("string", "deployment/service name, e.g. geo or mongodb-profile"),
	}),
	fn("get_metrics", "Request rate, error ratio, p95 latency, replicas, restarts and memory of one service.", map[string]any{
		"service":        prop("string", "deployment/service name"),
		"window_minutes": prop("integer", "time window, default 5"),
	}),
	fn("get_changes", "Recent rollouts (what changed between revisions), current spec and warning events of one service.", map[string]any{
		"service": prop("string", "deployment/service name"),
	}),
}

func fn(name, desc string, props map[string]any) llm.Tool {
	params := map[string]any{"type": "object", "properties": map[string]any{}}
	if props != nil {
		params["properties"] = props
		if _, ok := props["service"]; ok {
			params["required"] = []string{"service"}
		}
	}
	return llm.Tool{Type: "function", Function: llm.FunctionDef{Name: name, Description: desc, Parameters: params}}
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

// podSuffix matches the ReplicaSet and pod hash at the end of a pod name.
var podSuffix = regexp.MustCompile(`-[a-z0-9]{8,10}-[a-z0-9]{5}$`)

// serviceArg reads "service" from tool arguments and forgives common slips: a pod name
// (geo-59c49d4575-lb9xz → geo) or a "deploy/" prefix.
func serviceArg(args map[string]any) (string, error) {
	s, _ := args["service"].(string)
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "deploy/"), "deployment/")
	s = podSuffix.ReplaceAllString(s, "")
	if s == "" {
		return "", fmt.Errorf("missing argument service")
	}
	return s, nil
}

// runTool executes one tool call and returns its result as compact JSON text. Errors are
// returned as text too, so the model can see them and try something else.
func runTool(ctx context.Context, t Tools, call llm.FunctionCall) string {
	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return fmt.Sprintf(`{"error": "arguments are not valid JSON: %v"}`, err)
		}
	}
	var result any
	var err error
	switch call.Name {
	case "get_status":
		result, err = t.Status(ctx)
	case "get_logs", "get_metrics", "get_changes":
		var svc string
		if svc, err = serviceArg(args); err != nil {
			break
		}
		switch call.Name {
		case "get_logs":
			result, err = t.Logs(ctx, svc, 15, 15*time.Minute)
		case "get_metrics":
			w := 5
			if n, ok := args["window_minutes"].(float64); ok && n >= 1 && n <= 120 {
				w = int(n)
			}
			result, err = t.Metrics(ctx, svc, time.Duration(w)*time.Minute)
		default:
			result, err = t.Changes(ctx, svc)
		}
	default:
		err = fmt.Errorf("unknown tool %q; available: get_status, get_logs, get_metrics, get_changes", call.Name)
	}
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(b)
	}
	b, _ := json.Marshal(result)
	return truncate(string(b), 6000)
}
