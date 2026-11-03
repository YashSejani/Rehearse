# Phase 2: The Collector (`rehearse status | logs | metrics | changes`)

The Collector is a Go CLI that answers four questions about the app: *what is broken now*,
*what does it say*, *what do the numbers say*, and *what changed*. No AI yet. In Phase 3 these
same functions become the agent's **tools**.

## 1. The ideas

**Tools are just functions.** An AI agent cannot look at a cluster. It can only call the
functions we give it and read what they return. So the quality of the agent is capped by the
quality of its tools. Two rules follow:

- **Return small, typed results.** `PodIssue{Name: "mongodb-profile-68d…", Reason: "OOMKilled", Restarts: 3}`
  is a few tokens; 400 lines of `kubectl describe` are thousands, and a small model loses the
  thread. The AIOpsLab paper saw agents fail exactly like that.
- **Cap and deduplicate.** The frontend printed one Consul error ~350 times in Phase 1. We strip
  timestamps and colour codes, then keep one line with `count: 347` (idea from AIOpsLab's
  `log_deduplication.py`).

**client-go: list vs watch.** client-go is the official Go library for the Kubernetes API (kubectl
uses it inside).

| | List | Watch |
|---|---|---|
| Picture | a photo | a live video |
| Call | "give me all pods in `hotel`" | "tell me every time a pod changes" |
| Use here | `rehearse status`: what is wrong *now* | later (Phase 10 alert listener) |

`status` does one `List` of pods and one of deployments, so it finishes in ~20 ms.

**Interfaces (Go).** `collector.Getter` is an interface with one method, `Get(ctx, path, query)`.
Two types implement it: `ProxyGetter` (through the Kubernetes API) and `URLGetter` (plain HTTP).
The Prometheus and Loki clients only know "something with a Get method", so tests can plug in a
fake HTTP server and the real code does not change.

**Contexts (Go).** Every call takes a `context.Context` with a timeout (`limits.timeout_seconds`,
15 s). If Loki hangs, the call is cancelled instead of freezing the agent.

## 2. What we built

```
cmd/rehearse/main.go        thin main → internal/cli
internal/cli/               cobra commands: status, logs, metrics, changes (-o table|json)
internal/config/            config.yaml loader (+ defaults, validation)
internal/kube/              kubeconfig → client-go clientset
internal/collector/
  backend.go   Getter interface, ProxyGetter (K8s API service proxy), URLGetter
  prom.go      Prometheus instant queries        loki.go   Loki query_range
  status.go    Status()  → []PodIssue
  logs.go      Logs()    → LogLines   (Normalize, Dedup, CompactEvent)
  metrics.go   Metrics() → ServiceMetrics
  changes.go   Changes() → []ChangeEvent
config.yaml                 namespace, service label, endpoint map, backends, limits
```

**How the CLI reaches Prometheus and Loki:** through the Kubernetes API server's *service proxy*
(`/api/v1/namespaces/monitoring/services/loki:3100/proxy/...`), with the same kubeconfig as
kubectl. No port-forward needed. For a real cluster with an ingress, put `url:` in config.yaml
instead (decisions.md, 2026-11-04).

### The four tools

| Tool | Returns | Where the data comes from |
|---|---|---|
| `Status()` | `[]PodIssue{kind, name, service, reason, message, restarts, since}`, most serious first, max 20 | Pods: waiting reason (CrashLoopBackOff, ImagePullBackOff, CreateContainerConfigError...), last termination (OOMKilled, exit 137 = Killed), Pending/Unschedulable, running but NotReady > 60 s. Deployments: replicas 0 (ScaledToZero), ProgressDeadlineExceeded (StuckRollout), fewer available than desired (Unavailable) |
| `Logs(svc)` | `LogLines{raw_lines, lines: [{text, count, source, first, last}]}`, max 20 distinct | Loki: app lines matching an error filter, plus Kubernetes events about the service's pods and deployment (a pod that is down cannot log) |
| `Metrics(svc)` | `ServiceMetrics{endpoints: [{endpoint, rate, error_ratio, p95_ms}], replicas, restarts, memory, limit, cpu, last_terminated}` | Prometheus: k6 metrics for the endpoints this service serves (`app.endpoints` in config.yaml), kube-state-metrics, cAdvisor |
| `Changes(svc)` | `[]ChangeEvent{time, kind, summary}`, max 15 | ReplicaSet revisions with a diff to the previous one (image, memory limit, env, ports, probes, command), the current spec, warning / scaling events |

