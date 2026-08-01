SHELL := /bin/sh
COMPOSE := docker compose -f compose.yaml
PROCTOR_POSTGRES_PORT ?= 15432
PROCTOR_REDIS_PORT ?= 16379
PROCTOR_TEST_DATABASE_URL ?= postgres://proctor:proctor@127.0.0.1:$(PROCTOR_POSTGRES_PORT)/proctor?sslmode=disable
PROCTOR_TEST_REDIS_ADDRESS ?= 127.0.0.1:$(PROCTOR_REDIS_PORT)

export PROCTOR_POSTGRES_PORT PROCTOR_REDIS_PORT

.PHONY: check architecture openapi-agreement test test-race vet build postgres-up postgres-down postgres-logs redis-up redis-logs integration-all integration-postgres integration-redis integration-providers integration-realtime conformance-postgres conformance-realtime

check: test test-race vet

architecture:
	go test ./architecture

openapi-agreement:
	go test ./app/api -run 'OpenAPIAgreesWithRuntime$$'

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build ./...

postgres-up:
	$(COMPOSE) up --detach --wait postgres

postgres-down:
	$(COMPOSE) down --remove-orphans

postgres-logs:
	$(COMPOSE) logs postgres

redis-up:
	$(COMPOSE) up --detach --wait redis

redis-logs:
	$(COMPOSE) logs redis

integration-all:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres redis; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
	PROCTOR_TEST_REDIS_ADDRESS='$(PROCTOR_TEST_REDIS_ADDRESS)' \
		go test -tags=integration -p=1 ./... -count=1

integration-postgres:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^(TestInstitutionStore|TestAcademicUnitStore|TestProgrammeStore|TestProgrammeLevelStore|TestAcademicPeriodStore|TestClassStore|TestUserStore|TestExternalIdentityStore|TestExternalLoginStateStore|TestPasswordCredentialStore|TestUserTokenStore|TestPersonalAccessTokenStore|TestMFAStore|TestSessionStores|TestAffiliationStore|TestAcademicUnitMemberStore|TestClassMemberStore|TestRoleStore|TestRoleBindingStore|TestAuditStore|TestInstallationStore|TestMigrationsRoundTrip)$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./cmd/proctor -run '^TestMigrateIntegration$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^(Test(Authentication|BrowserCookieAuthentication|SessionManagement|AccountRecovery|PersonalAccessToken|MFA|CASExternalAuthentication|OIDCExternalAuthentication)Integration|TestAcademicMembershipAndUserAdministrationIntegration|TestPasswordResetRequestRateLimitDoesNotDependOnAccountExistence|TestBootstrapAndRoleAdministrationIntegration|TestPrivilegedAuditListingIsScopedAndDurablyAudited|TestAuthorizationResolvesCurrentAcademicHierarchy|TestPrincipalPermissionAndUserVisibilityPolicies)$$' -count=1

integration-redis:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait redis; \
	PROCTOR_TEST_REDIS_ADDRESS='$(PROCTOR_TEST_REDIS_ADDRESS)' \
		go test -tags=integration ./platform -run '^TestRedisClusterTwoNodeConformance$$' -count=1

integration-providers:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^Test(CAS|OIDC)ExternalAuthenticationIntegration$$' -count=1

integration-realtime:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres redis; \
	PROCTOR_TEST_REDIS_ADDRESS='$(PROCTOR_TEST_REDIS_ADDRESS)' \
		go test -tags=integration ./platform -run '^TestRedisClusterTwoNodeConformance$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
	PROCTOR_TEST_REDIS_ADDRESS='$(PROCTOR_TEST_REDIS_ADDRESS)' \
		go test -tags=integration ./app -run '^TestWebSocket(Integration|TwoNodeConformance)$$' -count=1

conformance-postgres: integration-postgres

conformance-realtime: integration-realtime
