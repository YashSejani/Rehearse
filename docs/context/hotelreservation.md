# HotelReservation: facts about the app we break

Source: DeathStarBench `hotelReservation/` (github.com/delimitrou/DeathStarBench) and AIOpsLab's
variant (`reference/AIOpsLab`, submodule `aiopslab-applications`). Items marked *(verify)* were not
yet checked on our cluster; confirm them in Phase 1 and remove the mark.

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
- Workload: wrk2 Lua script `wrk2/scripts/hotel-reservation/mixed-workload_type_1.lua` hitting endpoints `/hotels`, `/recommendations`, `/user`, `/reservation` *(verify paths and query parameters)*.

## AIOpsLab variant (recommended for Rehearse, see decisions.md)

- Namespace in AIOpsLab: `test-hotel-reservation`; metadata: `aiopslab/service/metadata/hotel-reservation.json` (Helm chart path `hotelReservation/helm-chart/hotelreservation`, K8s path `hotelReservation/kubernetes`).
- Images `yinfangchen/hotelreservation:latest`; a deliberately buggy geo image `yinfangchen/geo:app3` (used by the `misconfig_app` fault).
- MongoDB with authentication (`admin`/`admin`); databases such as `rate-db`, `geo-db`. Fault scripts in `aiopslab/generators/fault/script/` revoke or remove these users.
- The manifests live in the `aiopslab-applications` git submodule, which is **not cloned** yet:
  `git -C reference/AIOpsLab submodule update --init aiopslab-applications`

## Observability facts

- The services do not expose their own Prometheus `/metrics` endpoint *(verify)*. Metrics come from kube-state-metrics (pod status, restarts, OOMKilled, replicas), cAdvisor (CPU, memory) and the load generator (request rate, errors, latency per endpoint).
- Logs go to stdout (zerolog-style lines) and are collected into Loki.
- Traces go to the in-namespace Jaeger (UDP 6831 / Jaeger thrift) *(verify)*.

## Known gotcha for fault injection

With 1 replica and the default RollingUpdate strategy, a bad image or a too-low memory limit
creates a new failing pod while the **old pod keeps serving**. Users see no errors, only a stuck
rollout. Decide in Phase 1 whether that is the symptom we want (decisions.md).
