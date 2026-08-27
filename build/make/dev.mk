.PHONY: dev-tools dev-secrets dev-config dev-state dev-seed webapp-build run run-server run-webapp dev-up dev-down dev-reset dev-logs cluster-up run-cluster run-haserver cluster-down cluster-logs cluster-diagnostics cluster-smoke metrics-urls

dev-tools: ## Validate the host development toolchain.
	@"$(ROOT_DIR)/build/scripts/check-tools" "$(GO)" "$(NPM)" "$(DOCKER)" "$(JQ)" "$(CURL)"

dev-secrets:
	@"$(ROOT_DIR)/build/scripts/dev-secrets" "$(DEV_SECRETS_DIR)" "$(ROOT_DIR)/build/dev/metrics-openssl.cnf"

dev-config:
	@mkdir -p "$(DEV_CONFIG_DIR)" "$(DEV_LOG_DIR)/local" "$(DEV_LOG_DIR)/node-a" "$(DEV_LOG_DIR)/node-b" "$(DEV_LOG_DIR)/node-c" "$(DEV_DIR)/otel" "$(DEV_DIR)/observability" "$(DEV_DIR)/prometheus"
	@JQ_COMMAND="$(JQ)" "$(ROOT_DIR)/build/scripts/dev-config" "$(SERVER_DIR)/config/config.example.json" "$(DEV_CONFIG_DIR)/local.json" "$(DEV_LOG_DIR)/local/server.log" "$(WEBAPP_DIR)/dist"
	@JQ_COMMAND="$(JQ)" "$(ROOT_DIR)/build/scripts/dev-config" "$(SERVER_DIR)/config/config.example.json" "$(DEV_CONFIG_DIR)/node-a.json" "/var/log/proctor/node-a/server.log" "/opt/proctor/webapp/dist"
	@JQ_COMMAND="$(JQ)" "$(ROOT_DIR)/build/scripts/dev-config" "$(SERVER_DIR)/config/config.example.json" "$(DEV_CONFIG_DIR)/node-b.json" "/var/log/proctor/node-b/server.log" "/opt/proctor/webapp/dist"
	@JQ_COMMAND="$(JQ)" "$(ROOT_DIR)/build/scripts/dev-config" "$(SERVER_DIR)/config/config.example.json" "$(DEV_CONFIG_DIR)/node-c.json" "/var/log/proctor/node-c/server.log" "/opt/proctor/webapp/dist"
	@"$(ROOT_DIR)/build/scripts/dev-observability-targets" "$(DEV_DIR)/observability" "$(PROCTOR_METRICS_PORT)" 8067

dev-state: dev-secrets dev-config

dev-seed: dev-tools dev-state ## Create or report the guarded synthetic local-development fixture.
	@"$(ROOT_DIR)/build/scripts/dev-seed" \
		"$(DEV_SEED_DIR)" "$(DEV_ENV_FILE)" \
		"http://127.0.0.1:$(PROCTOR_SERVER_PORT)" "http://127.0.0.1:$(PROCTOR_MAILPIT_HTTP_PORT)" \
		"$(JQ)" "$(CURL)" openssl

webapp-build: webapp-install ## Compile the hosted webapp with matching build identity.
	cd "$(WEBAPP_DIR)" && PROCTOR_BUILD_VERSION='$(VERSION)' PROCTOR_BUILD_COMMIT='$(COMMIT)' $(NPM) run build

dev-up: dev-tools dev-state ## Start and health-wait for persistent dependencies and observability.
	@$(DEV_COMPOSE) up --detach --wait postgres redis minio mailpit
	@$(DEV_COMPOSE) run --rm minio-init
	@PROCTOR_PROMETHEUS_TARGETS_FILE=single-node-targets.json $(DEV_COMPOSE) up --detach --no-deps --force-recreate --wait prometheus grafana loki otel-collector
	@CURL_COMMAND="$(CURL)" "$(ROOT_DIR)/build/scripts/wait-http" 'http://127.0.0.1:$(PROCTOR_OTEL_HEALTH_PORT)'

run: run-server ## Run the production-shaped single-node development lifecycle.

run-server: dev-tools webapp-build dev-up ## Build the webapp, start dependencies, and run the server in the foreground.
	@set -a; . "$(DEV_ENV_FILE)"; set +a; \
	PROCTOR_SERVER_LISTEN_ADDRESS='127.0.0.1:$(PROCTOR_SERVER_PORT)' \
	PROCTOR_SERVER_PUBLIC_URL='$(PROCTOR_SERVER_PUBLIC_URL)' \
	PROCTOR_SERVER_WEBAPP_DIRECTORY='$(WEBAPP_DIR)/dist' \
	PROCTOR_DATABASE_DATA_SOURCE='postgres://proctor:proctor@127.0.0.1:$(PROCTOR_POSTGRES_PORT)/proctor?sslmode=disable' \
	PROCTOR_CACHE_BACKEND='redis' \
	PROCTOR_CACHE_NAMESPACE='proctor-dev' \
	PROCTOR_CACHE_REDIS_ADDRESSES='127.0.0.1:$(PROCTOR_REDIS_PORT)' \
	PROCTOR_VFS_BACKEND='s3' \
	PROCTOR_VFS_S3_ENDPOINT='127.0.0.1:$(PROCTOR_MINIO_PORT)' \
	PROCTOR_VFS_S3_ACCESS_KEY='proctorminio' \
	PROCTOR_VFS_S3_SECRET_KEY='proctorminio-secret' \
	PROCTOR_VFS_S3_BUCKET='proctor-dev' \
	PROCTOR_VFS_S3_REGION='us-east-1' \
	PROCTOR_VFS_S3_SECURE='false' \
	PROCTOR_MAIL_ENABLED='true' \
	PROCTOR_MAIL_FROM_ADDRESS='no-reply@proctor.local' \
	PROCTOR_MAIL_SMTP_ADDRESS='127.0.0.1:$(PROCTOR_MAILPIT_SMTP_PORT)' \
	PROCTOR_METRICS_ENABLED='true' \
	PROCTOR_METRICS_LISTEN_ADDRESS='0.0.0.0:$(PROCTOR_METRICS_PORT)' \
	PROCTOR_METRICS_TLS_CERTIFICATE_FILE='$(DEV_METRICS_CERT_FILE)' \
	PROCTOR_METRICS_TLS_PRIVATE_KEY_FILE='$(DEV_METRICS_KEY_FILE)' \
	PROCTOR_AUTHENTICATION_BOOTSTRAP_DEVELOPMENT_MODE='true' \
	exec $(GO) run $(GOFLAGS) -trimpath -ldflags '$(SERVER_LDFLAGS)' ./server/cmd/proctor serve --config "$(DEV_CONFIG_DIR)/local.json"