**Why CrashLoopBackOff is reported as OOMKilled:** "CrashLoopBackOff" only says "it keeps
crashing and Kubernetes is waiting before the next try". The *reason* is in the container's last
termination, so `Status` reports that (`OOMKilled`, or `Killed` for exit 137 without a label).

**Why a rollout's time is its last "Scaled up" event:** a rollback re-uses the old ReplicaSet, whose
creation time is hours old. The scale-up event says when that revision became live again.

**Safety:** service names are checked against `^[a-z0-9-]+$` before they go into a PromQL or
LogQL query, so a name like `geo"} or {x="` cannot change the query. Everything is read-only.

## 3. How to run it

```bash
make build                     # → bin/rehearse
./bin/rehearse status
./bin/rehearse logs geo --lines 10 --window 30m
./bin/rehearse metrics frontend --window 15m
./bin/rehearse changes geo
./bin/rehearse status -o json  # the struct the agent will see
make test                      # gofmt + go vet + go test
```

Flags on every command: `--config`, `--context`, `-n/--namespace`, `-o table|json`.

## 4. Results on the real cluster

Healthy app: `All pods and deployments in "hotel" are healthy.` (in 17 ms)

After `make break-oom` (75 s):

```
KIND        NAME                              SERVICE          REASON       RESTARTS  SINCE  MESSAGE
Pod         mongodb-profile-68d77b5685-tlpp4  mongodb-profile  OOMKilled    3         35s    exit 137
Deployment  mongodb-profile                   mongodb-profile  Unavailable  0         -      0/1 replicas available

$ rehearse changes mongodb-profile
Current        0s ago  now: replicas 1 desired / 0 available, strategy Recreate, ... image=mongo:4.4.6 mem-limit=24Mi
Event         35s ago  Warning BackOff Pod/mongodb-profile-68d77b5685-tlpp4: Back-off restarting failed container ... (x3)
Rollout     1m15s ago  revision 8: hotel-reserv-profile-mongo memory limit none -> 24Mi
```

After `make break-image break-scale`:

```
Pod         geo-b67c9dff5-j5czg  geo   ImagePullBackOff  ...  Back-off pulling image "yinfangchen/hotelreservation:does-not-exist"
Deployment  user                 user  ScaledToZero      ...  spec.replicas is 0: no pods will run
Deployment  geo                  geo   Unavailable       ...  0/1 replicas available
```

`metrics` and `logs` work for any service, including ones with no endpoints (MongoDB, memcached).
For those, only pod numbers are shown.

## 5. Tests

`go test ./...` passes with no cluster: a fake clientset (`k8s.io/client-go/kubernetes/fake`)
for pods, deployments, replica sets and events, and an `httptest` server that pretends to be
Prometheus and Loki and records the queries it receives.

| Test | Checks |
|---|---|
| `TestStatus` | 8 kinds of problem detected; healthy and just-starting pods not reported; most serious first |
| `TestDedup`, `TestNormalize` | 347 copies → 1 line ×347; colour codes, timestamps, UUIDs stripped |
| `TestCompactEvent` | Alloy's long logfmt event → `Warning Failed Pod/geo-...: Failed to pull image ...` |
| `TestLogs` | app + event lines merged newest first; queries scoped to namespace and service; LogQL injection rejected |
| `TestMetrics` | error ratio = errors / requests per endpoint; p95 in ms; pod regex does not match other services |
| `TestChanges` | image and memory-limit diff between revisions; routine events and other services filtered |
| config, kube, cli | config.yaml parses; kubeconfig contexts; commands and argument checks |

## 6. Problems we hit

| Problem | Fix |
|---|---|
| `go mod tidy` tried to compile AIOpsLab's Go code in `reference/` | `ignore ./reference` (and `./dashboard`) in go.mod (Go 1.25+ feature) |
| Rollback revision showed an old time | Use the last "Scaled up replica set" event time |
| Kubelet's "unable to retrieve container logs" looked like an app error | Filtered out in the LogQL |
| `go mod init` wrote `go 1.26.8` | Set to `go 1.26` (project rule: never require a newer toolchain) |
