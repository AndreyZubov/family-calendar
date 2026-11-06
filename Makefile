# Family Calendar — developer entrypoint.
# Run `make` or `make help` to see available targets.
#
# Migrations are managed by backend/cmd/migrate (goose library + pgx only).
# sqlc runs via `go run` at a pinned version (see SQLC_VERSION).

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

BACKEND_DIR  := backend
FRONTEND_DIR := frontend
DEPLOY_DIR   := deploy
BIN_DIR      := bin

DEV_COMPOSE  := $(DEPLOY_DIR)/docker-compose.dev.yml
MIGRATIONS   := migrations

# Load local env (DATABASE_URL etc.) if present; never committed.
-include .env
export

# Matches the defaults in deploy/docker-compose.dev.yml, so `make db-up
# migrate-up` works even without a .env file. Set only from .env or the make
# command line (`make migrate-up DATABASE_URL=...`): a DATABASE_URL left in the
# shell by another project must not point migrations at the wrong database.
ifneq ($(origin DATABASE_URL),file)
DATABASE_URL := postgres://calendar:calendar@localhost:5433/calendar?sslmode=disable
endif

MIGRATE := go run ./cmd/migrate

# sqlc is run at a pinned version instead of being a `tool` in go.mod: its
# dependency tree (SQL parsers for several databases) would bloat go.mod.
SQLC_VERSION := v1.31.1
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

# Same pinned version as in CI (.github/workflows/ci.yml).
GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
ACTIONLINT := go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

IMAGE   ?= family-calendar
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: env
env: ## Create .env from .env.example with a generated SESSION_SECRET
	@test ! -f .env || (echo ".env already exists; not overwriting" && exit 1)
	@sed "s|^SESSION_SECRET=$$|SESSION_SECRET=$$(openssl rand -base64 32)|" .env.example > .env
	@echo "Created .env (dev sign-in provider, fresh SESSION_SECRET)"

# With the local dev provider configured (the .env.example default), it is
# started first and the backend waits for it: the backend needs the
# provider's discovery document at startup.
DEV_OIDC_URL := http://localhost:9090

.PHONY: dev
dev: db-up ## Start Postgres, (dev OIDC,) backend and frontend dev server
	@test -f .env || (echo "No .env: run 'make env' first" && exit 1)
	@trap 'kill 0' EXIT; \
		if [ "$$OIDC_ISSUER_URL" = "$(DEV_OIDC_URL)" ]; then \
			$(MAKE) --no-print-directory dev-oidc & \
			until curl -sf $(DEV_OIDC_URL)/.well-known/openid-configuration >/dev/null; do sleep 0.3; done; \
		fi; \
		$(MAKE) --no-print-directory run-backend & \
		$(MAKE) --no-print-directory run-frontend & \
		wait

.PHONY: run-backend
run-backend: ## Run the Go API server
	cd $(BACKEND_DIR) && go run ./cmd/server

.PHONY: frontend-install
frontend-install: ## Install frontend dependencies exactly as locked
	cd $(FRONTEND_DIR) && npm ci

.PHONY: run-frontend
run-frontend: ## Run the Vite dev server (proxies /api and /auth to :8080)
	cd $(FRONTEND_DIR) && npm run dev

.PHONY: db-up
db-up: ## Start local Postgres
	docker compose -f $(DEV_COMPOSE) up -d --wait

.PHONY: db-down
db-down: ## Stop local Postgres (data volume is kept)
	docker compose -f $(DEV_COMPOSE) down

.PHONY: generate
generate: generate-backend generate-frontend ## Run code generators

.PHONY: generate-backend
generate-backend: ## Generate Go code from SQL queries (sqlc)
	cd $(BACKEND_DIR) && $(SQLC) generate

.PHONY: generate-frontend
generate-frontend: ## Generate TypeScript API types from backend/api/openapi.yaml
	cd $(FRONTEND_DIR) && npm run generate:api

.PHONY: sqlc-check
sqlc-check: ## Fail if generated sqlc code is out of date (for CI)
	cd $(BACKEND_DIR) && $(SQLC) diff

##@ Database

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations
	cd $(BACKEND_DIR) && $(MIGRATE) up

.PHONY: migrate-down
migrate-down: ## Roll back the last migration
	cd $(BACKEND_DIR) && $(MIGRATE) down

.PHONY: migrate-status
migrate-status: ## Show migration status
	cd $(BACKEND_DIR) && $(MIGRATE) status

.PHONY: migrate-create
migrate-create: ## Create a new SQL migration: make migrate-create name=add_events
	@test -n "$(name)" || (echo "usage: make migrate-create name=<name>" && exit 1)
	cd $(BACKEND_DIR) && $(MIGRATE) -dir $(MIGRATIONS) create $(name)

##@ Quality

.PHONY: lint
lint: lint-backend lint-frontend lint-workflows ## Run all linters

.PHONY: lint-backend
lint-backend:
	cd $(BACKEND_DIR) && $(GOLANGCI_LINT) run ./...

