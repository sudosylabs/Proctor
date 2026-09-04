SHELL := /bin/sh
GO_TEST ?= go test

COMPOSE := docker compose -f compose.yaml
MAIL_SMTP_PORT ?= 11025
MAIL_HTTP_PORT ?= 18025
MAIL_SMTP_ADDRESS ?= 127.0.0.1:$(MAIL_SMTP_PORT)

export MAIL_SMTP_PORT
export MAIL_HTTP_PORT

.PHONY: check test test-race vet smtp-up smtp-down smtp-logs conformance-smtp

check: test test-race vet

test:
	$(GO_TEST) ./...

test-race:
	$(GO_TEST) -race ./...

vet:
	go vet ./...

smtp-up:
	$(COMPOSE) up --detach --wait mailpit

smtp-down:
	$(COMPOSE) down --remove-orphans

smtp-logs:
	$(COMPOSE) logs mailpit

conformance-smtp:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait mailpit; \
	MAIL_SMTP_ADDRESS=$(MAIL_SMTP_ADDRESS) \
		$(GO_TEST) ./smtp -run '^TestIntegrationConformance$$' -count=1
