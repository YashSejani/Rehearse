package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/YashSejani/Rehearse/internal/actions"
	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/gate"
	"github.com/YashSejani/Rehearse/internal/gateclient"
	"github.com/YashSejani/Rehearse/internal/kube"
)

func init() {
	extraCommands = append(extraCommands, newFixCmd, newProposalsCmd, newDecideCmd(true), newDecideCmd(false))
}

// newDecideCmd builds `rehearse approve <id>` and `rehearse reject <id>`.
func newDecideCmd(approve bool) func(*rootFlags) *cobra.Command {
	return func(f *rootFlags) *cobra.Command {
		var by, reason string
		use, short := "approve <proposal-id>", "Approve a Safety Gate proposal (apply, watch, undo if worse)"
		if !approve {
			use, short = "reject <proposal-id>", "Reject a Safety Gate proposal (nothing changes)"
		}
		cmd := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var id int64
				if _, err := fmt.Sscan(args[0], &id); err != nil {
					return fmt.Errorf("bad id %q", args[0])
				}
				cfg, err := f.load()
				if err != nil {
					return err
				}
				gc, err := f.gateClient(cmd.Context(), cfg)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if !approve {
					p, err := gc.Reject(cmd.Context(), id, by, reason)
					if err == nil {
						PrintProposal(out, p)
					}
					return err
				}
				if _, err := gc.Approve(cmd.Context(), id, by); err != nil {
					return err
				}
				final, err := gc.WaitFinal(cmd.Context(), id, 5*time.Second, func(p *gate.Proposal) {
					fmt.Fprintf(out, "  %s  %s\n", time.Now().Format("15:04:05"), p.Status)
				})
				if err != nil {
					return err
				}
				PrintProposal(out, final)
				return nil
			}}
		cmd.Flags().StringVar(&by, "approver", envOr("USER", "cli"), "name recorded in the audit log")
		cmd.Flags().StringVar(&reason, "reason", "rejected in CLI", "why (reject only)")
		return cmd
	}
}

func (f *rootFlags) gateClient(ctx context.Context, cfg *config.Config) (*gateclient.Client, error) {
	cs, _, err := kube.Client(cfg.Kube.Context)
	if err != nil {
		return nil, err
	}
	token := os.Getenv("REHEARSE_GATE_TOKEN")
	if token == "" {
		ns, name, _ := strings.Cut(cfg.Gate.TokenSecret, "/")
		sec, err := cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("gate token secret %s (run `make gate`): %w", cfg.Gate.TokenSecret, err)
		}
		token = string(sec.Data["token"])
	}
	return gateclient.New(cfg.Gate.Backend, cs, token)
}

// PrintProposal shows what the approver must see: the raw diff, not only a description.
func PrintProposal(w io.Writer, p *gate.Proposal) {
	fmt.Fprintf(w, "\nProposal #%d  [%s]\n", p.ID, p.Status)
	if p.Plan != nil {
		fmt.Fprintf(w, "  Change:     %s\n", p.Plan.Description)
		if !p.Plan.Invertible {
			fmt.Fprintln(w, "  Note:       cannot be fully undone (pods restart)")
		}
	}
	fmt.Fprintf(w, "  Rehearsal:  %s\n", nonEmptyStr(p.Rehearsal, "-"))
	if p.Diff != "" {
		fmt.Fprintln(w, "  Raw diff (dry-run result vs live object):")
		for _, l := range strings.Split(p.Diff, "\n") {
			fmt.Fprintln(w, "    "+l)
		}
	}
	var a struct {
		RootCause  string   `json:"root_cause"`
		Confidence float64  `json:"confidence"`
		Evidence   []string `json:"evidence"`
	}
	if json.Unmarshal(p.Answer, &a) == nil && a.RootCause != "" {
		fmt.Fprintf(w, "  Diagnosis:  %s (confidence %.2f)\n", a.RootCause, a.Confidence)
		for _, e := range a.Evidence {
			fmt.Fprintf(w, "    evidence: %s\n", oneLine(e, 150))
		}
	}
	for _, e := range p.Events {
		mark := "ok  "
		if !e.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "  [%s] %-9s %s\n", mark, e.Step, oneLine(e.Detail, 150))
	}
	if p.Reason != "" && gateclient.Final(p.Status) {
		fmt.Fprintf(w, "  Result:     %s: %s\n", p.Status, p.Reason)
	}
}

