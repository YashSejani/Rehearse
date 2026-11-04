package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/YashSejani/Rehearse/internal/gate/sandbox"
	"github.com/YashSejani/Rehearse/internal/kube"
)

func init() { extraCommands = append(extraCommands, newSandboxCmd) }

func newSandboxCmd(f *rootFlags) *cobra.Command {
	root := &cobra.Command{Use: "sandbox", Short: "Rehearsal sandboxes (temporary rehearse-sbx-* namespaces)"}
	newSB := func() (*sandbox.Sandbox, error) {
		cfg, err := f.load()
		if err != nil {
			return nil, err
		}
		cs, _, err := kube.Client(cfg.Kube.Context)
		if err != nil {
			return nil, err
		}
		return &sandbox.Sandbox{Kube: cs, SourceNS: cfg.App.Namespace}, nil
	}
	root.AddCommand(&cobra.Command{
		Use:   "create <rehearse-sbx-name>",
		Short: "Copy the app namespace into a new sandbox namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, err := newSB()
			if err != nil {
				return err
			}
			if err := sb.Create(cmd.Context(), args[0]); err != nil {
				sb.Delete(args[0])
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "created", args[0])
			return nil
		},
	}, &cobra.Command{
		Use:   "delete <rehearse-sbx-name>",
		Short: "Delete one sandbox namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, err := newSB()
			if err != nil {
				return err
			}
			if !strings.HasPrefix(args[0], sandbox.Prefix) {
				return fmt.Errorf("refusing to delete %q: not a sandbox", args[0])
			}
			sb.Delete(args[0])
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "sync <rehearse-sbx-name> <deployment>",
		Short: "Reset one deployment (and its Service) in a sandbox to the real app's spec",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, err := newSB()
			if err != nil {
				return err
			}
			if !strings.HasPrefix(args[0], sandbox.Prefix) {
				return fmt.Errorf("%q is not a sandbox", args[0])
			}
			return sb.Sync(cmd.Context(), args[0], args[1])
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "cleanup",
		Short: "Delete every leftover sandbox namespace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := f.load()
			if err != nil {
				return err
			}
			cs, _, err := kube.Client(cfg.Kube.Context)
			if err != nil {
				return err
			}
			deleted, err := sandbox.CleanupAll(cmd.Context(), cs)
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %d sandbox namespace(s) %v\n", len(deleted), deleted)
			return err
		},
	})
	return root
}
