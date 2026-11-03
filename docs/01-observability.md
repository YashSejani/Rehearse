# Phase 1: The playground: logs, metrics and traces

We now have a real microservice app (HotelReservation) running on the kind cluster, a monitoring
stack watching it, constant fake users, and three faults we can switch on and off with `make`.
This is the "patient" the agent will diagnose from Phase 3 on.

## 1. The idea: three kinds of evidence

| Signal | Everyday picture | In this app | Stored in | Query language |
|---|---|---|---|---|
| **Logs** | A diary: text written when something happens | `geo` prints `Successfully registered in consul`; Kubernetes writes `Failed to pull image ...` | Loki | LogQL |
| **Metrics** | A heart-rate monitor: one number, sampled every 15 s | `/hotels` error rate, pod restart count, memory used | Prometheus | PromQL |
| **Traces** | Parcel tracking: one request's trip through services | `POST /reservation` → frontend → user → reservation, with the time of each hop | Jaeger | (search UI) |

- **Metrics** say *that* something is wrong and *when* it started.
- **Logs** say *why* (pull error, OOM kill).
- **Traces** say *where* (which hop is slow or failing).

**Lesson of this phase:** an agent can only be as good as the evidence it can read. We broke
the app three times and checked that each fault can be found with a query. Those queries are
listed below; Phase 2 turns them into Go functions (the agent's tools).

Two surprises we found while testing (both matter for the agent later):

1. **The app writes no error log when a whole service is down.** The frontend just returns
   HTTP 500 without logging, and a pod that is not running cannot log. So for these faults the
   "log" evidence comes from **Kubernetes events**, which we also ship into Loki.
2. **A cache can hide a broken database.** When `mongodb-profile` was OOM-killed, users saw
   *no* errors because `profile` answered from memcached. The only evidence was pod restarts.
   No errors does not always mean healthy.

## 2. What we built

```
          k6 load generator (monitoring ns, ~5 req/s)
                    │ HTTP                 │ remote write (k6_* metrics)
                    ▼                      ▼
   hotel ns: frontend → search/geo/rate/profile/recommendation/user/reservation
             + 6 MongoDB + 3 memcached + consul + jaeger   (19 pods)
                    │ stdout logs, k8s events      │ pod state       │ traces (UDP 6831)
                    ▼                              ▼                 ▼
   monitoring ns: Alloy ──► Loki     kube-state-metrics, cAdvisor ──► Prometheus     Jaeger (hotel ns)
                                    \______________ Grafana (all three as data sources) ________/
```

| Part | Where | Notes |
|---|---|---|
| HotelReservation (AIOpsLab variant: MongoDB with passwords) | `deploy/hotel/` | Vendored unchanged; all our changes are in `kustomization.yaml`: namespace `hotel`, pinned images (app image by digest), `strategy: Recreate`, 32Mi memory requests, generated Mongo init/fault ConfigMaps |
| kube-prometheus-stack 92.17.1 | `deploy/kube-prometheus-stack-values.yaml` | Prometheus (remote-write receiver on, 3 d retention), Grafana, Alertmanager, kube-state-metrics, node-exporter |
| Loki 7.5.0 (chart) | `deploy/loki-values.yaml` | One pod, filesystem storage, 72 h retention |
| Alloy 1.15.0 (chart) | `deploy/alloy-values.yaml` | Pod logs via the Kubernetes API + Kubernetes events (`job="kubernetes-events"`) |
| k6 2.3.0 load generator | `deploy/loadgen/` | Constant 5 req/s: 40% `/hotels`, 30% `/recommendations`, 15% `/user`, 15% `/reservation`; tag `endpoint` |
| Faults | `Makefile` | `break-image`/`fix-image`, `break-oom`/`fix-oom`, `break-scale`/`fix-scale` |

**Why Recreate?** With 1 replica and the default RollingUpdate, a broken new pod never replaces
the old healthy one, so users see nothing (only a stuck rollout). Recreate stops the old pod
first, so the fault is a real outage that the agent can see. See decisions.md.

**Why k6?** The app has no `/metrics` endpoint of its own. k6 measures every request from the
user's side and pushes per-endpoint request counts and latency into Prometheus.

## 3. How to run it

```bash
make playground        # cluster + monitoring + app + load (safe to re-run)
make status            # pods in hotel and monitoring
make grafana           # http://localhost:3000, user admin, password rehearse (Ctrl+C stops)
```

In Grafana use **Explore** (compass icon), pick the data source (Prometheus, Loki or Jaeger)
and paste a query from below. For Jaeger: data source Jaeger → Search → service `frontend`.

Ports, if you want the raw APIs instead of Grafana:

```bash
kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090
kubectl -n monitoring port-forward svc/loki 3100:3100
kubectl -n hotel port-forward svc/jaeger 16686:16686
kubectl -n hotel port-forward svc/frontend 5000:5000
```

## 4. Healthy baseline (what normal looks like)

| What | Query | Normal value |
|---|---|---|
| Request rate per endpoint | `sum by (endpoint) (rate(k6_http_reqs_total[2m]))` | ~2 / 1.5 / 0.75 / 0.75 req/s (total ~5) |
| Error ratio per endpoint | see below | 0 (no series at all is also "0") |
| p95 latency | `max by (endpoint) (k6_http_req_duration_p95)` | a few ms (`/hotels` ≈ 3.5 ms) |
| Restarts in last 5 min | `increase(kube_pod_container_status_restarts_total{namespace="hotel"}[5m]) > 0` | empty |
| Memory | Go services ≈ 6.5 MiB each, each MongoDB ≈ 60 MiB, whole `hotel` ns ≈ 600 MiB, `monitoring` ≈ 1.2 GiB |

The **error ratio** query is the most useful single line:

```promql
sum by (endpoint) (rate(k6_http_reqs_total{expected_response="false"}[1m]))
  / sum by (endpoint) (rate(k6_http_reqs_total[1m]))
```

## 5. The three faults and how to find each one

Each `break-*` changes one deployment; each `fix-*` re-applies that deployment from git, so the
spec is exactly the original (checked: the deployment specs were byte-identical before and after
each break/fix cycle).

### Fault 1: `make break-image` (geo gets an image tag that does not exist)

What happens: geo's pod is stuck in `ImagePullBackOff`; search cannot reach geo, so every
hotel search fails.

| Evidence | Query | Seen in our test |
|---|---|---|
| Error ratio jumps | error ratio query above | `/hotels` = **1.0** (100%), other endpoints 0 |
| Pod waiting reason | `kube_pod_container_status_waiting_reason{namespace="hotel", reason=~"ImagePullBackOff\|ErrImagePull"} == 1` | `hotel-reserv-geo`, `ImagePullBackOff` |
| No geo pod available | `kube_deployment_status_replicas_available{namespace="hotel"} == 0` | `geo` |
| Events (LogQL) | `{job="kubernetes-events", namespace="hotel"} \|= "name=geo-" \|~ "reason=(Failed\|BackOff)"` | `reason=Failed ... Failed to pull image "yinfangchen/hotelreservation:does-not-exist"` |

### Fault 2: `make break-oom` (mongodb-profile gets a 24Mi memory limit)

What happens: MongoDB needs ~60 MiB, so the kernel's out-of-memory killer kills `mongod` a second
after it starts; the pod restarts again and again (`CrashLoopBackOff`). Users see **no errors**
(memcached still answers), so only pod metrics show it.

| Evidence | Query | Seen in our test |
|---|---|---|
| Last exit was an OOM kill | `kube_pod_container_status_last_terminated_reason{namespace="hotel", reason="OOMKilled"} == 1` | `hotel-reserv-profile-mongo` |
| Restarts climbing | `increase(kube_pod_container_status_restarts_total{namespace="hotel"}[5m]) > 0` | 4 restarts in 90 s |
| Memory limit now set | `kube_pod_container_resource_limits{namespace="hotel", resource="memory"}` | 25165824 bytes (24Mi) on mongodb-profile |
| Events (LogQL) | `{job="kubernetes-events", namespace="hotel"} \|= "mongodb-profile" \|~ "reason=(BackOff\|Killing)"` | `BackOff` "Back-off restarting failed container" |

### Fault 3: `make break-scale` (user scaled to 0 replicas)

What happens: there is no user pod at all, so login fails, and reservations (which check the
login first) fail too. Nothing crashes, nothing restarts, and nothing logs an error.

| Evidence | Query | Seen in our test |
|---|---|---|
| Error ratio jumps | error ratio query above | `/user` = **1.0**, `/reservation` = **1.0**, `/hotels` and `/recommendations` 0 |
| Desired replicas is 0 | `kube_deployment_spec_replicas{namespace="hotel"} == 0` | `user` |
| Events (LogQL) | `{job="kubernetes-events", namespace="hotel"} \|= "name=user " \|= "reason=ScalingReplicaSet"` | `Scaled down replica set user-... from 1 to 0` |

Careful: with Recreate, *every* rollout writes "Scaled down ... to 0" for the old ReplicaSet, so
the event alone does not prove a scale fault. `kube_deployment_spec_replicas == 0` does.

### Useful LogQL for app logs

App lines are zerolog with terminal colour codes, e.g.
`␛[90m2026-11-03T21:00:04Z␛[0m ␛[32mINF␛[0m cmd/geo/main.go:79 > Starting server...`.

```logql
{namespace="hotel", app="geo"}                       # all geo logs (app = io.kompose.service label)
{namespace="hotel"} |~ "ERR|FTL|error"                # error-ish lines from every service
sum by (app) (count_over_time({namespace="hotel"} |~ "ERR|error" [5m]))   # errors per service
```

## 6. Problems we hit (and the fixes)

| Problem | Cause | Fix |
|---|---|---|
| Image pulls took 10+ min, some `ErrImagePull` | Slow network, DNS hiccups | Nothing: kubelet retries. First `make playground` is slow; later runs use cached images |
| `helm repo update` "connection reset by peer" | Same network | `make helm-repos` retries 3 times |
| Every container's log showed `failed to create fsnotify watcher: too many open files` | kind nodes share the host's inotify limit (128 instances) | Yash raised it: `fs.inotify.max_user_instances=512`, `max_user_watches=524288` in `/etc/sysctl.d/99-rehearse-inotify.conf` |
| OOM kill showed as `Error` (exit 137), not `OOMKilled` | Same inotify limit: containerd could not watch `memory.events` | Fixed by the same sysctl |
| 10Mi limit on `profile` did nothing; 4Mi gave `StartError` | Go services use only ~6.5 MiB; 4Mi kills runc itself before the app starts | Target `mongodb-profile` (60 MiB) with 24Mi instead |
| k6: `unknown flag: --no-summary` | Flag removed in k6 2.x | Dropped the flag |
| Alloy ignored a new ConfigMap after `helm upgrade` | Config not reloaded | `make monitoring` restarts Alloy |
| Alloy errors "entry too far behind" at first start | It replays days-old kube-system logs; Loki rejects old lines | Harmless, only at first start |
| 7 app pods restarted 5–6 times on first deploy | Services start before MongoDB/Consul are ready | Normal; they settle in ~5 min |

## 7. What Phase 2 reuses

- The PromQL and LogQL above become `rehearse metrics` / `rehearse logs`.
- Pod and deployment state (waiting reason, last terminated reason, replicas) becomes `rehearse status`.
- Kubernetes events become `rehearse changes`.
