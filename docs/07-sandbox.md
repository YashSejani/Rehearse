# Phase 7: Sandbox rehearsal (test the fix on a copy first)

Before a human is asked to approve, the gate copies the app into a temporary namespace,
checks the fault is visible there, applies the proposed fix to the copy, and reports whether
the copy became healthy. A wrong fix is caught in the copy and never reaches the real app.

## 1. The idea

This is AIOpsLab turned around. AIOpsLab breaks a test app to grade an agent; Rehearse breaks
(or keeps broken) a *copy of the real app* to test one fix before production.

**What a rehearsal proves:** the proposed change to the Deployment makes the copy's pods
become Ready and a smoke request (`GET /hotels?...` on the copy's frontend) succeeds.

**What it cannot prove (be honest):**
- **Data is not copied.** Each MongoDB in the copy starts empty (the services generate their test
  data at start). A fault that lives in the data (a revoked database user, corrupted records)
  does not reproduce, so the result is `not rehearsed (fault did not reproduce in the copy)`.
- **Real traffic, external APIs, other namespaces and cluster-wide state** are not copied.
- **Secrets** are not read; a scenario that needs a secret gets dummy values.
- Timing: the copy runs next to the real app on the same laptop, so it is slower.

The approval screen and the audit log always say one of: `rehearsed: healthy in Ns`,
`rehearsed: still failing (...)` (the proposal is blocked), or `not rehearsed (reason)`.

## 2. vCluster or namespace?

| | vCluster | Temporary namespace |
|---|---|---|
| Isolation | own API server, can copy cluster-scoped objects | shares the cluster; isolated by namespace DNS |
| Extra RAM | a control plane (k3s/k8s + etcd/sqlite), ~0.5–1 GiB, plus the app copy | only the app copy (~0.6 GiB for HotelReservation) |
| Start time | slower (control plane first) | as fast as the pods start |

Measured before the decision: ~10 GiB of 14 GiB used with Phases 1–6 running (kind nodes
~4 GiB, Ollama ~2.4 GiB with the model loaded), ~4 GiB available. A vCluster plus a second copy
of the app does not fit reliably, so **sandboxes are namespaces** (`gate.sandbox.mode:
namespace`, decisions.md). The copy has its own Consul, so the copied services discover only
each other, never the real app.

## 3. What we built

`internal/gate/sandbox` (used by the gate between dry-run and approval):

1. Create namespace `rehearse-sbx-<random>` labelled `rehearse.dev/sandbox=true`.
2. Bind the gate, **inside that namespace only**, to ClusterRole `rehearse-sandbox-admin`
   (Kyverno policy `rehearse-sandbox-only`: the gate may create namespaces and RoleBindings only
   for `rehearse-sbx-*`). Waits until the new RBAC is effective.
3. Copy ConfigMaps (not `kube-root-ca.crt`), PVC specs (new empty volumes), Services (NodePort
   and LoadBalancer turned into ClusterIP: never exposed), Deployments (current spec, so the
   broken spec is reproduced).
4. If the copy becomes healthy without the fix within 90 s: `not rehearsed (fault did not
   reproduce)`.
5. Apply the same JSON Patch to the copy; wait up to `gate.sandbox.timeout` (180 s) for all
   deployments to be available; send the smoke request.
6. **Always** delete the namespace (`defer`, with a fresh context so it runs even after errors
   or a cancelled request). `rehearse sandbox cleanup` deletes any leftovers.

The same package also gives the Phase 8 data generator `rehearse sandbox create|sync|delete`.

## 4. Tests (no cluster)

`internal/gate/sandbox/sandbox_test.go` uses a fake clientset that simulates the deployment
controller: a good fix is "rehearsed: healthy" and the real deployment is unchanged; a wrong fix
is "rehearsed: still failing"; a fault outside the copy is "did not reproduce"; the copy never
exposes a NodePort or mounts the real volume; no sandbox is left after any test.

## 5. Results on the cluster (2026-11-04)

| Proposal | Situation | Rehearsal result | Then |
|---|---|---|---|
| #7 PatchMemoryLimit mongodb-profile 256Mi | real OOM (`make break-oom`) | **rehearsed: healthy in 50s** (sandbox created in 7 s) | approved, applied, healthy |
| #8 ScaleReplicas geo 2 (deliberately wrong) | real image-pull failure (`make break-image`) | **rehearsed: still failing after 3m0s (not ready: geo)** | **blocked**; real geo untouched (still 1 replica) |
| #9 FixImage geo to a broken tag | geo healthy | **not rehearsed (fault did not reproduce in the copy)**, honest | approved on purpose → the gate's watch undid it in 60 s |

Five sandboxes were created during testing (one failed while copying, see below; one was
cancelled by the old synchronous API). **None was left afterwards**: `kubectl get ns -l
rehearse.dev/sandbox=true` is empty and `rehearse sandbox cleanup` finds nothing.

## 6. Problems we hit

| Problem | Fix |
|---|---|
| Copying the `jaeger-out` NodePort Service as ClusterIP failed: `externalTrafficPolicy may only be set for externally-accessible services` | clear external-only fields when turning a Service into ClusterIP |
| The proxy timeout cancelled long rehearsals | asynchronous proposals (docs/06-safety.md) |
