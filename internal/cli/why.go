package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/YashSejani/Rehearse/internal/agent"
	"github.com/YashSejani/Rehearse/internal/llm"
)

func init() { extraCommands = append(extraCommands, newWhyCmd) }

// parseTarget accepts deploy/x, deployment/x or x.
func parseTarget(s string) (string, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "deploy/"), "deployment/")
	if s == "" || strings.Contains(s, "/") {
		return "", fmt.Errorf("target must look like deploy/<name>, got %q", s)
	}
	return s, nil
}

// newAgent builds the agent from config; shared by `why` and later commands.
func (f *rootFlags) newAgent(target string, trace bool) (*agent.Agent, func(), error) {
	c, err := f.collector()
	if err != nil {
		return nil, nil, err
	}
	cfg := c.Cfg
	a := &agent.Agent{
		LLM:         llm.New(cfg.Agent.BaseURL, os.Getenv("REHEARSE_LLM_API_KEY")),
		Tools:       c,
		Model:       cfg.Agent.Model,
		MaxSteps:    cfg.Agent.MaxSteps,
		Temperature: cfg.Agent.Temperature,
		MaxTokens:   cfg.Agent.MaxTokens,

		SkipWhenHealthy: !cfg.Agent.AlwaysAskModel && !f.alwaysAsk,
	}
	done := func() {}
	if trace {
		t, err := agent.NewTrace(cfg.Agent.TraceDir, target)
		if err != nil {
			return nil, nil, err
		}
		a.Trace = t
		done = func() { _ = t.Close() }
	}
	return a, done, nil
}

func newWhyCmd(f *rootFlags) *cobra.Command {
	var asJSON, dumpFacts, noTrace bool
	cmd := &cobra.Command{
		Use:   "why deploy/<name>",
		Short: "Diagnose a deployment with the AI agent: root cause, evidence, confidence, one typed fix",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := parseTarget(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if dumpFacts {
				c, err := f.collector()
				if err != nil {
					return err
				}
				facts := agent.GatherFacts(cmd.Context(), c, target)
				if asJSON || f.json() {
					fmt.Fprintln(out, facts.JSON())
				} else {
					fmt.Fprint(out, facts.Render())
				}
				return nil
			}
			a, done, err := f.newAgent(target, !noTrace)
			if err != nil {
				return err
			}
			defer done()
			res := a.Diagnose(cmd.Context(), target)
			if asJSON || f.json() {
				return printJSON(out, res)
			}
			fmt.Fprint(out, res.Summary())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the full result as JSON")
	cmd.Flags().BoolVar(&dumpFacts, "dump-facts", false, "only print the facts the agent would see (no model call)")
	cmd.Flags().BoolVar(&noTrace, "no-trace", false, "do not write a trace file")
	cmd.Flags().BoolVar(&f.alwaysAsk, "always-ask", false, "ask the model even when the health check passes")
	return cmd
}
