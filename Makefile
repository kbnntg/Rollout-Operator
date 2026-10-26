IMG ?= ghcr.io/kbnntg/rollout-operator:latest
CONSOLE_IMG ?= ghcr.io/kbnntg/rollout-console:latest

.PHONY: build
build:
	go build -o bin/manager ./cmd/manager
	go build -o bin/controlplane ./cmd/controlplane

.PHONY: test
test:
	go test ./... -count=1

.PHONY: run-manager
run-manager:
	go run ./cmd/manager --leader-elect=false --enable-webhooks=false \
		--metrics-bind-address=:19080 --health-probe-bind-address=:19081

.PHONY: run-console
run-console:
	go run ./cmd/controlplane --addr=:8090 --namespace=rollout-system \
		--users=admin:admin123:admin,viewer:viewer123:viewer \
		--jwt-secret=dev-local-secret-0123456789abcdef

.PHONY: run-web
run-web:
	cd web && npm run dev
