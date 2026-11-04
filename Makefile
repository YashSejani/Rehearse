CLUSTER_NAME := rehearse
KIND_CONFIG  := deploy/kind-config.yaml
KUBECTL      := kubectl --context kind-$(CLUSTER_NAME)
HELM         := helm --kube-context kind-$(CLUSTER_NAME)

# Pinned chart versions (bump on purpose, never float).
KPS_CHART_VERSION   := 92.17.1
LOKI_CHART_VERSION  := 7.5.0
ALLOY_CHART_VERSION := 1.15.0
CHAOS_CHART_VERSION := 2.8.6
KYVERNO_CHART_VERSION := 3.9.1
GATE_TAG := $(shell git rev-parse --short HEAD)

HOTEL_NS := hotel
MON_NS   := monitoring

# Fault targets (decisions.md, Phase 1).
BAD_IMAGE    := yinfangchen/hotelreservation:does-not-exist
# MongoDB needs ~60 MiB; 24Mi lets the container start and then mongod is OOM-killed.
# (The Go services use only ~6.5 MiB: a limit that low kills runc itself -> StartError, not OOMKilled.)
OOM_LIMIT    := 24Mi

.PHONY: build test help cluster delete-cluster playground hotel helm-repos monitoring loadgen status grafana chaos-mesh eval rehearse-db kyverno policies gate-image gate learn ingest app2 serve mcp \
	break-image fix-image break-oom fix-oom break-scale fix-scale

help: ## Show available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-16s %s\n", $$1, $$2}'

build: ## Build the rehearse CLI into bin/
	go build -o bin/rehearse ./cmd/rehearse

test: ## gofmt check, go vet and unit tests
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; echo "run gofmt -w"; exit 1; }
	go vet ./...
	go test ./...

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

## ---- Playground: app + monitoring + load ------------------------------------------------

playground: cluster monitoring hotel loadgen status ## Everything for Phase 1 (safe to re-run)

hotel: ## Deploy HotelReservation into namespace hotel and wait until Ready
	$(KUBECTL) apply -k deploy/hotel
	$(KUBECTL) -n $(HOTEL_NS) wait --for=condition=Available deployment --all --timeout=600s

helm-repos: ## Add the Helm chart repositories (once per machine)
	@helm repo list 2>/dev/null | grep -q '^prometheus-community' || helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	@helm repo list 2>/dev/null | grep -q '^grafana' || helm repo add grafana https://grafana.github.io/helm-charts
	@helm repo list 2>/dev/null | grep -q '^chaos-mesh' || helm repo add chaos-mesh https://charts.chaos-mesh.org
	@helm repo list 2>/dev/null | grep -q '^kyverno' || helm repo add kyverno https://kyverno.github.io/kyverno/
	@# Retry: the chart index downloads sometimes get reset on this network.
	@for i in 1 2 3; do helm repo update prometheus-community grafana chaos-mesh kyverno && exit 0; sleep 5; done; exit 1

monitoring: helm-repos ## Install Prometheus/Grafana/Alertmanager, Loki and Alloy into namespace monitoring
	$(HELM) upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
		--version $(KPS_CHART_VERSION) -n $(MON_NS) --create-namespace \
		-f deploy/kube-prometheus-stack-values.yaml --wait --timeout 10m
	$(HELM) upgrade --install loki grafana/loki --version $(LOKI_CHART_VERSION) -n $(MON_NS) \
		-f deploy/loki-values.yaml --wait --timeout 10m
	$(HELM) upgrade --install alloy grafana/alloy --version $(ALLOY_CHART_VERSION) -n $(MON_NS) \
		-f deploy/alloy-values.yaml --wait --timeout 5m
	@# Alloy does not always reload a changed ConfigMap by itself; restart to be sure.
	$(KUBECTL) -n $(MON_NS) rollout restart deployment/alloy
	$(KUBECTL) -n $(MON_NS) rollout status deployment/alloy --timeout=180s

loadgen: ## Start the k6 load generator (~5 req/s, metrics to Prometheus)
	$(KUBECTL) apply -k deploy/loadgen
	$(KUBECTL) -n $(MON_NS) rollout status deployment/loadgen --timeout=300s

status: ## Show pods of the app and the monitoring stack
	$(KUBECTL) -n $(HOTEL_NS) get pods
	$(KUBECTL) -n $(MON_NS) get pods

grafana: ## Open Grafana on http://localhost:3000 (user admin, password rehearse); Ctrl+C to stop
	$(KUBECTL) -n $(MON_NS) port-forward svc/kube-prometheus-stack-grafana 3000:80

chaos-mesh: helm-repos ## Install Chaos Mesh (pod kill, network delay, CPU stress) for Phase 4
	$(HELM) upgrade --install chaos-mesh chaos-mesh/chaos-mesh --version $(CHAOS_CHART_VERSION) \
		-n chaos-mesh --create-namespace -f deploy/chaos-mesh-values.yaml --wait --timeout 10m

## ---- Rehearse's own services (Phases 5-6) ---------------------------------------------

rehearse-db: ## Postgres + pgvector in namespace rehearse (password Secret generated once, never committed)
	$(KUBECTL) apply -f deploy/rehearse/namespace.yaml
	@$(KUBECTL) -n rehearse get secret rehearse-db >/dev/null 2>&1 || \
		$(KUBECTL) -n rehearse create secret generic rehearse-db --from-literal=password=$$(openssl rand -hex 16)
	$(KUBECTL) apply -f deploy/rehearse/postgres.yaml
	$(KUBECTL) -n rehearse rollout status statefulset/postgres --timeout=300s

