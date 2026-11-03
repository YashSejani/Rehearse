// Package cli holds the cobra commands of the `rehearse` binary.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/YashSejani/Rehearse/internal/collector"
	"github.com/YashSejani/Rehearse/internal/config"
	"github.com/YashSejani/Rehearse/internal/kube"
)

type rootFlags struct {
	configPath string
	context    string
	namespace  string
	output     string
	alwaysAsk  bool
}

// NewRoot builds the command tree. Subcommands added in later phases register here.
func NewRoot() *cobra.Command {
	f := &rootFlags{}
	root := &cobra.Command{
		Use:           "rehearse",
		Short:         "Rehearse: learn an app on Kubernetes, diagnose failures, propose safe fixes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&f.configPath, "config", envOr("REHEARSE_CONFIG", "config.yaml"), "path to config.yaml")
	root.PersistentFlags().StringVar(&f.context, "context", "", "kubeconfig context (overrides config)")
	root.PersistentFlags().StringVarP(&f.namespace, "namespace", "n", "", "app namespace (overrides config)")
	root.PersistentFlags().StringVarP(&f.output, "output", "o", "table", "output format: table or json")

	root.AddCommand(newStatusCmd(f), newLogsCmd(f), newMetricsCmd(f), newChangesCmd(f))
	for _, add := range extraCommands {
		root.AddCommand(add(f))
	}
	return root
}

// extraCommands lets other files (later phases) add commands without touching NewRoot.
var extraCommands []func(*rootFlags) *cobra.Command

func (f *rootFlags) load() (*config.Config, error) {
	cfg, err := config.Load(f.configPath)
	if err != nil {
		return nil, err
	}
	if f.context != "" {
		cfg.Kube.Context = f.context
	}
	if f.namespace != "" {
		cfg.App.Namespace = f.namespace
	}
	return cfg, nil
}

func (f *rootFlags) collector() (*collector.Collector, error) {
	cfg, err := f.load()
	if err != nil {
		return nil, err
	}
	cs, _, err := kube.Client(cfg.Kube.Context)
	if err != nil {
		return nil, err
	}
	return collector.New(cfg, cs)
}

func (f *rootFlags) json() bool { return f.output == "json" }

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func table(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) }

func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t).Round(time.Second)
	if d < 0 {
		d = 0
	}
	return d.String()
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func oneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	if err := NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}
