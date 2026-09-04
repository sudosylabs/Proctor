.PHONY: check build-scripts-check webapp-install webapp-check docs-install docs-start docs-check server-check integration independent-modules ci

check: build-scripts-check webapp-check docs-check server-check ## Run the hermetic product gate.

build-scripts-check:
	@node --test "$(ROOT_DIR)"/build/ci/*.test.mjs
	@for script in "$(ROOT_DIR)"/build/scripts/*; do sh -n "$$script"; done
	@"$(ROOT_DIR)/build/scripts/test-check-tools" "$(ROOT_DIR)/build/scripts/check-tools"
	@"$(ROOT_DIR)/build/scripts/test-check-go-tools" "$(ROOT_DIR)/build/scripts/check-go-tools"
	@"$(ROOT_DIR)/build/scripts/test-go-quality" "$(ROOT_DIR)/build/scripts/go-quality"
	@"$(ROOT_DIR)/build/scripts/test-repository-toolchain" "$(ROOT_DIR)"
	@"$(ROOT_DIR)/build/scripts/test-dev-doctor" "$(ROOT_DIR)/build/scripts/dev-doctor"
	@"$(ROOT_DIR)/build/scripts/test-dev-secrets" "$(ROOT_DIR)/build/scripts/dev-secrets" "$(ROOT_DIR)/build/dev/metrics-openssl.cnf"
	@"$(ROOT_DIR)/build/scripts/test-dev-seed" "$(ROOT_DIR)/build/scripts/dev-seed"
	@"$(ROOT_DIR)/build/scripts/test-dev-server-env" "$(ROOT_DIR)/build/scripts/dev-server-env"
	@"$(ROOT_DIR)/build/scripts/test-with-dev-server-env" "$(ROOT_DIR)/build/scripts/with-dev-server-env"
	@"$(ROOT_DIR)/build/scripts/test-test-diagnostics" "$(ROOT_DIR)/build/scripts/test-diagnostics"

webapp-install:
	cd "$(WEBAPP_DIR)" && $(NPM_CI_DEV)

webapp-check: webapp-install
	cd "$(WEBAPP_DIR)" && $(NPM) run check

docs-install:
	cd "$(DOCS_SITE_DIR)" && $(NPM_CI_DEV)

docs-start: docs-install ## Preview the public documentation site.
	cd "$(DOCS_SITE_DIR)" && $(NPM) start

docs-check: docs-install ## Validate and build the public documentation site.
	$(MAKE) -C "$(SERVER_DIR)" openapi-check
	cd "$(DOCS_SITE_DIR)" && $(NPM) run check

server-check:
	$(MAKE) -C "$(SERVER_DIR)" check

integration: ## Run dependency-backed server and reusable-module conformance suites.
	$(MAKE) -C "$(SERVER_DIR)" integration-all integration-s3 integration-realtime
	$(MAKE) -C "$(ROOT_DIR)/packages/cache" conformance-redis
	$(MAKE) -C "$(ROOT_DIR)/packages/mail" conformance-smtp

independent-modules: ## Verify every Go module outside workspace assistance.
	$(MAKE) -C "$(SERVER_DIR)" independent-modules

ci: check integration independent-modules package-verify ## Run the complete CI-facing product gate.
