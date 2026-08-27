.PHONY: check build-scripts-check webapp-install webapp-check docs-install docs-start docs-check server-check integration independent-modules ci

check: build-scripts-check webapp-check docs-check server-check ## Run the hermetic product gate.

build-scripts-check:
	@for script in "$(ROOT_DIR)"/build/scripts/*; do sh -n "$$script"; done
	@"$(ROOT_DIR)/build/scripts/test-check-tools" "$(ROOT_DIR)/build/scripts/check-tools"
	@"$(ROOT_DIR)/build/scripts/test-dev-secrets" "$(ROOT_DIR)/build/scripts/dev-secrets" "$(ROOT_DIR)/build/dev/metrics-openssl.cnf"
	@"$(ROOT_DIR)/build/scripts/test-dev-seed" "$(ROOT_DIR)/build/scripts/dev-seed"

webapp-install:
	cd "$(WEBAPP_DIR)" && $(NPM) ci

webapp-check: webapp-install
	cd "$(WEBAPP_DIR)" && $(NPM) run check

docs-install:
	cd "$(DOCS_SITE_DIR)" && $(NPM) ci

docs-start: docs-install ## Preview the public documentation site.
	cd "$(DOCS_SITE_DIR)" && $(NPM) start

docs-check: docs-install ## Validate and build the public documentation site.
	$(MAKE) -C "$(SERVER_DIR)" openapi-check
	cd "$(DOCS_SITE_DIR)" && $(NPM) run check

server-check:
	$(MAKE) -C "$(SERVER_DIR)" check

integration: ## Run all dependency-backed server integration suites.
	$(MAKE) -C "$(SERVER_DIR)" integration-all integration-s3 integration-realtime

independent-modules: ## Verify every Go module outside workspace assistance.
	$(MAKE) -C "$(SERVER_DIR)" independent-modules

ci: check integration independent-modules package-verify ## Run the complete CI-facing product gate.
