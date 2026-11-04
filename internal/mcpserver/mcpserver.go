// Package mcpserver exposes Rehearse to other AI tools (Claude Code, IDE agents) through the
// Model Context Protocol. Tools: get_app_model, explain_failure, propose_action. A proposed
// action goes through the Safety Gate exactly like `rehearse fix`; there is deliberately no
// approve tool: an outside AI can propose, only a human can approve.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/YashSejani/Rehearse/internal/actions"
	"github.com/YashSejani/Rehearse/internal/agent"
	"github.com/YashSejani/Rehearse/internal/gate"
	"github.com/YashSejani/Rehearse/internal/knowledge"
)

// Backend is what the tools need; the CLI wires real implementations, tests fakes.
type Backend struct {
	Namespace string
	AppModel  func(ctx context.Context) (*knowledge.ServiceMap, error)
	Diagnose  func(ctx context.Context, deployment string) *agent.Result
	Propose   func(ctx context.Context, req gate.Request) (*gate.Proposal, error)
}

type ExplainIn struct {
	Deployment string `json:"deployment" jsonschema:"name of the deployment to diagnose, e.g. geo"`
}

type ExplainOut struct {
	Answer    *agent.Answer `json:"answer"`
	Valid     bool          `json:"valid"`
	ModelCall int           `json:"model_calls"`
}

type ProposeIn struct {
	Deployment string         `json:"deployment" jsonschema:"deployment to change"`
	Type       string         `json:"type" jsonschema:"one of RollbackDeploy, RestartDeployment, ScaleReplicas, PatchMemoryLimit, FixImage"`
	Params     map[string]any `json:"params,omitempty" jsonschema:"action parameters, e.g. {\"replicas\": 1} or {\"memory\": \"256Mi\"}"`
	Reason     string         `json:"reason,omitempty" jsonschema:"why you propose it (stored in the audit log)"`
}

type ProposeOut struct {
	ProposalID int64  `json:"proposal_id"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Diff       string `json:"diff,omitempty"`
	Rehearsal  string `json:"rehearsal,omitempty"`
	Next       string `json:"next"`
}

type Empty struct{}

// New builds the MCP server with the three tools.
func New(b Backend) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "rehearse", Version: "v0.10.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "get_app_model",
		Description: "Rehearse's learned model of the app: services, who depends on whom, and normal behaviour (baselines)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, *knowledge.ServiceMap, error) {
			m, err := b.AppModel(ctx)
			if err != nil {
				return nil, nil, fmt.Errorf("app model (run `rehearse learn`): %w", err)
			}
			return nil, m, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "explain_failure",
		Description: "Diagnose one deployment with Rehearse's read-only agent: root cause, evidence quoted from logs/metrics/events, confidence, and one proposed typed action. Changes nothing."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ExplainIn) (*mcp.CallToolResult, ExplainOut, error) {
			if in.Deployment == "" {
				return nil, ExplainOut{}, fmt.Errorf("deployment is required")
			}
			r := b.Diagnose(ctx, in.Deployment)
			return nil, ExplainOut{Answer: r.Answer, Valid: r.Valid, ModelCall: r.Steps}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "propose_action",
		Description: "Propose ONE typed fix to Rehearse's Safety Gate. The gate dry-runs it, checks policies (no scale to 0, no :latest, memory <= 2Gi), rehearses it in a sandbox, and then a human must approve it in the dashboard or CLI. Arbitrary commands are not possible."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ProposeIn) (*mcp.CallToolResult, ProposeOut, error) {
			params := map[string]any{}
			for k, v := range in.Params {
				params[k] = v
			}
			params["deployment"] = in.Deployment
			answer, _ := json.Marshal(map[string]any{"root_cause": in.Reason, "source": "mcp"})
			p, err := b.Propose(ctx, gate.Request{Namespace: b.Namespace, Answer: answer,
				Action: actions.Action{Type: in.Type, Params: params}})
			if err != nil {
				return nil, ProposeOut{}, err
			}
			out := ProposeOut{ProposalID: p.ID, Status: p.Status, Reason: p.Reason, Diff: p.Diff, Rehearsal: p.Rehearsal}
			if p.Status == gate.StatusAwaiting {
				out.Next = "waiting for a human to approve it (dashboard or `rehearse proposals`)"
			} else {
				out.Next = "the gate blocked this action; it will not be applied"
			}
			return nil, out, nil
		})
	return s
}