run-webapp: webapp-install ## Run Vite with HMR and proxy API/WebSocket traffic to Proctor.
	cd "$(WEBAPP_DIR)" && PROCTOR_SERVER_DEV_URL='http://127.0.0.1:$(PROCTOR_SERVER_PORT)' $(NPM) run dev

dev-down: ## Stop developer services without deleting persistent data.
	@$(DEV_COMPOSE) down --remove-orphans

dev-reset: ## Delete all developer containers and persistent volumes.
	@$(DEV_COMPOSE) down --volumes --remove-orphans
	@rm -f "$(DEV_SEED_CREDENTIALS_FILE)" "$(DEV_SEED_FIXTURE_FILE)" "$(DEV_SEED_IN_PROGRESS_FILE)"
	@rmdir "$(DEV_SEED_DIR)" 2>/dev/null || true

dev-logs: ## Follow dependency and observability logs.
	@$(DEV_COMPOSE) logs --follow

cluster-up: dev-tools dev-state container ## Start and health-wait for the three-node development cluster.
	@$(CLUSTER_DEV_COMPOSE) up --detach --wait postgres redis minio mailpit
	@$(CLUSTER_DEV_COMPOSE) run --rm minio-init
	@$(CLUSTER_DEV_COMPOSE) up --detach --no-deps --force-recreate --wait node-a node-b node-c
	@PROCTOR_PROMETHEUS_TARGETS_FILE=cluster-targets.json $(CLUSTER_DEV_COMPOSE) up --detach --no-deps --force-recreate --wait prometheus grafana loki otel-collector
	@$(CLUSTER_DEV_COMPOSE) up --detach --no-deps --force-recreate --wait gateway
	@CURL_COMMAND="$(CURL)" "$(ROOT_DIR)/build/scripts/wait-http" 'http://127.0.0.1:$(PROCTOR_OTEL_HEALTH_PORT)'

run-cluster: cluster-up ## Run the complete HA development topology and follow its logs.
	@$(CLUSTER_DEV_COMPOSE) logs --follow gateway node-a node-b node-c

run-haserver: run-cluster ## Mattermost-compatible alias for the HA development lifecycle.

cluster-down: ## Stop the clustered development topology without deleting data.
	@$(CLUSTER_DEV_COMPOSE) down --remove-orphans

cluster-logs: ## Follow gateway and all Proctor node logs.
	@$(CLUSTER_DEV_COMPOSE) logs --follow gateway node-a node-b node-c

cluster-diagnostics: ## Print bounded cluster state and logs for CI/failure diagnosis.
	@$(CLUSTER_DEV_COMPOSE) ps
	@$(CLUSTER_DEV_COMPOSE) logs --no-color --tail=300 gateway node-a node-b node-c prometheus otel-collector

cluster-smoke: cluster-up ## Certify readiness, metrics, load balancing, and one-node failure recovery.
	@CURL_COMMAND="$(CURL)" JQ_COMMAND="$(JQ)" PROCTOR_EXPECTED_VERSION='$(VERSION)' PROCTOR_EXPECTED_COMMIT='$(COMMIT)' "$(ROOT_DIR)/build/scripts/cluster-smoke" 'http://127.0.0.1:$(PROCTOR_CLUSTER_HTTP_PORT)' 'http://127.0.0.1:$(PROCTOR_PROMETHEUS_PORT)'
	@$(CLUSTER_DEV_COMPOSE) stop node-b
	@CURL_COMMAND="$(CURL)" JQ_COMMAND="$(JQ)" "$(ROOT_DIR)/build/scripts/cluster-smoke" 'http://127.0.0.1:$(PROCTOR_CLUSTER_HTTP_PORT)' 'http://127.0.0.1:$(PROCTOR_PROMETHEUS_PORT)' gateway-only
	@$(CLUSTER_DEV_COMPOSE) up --detach --no-deps --wait node-b
	@CURL_COMMAND="$(CURL)" JQ_COMMAND="$(JQ)" PROCTOR_EXPECTED_VERSION='$(VERSION)' PROCTOR_EXPECTED_COMMIT='$(COMMIT)' "$(ROOT_DIR)/build/scripts/cluster-smoke" 'http://127.0.0.1:$(PROCTOR_CLUSTER_HTTP_PORT)' 'http://127.0.0.1:$(PROCTOR_PROMETHEUS_PORT)'

metrics-urls: ## Print local observability endpoints.
	@printf 'Prometheus: http://127.0.0.1:%s\nGrafana:    http://127.0.0.1:%s\nLoki:       http://127.0.0.1:%s\n' '$(PROCTOR_PROMETHEUS_PORT)' '$(PROCTOR_GRAFANA_PORT)' '$(PROCTOR_LOKI_PORT)'
