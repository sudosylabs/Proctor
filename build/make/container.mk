.PHONY: buildenv buildenv-check buildenv-shell container container-load

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

container: ## Build the immutable non-root Proctor runtime image.
	$(DOCKER) build --file "$(ROOT_DIR)/build/docker/Dockerfile.runtime" \
		--build-arg GO_IMAGE='$(PROCTOR_GO_IMAGE)' \
		--build-arg VERSION='$(VERSION)' \
		--build-arg COMMIT='$(COMMIT)' \
		--build-arg BUILD_TIME='$(BUILD_TIME)' \
		--build-arg SOURCE_DATE_EPOCH='$(SOURCE_DATE_EPOCH)' \
		--tag "$(PROCTOR_RUNTIME_IMAGE)" "$(ROOT_DIR)"

container-load: container ## Compatibility alias for local image builds.
