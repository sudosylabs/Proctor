# Tracked product-build defaults. Operational values accept command-line,
# config.override.mk, and environment overrides. Repository Go/tool authority
# ignores ambient and file-based overrides; change it only on the Make command
# line so a contributor's shell cannot silently alter shared checks.

WEBAPP_DIR ?= $(ROOT_DIR)/webapp
DOCS_SITE_DIR ?= $(ROOT_DIR)/docs/site
SERVER_DIR ?= $(ROOT_DIR)/server
BUILD_DIR ?= $(ROOT_DIR)/.build
DEV_DIR ?= $(BUILD_DIR)/dev
DIST_DIR ?= $(ROOT_DIR)/dist
PACKAGE_DIR ?= $(DIST_DIR)/proctor

GO ?= go
NPM ?= npm
DOCKER ?= docker
COMPOSE ?= $(DOCKER) compose
CURL ?= curl
JQ ?= jq

# Development installs must include the locked build, test, and documentation
# toolchain even when a contributor's shell defaults npm to production mode.
NPM_CI_DEV = NODE_ENV=development npm_config_omit= npm_config_production= $(NPM) ci --include=dev

ifneq ($(origin GO_VERSION),command line)
override GO_VERSION := $(shell awk '$$1 == "go" { print $$2; exit }' "$(ROOT_DIR)/go.work")
endif
ifneq ($(origin GOTOOLCHAIN),command line)
override GOTOOLCHAIN := go$(GO_VERSION)
endif
export GOTOOLCHAIN
ifneq ($(origin GOWORK),command line)
override GOWORK := $(ROOT_DIR)/go.work
endif
export GOWORK
ifneq ($(origin GO_HOST_OS),command line)
override GO_HOST_OS := $(shell $(GO) env GOHOSTOS)
endif
ifneq ($(origin GO_HOST_ARCH),command line)
override GO_HOST_ARCH := $(shell $(GO) env GOHOSTARCH)
endif

ifneq ($(origin TOOL_MODULE_DIR),command line)
override TOOL_MODULE_DIR := $(ROOT_DIR)/build/tools
endif
ifneq ($(origin TOOL_BIN_DIR),command line)
override TOOL_BIN_DIR := $(ROOT_DIR)/.build/bin
endif
ifneq ($(origin GOLANGCI_LINT),command line)
override GOLANGCI_LINT := $(TOOL_BIN_DIR)/golangci-lint
endif
ifneq ($(origin GOIMPORTS),command line)
override GOIMPORTS := $(TOOL_BIN_DIR)/goimports
endif
ifneq ($(origin GOPLS),command line)
override GOPLS := $(TOOL_BIN_DIR)/gopls
endif
ifneq ($(origin GOVULNCHECK),command line)
override GOVULNCHECK := $(TOOL_BIN_DIR)/govulncheck
endif
ifneq ($(origin DLV),command line)
override DLV := $(TOOL_BIN_DIR)/dlv
endif
ifneq ($(origin PPROF),command line)
override PPROF := $(TOOL_BIN_DIR)/pprof
endif
ifneq ($(origin TRACE),command line)
override TRACE := $(TOOL_BIN_DIR)/trace
endif

VERSION ?= dev
COMMIT ?= $(shell git -C "$(ROOT_DIR)" rev-parse HEAD 2>/dev/null || printf unknown)
BUILD_TIME ?= $(shell git -C "$(ROOT_DIR)" show -s --format=%cI HEAD 2>/dev/null || printf unknown)
SOURCE_DATE_EPOCH ?= $(shell git -C "$(ROOT_DIR)" show -s --format=%ct HEAD 2>/dev/null || printf 0)

ifneq ($(origin GOFLAGS),command line)
override GOFLAGS := -buildvcs=false -mod=readonly
endif
export GOFLAGS
SERVER_LDFLAGS := -s -w -X github.com/sudosylabs/proctor/server/app.Version=$(VERSION) -X github.com/sudosylabs/proctor/server/app.Commit=$(COMMIT) -X github.com/sudosylabs/proctor/server/app.BuildTime=$(BUILD_TIME)

