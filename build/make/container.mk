.PHONY: buildenv buildenv-check buildenv-shell container container-load container-scan

buildenv: ## Build the exact Go and Node maintainer environment.
	$(DOCKER) build --file "$(ROOT_DIR)/build/docker/Dockerfile.buildenv" \
		--build-arg GO_IMAGE='$(PROCTOR_GO_IMAGE)' \
		--tag "$(PROCTOR_BUILDENV_IMAGE)" "$(ROOT_DIR)"

buildenv-check: buildenv ## Run the hermetic product gate in the pinned maintainer environment.
	$(DOCKER) run --rm --user '$(PROCTOR_UID):$(PROCTOR_GID)' \
		--env HOME=/tmp/proctor-build-home \
		--volume '$(ROOT_DIR):/workspace' --workdir /workspace \
		$(PROCTOR_BUILDENV_RUN_FLAGS) "$(PROCTOR_BUILDENV_IMAGE)" make check

buildenv-shell: buildenv ## Open an interactive shell in the pinned maintainer environment.
	$(DOCKER) run --rm --interactive --tty --user '$(PROCTOR_UID):$(PROCTOR_GID)' \
		--env HOME=/tmp/proctor-build-home \
		--volume '$(ROOT_DIR):/workspace' --workdir /workspace \
		$(PROCTOR_BUILDENV_RUN_FLAGS) "$(PROCTOR_BUILDENV_IMAGE)" bash

# The local image uses the release recipe: package Linux binaries once, then
# copy that package into the distroless image with the release Dockerfile.
CONTAINER_ARCH ?= $(GO_HOST_ARCH)
CONTAINER_CONTEXT_DIR := $(BUILD_DIR)/container

container: webapp-build ## Build the immutable non-root runtime image from a Linux package.
	rm -rf "$(CONTAINER_CONTEXT_DIR)"
	$(call package-linux,$(CONTAINER_ARCH),$(CONTAINER_CONTEXT_DIR)/$(CONTAINER_ARCH)/proctor)
	$(DOCKER) build --file "$(ROOT_DIR)/build/docker/Dockerfile.release" \
		--platform 'linux/$(CONTAINER_ARCH)' \
		--label 'org.opencontainers.image.revision=$(COMMIT)' \
		--label 'org.opencontainers.image.version=$(VERSION)' \
		--label 'org.opencontainers.image.licenses=AGPL-3.0-only' \
		--tag "$(PROCTOR_RUNTIME_IMAGE)" "$(CONTAINER_CONTEXT_DIR)"

container-load: container ## Compatibility alias for local image builds.

# The scan saves one platform of a local image, built by `make container` or
# loaded from the release job's unpublished OCI archive, and gives Trivy only
# that archive: no registry credential or Docker socket reaches the scanner.
CONTAINER_SCAN_DIR := $(BUILD_DIR)/container-scan
CONTAINER_SCAN_CACHE_DIR := $(BUILD_DIR)/trivy-cache

container-scan: ## Fail on critical or fixable high vulnerabilities in the runtime image.
	rm -rf "$(CONTAINER_SCAN_DIR)"
	mkdir -p "$(CONTAINER_SCAN_DIR)" "$(CONTAINER_SCAN_CACHE_DIR)"
	$(DOCKER) save --platform 'linux/$(CONTAINER_ARCH)' \
		--output "$(CONTAINER_SCAN_DIR)/image.tar" "$(PROCTOR_RUNTIME_IMAGE)"
	$(DOCKER) run --rm --user '$(PROCTOR_UID):$(PROCTOR_GID)' --env HOME=/tmp \
		--volume "$(CONTAINER_SCAN_DIR):/scan" --volume "$(CONTAINER_SCAN_CACHE_DIR):/cache" \
		"$(PROCTOR_TRIVY_IMAGE)" image --cache-dir /cache --scanners vuln \
		--no-progress --skip-version-check --format json --output /scan/report.json \
		--exit-code 0 --input /scan/image.tar
	node "$(ROOT_DIR)/build/ci/container-scan.mjs" "$(CONTAINER_SCAN_DIR)/report.json"
