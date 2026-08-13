SHELL := /bin/sh

COMPOSE := docker compose -f compose.yaml
CACHE_REDIS_PORT ?= 16379
CACHE_REDIS_ADDRESS ?= 127.0.0.1:$(CACHE_REDIS_PORT)

export CACHE_REDIS_PORT

.PHONY: check test test-race vet redis-up redis-down redis-logs conformance-redis

check: test test-race vet

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

redis-up:
	$(COMPOSE) up --detach --wait redis

redis-down:
	$(COMPOSE) down --remove-orphans

redis-logs:
	$(COMPOSE) logs redis

conformance-redis:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait redis; \
	CACHE_REDIS_ADDRESS=$(CACHE_REDIS_ADDRESS) \
		go test ./redis -count=1
