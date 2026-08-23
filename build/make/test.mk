.PHONY: check build-scripts-check webapp-install webapp-check server-check integration independent-modules ci

check: build-scripts-check webapp-check server-check ## Run the hermetic product gate.

build-scripts-check:
	@for script in "$(ROOT_DIR)"/build/scripts/*; do sh -n "$$script"; done
	@"$(ROOT_DIR)/build/scripts/test-dev-secrets" "$(ROOT_DIR)/build/scripts/dev-secrets" "$(ROOT_DIR)/build/dev/metrics-openssl.cnf"

webapp-install:
	cd "$(WEBAPP_DIR)" && $(NPM) ci

webapp-check: webapp-install
	cd "$(WEBAPP_DIR)" && $(NPM) run check

server-check:
	$(MAKE) -C "$(SERVER_DIR)" check

integration: ## Run all dependency-backed server integration suites.
	$(MAKE) -C "$(SERVER_DIR)" integration-all integration-s3 integration-realtime

independent-modules: ## Verify every Go module outside workspace assistance.
	$(MAKE) -C "$(SERVER_DIR)" independent-modules

ci: check integration independent-modules package-verify ## Run the complete CI-facing product gate.
