# Leme development shortcuts.
# Tests run ONLY against the isolated kind cluster (see DEV_KUBECONFIG); never against your own clusters.
SHELL := /bin/bash
GO ?= go
DEV_KUBECONFIG ?= $(HOME)/.kube/leme-dev/kubeconfig

.PHONY: deps engine ui licenses run run-dev serve-dev test test-go test-e2e perf scale-lab dist dev-cluster kind-up kind-down sweep

deps:
	cd app && npm ci

engine:
	cd engine && $(GO) build -trimpath -ldflags "-s -w" -o bin/leme ./cmd/leme

ui:
	cd app && npx vite build

## Desktop app with YOUR kubeconfig (~/.kube/config). Remote clusters open read-only.
run: engine ui
	cd app && npm start

## Desktop app with the kind test cluster only.
run-dev: engine ui
	cd app && KUBECONFIG=$(DEV_KUBECONFIG) npm start

## Engine + UI in the browser (kind test cluster). Prints the link with the session token.
serve-dev: engine ui
	KUBECONFIG=$(DEV_KUBECONFIG) ./engine/bin/leme serve --addr 127.0.0.1:47800 --static app/renderer-dist

test: test-go test-e2e

test-go:
	cd engine && $(GO) vet ./... && $(GO) test ./...

test-e2e: engine ui
	cd app && npx tsc --noEmit -p . && npx playwright test

## Measures the grid with every pod of the kind cluster (run scale-lab first).
perf: engine ui
	cd app && PERF=1 npx playwright test e2e/perf

## Simulates N pods (default 5000) in the kind test cluster with kwok.
scale-lab:
	bash scripts/kwok-load.sh $(or $(N),5000)

## License texts of the Go modules linked into the engine (shipped in the packages).
licenses: engine
	GO=$(GO) bash scripts/go-licenses.sh engine/bin/leme engine/bin/THIRD_PARTY_LICENSES.txt

dist: licenses ui
	cd app && npx electron-builder --linux AppImage deb

## Creates the kind test cluster and the fixtures used by the e2e tests.
dev-cluster:
	bash scripts/dev-cluster.sh

## Screenshots of every screen + overflow/clipping report (kind + fake contexts). OUT=<dir>
sweep: engine
	cd app && npx vite build --outDir $(abspath $(or $(OUT),/tmp/leme-sweep))/dist --emptyOutDir && \
	  KUBECONFIG_SWEEP=$(DEV_KUBECONFIG):e2e/fixtures/sweep-fakes.kubeconfig node scripts/ui-sweep.mjs $(abspath $(or $(OUT),/tmp/leme-sweep)) 1500 900 dark && \
	  KUBECONFIG_SWEEP=$(DEV_KUBECONFIG):e2e/fixtures/sweep-fakes.kubeconfig node scripts/ui-sweep.mjs $(abspath $(or $(OUT),/tmp/leme-sweep)) 1280 760 light

kind-up:
	kind create cluster --name leme-dev --kubeconfig $(DEV_KUBECONFIG)

kind-down:
	kind delete cluster --name leme-dev --kubeconfig $(DEV_KUBECONFIG)
