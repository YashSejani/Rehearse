CLUSTER_NAME := rehearse
KIND_CONFIG  := deploy/kind-config.yaml

.PHONY: help cluster delete-cluster

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-16s %s\n", $$1, $$2}'

cluster: ## Create the local kind cluster (1 control plane, 1 worker)
	@if kind get clusters | grep -qx $(CLUSTER_NAME); then \
		echo "Cluster '$(CLUSTER_NAME)' already exists"; \
	else \
		kind create cluster --config $(KIND_CONFIG) --wait 120s; \
	fi
	kubectl cluster-info --context kind-$(CLUSTER_NAME)
	kubectl wait --for=condition=Ready nodes --all --timeout=120s
	kubectl get nodes

delete-cluster: ## Delete the local kind cluster
	kind delete cluster --name $(CLUSTER_NAME)
