# Pheme — developer convenience targets.
# Run `make help` for the list.

.DEFAULT_GOAL := help
SHELL := /bin/bash

.PHONY: help setup dev stop infra-up infra-down infra-reset infra-status logs \
        build test test-cover lint web-build e2e e2e-install vapid

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

setup: ## One-time bootstrap (env, ports, VAPID keys)
	@./scripts/setup.sh

dev: ## Run the full dev stack (infra + services + web)
	@./scripts/dev.sh

stop: ## Stop dev services (add ARGS=--all to stop infra too)
	@./scripts/stop.sh $(ARGS)

infra-up: ## Start infrastructure containers
	@./scripts/infra.sh up

infra-down: ## Stop infrastructure containers (keep data)
	@./scripts/infra.sh down

infra-reset: ## Stop infrastructure and remove data volumes
	@./scripts/infra.sh reset

infra-status: ## Show infrastructure status
	@./scripts/infra.sh status

logs: ## Follow infrastructure container logs
	@./scripts/infra.sh logs

build: ## Build the Go binaries
	@cd api && go build ./...

test: ## Run Go unit tests with the race detector
	@cd api && go test -race ./...

test-cover: ## Run Go unit tests with a coverage summary
	@cd api && go test -race -cover ./...

lint: ## Vet Go and lint web
	@cd api && go vet ./...
	@cd web && npx eslint src --max-warnings 0

web-build: ## Production build of the web app
	@cd web && npm run build

e2e-install: ## Install Playwright browsers for the E2E suite
	@cd web && npm ci && npx playwright install --with-deps chromium

e2e: ## Run the Playwright E2E suite (builds API + web automatically)
	@cd web && npx playwright test

vapid: ## Print a fresh VAPID key pair
	@cd api && go run ./cmd/vapidgen
