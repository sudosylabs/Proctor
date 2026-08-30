SHELL := /bin/sh

ROOT_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))

# Command-line values have highest priority. The ignored override file is read
# before tracked ?= defaults; environment values apply when it does not set the
# same variable.
-include $(ROOT_DIR)/config.override.mk

include $(ROOT_DIR)/build/make/config.mk
include $(ROOT_DIR)/build/make/tools.mk
include $(ROOT_DIR)/build/make/dev.mk
include $(ROOT_DIR)/build/make/test.mk
include $(ROOT_DIR)/build/make/release.mk
include $(ROOT_DIR)/build/make/container.mk

.DEFAULT_GOAL := help

.PHONY: help
help: ## List product build, development, test, and release commands.
	@awk 'BEGIN {FS = ":.*## "; printf "Proctor commands:\n\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-24s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
