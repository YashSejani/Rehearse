# HotelReservation: facts about the app we break

Source: DeathStarBench `hotelReservation/` (github.com/delimitrou/DeathStarBench) and AIOpsLab's
variant (`reference/AIOpsLab`, submodule `aiopslab-applications`). Facts were checked against the
source and our cluster in Phase 1 (2026-11-04).

## What it is

A hotel search and booking site written in Go, using gRPC between services, MongoDB for storage,
memcached for caching, Consul for service discovery and Jaeger for tracing. Originally generated
with `kompose`, so every object has the label `io.kompose.service: <name>`.

## Services

| Service | Job | Talks to |
|---|---|---|
| `frontend` | HTTP entry point, port **5000** | search, profile, recommendation, user, reservation |
| `search` | Find hotels near a location | geo, rate |
| `geo` | Hotels near lat/lon | mongodb-geo |
| `rate` | Prices for dates | mongodb-rate, memcached-rate |
| `profile` | Hotel details | mongodb-profile, memcached-profile |
| `recommendation` | Recommend hotels (folder is spelled `reccomend` upstream) | mongodb-recommendation |
| `user` | Login check | mongodb-user |
| `reservation` | Bookings (folder `reserve`) | mongodb-reservation, memcached-reserve |
| `consul` | Service discovery for gRPC | — |
| `jaeger` | Trace collector + UI (all-in-one) | — |

About 19 pods in total. Container names follow `hotel-reserv-<service>`, e.g. `hotel-reserv-geo`.

## Upstream deployment details (DeathStarBench)

- Manifests: `hotelReservation/kubernetes/<service>/*.yaml`; README says `kubectl apply -Rf hotelReservation/kubernetes/`. No namespace inside, so apply with a namespace (we use kustomize `namespace: hotel`).
- Images: `deathstarbench/hotel-reservation:latest` (pin a tag: newest is `0.3.5`), `mongo:4.4.6`, `hashicorp/consul:latest`, `jaegertracing/all-in-one:latest`. Pin every `:latest` (Kyverno will forbid it in Phase 6).
- Resources: only CPU requests/limits (100m / 1000m); no memory limits, so break-oom must add one.
- Storage: each MongoDB has a PersistentVolume with `hostPath` and its own storageClassName (e.g. `geo-storage`), bound statically. Works on kind.
- Workload: wrk2 Lua script `wrk2/scripts/hotel-reservation/mixed-workload_type_1.lua` hitting `GET /hotels?inDate&outDate&lat&lon`, `GET /recommendations?require=dis|rate|price&lat&lon`, `POST /user?username&password`, `POST /reservation?inDate&outDate&hotelId&customerName&username&password&number` (verified in `services/frontend/server.go`). Test users are `Cornell_<0..500>`, password = the id written 10 times. Our k6 port: `deploy/loadgen/k6-script.js`.

## AIOpsLab variant (the one we run, vendored in `deploy/hotel/`)

- Vendored from `aiopslab-applications` commit `8038be6`. That repo is **Apache-2.0** (AIOpsLab itself is MIT); its license is kept as `deploy/hotel/LICENSE-aiopslab-applications`.
- Image `yinfangchen/hotelreservation` has only a `latest` tag; we pin the digest `sha256:c46c018d…` in `deploy/hotel/kustomization.yaml`. Other pins: `mongo:4.4.6`, `memcached:1.6.45`, `hashicorp/consul:1.22.7`, `jaegertracing/all-in-one:1.57`.
- Only `mongodb-geo` and `mongodb-rate` run with `--auth`; `geo` and `rate` connect as `admin:admin` (hard-coded in `cmd/geo/db.go`, `cmd/rate/db.go`). The users are created by init scripts from ConfigMaps `mongo-geo-script` / `mongo-rate-script`; the fault scripts live in `failure-admin-geo` / `failure-admin-rate`. AIOpsLab creates these from Python; we generate them with kustomize from `deploy/hotel/scripts/`.
- Each MongoDB uses a 1Gi PVC on kind's default `standard` StorageClass (dynamic, no static hostPath PVs).
- All deployments have `strategy: Recreate` and a 32Mi memory request (kustomize patch).

- Namespace in AIOpsLab: `test-hotel-reservation`; metadata: `aiopslab/service/metadata/hotel-reservation.json` (Helm chart path `hotelReservation/helm-chart/hotelreservation`, K8s path `hotelReservation/kubernetes`).
- Images `yinfangchen/hotelreservation:latest`; a deliberately buggy geo image `yinfangchen/geo:app3` (used by the `misconfig_app` fault).
- MongoDB with authentication (`admin`/`admin`); databases such as `rate-db`, `geo-db`. Fault scripts in `aiopslab/generators/fault/script/` revoke or remove these users.
- The manifests live in the `aiopslab-applications` git submodule, which is **not cloned** yet:
  `git -C reference/AIOpsLab submodule update --init aiopslab-applications`

## Observability facts

- The services do not expose their own Prometheus `/metrics` endpoint (verified: no prometheus import in the Go source). Metrics come from kube-state-metrics (pod status, restarts, OOMKilled, replicas), cAdvisor (CPU, memory) and the load generator (request rate, errors, latency per endpoint).
- Logs go to stdout (zerolog-style lines) and are collected into Loki.
- Traces go to the in-namespace Jaeger (`jaeger:6831` UDP, from `config.json`; sample ratio 1 via `JAEGER_SAMPLE_RATIO`). Jaeger UI/API on `jaeger.hotel:16686`.

## Known gotcha for fault injection

With 1 replica and the default RollingUpdate strategy, a bad image or a too-low memory limit
creates a new failing pod while the **old pod keeps serving**. Users see no errors, only a stuck
rollout. Decided in Phase 1: all app deployments use `strategy: Recreate`, so the fault is a real outage.
