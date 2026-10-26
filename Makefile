SHELL := /bin/bash

FRONTEND_DIR := ./apps/web
GO_TEST_PACKAGES := ./apps/... ./internal/... ./packages/...
APP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  make stack-up      # start docker stack"
	@echo "  make stack-build   # start docker stack and rebuild images"
	@echo "  make stack-down    # stop docker stack"
	@echo "  make stack-logs    # follow docker stack logs"
	@echo "  make stack-ps      # list docker stack services"
	@echo "  make stack-clean   # stop stack, remove volumes, and purge local data"
	@echo "  make web-build     # build frontend assets"
	@echo "  make sync-docs-openapi # sync contracts OpenAPI into apps/docs public assets"
	@echo "  make build-api     # build backend binary to ./.runtime/bin/lazu-api"
	@echo "  make build-worker  # build worker binary to ./.runtime/bin/lazu-worker"
	@echo "  make build-migrate # build schema migration binary to ./.runtime/bin/lazu-migrate"
	@echo "  make clean-bin     # remove local binaries under ./.runtime/bin"
	@echo "  make test-go       # run go tests"
	@echo "  make test-go-json  # run go tests with JSON stream output"
	@echo "  make test-docs     # verify active docs use current path conventions"
	@echo "  make test-secrets  # scan tracked files for committed secrets"
	@echo "  make test-relay-safety # verify relay adaptor panic guards"
	@echo "  make test-deadcode # fail on Go functions nothing calls"

.PHONY: stack-up stack-build stack-down stack-logs stack-ps stack-clean
stack-up:
	@./ops/scripts/stack.sh up

stack-build:
	@./ops/scripts/stack.sh up --build

stack-down:
	@./ops/scripts/stack.sh down

stack-logs:
	@./ops/scripts/stack.sh logs

stack-ps:
	@./ops/scripts/stack.sh ps

stack-clean:
	@./ops/scripts/stack.sh clean --yes

.PHONY: web-build sync-docs-openapi
web-build:
	@cd $(FRONTEND_DIR) && bun install && DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION='$(APP_VERSION)' bun run build

sync-docs-openapi:
	@cd apps/docs && npm run sync:openapi

.PHONY: build-api build-worker build-migrate clean-bin
build-api:
	@mkdir -p ./.runtime/bin
	@go build -ldflags "-s -w -X github.com/lazu-ai/lazu/internal/common.Version=$(APP_VERSION)" -o ./.runtime/bin/lazu-api ./apps/api

build-worker:
	@mkdir -p ./.runtime/bin
	@go build -ldflags "-s -w -X github.com/lazu-ai/lazu/internal/common.Version=$(APP_VERSION)" -o ./.runtime/bin/lazu-worker ./apps/worker

build-migrate:
	@mkdir -p ./.runtime/bin
	@go build -ldflags "-s -w -X github.com/lazu-ai/lazu/internal/common.Version=$(APP_VERSION)" -o ./.runtime/bin/lazu-migrate ./apps/migrate

clean-bin:
	@rm -rf ./.runtime/bin

.PHONY: test-go test-go-json test-docs test-secrets test-relay-safety test-deadcode
test-go:
	@go test $(GO_TEST_PACKAGES)

test-go-json:
	@go test -json $(GO_TEST_PACKAGES)

test-docs:
	@./ops/scripts/verify-doc-paths.sh

test-secrets:
	@python3 -m unittest discover -s ops/scripts -p test_secret_scan.py -q
	@./ops/scripts/secret_scan.py

test-relay-safety:
	@./ops/scripts/verify-relay-panics.sh

# deadcode only sees the current platform and default build tags; the
# *_windows.go variants are never analysed. Bumping the version can move
# findings: regenerate ops/deadcode-baseline.txt in the same change.
DEADCODE_VERSION ?= v0.52.0
test-deadcode:
	@python3 -m unittest discover -s ops/scripts -p test_check_deadcode.py -q
	@set -o pipefail; go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION) -test \
		-f '{{range .Funcs}}{{$$.Path}}.{{.Name}}{{"\n"}}{{end}}' ./... \
		| sed -e '\#/node_modules/#d' -e 's|^github.com/lazu-ai/lazu/||' | LC_ALL=C sort -u \
		| python3 ops/scripts/check_deadcode.py ops/extension-surface.txt ops/deadcode-baseline.txt
