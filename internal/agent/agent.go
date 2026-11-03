// Package agent is the diagnosing AI: it collects facts with the read-only collector
// tools, lets a language model call more tools if it wants (think → act → observe), and
// returns one validated Answer with exactly one typed action.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/YashSejani/Rehearse/internal/llm"
)

// Chatter is the part of the LLM client the agent needs (a fake in tests).
type Chatter interface {
	Chat(ctx context.Context, req llm.Request) (*llm.Response, error)
}

type Agent struct {
	LLM         Chatter
	Tools       Tools
	Model       string
	MaxSteps    int
	Temperature float64
	MaxTokens   int
	Trace       *Trace // may be nil
	// SkipWhenHealthy answers "nothing is wrong" without calling the model when the
	// deterministic health check passes (triage before AI). Off = always ask the model.
	SkipWhenHealthy bool
	// Context is extra reference text (Phase 5 RAG) placed in its own data block. May be empty.
	Context func(ctx context.Context, f *Facts) string
}

// Result is everything one investigation produced.
type Result struct {
	Answer           *Answer       `json:"answer"`
	Valid            bool          `json:"valid"`   // false: model output never validated, Answer is the fallback
	Triaged          bool          `json:"triaged"` // true: healthy by the deterministic check, model not called
	Steps            int           `json:"steps"`   // model calls
	ToolCalls        int           `json:"tool_calls"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	Duration         time.Duration `json:"duration_ns"`
	Error            string        `json:"error,omitempty"`
	TracePath        string        `json:"trace_path,omitempty"`
	Facts            *Facts        `json:"-"`
}

// Fallback is the safe answer when the model fails: nothing is changed.
func Fallback(target, why string) *Answer {
	return &Answer{RootCause: "could not determine: " + why, Category: "unknown", Target: target,
		Evidence: []string{}, Confidence: 0, Action: Action{Type: "None", Params: map[string]any{}}}
}

// Diagnose investigates one deployment.
func (a *Agent) Diagnose(ctx context.Context, target string) *Result {
	start := time.Now()
	res := &Result{}
	defer func() { res.Duration = time.Since(start) }()
	if a.Trace != nil {
		res.TracePath = a.Trace.Path
	}

	// Step 1: deterministic facts.
	facts := GatherFacts(ctx, a.Tools, target)
	res.Facts = facts
	rendered := facts.Render()
	evidence := rendered // everything the model saw; evidence must come from here
	a.Trace.Write("facts", map[string]any{"target": target, "facts": facts, "rendered": rendered})

	if a.SkipWhenHealthy && facts.Healthy() {
		res.Answer = &Answer{RootCause: "nothing is wrong: " + facts.HealthLine(), Category: "unknown", Target: target,
			Evidence: []string{"CURRENT STATE: " + facts.HealthLine()}, Confidence: 0.9, Action: Action{Type: "None", Params: map[string]any{}}}
		res.Valid, res.Triaged = true, true
		a.Trace.Write("answer", map[string]any{"answer": res.Answer, "triaged": true})
		return res
	}

	msgs := []llm.Message{{Role: "system", Content: SystemPrompt}}
	if a.Context != nil {
		if ref := a.Context(ctx, facts); ref != "" {
			msgs = append(msgs, llm.Message{Role: "user", Content: ref})
			a.Trace.Write("context", map[string]any{"text": ref})
		}
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: userPrompt(target, rendered)})

	// Step 2: the loop. Each model call is one step; tool calls are run and their results
	// appended, until the model answers or we run out of steps.
	retried := false
	for step := 1; step <= a.MaxSteps+1; step++ {
		last := step >= a.MaxSteps
		if step > a.MaxSteps && !retried {
			break
		}
		req := llm.Request{Model: a.Model, Messages: msgs, Temperature: a.Temperature, MaxTokens: a.MaxTokens}
		if retried {
			// The retry is a pure "fix your JSON" turn: JSON mode makes the syntax valid.
			req.ResponseFormat = &llm.ResponseFormat{Type: "json_object"}
		} else if last {
			// Out of steps: no more tools, force a JSON answer.
			msgs = append(msgs, llm.Message{Role: "user", Content: "No more tool calls. Give your final JSON answer now."})
			req.Messages = msgs
			req.ResponseFormat = &llm.ResponseFormat{Type: "json_object"}
		} else {
			req.Tools = toolDefs
		}
		resp, err := a.LLM.Chat(ctx, req)
		res.Steps++
		if err != nil {
			a.Trace.Write("error", map[string]any{"step": step, "error": err.Error()})
			res.Error = err.Error()
			res.Answer = Fallback(target, "model call failed")
			return res
		}
		res.PromptTokens += resp.Usage.PromptTokens
		res.CompletionTokens += resp.Usage.CompletionTokens
		a.Trace.Write("model", map[string]any{
			"step": step, "request_messages": len(req.Messages), "tools_offered": len(req.Tools) > 0,
			"response": resp.Message, "finish": resp.FinishReason, "usage": resp.Usage, "latency_ms": resp.Latency.Milliseconds(),
		})

		msg := resp.Message
		if len(msg.ToolCalls) > 0 && !last {
			msgs = append(msgs, llm.Message{Role: "assistant", Content: msg.Content, ToolCalls: msg.ToolCalls})
			for _, tc := range msg.ToolCalls {
				out := runTool(ctx, a.Tools, tc.Function)
				res.ToolCalls++
				evidence += "\n" + out
				a.Trace.Write("tool", map[string]any{"step": step, "name": tc.Function.Name, "arguments": tc.Function.Arguments, "result": out})
				msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
					Content: "<data>\n" + out + "\n</data>"})
			}
			continue
		}

		// The model answered in text: parse and validate.
		ans, err := ParseAnswer(msg.Content)
		if err == nil {
			err = ans.Validate(evidence)
		}
		if err == nil {
			res.Answer, res.Valid = ans, true
			a.Trace.Write("answer", map[string]any{"answer": ans})
			return res
		}
		a.Trace.Write("invalid", map[string]any{"step": step, "error": err.Error()})
		if retried {
			res.Error = err.Error()
			res.Answer = Fallback(target, "model output invalid twice")
			a.Trace.Write("answer", map[string]any{"answer": res.Answer, "fallback": true})
			return res
		}
		// One retry, telling the model exactly what was wrong.
		retried = true
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: msg.Content},
			llm.Message{Role: "user", Content: "Your answer was rejected: " + err.Error() + ". Reply again with ONLY the corrected JSON object."})
	}
	res.Error = "step limit reached"
	res.Answer = Fallback(target, "step limit reached")
	return res
}

// Summary is a short human-readable view of a result.
func (r *Result) Summary() string {
	a := r.Answer
	var b strings.Builder
	fmt.Fprintf(&b, "Root cause:  %s\n", a.RootCause)
	fmt.Fprintf(&b, "Category:    %s   Target: %s   Confidence: %.2f\n", a.Category, a.Target, a.Confidence)
	params, _ := json.Marshal(a.Action.Params)
	fmt.Fprintf(&b, "Action:      %s %s\n", a.Action.Type, params)
	if len(a.Evidence) > 0 {
		b.WriteString("Evidence:\n")
		for _, e := range a.Evidence {
			fmt.Fprintf(&b, "  - %s\n", e)
		}
	}
	fmt.Fprintf(&b, "(%d model calls, %d tool calls, %d+%d tokens, %s", r.Steps, r.ToolCalls,
		r.PromptTokens, r.CompletionTokens, r.Duration.Round(time.Second))
	if !r.Valid {
		b.WriteString(", FALLBACK: " + r.Error)
	}
	if r.TracePath != "" {
		b.WriteString(", trace " + r.TracePath)
	}
	b.WriteString(")\n")
	return b.String()
}
