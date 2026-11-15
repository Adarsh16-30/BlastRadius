COMPOSE := docker compose -f deploy/compose.yaml --env-file $(or $(wildcard .env),.env.example)
COMPOSE_PROJECT := blastradius
GOLANGCI_LINT ?= $(or $(shell command -v golangci-lint),$(shell go env GOPATH)/bin/golangci-lint)

.PHONY: up down nuke test chaos backup restore-drill stats public smoke baseline itest pgstatus

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

# Role and replica lag of both Postgres nodes through a SQL probe; fails unless exactly one primary.
pgstatus:
	@COMPOSE_PROJECT=$(COMPOSE_PROJECT) bash scripts/pgstatus.sh

# Integration tests against the running primary (needs `make up`). Uses a separate blastradius_test
# database and the container's bridge IP, so no extra port is published (PRD 9).
itest:
	set -a; . ./.env; set +a; \
	pg=$(COMPOSE_PROJECT)-pg1-1; \
	[ "$$(docker exec $$pg psql -XAtU postgres -c 'SELECT pg_is_in_recovery()')" = f ] || pg=$(COMPOSE_PROJECT)-pg2-1; \
	ip=$$(docker inspect $$pg | jq -r '.[0].NetworkSettings.Networks[].IPAddress'); \
	docker exec $$pg psql -U postgres -tc "SELECT 1 FROM pg_database WHERE datname='blastradius_test'" | grep -q 1 || \
	  docker exec $$pg psql -U postgres -c 'CREATE DATABASE blastradius_test'; \
	TEST_DATABASE_URL="postgres://postgres:$$POSTGRES_PASSWORD@$$ip:5432/blastradius_test" go test -race -cover -count=1 ./...

smoke:
	bash scripts/smoke.sh

# PRD 8.4 baseline: 30 RPS for 5 minutes, p99 < 100 ms, zero failed requests.
baseline:
	$(COMPOSE) --profile chaos run --rm -e RPS=30 -e DURATION=5m k6 run /load/steady.js

public:
	$(COMPOSE) --profile public up -d --wait cloudflared
