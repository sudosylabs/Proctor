.PHONY: package package-verify package-linux-amd64 package-linux-arm64 dist clean-dist

package: webapp-build ## Assemble the current-platform Proctor release directory.
	$(MAKE) -C "$(SERVER_DIR)" package \
		PACKAGE_DIR="$(abspath $(PACKAGE_DIR))" \
		WEBAPP_DIST_DIR="$(abspath $(WEBAPP_DIR)/dist)" \
		GO_BUILD_FLAGS='$(GOFLAGS) -trimpath' \
		GO_LDFLAGS='$(SERVER_LDFLAGS)'
	@"$(ROOT_DIR)/build/scripts/package-support" "$(ROOT_DIR)" "$(PACKAGE_DIR)" "$(VERSION)" "$(COMMIT)" "$(BUILD_TIME)" "$(SOURCE_DATE_EPOCH)"

package-verify: package ## Verify packaged identity, configuration, and hosted assets.
	@test -x "$(PACKAGE_DIR)/proctor"
	@test -f "$(PACKAGE_DIR)/config/config.example.json"
	@test -f "$(PACKAGE_DIR)/webapp/dist/webapp-build.json"
	@test -f "$(PACKAGE_DIR)/deploy/systemd/proctor.service"
	@test -f "$(PACKAGE_DIR)/BUILD_METADATA"
	@"$(PACKAGE_DIR)/proctor" version | grep -F '$(COMMIT)' >/dev/null

package-linux-amd64: webapp-build ## Assemble the Linux amd64 release directory.
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(MAKE) -C "$(SERVER_DIR)" package \
		PACKAGE_DIR="$(DIST_DIR)/proctor-linux-amd64" \
		WEBAPP_DIST_DIR="$(abspath $(WEBAPP_DIR)/dist)" \
		GO_BUILD_FLAGS='$(GOFLAGS) -trimpath' \
		GO_LDFLAGS='$(SERVER_LDFLAGS)'
	@"$(ROOT_DIR)/build/scripts/package-support" "$(ROOT_DIR)" "$(DIST_DIR)/proctor-linux-amd64" "$(VERSION)" "$(COMMIT)" "$(BUILD_TIME)" "$(SOURCE_DATE_EPOCH)"

package-linux-arm64: webapp-build ## Assemble the Linux arm64 release directory.
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(MAKE) -C "$(SERVER_DIR)" package \
		PACKAGE_DIR="$(DIST_DIR)/proctor-linux-arm64" \
		WEBAPP_DIST_DIR="$(abspath $(WEBAPP_DIR)/dist)" \
		GO_BUILD_FLAGS='$(GOFLAGS) -trimpath' \
		GO_LDFLAGS='$(SERVER_LDFLAGS)'
	@"$(ROOT_DIR)/build/scripts/package-support" "$(ROOT_DIR)" "$(DIST_DIR)/proctor-linux-arm64" "$(VERSION)" "$(COMMIT)" "$(BUILD_TIME)" "$(SOURCE_DATE_EPOCH)"

dist: package-linux-amd64 package-linux-arm64 ## Create deterministic Linux archives and SHA-256 checksums.
	cd "$(SERVER_DIR)" && $(GO) run $(GOFLAGS) ./cmd/ptool release archive \
		--source "$(DIST_DIR)/proctor-linux-amd64" \
		--output "$(DIST_DIR)/proctor-$(VERSION)-linux-amd64.tar.gz" \
		--prefix proctor --epoch '$(SOURCE_DATE_EPOCH)'
	cd "$(SERVER_DIR)" && $(GO) run $(GOFLAGS) ./cmd/ptool release archive \
		--source "$(DIST_DIR)/proctor-linux-arm64" \
		--output "$(DIST_DIR)/proctor-$(VERSION)-linux-arm64.tar.gz" \
		--prefix proctor --epoch '$(SOURCE_DATE_EPOCH)'
	cd "$(DIST_DIR)" && shasum -a 256 proctor-$(VERSION)-linux-*.tar.gz > SHA256SUMS

clean-dist: ## Remove release output only.
	@test "$(DIST_DIR)" = "$(ROOT_DIR)/dist"
	rm -rf "$(DIST_DIR)"
