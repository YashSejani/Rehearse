package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandsRegistered(t *testing.T) {
	root := NewRoot()
	for _, name := range []string{"status", "logs", "metrics", "changes"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("command %q not registered", name)
		}
	}
}

func TestArgsValidated(t *testing.T) {
	for _, args := range [][]string{{"logs"}, {"metrics"}, {"changes", "a", "b"}, {"status", "extra"}} {
		root := NewRoot()
		root.SetArgs(append(args, "--config", "../../config.yaml"))
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		if err := root.Execute(); err == nil || !(strings.Contains(err.Error(), "arg") || strings.Contains(err.Error(), "unknown command")) {
			t.Errorf("%v: want argument error, got %v", args, err)
		}
	}
}

func TestBadConfigPath(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{"status", "--config", "/does/not/exist.yaml"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for missing config")
	}
}
