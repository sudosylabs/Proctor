.PHONY: bootstrap tools tools-check fmt fmt-check lint-config lint lint-full lint-fix vulncheck quality security precommit

GO_QUALITY = GOLANGCI_LINT="$(GOLANGCI_LINT)" GOIMPORTS="$(GOIMPORTS)" \
	GOVULNCHECK="$(GOVULNCHECK)" GO_COMMAND="$(GO)" \
	"$(ROOT_DIR)/build/scripts/go-quality"

bootstrap: dev-tools tools webapp-install docs-install ## Validate the host and install repository-pinned development dependencies.
	@mkdir -p "$(DEV_DIAGNOSTICS_DIR)"

tools: ## Install the repository-pinned Go developer tools into .build/bin.
	@mkdir -p "$(TOOL_BIN_DIR)"
	@cd "$(TOOL_MODULE_DIR)" && GOWORK=off GOFLAGS="$(GOFLAGS)" GOOS="$(GO_HOST_OS)" GOARCH="$(GO_HOST_ARCH)" GOBIN="$(TOOL_BIN_DIR)" $(GO) install tool
	@cd "$(TOOL_MODULE_DIR)/gopls" && GOWORK=off GOFLAGS="$(GOFLAGS)" GOOS="$(GO_HOST_OS)" GOARCH="$(GO_HOST_ARCH)" GOBIN="$(TOOL_BIN_DIR)" $(GO) install tool
	@tool_goroot=$$($(GO) env GOROOT); \
		cd "$$tool_goroot/src/cmd" && \
		GOWORK=off GOFLAGS="$(GOFLAGS)" GOOS="$(GO_HOST_OS)" GOARCH="$(GO_HOST_ARCH)" \
			$(GO) build -o "$(PPROF)" ./pprof && \
		GOWORK=off GOFLAGS="$(GOFLAGS)" GOOS="$(GO_HOST_OS)" GOARCH="$(GO_HOST_ARCH)" \
			$(GO) build -o "$(TRACE)" ./trace

tools-check: ## Verify that every pinned Go developer tool is installed at the expected version.
	@"$(ROOT_DIR)/build/scripts/check-go-tools" "$(TOOL_MODULE_DIR)" "$(TOOL_BIN_DIR)" "$(GO)"

fmt: tools ## Format versioned and unignored Go source with gofmt and goimports.
	@$(GO_QUALITY) format "$(ROOT_DIR)"

fmt-check: tools ## Fail when versioned or unignored Go source is not formatted.
	@$(GO_QUALITY) format-check "$(ROOT_DIR)"

lint-config: tools ## Validate the golangci-lint configuration without analyzing source.
	@$(GO_QUALITY) lint-config "$(ROOT_DIR)"

lint: tools ## Reject new Go linter findings across all product modules.
	@$(GO_QUALITY) lint "$(ROOT_DIR)"

lint-full: tools ## Report the complete pre-existing and new Go linter backlog.
	@$(GO_QUALITY) lint-full "$(ROOT_DIR)"

lint-fix: tools ## Apply safe automated fixes from the pinned Go linter suite.
	@$(GO_QUALITY) lint-fix "$(ROOT_DIR)"

vulncheck: tools ## Scan every product module against the current Go vulnerability database.
	@$(GO_QUALITY) vulncheck "$(ROOT_DIR)"

quality: fmt-check lint ## Run repository formatting and new-finding lint gates.

security: lint vulncheck ## Run static security analysis and the live vulnerability scan.

precommit: build-scripts-check quality ## Run the fast repository-owned pre-commit gate.
