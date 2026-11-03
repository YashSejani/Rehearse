package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List unhealthy pods and deployments (CrashLoopBackOff, OOMKilled, ImagePullBackOff, ...)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := f.collector()
			if err != nil {
				return err
			}
			issues, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if f.json() {
				return printJSON(out, issues)
			}
			if len(issues) == 0 {
				fmt.Fprintf(out, "All pods and deployments in %q are healthy.\n", c.Cfg.App.Namespace)
				return nil
			}
			tw := table(out)
			fmt.Fprintln(tw, "KIND\tNAME\tSERVICE\tREASON\tRESTARTS\tSINCE\tMESSAGE")
			for _, is := range issues {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", is.Kind, is.Name, is.Service, is.Reason,
					is.Restarts, ago(is.Since), oneLine(is.Message, 80))
			}
			return tw.Flush()
		},
	}
}

func newLogsCmd(f *rootFlags) *cobra.Command {
	var lines int
	var window time.Duration
	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Recent error log lines and Kubernetes events of a service, deduplicated",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.collector()
			if err != nil {
				return err
			}
			ll, err := c.Logs(cmd.Context(), args[0], lines, window)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if f.json() {
				return printJSON(out, ll)
			}
			fmt.Fprintf(out, "%s: %d distinct lines from %d raw (last %s)\n", ll.Service, len(ll.Lines), ll.Raw, ll.Window)
			for _, l := range ll.Lines {
				fmt.Fprintf(out, "  [%s] x%-4d %s ago  %s\n", l.Source, l.Count, ago(l.Last), oneLine(l.Text, 160))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&lines, "lines", 20, "max distinct lines")
	cmd.Flags().DurationVar(&window, "window", 15*time.Minute, "how far back to look")
	return cmd
}

func newMetricsCmd(f *rootFlags) *cobra.Command {
	var window time.Duration
	cmd := &cobra.Command{
		Use:   "metrics <service>",
		Short: "Request rate, error ratio, p95 latency, restarts and memory of a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.collector()
			if err != nil {
				return err
			}
			m, err := c.Metrics(cmd.Context(), args[0], window)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if f.json() {
				return printJSON(out, m)
			}
			fmt.Fprintf(out, "%s (last %s): replicas %.0f/%.0f available, restarts %.0f, memory %.1f MiB",
				m.Service, m.Window, m.AvailableReplicas, m.DesiredReplicas, m.Restarts, m.MemoryMiB)
			if m.MemoryLimitMiB > 0 {
				fmt.Fprintf(out, " / limit %.0f MiB", m.MemoryLimitMiB)
			}
			fmt.Fprintf(out, ", cpu %.3f cores", m.CPUCores)
			if m.LastTerminated != "" {
				fmt.Fprintf(out, ", last terminated: %s", m.LastTerminated)
			}
			fmt.Fprintln(out)
			if len(m.Endpoints) > 0 {
				tw := table(out)
				fmt.Fprintln(tw, "  ENDPOINT\tREQ/S\tERRORS\tP95")
				for _, e := range m.Endpoints {
					fmt.Fprintf(tw, "  /%s\t%.2f\t%.1f%%\t%.1f ms\n", e.Endpoint, e.RatePerSec, e.ErrorRatio*100, e.P95Ms)
				}
				return tw.Flush()
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&window, "window", 5*time.Minute, "time window for rates")
	return cmd
}

func newChangesCmd(f *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "changes <service>",
		Short: "Recent rollouts (what changed), current spec and warning events of a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.collector()
			if err != nil {
				return err
			}
			ch, err := c.Changes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if f.json() {
				return printJSON(out, ch)
			}
			for _, e := range ch {
				n := ""
				if e.Count > 1 {
					n = fmt.Sprintf(" (x%d)", e.Count)
				}
				fmt.Fprintf(out, "%-8s %8s ago  %s%s\n", e.Kind, ago(e.Time), oneLine(e.Summary, 170), n)
			}
			return nil
		},
	}
}
