---
paths:
  - "cmd/**"
  - "internal/**"
  - "go.mod"
  - "go.sum"
---

# Go code rules

- Module `github.com/YashSejani/Rehearse`, `go 1.26` in go.mod (Go comes from Ubuntu apt; never require a newer toolchain).
- CLI with Cobra in `cmd/rehearse`; the gate's main in `cmd/gate`. `main` packages stay thin; logic lives in `internal/`.
- Collector and agent tools return **small typed structs** (PodIssue, LogLines, ServiceMetrics, ChangeEvent), never printed text. Cap lists and log lines; deduplicate repeated lines with a count.
- Every external call takes a `context.Context` with a timeout. Wrap errors with `fmt.Errorf("...: %w", err)`.
- URLs, model names and namespaces come from `config.yaml`, never hard-coded.
- Kubernetes access through client-go. Agent code uses a read-only client; only `internal/gate` and `internal/actions` may build write requests.
- Tests next to the code (`*_test.go`): client-go fake clientset for Kubernetes, `httptest` servers for Loki, Prometheus and Ollama. No test may need a real cluster or model unless it is tagged `//go:build integration`.
- Before saying a change is done: `gofmt -l .` is empty, `go vet ./...` and `go test ./...` pass.
- Explain new Go concepts to Yash briefly when they first appear (interfaces, contexts, goroutines).
