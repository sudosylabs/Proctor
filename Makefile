SHELL := /bin/sh

WEBAPP_DIR ?= webapp
SERVER_DIR ?= server
PACKAGE_DIR ?= dist/proctor
VERSION ?= dev
COMMIT ?= unknown
BUILD_TIME ?= unknown

SERVER_LDFLAGS := -X github.com/sudosylabs/proctor/server/app.Version=$(VERSION) -X github.com/sudosylabs/proctor/server/app.Commit=$(COMMIT) -X github.com/sudosylabs/proctor/server/app.BuildTime=$(BUILD_TIME)

.PHONY: check webapp-install webapp-check webapp-build server-check package

check: webapp-check server-check

webapp-install:
	cd "$(WEBAPP_DIR)" && npm ci

webapp-check: webapp-install
	cd "$(WEBAPP_DIR)" && npm run check

webapp-build: webapp-install
	cd "$(WEBAPP_DIR)" && npm run verify
	cd "$(WEBAPP_DIR)" && PROCTOR_BUILD_VERSION='$(VERSION)' PROCTOR_BUILD_COMMIT='$(COMMIT)' npm run build

server-check:
	$(MAKE) -C "$(SERVER_DIR)" check

package: webapp-build
	$(MAKE) -C "$(SERVER_DIR)" package \
		PACKAGE_DIR="$(abspath $(PACKAGE_DIR))" \
		WEBAPP_DIST_DIR="$(abspath $(WEBAPP_DIR)/dist)" \
		GO_LDFLAGS='$(SERVER_LDFLAGS)'
