---
paths:
  - "deploy/**"
  - "scenarios/**"
  - "Makefile"
  - "**/*.yaml"
---

# Kubernetes, Helm and Makefile rules

- Cluster: kind `rehearse`, context `kind-rehearse`. Check `kubectl config current-context` before changing anything.
- Namespaces: app in `hotel`, monitoring in `monitoring`, Chaos Mesh in `chaos-mesh`, sandboxes `rehearse-sbx-<id>`.
- **Show every command that changes the cluster before running it.** Read-only commands (`get`, `describe`, `logs`, `top`) are fine.
- All Helm values in `deploy/<chart>-values.yaml`; pin chart versions in the Makefile. Helm on this machine is v4.
- Pin every image tag; never `:latest`. App manifests vendored in `deploy/hotel/` with kustomize.
- Laptop budget: ~14 GiB RAM total, Ollama needs ~2.5 GB. Set small requests/limits; disable chart parts we don't need.
- Make targets are idempotent (safe to run twice) and listed by `make help` (`## comment` after the target).
- Every `break-*` has a `fix-*` that restores the exact original spec. Faults must be visible: with 1 replica and RollingUpdate, the old pod keeps serving (see docs/context/hotelreservation.md).
- HotelReservation objects are selected by label `io.kompose.service=<name>`; containers are named `hotel-reserv-<name>`.
- Scenario files (`scenarios/*.yaml`) record their AIOpsLab source file when ported, and keep AIOpsLab's MIT copyright line if text is copied.
