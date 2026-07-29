SHELL := /bin/sh
COMPOSE := docker compose -f compose.yaml
PROCTOR_POSTGRES_PORT ?= 15432
PROCTOR_TEST_DATABASE_URL ?= postgres://proctor:proctor@127.0.0.1:$(PROCTOR_POSTGRES_PORT)/proctor?sslmode=disable

export PROCTOR_POSTGRES_PORT

.PHONY: check test test-race vet build postgres-up postgres-down postgres-logs conformance-postgres

check: test test-race vet

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

conformance-postgres:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test ./store/sqlstore -run '^(TestInstitutionStore|TestAcademicUnitStore|TestProgrammeStore|TestProgrammeLevelStore|TestAcademicPeriodStore|TestClassStore|TestUserStore|TestPasswordCredentialStore|TestSessionStores|TestRoleStore|TestRoleBindingStore|TestAuditStore|TestInstallationStore|TestMigrationsRoundTrip)$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test ./cmd/proctor -run '^TestMigrateIntegration$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test ./app -run '^(Test(Authentication|SessionManagement)Integration|TestBootstrapAndRoleAdministrationIntegration|TestPrivilegedAuditListingIsScopedAndDurablyAudited|TestAuthorizationResolvesCurrentAcademicHierarchy)$$' -count=1
