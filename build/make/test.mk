.PHONY: check e2e brand-check brand-copy build-scripts-check webapp-install webapp-check docs-install docs-start docs-check mail-templates-install mail-templates-check server-check integration independent-modules module-pins-check ci

check: brand-check build-scripts-check webapp-check docs-check mail-templates-check server-check ## Run the hermetic product gate.

brand-check: ## Check package-local copies against approved brand artwork.
	@node "$(ROOT_DIR)/build/brand-assets.mjs" check

brand-copy: ## Copy approved artwork into its owning packages for review.
	@node "$(ROOT_DIR)/build/brand-assets.mjs" copy

build-scripts-check:
	@node --test "$(ROOT_DIR)"/build/ci/*.test.mjs
	@node --test "$(ROOT_DIR)/build/brand-assets.test.mjs"
	@for script in "$(ROOT_DIR)"/build/scripts/*; do sh -n "$$script"; done
	@"$(ROOT_DIR)/build/scripts/test-check-tools" "$(ROOT_DIR)/build/scripts/check-tools"
	@"$(ROOT_DIR)/build/scripts/test-check-go-tools" "$(ROOT_DIR)/build/scripts/check-go-tools"
	@"$(ROOT_DIR)/build/scripts/test-check-module-pins" "$(ROOT_DIR)/build/scripts/check-module-pins"
	@"$(ROOT_DIR)/build/scripts/test-go-quality" "$(ROOT_DIR)/build/scripts/go-quality"
	@"$(ROOT_DIR)/build/scripts/test-repository-toolchain" "$(ROOT_DIR)"
	@"$(ROOT_DIR)/build/scripts/test-dev-doctor" "$(ROOT_DIR)/build/scripts/dev-doctor"
	@"$(ROOT_DIR)/build/scripts/test-dev-secrets" "$(ROOT_DIR)/build/scripts/dev-secrets" "$(ROOT_DIR)/build/dev/metrics-openssl.cnf"
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

mail-templates-install:
	cd "$(SERVER_DIR)/templates" && $(NPM_CI_DEV)

mail-templates-check: mail-templates-install ## Verify generated transactional-mail HTML is fresh and renders.
	cd "$(SERVER_DIR)/templates" && $(NPM) run check && $(NPM) test

server-check:
	$(MAKE) -C "$(SERVER_DIR)" check

integration: ## Run dependency-backed server and reusable-module conformance suites.
	$(MAKE) -C "$(SERVER_DIR)" integration-all integration-s3 integration-realtime
	$(MAKE) -C "$(ROOT_DIR)/packages/cache" conformance-redis
	$(MAKE) -C "$(ROOT_DIR)/packages/mail" conformance-smtp

e2e: dev-tools webapp-build ## Run the browser end-to-end suite against one real server node.
	@mkdir -p "$(E2E_DIR)/bin"
	$(GO) build $(GOFLAGS) -trimpath -ldflags '$(SERVER_LDFLAGS)' -o "$(E2E_DIR)/bin/proctor" ./server/cmd/proctor
	@DOCKER_COMMAND="$(DOCKER)" CURL_COMMAND="$(CURL)" JQ_COMMAND="$(JQ)" NPM_COMMAND="$(NPM)" \
		PROCTOR_E2E_SERVER_PORT='$(PROCTOR_E2E_SERVER_PORT)' PROCTOR_E2E_POSTGRES_PORT='$(PROCTOR_E2E_POSTGRES_PORT)' \
		PROCTOR_E2E_MAILPIT_SMTP_PORT='$(PROCTOR_E2E_MAILPIT_SMTP_PORT)' PROCTOR_E2E_MAILPIT_HTTP_PORT='$(PROCTOR_E2E_MAILPIT_HTTP_PORT)' \
		PROCTOR_POSTGRES_IMAGE='$(PROCTOR_POSTGRES_IMAGE)' MAILPIT_IMAGE='$(PROCTOR_MAILPIT_IMAGE)' \
		"$(ROOT_DIR)/build/scripts/e2e" "$(E2E_DIR)/bin/proctor" "$(SERVER_DIR)/config/config.example.json" \
		"$(SERVER_DIR)/compose.yaml" "$(WEBAPP_DIR)" "$(E2E_DIR)/run" '$(PROCTOR_E2E_COMPOSE_PROJECT_NAME)' $(E2E_ARGS)

independent-modules: ## Verify every Go module outside workspace assistance.
	$(MAKE) -C "$(SERVER_DIR)" independent-modules

module-pins-check: ## Verify the server pins each packages/* module at its current content.
	@"$(ROOT_DIR)/build/scripts/check-module-pins" "$(ROOT_DIR)"

ci: check integration independent-modules module-pins-check package-verify ## Run the complete CI-facing product gate.