.PHONY: lint-workflows
lint-workflows: ## Check GitHub Actions workflows (actionlint)
	$(ACTIONLINT)

.PHONY: lint-frontend
lint-frontend:
	cd $(FRONTEND_DIR) && npm run lint && npm run typecheck && npm run format:check

.PHONY: test
test: test-backend test-frontend ## Run all tests

# DB integration tests get a throwaway Postgres from testcontainers (needs
# Docker). Each test runs in its own database cloned from a migrated template.
.PHONY: test-backend
test-backend: ## Run backend tests (DB tests start Postgres in Docker)
	cd $(BACKEND_DIR) && go test -race -count=1 ./...

.PHONY: test-short
test-short: ## Run backend unit tests only (no Docker, no database)
	cd $(BACKEND_DIR) && go test -short -race -count=1 ./...

.PHONY: test-db
test-db: db-up ## Run backend tests against the local dev Postgres (faster, no containers)
	cd $(BACKEND_DIR) && TEST_DATABASE_URL="$(DATABASE_URL)" go test -race -count=1 ./...

.PHONY: cover
cover: ## Backend test coverage report (opens coverage.html)
	cd $(BACKEND_DIR) && go test -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./... \
		&& go tool cover -func=coverage.out | tail -1 \
		&& go tool cover -html=coverage.out -o coverage.html

.PHONY: test-frontend
test-frontend:
	cd $(FRONTEND_DIR) && npm test -- --run

# End-to-end tests in a real browser (Playwright, Chromium) against the full
# stack on separate ports, with the dev OIDC provider instead of Google and a
# fresh database. First run: `cd frontend && npx playwright install chromium`.
E2E_DB := calendar_e2e
E2E_DATABASE_URL := postgres://calendar:calendar@localhost:5433/$(E2E_DB)?sslmode=disable

.PHONY: e2e
e2e: db-up build-backend build-devoidc ## Run end-to-end browser tests (Playwright)
	docker compose -f $(DEV_COMPOSE) exec -T postgres psql -U calendar -d postgres -q \
		-c "DROP DATABASE IF EXISTS $(E2E_DB) WITH (FORCE)" -c "CREATE DATABASE $(E2E_DB)"
	cd $(FRONTEND_DIR) && E2E_DATABASE_URL="$(E2E_DATABASE_URL)" npx --no-install playwright test

# Same scenarios against the production build served by the Go binary
# (no Vite): checks the embedded app, CSP and caching as in the Docker image.
.PHONY: e2e-binary
e2e-binary: db-up build build-devoidc ## Run end-to-end tests against the binary with the embedded frontend
	docker compose -f $(DEV_COMPOSE) exec -T postgres psql -U calendar -d postgres -q \
		-c "DROP DATABASE IF EXISTS $(E2E_DB) WITH (FORCE)" -c "CREATE DATABASE $(E2E_DB)"
	cd $(FRONTEND_DIR) && E2E_TARGET=binary E2E_DATABASE_URL="$(E2E_DATABASE_URL)" npx --no-install playwright test

GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@v1.9.0

.PHONY: vuln
vuln: ## Check Go dependencies for known vulnerabilities
	cd $(BACKEND_DIR) && $(GOVULNCHECK) ./...

##@ Build

.PHONY: build
build: build-frontend embed-frontend build-backend ## Build the frontend and a server binary that embeds it

.PHONY: build-frontend
build-frontend:
	cd $(FRONTEND_DIR) && npm ci && npm run build

# Copy the frontend build where go:embed picks it up (internal/web/dist).
.PHONY: embed-frontend
embed-frontend:
	find $(BACKEND_DIR)/internal/web/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R $(FRONTEND_DIR)/dist/. $(BACKEND_DIR)/internal/web/dist/

.PHONY: build-backend
build-backend:
	mkdir -p $(BIN_DIR)
	cd $(BACKEND_DIR) && CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.version=$(VERSION)" \
		-o ../$(BIN_DIR)/server ./cmd/server

.PHONY: build-devoidc
build-devoidc:
	mkdir -p $(BIN_DIR)
	cd $(BACKEND_DIR) && go build -o ../$(BIN_DIR)/devoidc ./cmd/devoidc

.PHONY: dev-oidc
dev-oidc: ## Run the local OIDC provider (sign in without Google; see README)
	cd $(BACKEND_DIR) && go run ./cmd/devoidc

.PHONY: docker-build
docker-build: ## Build the production Docker image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):dev .

.PHONY: compose-test
compose-test: docker-build ## Smoke-test the production Compose stack locally (Caddy HTTPS + app + Postgres)
	scripts/compose-smoke.sh $(VERSION)

.PHONY: docker-test
docker-test: docker-build ## Build the image and smoke-test it (locked down, next to a throwaway Postgres)
	scripts/docker-smoke.sh $(IMAGE):$(VERSION)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) $(FRONTEND_DIR)/dist
	find $(BACKEND_DIR)/internal/web/dist -mindepth 1 ! -name .gitkeep -delete
