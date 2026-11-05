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

IMAGE   ?= family-calendar
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: dev
dev: db-up ## Start Postgres, backend and frontend dev server
	@trap 'kill 0' EXIT; \
		$(MAKE) --no-print-directory run-backend & \
		$(MAKE) --no-print-directory run-frontend & \
		wait

.PHONY: run-backend
run-backend: ## Run the Go API server
	cd $(BACKEND_DIR) && go run ./cmd/server

.PHONY: run-frontend
run-frontend: ## Run the Vite dev server
	cd $(FRONTEND_DIR) && npm run dev

.PHONY: db-up
db-up: ## Start local Postgres
	docker compose -f $(DEV_COMPOSE) up -d --wait

.PHONY: db-down
db-down: ## Stop local Postgres (data volume is kept)
	docker compose -f $(DEV_COMPOSE) down

.PHONY: generate
generate: generate-backend ## Run code generators

.PHONY: generate-backend
generate-backend: ## Generate Go code from SQL queries (sqlc)
	cd $(BACKEND_DIR) && $(SQLC) generate

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
lint: lint-backend lint-frontend ## Run all linters

.PHONY: lint-backend
lint-backend:
	cd $(BACKEND_DIR) && golangci-lint run ./...

.PHONY: lint-frontend
lint-frontend:
	cd $(FRONTEND_DIR) && npm run lint && npm run typecheck

.PHONY: test
test: test-backend test-frontend ## Run all tests

.PHONY: test-backend
test-backend:
	cd $(BACKEND_DIR) && go test -race -count=1 ./...

.PHONY: test-db
test-db: db-up ## Run backend tests including DB integration tests (local Postgres)
	cd $(BACKEND_DIR) && TEST_DATABASE_URL="$(DATABASE_URL)" go test -race -count=1 ./...

.PHONY: test-frontend
test-frontend:
	cd $(FRONTEND_DIR) && npm test -- --run

.PHONY: vuln
vuln: ## Check Go dependencies for known vulnerabilities
	cd $(BACKEND_DIR) && go run golang.org/x/vuln/cmd/govulncheck@latest ./...

##@ Build

.PHONY: build
build: build-frontend build-backend ## Build frontend and backend binary

.PHONY: build-frontend
build-frontend:
	cd $(FRONTEND_DIR) && npm ci && npm run build

.PHONY: build-backend
build-backend:
	mkdir -p $(BIN_DIR)
	cd $(BACKEND_DIR) && CGO_ENABLED=0 go build -trimpath \
		-ldflags="-s -w -X main.version=$(VERSION)" \
		-o ../$(BIN_DIR)/server ./cmd/server

.PHONY: docker-build
docker-build: ## Build the production Docker image
	docker build -t $(IMAGE):$(VERSION) .

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) $(FRONTEND_DIR)/dist