func newFixCmd(f *rootFlags) *cobra.Command {
	var yes bool
	var approver string
	cmd := &cobra.Command{
		Use:   "fix deploy/<name>",
		Short: "Diagnose (why), send the one proposed action to the Safety Gate, approve it, and watch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := parseTarget(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			cfg, err := f.load()
			if err != nil {
				return err
			}
			a, done, err := f.newAgent(target, true)
			if err != nil {
				return err
			}
			defer done()
			if cfg.Knowledge.RAG {
				if hook, stop, err := f.ragContext(cmd.Context(), cfg); err == nil {
					defer stop()
					a.Context = hook
				}
			}
			res := a.Diagnose(cmd.Context(), target)
			fmt.Fprint(out, res.Summary())
			if res.Answer.Action.Type == "None" {
				fmt.Fprintln(out, "No action proposed; nothing to send to the gate.")
				return nil
			}
			gc, err := f.gateClient(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			answer, _ := json.Marshal(res.Answer)
			p, err := gc.Propose(cmd.Context(), gate.Request{Namespace: cfg.App.Namespace, Answer: answer,
				Action: actions.Action{Type: res.Answer.Action.Type, Params: res.Answer.Action.Params}})
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Sent to the Safety Gate as proposal #%d: dry-run, policies and sandbox rehearsal running...\n", p.ID)
			if p, err = gc.WaitChecked(cmd.Context(), p.ID, 5*time.Second); err != nil {
				return err
			}
			PrintProposal(out, p)
			if p.Status != gate.StatusAwaiting {
				return fmt.Errorf("the gate did not accept the action: %s", p.Reason)
			}
			if !yes && !confirm(cmd.InOrStdin(), out, "Apply this change? [y/N] ") {
				_, err := gc.Reject(cmd.Context(), p.ID, approver, "declined in CLI")
				fmt.Fprintln(out, "Rejected; nothing changed.")
				return err
			}
			if _, err := gc.Approve(cmd.Context(), p.ID, approver); err != nil {
				return err
			}
			fmt.Fprintln(out, "Approved. Applying and watching the app (settle window, then 2 minutes)...")
			final, err := gc.WaitFinal(cmd.Context(), p.ID, 5*time.Second, func(p *gate.Proposal) {
				fmt.Fprintf(out, "  %s  %s\n", time.Now().Format("15:04:05"), p.Status)
			})
			if err != nil {
				return err
			}
			PrintProposal(out, final)
			if final.Status != gate.StatusApplied {
				return fmt.Errorf("change not kept: %s", final.Reason)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "approve without asking (automation only)")
	cmd.Flags().StringVar(&approver, "approver", envOr("USER", "cli"), "name recorded in the audit log")
	return cmd
}

func newProposalsCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "proposals [id]",
		Short: "List Safety Gate proposals (audit history), or show one with all its steps",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.load()
			if err != nil {
				return err
			}
			gc, err := f.gateClient(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				var id int64
				if _, err := fmt.Sscan(args[0], &id); err != nil {
					return fmt.Errorf("bad id %q", args[0])
				}
				p, err := gc.Get(cmd.Context(), id)
				if err != nil {
					return err
				}
				if f.json() {
					return printJSON(out, p)
				}
				PrintProposal(out, p)
				return nil
			}
			ps, err := gc.List(cmd.Context())
			if err != nil {
				return err
			}
			if f.json() {
				return printJSON(out, ps)
			}
			tw := table(out)
			fmt.Fprintln(tw, "ID\tCREATED\tDEPLOYMENT\tACTION\tSTATUS\tREASON")
			for _, p := range ps {
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.CreatedAt.Format("01-02 15:04"), p.Deployment, p.Action.Type, p.Status, oneLine(p.Reason, 70))
			}
			return tw.Flush()
		},
	}
}

func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
}

func nonEmptyStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