PROCTOR_COMPOSE_PROJECT_NAME ?= proctor-dev
PROCTOR_POSTGRES_PORT ?= 15432
PROCTOR_REDIS_PORT ?= 16379
PROCTOR_MINIO_PORT ?= 19000
PROCTOR_MINIO_CONSOLE_PORT ?= 19001
PROCTOR_MAILPIT_SMTP_PORT ?= 11025
PROCTOR_MAILPIT_HTTP_PORT ?= 18025
PROCTOR_PROMETHEUS_PORT ?= 19090
PROCTOR_GRAFANA_PORT ?= 13000
PROCTOR_LOKI_PORT ?= 13100
PROCTOR_OTEL_HEALTH_PORT ?= 13133
PROCTOR_SERVER_PORT ?= 8065
PROCTOR_SERVER_PUBLIC_URL ?= http://localhost:$(PROCTOR_SERVER_PORT)
PROCTOR_METRICS_PORT ?= 8067
PROCTOR_CLUSTER_HTTP_PORT ?= 18065
PROCTOR_DAP_PORT ?= 2345
PROCTOR_DLV_PORT ?= 2346

PROCTOR_UID ?= $(shell id -u 2>/dev/null || printf 1000)
PROCTOR_GID ?= $(shell id -g 2>/dev/null || printf 1000)

PROCTOR_POSTGRES_IMAGE ?= postgres:16.6-alpine3.20@sha256:1e59919c179e296eaf3cc701f4d50bab5c393d7ed9746c188c9d519489c998dc
PROCTOR_REDIS_IMAGE ?= redis:7.2.15-alpine3.21@sha256:05a97a479bc73de66f087dc05b569010772880f778cc8671fa6b8aadee32e5c6
PROCTOR_MINIO_IMAGE ?= minio/minio:RELEASE.2025-09-07T16-13-09Z@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e
PROCTOR_MAILPIT_IMAGE ?= axllent/mailpit:v1.30.5@sha256:b868afa176bfd6cce2323ea316cd99ccad77915e51e595748f6d786700ecf109
PROCTOR_PROMETHEUS_IMAGE ?= prom/prometheus:v3.5.0@sha256:63805ebb8d2b3920190daf1cb14a60871b16fd38bed42b857a3182bc621f4996
PROCTOR_GRAFANA_IMAGE ?= grafana/grafana:12.1.1@sha256:a1701c2180249361737a99a01bc770db39381640e4d631825d38ff4535efa47d
PROCTOR_LOKI_IMAGE ?= grafana/loki:3.5.3@sha256:3165cecce301ce5b9b6e3530284b080934a05cd5cafac3d3d82edcb887b45ecd
PROCTOR_OTEL_IMAGE ?= otel/opentelemetry-collector-contrib:0.145.0@sha256:a7343f01869071ea3f4c5e1e97df1bb1b3c4d5c77247db80e053a80b9df530c4
PROCTOR_HAPROXY_IMAGE ?= haproxy:3.2.7-alpine@sha256:3b80483d47e1c7d1fc7eb4b9104f33d9a51259769be299eb675524dca2bc8157

PROCTOR_GO_IMAGE ?= golang:1.25.13-bookworm@sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48
PROCTOR_BUILDENV_IMAGE ?= proctor-buildenv:go$(GO_VERSION)-node22.22.0
PROCTOR_RUNTIME_IMAGE ?= proctor:dev
PROCTOR_BUILDENV_RUN_FLAGS ?=

DEV_CONFIG_DIR := $(DEV_DIR)/config
DEV_SECRETS_DIR := $(DEV_DIR)/secrets
DEV_LOG_DIR := $(DEV_DIR)/logs
DEV_DIAGNOSTICS_DIR := $(DEV_DIR)/diagnostics
DEV_ENV_FILE := $(DEV_SECRETS_DIR)/environment
DEV_SERVER_ENV_FILE := $(DEV_CONFIG_DIR)/server-environment
DEV_METRICS_TOKEN_FILE := $(DEV_SECRETS_DIR)/metrics-token
DEV_METRICS_CERT_FILE := $(DEV_SECRETS_DIR)/metrics-certificate.pem
DEV_METRICS_KEY_FILE := $(DEV_SECRETS_DIR)/metrics-private-key.pem
DEV_SEED_DIR := $(DEV_DIR)/seed
DEV_SEED_CREDENTIALS_FILE := $(DEV_SEED_DIR)/credentials.json
DEV_SEED_FIXTURE_FILE := $(DEV_SEED_DIR)/fixture.json
DEV_SEED_IN_PROGRESS_FILE := $(DEV_SEED_DIR)/in-progress.json

