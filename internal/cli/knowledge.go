package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/YashSejani/Rehearse/internal/agent"
	"github.com/YashSejani/Rehearse/internal/collector"
	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/knowledge"
	"github.com/YashSejani/Rehearse/internal/kube"
)

func init() {
	extraCommands = append(extraCommands, newLearnCmd, newKnowledgeCmd)
}

// openStore connects to Postgres: the configured DSN (or $REHEARSE_DB_DSN), or else a
// port-forward to the in-cluster Service with the password read from its Secret.
func (f *rootFlags) openStore(ctx context.Context, cfg *config.Config) (*knowledge.Store, func(), error) {
	dsn := os.Getenv("REHEARSE_DB_DSN")
	if dsn == "" {
		dsn = cfg.Knowledge.DSN
	}
	stop := func() {}
	if dsn == "" {
		cs, rc, err := kube.Client(cfg.Kube.Context)
		if err != nil {
			return nil, nil, err
		}
		ref, err := config.ParseServiceRef(cfg.Knowledge.Service)
		if err != nil {
			return nil, nil, fmt.Errorf("knowledge.service: %w", err)
		}
		secNS, secName, _ := strings.Cut(cfg.Knowledge.Secret, "/")
		sec, err := cs.CoreV1().Secrets(secNS).Get(ctx, secName, metav1.GetOptions{})
		if err != nil {
			return nil, nil, fmt.Errorf("db secret %s (run `make rehearse-db`): %w", cfg.Knowledge.Secret, err)
		}
		addr, s, err := kube.PortForward(ctx, cs, rc, ref.Namespace, ref.Name, ref.Port)
		if err != nil {
			return nil, nil, fmt.Errorf("postgres (run `make rehearse-db`): %w", err)
		}
		stop = s
		dsn = fmt.Sprintf("postgres://rehearse:%s@%s/rehearse?sslmode=disable", url.QueryEscape(string(sec.Data["password"])), addr)
	}
	st, err := knowledge.Open(ctx, dsn)
	if err != nil {
		stop()
		return nil, nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		stop()
		return nil, nil, err
	}
	return st, func() { st.Close(); stop() }, nil
}

func newLearnCmd(f *rootFlags) *cobra.Command {
	var noSave bool
	cmd := &cobra.Command{
		Use:   "learn",
		Short: "Learn the app: service map (traces, config, naming) and normal behaviour (baselines)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := f.collector()
			if err != nil {
				return err
			}
			cfg := c.Cfg
			l := &knowledge.Learner{Kube: c.Kube, Prom: c.Prom, Namespace: cfg.App.Namespace,
				ServiceLabel: cfg.App.ServiceLabel, Endpoints: cfg.App.Endpoints}
			if cfg.Jaeger.Service != "" || cfg.Jaeger.URL != "" {
				if l.Jaeger, err = collector.NewGetter(cfg.Jaeger, c.Kube); err != nil {
					return err
				}
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			m, err := l.Learn(ctx)
			if err != nil {
				return err
			}
			if !noSave {
				st, done, err := f.openStore(ctx, cfg)
				if err != nil {
					return err
				}
				defer done()
				if err := st.SaveMap(ctx, m); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if f.json() {
				return printJSON(out, m)
			}
			fmt.Fprintf(out, "Service map of %q: %d services, %d dependencies\n", m.Namespace, len(m.Services), len(m.Edges))
			for _, s := range m.Services {
				deps, users := m.Neighbours(s.Name)
				fmt.Fprintf(out, "  %-24s %-9s -> %s", s.Name, s.Kind, strings.Join(deps, ", "))
				if len(users) > 0 {
					fmt.Fprintf(out, "   (used by %s)", strings.Join(users, ", "))
				}
				fmt.Fprintln(out)
			}
			fmt.Fprintf(out, "Baselines: %d values over the last hour\n", len(m.Baselines))
			for _, n := range m.Notes {
				fmt.Fprintln(out, "  note:", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noSave, "no-save", false, "print only, do not store in Postgres")
	return cmd
}

func newKnowledgeCmd(f *rootFlags) *cobra.Command {
	root := &cobra.Command{Use: "knowledge", Short: "Runbooks, READMEs and past incidents for RAG"}
	ingest := &cobra.Command{
		Use:   "ingest [paths...]",
		Short: "Chunk, embed and store documents (default: knowledge.sources) and resolved incidents",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.load()
			if err != nil {
				return err
			}
			paths := args
			if len(paths) == 0 {
				paths = cfg.Knowledge.Sources
			}
			chunks, err := knowledge.LoadSources(paths)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
			defer cancel()
			st, done, err := f.openStore(ctx, cfg)
			if err != nil {
				return err
			}
			defer done()
			inc, err := st.ResolvedIncidentChunks(ctx)
			if err != nil {
				return err
			}
			e := knowledge.NewEmbedder(cfg.Agent.BaseURL, cfg.Knowledge.EmbedModel)
			start := time.Now()
			added, removed, err := st.Ingest(ctx, e, append(chunks, inc...))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d chunks from %d sources + %d incidents: %d new, %d removed (%s)\n",
				len(chunks), len(paths), len(inc), added, removed, time.Since(start).Round(time.Second))
			return nil
		},
	}
	search := &cobra.Command{
		Use:   "search <text>",
		Short: "Show the documents closest in meaning to a text",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.load()
			if err != nil {
				return err
			}
			st, done, err := f.openStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer done()
			docs, err := st.Search(cmd.Context(), knowledge.NewEmbedder(cfg.Agent.BaseURL, cfg.Knowledge.EmbedModel),
				strings.Join(args, " "), cfg.Knowledge.TopK)
			if err != nil {
				return err
			}
			if f.json() {
				return printJSON(cmd.OutOrStdout(), docs)
			}
			for i, d := range docs {
				fmt.Fprintf(cmd.OutOrStdout(), "%d. %.3f  %-8s %s\n   %s\n", i+1, d.Distance, d.Kind, d.Title, oneLine(d.Text, 150))
			}
			return nil
		},
	}
	root.AddCommand(ingest, search)
	return root
}

// ragContext builds the agent's Context hook: neighbours + baselines + top-k documents.
func (f *rootFlags) ragContext(ctx context.Context, cfg *config.Config) (func(context.Context, *agent.Facts) string, func(), error) {
	st, done, err := f.openStore(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	m, err := st.LoadMap(ctx, cfg.App.Namespace)
	if err != nil {
		done()
		return nil, nil, err
	}
	r := &knowledge.RAG{Store: st, Embedder: knowledge.NewEmbedder(cfg.Agent.BaseURL, cfg.Knowledge.EmbedModel), TopK: cfg.Knowledge.TopK, Map: m}
	return func(ctx context.Context, facts *agent.Facts) string {
		text, _, err := r.Context(ctx, facts.Target, facts.Symptoms())
		if err != nil {
			return ""
		}
		return text
	}, done, nil
}
