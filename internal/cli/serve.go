package cli

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/YashSejani/Rehearse/internal/agent"
	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/gate"
	"github.com/YashSejani/Rehearse/internal/knowledge"
	"github.com/YashSejani/Rehearse/internal/mcpserver"
	"github.com/YashSejani/Rehearse/internal/server"
)

func init() { extraCommands = append(extraCommands, newServeCmd, newMCPCmd) }

// diagnoser returns a function that runs one full investigation (with RAG if configured),
// writing a trace per run.
func (f *rootFlags) diagnoser(ctx context.Context, cfg *config.Config, ragHook func(context.Context, *agent.Facts) string) func(context.Context, string) *agent.Result {
	return func(ctx context.Context, target string) *agent.Result {
		a, done, err := f.newAgent(target, true)
		if err != nil {
			return &agent.Result{Answer: agent.Fallback(target, err.Error()), Error: err.Error()}
		}
		defer done()
		a.Context = ragHook
		return a.Diagnose(ctx, target)
	}
}

func newServeCmd(f *rootFlags) *cobra.Command {
	var listen, dashboard string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Agent service: Alertmanager webhook (POST /alerts), incidents, Slack, dashboard API",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := f.load()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			st, done, err := f.openStore(ctx, cfg)
			if err != nil {
				return err
			}
			defer done()
			gc, err := f.gateClient(ctx, cfg)
			if err != nil {
				return err
			}
			var hook func(context.Context, *agent.Facts) string
			if cfg.Knowledge.RAG {
				if h, stop, err := f.ragContext(ctx, cfg); err == nil {
					defer stop()
					hook = h
				}
			}
			s := &server.Server{Namespace: cfg.App.Namespace, Incidents: st, Gate: gc, Diagnose: f.diagnoser(ctx, cfg, hook),
				DashboardURL: dashboard, Token: os.Getenv("REHEARSE_DASHBOARD_TOKEN"), Dedupe: 15 * time.Minute}
			if u := os.Getenv("SLACK_WEBHOOK_URL"); u != "" {
				s.Slack = &server.Slack{URL: u}
			}
			s.OnResolved = func(ctx context.Context) error {
				chunks, err := knowledge.LoadSources(cfg.Knowledge.Sources)
				if err != nil {
					return err
				}
				inc, err := st.ResolvedIncidentChunks(ctx)
				if err != nil {
					return err
				}
				added, _, err := st.Ingest(ctx, knowledge.NewEmbedder(cfg.Agent.BaseURL, cfg.Knowledge.EmbedModel), append(chunks, inc...))
				log.Printf("saved resolved incidents into memory: %d new chunks", added)
				return err
			}
			if s.Token == "" {
				return fmt.Errorf("set REHEARSE_DASHBOARD_TOKEN (the dashboard sends it as X-Rehearse-Token)")
			}
			log.Printf("agent service on %s (slack: %v, rag: %v)", listen, s.Slack != nil, hook != nil)
			srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
			go func() { <-ctx.Done(); _ = srv.Close() }()
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-time.After(15 * time.Second):
						if err := s.SyncIncidents(ctx); err != nil {
							log.Printf("sync incidents: %v", err)
						}
					}
				}
			}()
			err = srv.ListenAndServe()
			s.Wait()
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		},
	}
	// 172.20.0.1 is the kind Docker network's gateway: Alertmanager pods can reach it, the
	// outside world cannot. Ollama stays on 127.0.0.1.
	cmd.Flags().StringVar(&listen, "listen", "172.20.0.1:8090", "listen address")
	cmd.Flags().StringVar(&dashboard, "dashboard-url", "http://localhost:3001", "link put into Slack messages")
	return cmd
}

func newMCPCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "MCP server on stdio: get_app_model, explain_failure, propose_action (through the Safety Gate)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := f.load()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			st, done, err := f.openStore(ctx, cfg)
			if err != nil {
				return err
			}
			defer done()
			gc, err := f.gateClient(ctx, cfg)
			if err != nil {
				return err
			}
			b := mcpserver.Backend{
				Namespace: cfg.App.Namespace,
				AppModel:  func(ctx context.Context) (*knowledge.ServiceMap, error) { return st.LoadMap(ctx, cfg.App.Namespace) },
				Diagnose:  f.diagnoser(ctx, cfg, nil),
				Propose: func(ctx context.Context, r gate.Request) (*gate.Proposal, error) {
					p, err := gc.Propose(ctx, r)
					if err != nil || p.Status != gate.StatusChecking {
						return p, err
					}
					wctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
					defer cancel()
					return gc.WaitChecked(wctx, p.ID, 5*time.Second)
				},
			}
			log.SetOutput(os.Stderr) // stdout is the MCP channel
			return mcpserver.New(b).Run(ctx, &mcp.StdioTransport{})
		},
	}
}