DEBUG_MODULE ?= server
DEBUG_PACKAGE ?= ./app
DEBUG_TEST ?= .
DEBUG_PROFILE ?= cpu
TEST_DIAGNOSTICS := "$(ROOT_DIR)/build/scripts/test-diagnostics"
TEST_DIAGNOSTIC_CONTEXT = "$(ROOT_DIR)" "$(DEBUG_MODULE)" "$(DEBUG_PACKAGE)" "$(DEBUG_TEST)" "$(DEV_DIAGNOSTICS_DIR)"

DEPENDENCIES_COMPOSE := $(ROOT_DIR)/build/compose/dependencies.yaml
OBSERVABILITY_COMPOSE := $(ROOT_DIR)/build/compose/observability.yaml
CLUSTER_COMPOSE := $(ROOT_DIR)/build/compose/cluster.yaml

DEV_COMPOSE = PROCTOR_COMPOSE_PROJECT_NAME='$(PROCTOR_COMPOSE_PROJECT_NAME)' \
	PROCTOR_POSTGRES_PORT='$(PROCTOR_POSTGRES_PORT)' \
	PROCTOR_REDIS_PORT='$(PROCTOR_REDIS_PORT)' \
	PROCTOR_MINIO_PORT='$(PROCTOR_MINIO_PORT)' \
	PROCTOR_MINIO_CONSOLE_PORT='$(PROCTOR_MINIO_CONSOLE_PORT)' \
	PROCTOR_MAILPIT_SMTP_PORT='$(PROCTOR_MAILPIT_SMTP_PORT)' \
	PROCTOR_MAILPIT_HTTP_PORT='$(PROCTOR_MAILPIT_HTTP_PORT)' \
	PROCTOR_PROMETHEUS_PORT='$(PROCTOR_PROMETHEUS_PORT)' \
	PROCTOR_GRAFANA_PORT='$(PROCTOR_GRAFANA_PORT)' \
	PROCTOR_LOKI_PORT='$(PROCTOR_LOKI_PORT)' \
	PROCTOR_OTEL_HEALTH_PORT='$(PROCTOR_OTEL_HEALTH_PORT)' \
	PROCTOR_SERVER_PUBLIC_URL='$(PROCTOR_SERVER_PUBLIC_URL)' \
	PROCTOR_CLUSTER_HTTP_PORT='$(PROCTOR_CLUSTER_HTTP_PORT)' \
	PROCTOR_UID='$(PROCTOR_UID)' PROCTOR_GID='$(PROCTOR_GID)' \
	PROCTOR_POSTGRES_IMAGE='$(PROCTOR_POSTGRES_IMAGE)' \
	PROCTOR_REDIS_IMAGE='$(PROCTOR_REDIS_IMAGE)' \
	PROCTOR_MINIO_IMAGE='$(PROCTOR_MINIO_IMAGE)' \
	PROCTOR_MAILPIT_IMAGE='$(PROCTOR_MAILPIT_IMAGE)' \
	PROCTOR_PROMETHEUS_IMAGE='$(PROCTOR_PROMETHEUS_IMAGE)' \
	PROCTOR_GRAFANA_IMAGE='$(PROCTOR_GRAFANA_IMAGE)' \
	PROCTOR_LOKI_IMAGE='$(PROCTOR_LOKI_IMAGE)' \
	PROCTOR_OTEL_IMAGE='$(PROCTOR_OTEL_IMAGE)' \
	PROCTOR_HAPROXY_IMAGE='$(PROCTOR_HAPROXY_IMAGE)' \
	PROCTOR_RUNTIME_IMAGE='$(PROCTOR_RUNTIME_IMAGE)' \
	$(COMPOSE) -f '$(DEPENDENCIES_COMPOSE)' -f '$(OBSERVABILITY_COMPOSE)'

CLUSTER_DEV_COMPOSE = $(DEV_COMPOSE) -f '$(CLUSTER_COMPOSE)'

DEV_SERVER_ENV = "$(ROOT_DIR)/build/scripts/with-dev-server-env" "$(DEV_SERVER_ENV_FILE)"