learn: build ## Learn the app's service map and baselines into Postgres
	./bin/rehearse learn

ingest: build ## Embed runbooks, docs and resolved incidents for RAG
	./bin/rehearse knowledge ingest

kyverno: helm-repos ## Install Kyverno (admission policies for the Safety Gate)
	$(HELM) upgrade --install kyverno kyverno/kyverno --version $(KYVERNO_CHART_VERSION) \
		-n kyverno --create-namespace -f deploy/kyverno-values.yaml --wait --timeout 10m

policies: ## Apply the Safety Gate's Kyverno policies (deploy/policies)
	$(KUBECTL) apply -f deploy/policies/

gate-image: ## Build the gate image (static binary, FROM scratch) and load it into kind
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o bin/gate-linux ./cmd/gate
	docker build -q -f Dockerfile.gate -t rehearse-gate:$(GATE_TAG) .
	kind load docker-image rehearse-gate:$(GATE_TAG) --name $(CLUSTER_NAME)

gate: rehearse-db gate-image ## Deploy the Safety Gate (own ServiceAccount; token Secret generated once)
	@$(KUBECTL) -n rehearse get secret rehearse-gate >/dev/null 2>&1 || \
		$(KUBECTL) -n rehearse create secret generic rehearse-gate --from-literal=token=$$(openssl rand -hex 24)
	$(KUBECTL) apply -k deploy/rehearse
	$(KUBECTL) -n rehearse set image deployment/gate gate=rehearse-gate:$(GATE_TAG)
	$(KUBECTL) -n rehearse rollout status deployment/gate --timeout=180s

app2: ## Phase 11: deploy the second app "shop" into namespace app2 (+ its load generator)
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o bin/shopdemo-linux ./cmd/shopdemo
	docker build -q -f Dockerfile.shop -t rehearse-shop:$(GATE_TAG) .
	kind load docker-image rehearse-shop:$(GATE_TAG) --name $(CLUSTER_NAME)
	$(KUBECTL) apply -f deploy/app2/app.yaml --dry-run=client -o name >/dev/null
	@$(KUBECTL) get namespace app2 >/dev/null 2>&1 || $(KUBECTL) create namespace app2
	@$(KUBECTL) -n app2 get secret shop-db >/dev/null 2>&1 || \
		$(KUBECTL) -n app2 create secret generic shop-db --from-literal=password=$$(openssl rand -hex 16)
	$(KUBECTL) apply -f deploy/app2/app.yaml
	$(KUBECTL) -n app2 set image deployment/catalog catalog=rehearse-shop:$(GATE_TAG)
	$(KUBECTL) -n app2 set image deployment/orders orders=rehearse-shop:$(GATE_TAG)
	$(KUBECTL) -n monitoring create configmap k6-shop --from-file=deploy/app2/k6-shop.js --dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f deploy/app2/loadgen.yaml
	$(KUBECTL) -n app2 wait --for=condition=Available deployment --all --timeout=300s

serve: build ## Agent service on 172.20.0.1:8090 (needs REHEARSE_DASHBOARD_TOKEN; SLACK_WEBHOOK_URL optional)
	./bin/rehearse serve

eval: build ## Run the evaluation harness (RUNS=3 LABEL=baseline ONLY=a,b ARGS=--always-ask)
	ml/.venv/bin/python ml/eval/run.py --runs $(or $(RUNS),3) --label $(or $(LABEL),baseline) \
		$(if $(ONLY),--only $(ONLY)) $(if $(ARGS),--extra-args="$(ARGS)")

## ---- Faults: every break-* has a fix-* that restores the vendored spec --------------------
# fix-* re-applies only the target's objects from deploy/hotel, so the spec comes from git.

break-image: ## Fault: geo gets an image tag that does not exist (ImagePullBackOff)
	$(KUBECTL) -n $(HOTEL_NS) set image deployment/geo hotel-reserv-geo=$(BAD_IMAGE)

fix-image: ## Restore geo's original image
	$(KUBECTL) apply -k deploy/hotel -l io.kompose.service=geo
	$(KUBECTL) -n $(HOTEL_NS) rollout status deployment/geo --timeout=180s

break-oom: ## Fault: mongodb-profile gets a 24Mi memory limit (OOMKilled, CrashLoopBackOff)
	$(KUBECTL) -n $(HOTEL_NS) set resources deployment/mongodb-profile -c hotel-reserv-profile-mongo \
		--requests=memory=$(OOM_LIMIT) --limits=memory=$(OOM_LIMIT)

fix-oom: ## Remove mongodb-profile's memory limit and restore its original request
	@# apply cannot delete a field it never set, so remove the limit explicitly first.
	-$(KUBECTL) -n $(HOTEL_NS) patch deployment/mongodb-profile --type=json \
		-p '[{"op":"remove","path":"/spec/template/spec/containers/0/resources/limits/memory"}]'
	$(KUBECTL) apply -k deploy/hotel -l io.kompose.service=mongodb-profile
	$(KUBECTL) -n $(HOTEL_NS) rollout status deployment/mongodb-profile --timeout=180s

break-scale: ## Fault: user scaled to 0 replicas (login and reservation fail)
	$(KUBECTL) -n $(HOTEL_NS) scale deployment/user --replicas=0

fix-scale: ## Scale user back to its original replica count
	$(KUBECTL) apply -k deploy/hotel -l io.kompose.service=user
	$(KUBECTL) -n $(HOTEL_NS) rollout status deployment/user --timeout=180s
