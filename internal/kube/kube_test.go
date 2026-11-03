package kube

import (
	"os"
	"path/filepath"
	"testing"
)

const kubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: a
  cluster: {server: "https://a.example:6443"}
- name: b
  cluster: {server: "https://b.example:6443"}
users:
- name: u
  user: {token: test}
contexts:
- name: ctx-a
  context: {cluster: a, user: u}
- name: ctx-b
  context: {cluster: b, user: u}
current-context: ctx-a
`

func TestRESTConfigContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	t.Setenv("KUBERNETES_SERVICE_HOST", "") // not in a pod

	cfg, err := RESTConfig("")
	if err != nil || cfg.Host != "https://a.example:6443" {
		t.Fatalf("current context: host %v, err %v", cfg, err)
	}
	cfg, err = RESTConfig("ctx-b")
	if err != nil || cfg.Host != "https://b.example:6443" {
		t.Fatalf("explicit context: host %v, err %v", cfg, err)
	}
	if _, err := RESTConfig("nope"); err == nil {
		t.Error("unknown context should fail")
	}
}
