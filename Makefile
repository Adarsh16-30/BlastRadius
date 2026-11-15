COMPOSE := docker compose -f deploy/compose.yaml --env-file $(or $(wildcard .env),.env.example)
GOLANGCI_LINT ?= $(or $(shell command -v golangci-lint),$(shell go env GOPATH)/bin/golangci-lint)

.PHONY: up down nuke test chaos backup restore-drill stats public

up:
	$(COMPOSE) up -d --wait

down:
	$(COMPOSE) down

nuke:
	$(COMPOSE) --profile chaos --profile public down -v

test:
	go vet ./...
	$(GOLANGCI_LINT) run ./...
	go test -race ./...

chaos:
	@echo "make chaos: not implemented until M4"; exit 1

backup:
	@echo "make backup: not implemented until M5"; exit 1

restore-drill:
	@echo "make restore-drill: not implemented until M5"; exit 1

stats:
	docker stats --no-stream

public:
	$(COMPOSE) --profile public up -d --wait cloudflared
