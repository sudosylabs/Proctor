SHELL := /bin/sh
COMPOSE := docker compose -f compose.yaml
PROCTOR_POSTGRES_PORT ?= 15432
PROCTOR_REDIS_PORT ?= 16379
PROCTOR_MINIO_PORT ?= 19000
PROCTOR_TEST_DATABASE_URL ?= postgres://proctor:proctor@127.0.0.1:$(PROCTOR_POSTGRES_PORT)/proctor?sslmode=disable
PROCTOR_TEST_REDIS_ADDRESS ?= 127.0.0.1:$(PROCTOR_REDIS_PORT)
PROCTOR_TEST_S3_ENDPOINT ?= 127.0.0.1:$(PROCTOR_MINIO_PORT)

export PROCTOR_POSTGRES_PORT PROCTOR_REDIS_PORT PROCTOR_MINIO_PORT

.PHONY: check architecture openapi-validate openapi-agreement test test-race vet build postgres-up postgres-down postgres-logs redis-up redis-logs minio-up minio-logs integration-all integration-postgres integration-redis integration-s3 integration-providers integration-realtime independent-modules phase-file-jobs conformance-postgres conformance-realtime

check: architecture openapi-agreement test test-race vet

architecture:
	go test ./architecture

openapi-validate:
	go test ./app/api -run 'OpenAPISchemaIsValid$$'

openapi-agreement: openapi-validate
	go test ./app/api -run 'OpenAPI'

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

minio-up:
	$(COMPOSE) up --detach --wait minio

minio-logs:
	$(COMPOSE) logs minio

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
		go test -tags=integration ./store/sqlstore -run '^(TestLocalCacheLayerConformance|TestRetryLayerConformance|TestTimerLayerConformance|TestInstitutionStore|TestAcademicUnitStore|TestProgrammeStore|TestProgrammeLevelStore|TestAcademicPeriodStore|TestExamAuthoringStore|TestExamAttemptStore|TestExamAttemptWorkspaceStore|TestExamResourceStore|TestExamStarterWorkspaceStore|TestClassStore|TestUserStore|TestFileStore|TestFilePurgeCandidatesRespectPurposeReferencesAndLegalHolds|TestJobStore|TestJobHistoryCleanupHonorsStateRetentionAndSelfPreservation|TestClusterDiscoveryStore|TestClustered(ProfilePicturePublicationHasOneVisibleWinner|JobsDeduplicateOccurrencesAndClaimDistinctWork|JobLeaseRecoveryFencesTheDeadNodeAndPreservesAttempts)|TestExternalIdentityStore|TestExternalLoginStateStore|TestPasswordCredentialStore|TestUserTokenStore|TestPersonalAccessTokenStore|TestMFAStore|TestSessionStores|TestAffiliationStore|TestAcademicUnitMemberStore|TestClassMemberStore|TestRoleStore|TestRoleBindingStore|TestAuditStore|TestInstallationStore|TestMigrationsRoundTrip|TestMembershipCanonicalIDConstraintsRejectNoncanonicalValues|TestInstitutionAcademicUnitCanonicalIDConstraints|TestProgrammeCanonicalIDConstraintsRejectNoncanonicalValues|TestAcademicPeriodClassCanonicalIDConstraintsRejectNoncanonicalValues|TestUserIdentityRecoveryCanonicalIDConstraints|TestSessionCanonicalIDConstraintsRejectNoncanonicalValues|TestMFAPersonalAccessTokenCanonicalIDConstraints|TestMFAActivationRejectsCorruptionBeforeCommit|TestAuthorizationAuditCanonicalIDConstraintsRejectNoncanonicalValues|TestManagedFileCanonicalIDConstraints|TestJobCanonicalIDConstraintsRejectNoncanonicalValues|TestInstallationCanonicalIDConstraints|TestCanonicalIDConstraintCompleteness)$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestCommandOutcomeStore$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./cmd/proctor -run '^TestMigrateIntegration$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^(Test(Authentication|AuthenticationFanoutFailure|BrowserCookieAuthentication|SessionManagement|AccountRecovery|PersonalAccessToken|MFA|CASExternalAuthentication|OIDCExternalAuthentication|ExamAuthoring)Integration|TestJobRunnerRecoversNodeLossAroundADurableDomainCommit|TestRecurringMaintenance(UsesOnePermanentOccurrenceAcrossNodesAndRestart|RecoversTransientProposalFailureWithoutRepeatingCompletion)|TestLocalUserCreationCommitsDefaultPictureIntent|TestAcademicMembershipAndUserAdministrationIntegration|TestPasswordResetRequestRateLimitDoesNotDependOnAccountExistence|TestBootstrapAndRoleAdministrationIntegration|TestPrivilegedAuditListingIsScopedAndDurablyAudited|TestAuthorizationResolvesCurrentAcademicHierarchy|TestPrincipalPermissionAndUserVisibilityPolicies)$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration . -run '^(TestFilePurgeStorageIntegrationOnLocalVFS|TestAttemptWorkspaceContentIntegrationOnLocalVFS)$$' -count=1

integration-redis:
	$(MAKE) -C ../packages/cache \
		CACHE_REDIS_PORT='$(PROCTOR_REDIS_PORT)' \
		CACHE_REDIS_ADDRESS='$(PROCTOR_TEST_REDIS_ADDRESS)' \
		conformance-redis

integration-s3:
	@set -eu; \
	cleanup() { docker compose -f '$(CURDIR)/compose.yaml' down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres minio; \
	cd ../packages/vfs; \
	VFS_S3_ENDPOINT='$(PROCTOR_TEST_S3_ENDPOINT)' \
	VFS_S3_BUCKET='proctor-test' \
	VFS_S3_ACCESS_KEY='proctorminio' \
	VFS_S3_SECRET_KEY='proctorminio-secret' \
	VFS_S3_SECURE='false' \
	VFS_S3_CREATE_BUCKET='true' \
		GOWORK=off go test ./s3 -run '^TestIntegrationConformance$$' -count=1; \
	cd ../../server; \
	VFS_S3_ENDPOINT='$(PROCTOR_TEST_S3_ENDPOINT)' \
	VFS_S3_BUCKET='proctor-test' \
	VFS_S3_ACCESS_KEY='proctorminio' \
	VFS_S3_SECRET_KEY='proctorminio-secret' \
	VFS_S3_SECURE='false' \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration . -run '^(TestFilePurgeStorageIntegrationOnS3|TestAttemptWorkspaceContentIntegrationOnS3)$$' -count=1

independent-modules:
	@set -eu; \
	for module in ../packages/cache ../packages/mail ../packages/vfs .; do \
		(cd "$$module" && GOWORK=off go test ./... && GOWORK=off go build ./...); \
	done

phase-file-jobs: check integration-postgres integration-s3 independent-modules

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
	$(COMPOSE) up --detach --wait postgres; \
	go test -tags=integration ./cluster/memberlist -run '^TestAdmissionFailureWithdrawsBeforeRetryAndJoinFailureRemainsNonfatal$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^TestWebSocket(Integration|TwoNodeConformance)$$' -count=1

conformance-postgres: integration-postgres

conformance-realtime: integration-realtime
