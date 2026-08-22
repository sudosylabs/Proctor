SHELL := /bin/sh
COMPOSE := docker compose -f compose.yaml
PROCTOR_POSTGRES_PORT ?= 15432
PROCTOR_REDIS_PORT ?= 16379
PROCTOR_MINIO_PORT ?= 19000
PROCTOR_MAILPIT_SMTP_PORT ?= 11025
PROCTOR_MAILPIT_HTTP_PORT ?= 18025
PROCTOR_TEST_DATABASE_URL ?= postgres://proctor:proctor@127.0.0.1:$(PROCTOR_POSTGRES_PORT)/proctor?sslmode=disable
PROCTOR_TEST_REDIS_ADDRESS ?= 127.0.0.1:$(PROCTOR_REDIS_PORT)
PROCTOR_TEST_S3_ENDPOINT ?= 127.0.0.1:$(PROCTOR_MINIO_PORT)
PROCTOR_TEST_MAIL_SMTP_ADDRESS ?= 127.0.0.1:$(PROCTOR_MAILPIT_SMTP_PORT)
PROCTOR_TEST_MAILPIT_HTTP_URL ?= http://127.0.0.1:$(PROCTOR_MAILPIT_HTTP_PORT)

export PROCTOR_POSTGRES_PORT PROCTOR_REDIS_PORT PROCTOR_MINIO_PORT PROCTOR_MAILPIT_SMTP_PORT PROCTOR_MAILPIT_HTTP_PORT

.PHONY: check architecture i18n-check openapi-validate openapi-agreement test test-race vet build package mail-templates-install mail-templates-generate mail-templates-check mail-templates-test mail-preview postgres-up postgres-down postgres-logs redis-up redis-logs minio-up minio-logs mailpit-up mailpit-down mailpit-logs integration-all integration-postgres integration-redis integration-s3 integration-providers integration-realtime independent-modules phase-file-jobs phase-access-onboarding phase-transactional-mail conformance-postgres conformance-realtime

check: architecture i18n-check openapi-agreement test test-race vet

architecture:
	go test ./architecture

i18n-check:
	go run ./cmd/ptool i18n check

openapi-validate:
	go test ./httpapi -run 'OpenAPISchemaIsValid$$'

openapi-agreement: openapi-validate
	go test ./httpapi -run 'OpenAPI'

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build ./...

PACKAGE_DIR ?= dist/proctor

package:
	@test ! -e "$(PACKAGE_DIR)" || (echo 'package output already exists; choose a new PACKAGE_DIR or remove it explicitly' >&2; exit 2)
	mkdir -p "$(PACKAGE_DIR)/config"
	go build -o "$(PACKAGE_DIR)/proctor" ./cmd/proctor
	cp config/config.example.json "$(PACKAGE_DIR)/config/config.example.json"
	@test ! -e "$(PACKAGE_DIR)/config/config.json"

mail-templates-install:
	$(MAKE) -C templates install

mail-templates-generate:
	$(MAKE) -C templates generate

mail-templates-check:
	$(MAKE) -C templates check

mail-templates-test:
	$(MAKE) -C templates test

mail-preview:
	@test -n "$(OUTPUT)" || (echo 'usage: make -C server mail-preview OUTPUT=/path/to/previews' >&2; exit 2)
	go run ./cmd/mailpreview -output "$(OUTPUT)"

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

mailpit-up:
	$(COMPOSE) up --detach --wait mailpit

mailpit-down:
	$(COMPOSE) down --remove-orphans

mailpit-logs:
	$(COMPOSE) logs mailpit

integration-all:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres redis mailpit; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
	PROCTOR_TEST_REDIS_ADDRESS='$(PROCTOR_TEST_REDIS_ADDRESS)' \
	PROCTOR_TEST_MAIL_SMTP_ADDRESS='$(PROCTOR_TEST_MAIL_SMTP_ADDRESS)' \
	PROCTOR_TEST_MAILPIT_HTTP_URL='$(PROCTOR_TEST_MAILPIT_HTTP_URL)' \
		go test -tags=integration -p=1 ./... -count=1

integration-postgres:
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^(TestLocalCacheLayerConformance|TestRetryLayerConformance|TestTimerLayerConformance|TestInstitutionStore|TestAccessPolicyStore|TestAcademicUnitStore|TestProgrammeStore|TestProgrammeLevelStore|TestAcademicPeriodStore|TestMail(Store|MaintenanceRetentionAndSharedRateLimit|PrimaryKeyFenceSerializesEnqueueBeforeRekey)|TestExamAuthoringStore|TestExamSittingStore|TestExamAttemptStore|TestExecutionGrantStore|TestExamAttemptWorkspaceStore|TestExamSubmissionStore|TestExamIntegrityReviewStore|TestExamResourceStore|TestExamStarterWorkspaceStore|TestClassStore|TestInvitationStore|TestUserStore|TestUserSettingsStore|TestUserSettingsRequiredRowAndCascade|TestFileStore|TestFilePurgeCandidatesRespectPurposeReferencesAndLegalHolds|TestJobStore|TestJobHistoryCleanupHonorsStateRetentionAndSelfPreservation|TestClusterDiscoveryStore|TestClustered(ProfilePicturePublicationHasOneVisibleWinner|JobsDeduplicateOccurrencesAndClaimDistinctWork|JobLeaseRecoveryFencesTheDeadNodeAndPreservesAttempts|MailRekeyRecoversCheckpointAndRotatesFanoutBundle)|TestExternalIdentity(Store|AdmissionModes)|TestExternalLoginStateStore|TestPasswordCredentialStore|TestUserTokenStore|TestPersonalAccessTokenStore|TestMFAStore|TestSessionStores|TestAffiliationStore|TestAcademicUnitMemberStore|TestClassMemberStore|TestRoleStore|TestRoleBindingStore|TestAuditStore|TestInstallationStore|TestMigrationsRoundTrip|TestMembershipCanonicalIDConstraintsRejectNoncanonicalValues|TestInstitutionAcademicUnitCanonicalIDConstraints|TestProgrammeCanonicalIDConstraintsRejectNoncanonicalValues|TestAcademicPeriodClassCanonicalIDConstraintsRejectNoncanonicalValues|TestUserIdentityRecoveryCanonicalIDConstraints|TestSessionCanonicalIDConstraintsRejectNoncanonicalValues|TestMFAPersonalAccessTokenCanonicalIDConstraints|TestMFAActivationRejectsCorruptionBeforeCommit|TestAuthorizationAuditCanonicalIDConstraintsRejectNoncanonicalValues|TestManagedFileCanonicalIDConstraints|TestJobCanonicalIDConstraintsRejectNoncanonicalValues|TestInstallationCanonicalIDConstraints|TestCanonicalIDConstraintCompleteness)$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestCommandOutcomeStore$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./cmd/proctor -run '^TestMigrateIntegration$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^(Test(Authentication|AuthenticationFanoutFailure|BrowserCookieAuthentication|SessionManagement|AccountRecovery|PersonalAccessToken|MFA|CASExternalAuthentication|OIDCExternalAuthentication|ExamAuthoring)Integration|TestAccountAndAdministrativeSessionNoticesUseRealServerGraph|TestAccessPolicyTwoNodePostgreSQLFenceAndSafeRevisionFanout|TestJobRunnerRecoversNodeLossAroundADurableDomainCommit|TestRecurringMaintenance(UsesOnePermanentOccurrenceAcrossNodesAndRestart|RecoversTransientProposalFailureWithoutRepeatingCompletion)|TestLocalUserCreationCommitsDefaultPictureIntent|TestUserSettings(ReadOwnIntegrationPreservesExactSource|UnsupportedFormatIsOpaqueAndReadOnlyAfterRollback|TwoNodePostgreSQLConvergencePublishesOnePrivateRefetchHint|UnknownOutcomeReconcilesByReadWithoutContentLeak)|TestAcademicMembershipAndUserAdministrationIntegration|TestInvitationBatchCommitsMixedRowsAndRecoversAcrossRequests|TestPasswordResetRequestRateLimitDoesNotDependOnAccountExistence|TestBootstrapAndRoleAdministrationIntegration|TestPrivilegedAuditListingIsScopedAndDurablyAudited|TestAuthorizationResolvesCurrentAcademicHierarchy|TestPrincipalPermissionAndUserVisibilityPolicies)$$' -count=1; \
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
		(cd "$$module" && \
			GOWORK=off go test ./... && \
			GOWORK=off go test -race ./... && \
			GOWORK=off go vet ./... && \
			GOWORK=off go build ./...); \
	done

phase-file-jobs: check integration-postgres integration-s3 independent-modules

phase-access-onboarding: check
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^TestAccessAndOnboardingServerPhaseCertification$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^TestJobRunnerRecoversNodeLossAroundADurableDomainCommit$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestAccessAndOnboardingDecoratedLayerConformance$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestAuthenticationTerminalCommitsRecheckCurrentAccessPolicy$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestDesktopAuthorizationMaintenanceIsBoundedAndMultiNodeSafe$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestDesktopAuthorizationExchangeSerializesWithConcurrentUserDisable$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestDesktopAuthorizationIssueCodeSerializesWithConcurrentUserDisableAcrossNodes$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^(TestExamSittingReconciliationAndInvitationAcceptanceShareUserClassLockOrder|TestDisabledSittingReconciliationAndInvitationAcceptanceUseUserSingletonClassOrder)$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./store/sqlstore -run '^TestMigrationsRoundTrip$$' -count=1

phase-transactional-mail: check integration-postgres independent-modules mail-templates-check mail-templates-test
	@set -eu; \
	cleanup() { $(COMPOSE) down --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(COMPOSE) up --detach --wait postgres mailpit; \
	cd ../packages/mail; \
	MAIL_SMTP_ADDRESS='$(PROCTOR_TEST_MAIL_SMTP_ADDRESS)' \
		GOWORK=off go test ./smtp -run '^TestIntegrationConformance$$' -count=1; \
	cd ../../server; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
	PROCTOR_TEST_MAIL_SMTP_ADDRESS='$(PROCTOR_TEST_MAIL_SMTP_ADDRESS)' \
	PROCTOR_TEST_MAILPIT_HTTP_URL='$(PROCTOR_TEST_MAILPIT_HTTP_URL)' \
		go test -tags=integration ./app -run '^TestTransactionalMailPhaseCertification$$' -count=1

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
	go test -tags=integration ./cluster/memberlist -run '^(TestAdmissionFailureWithdrawsBeforeRetryAndJoinFailureRemainsNonfatal|TestLateIncompatiblePeerIsRejectedByContinuousAdmission|TestAbruptStableIDRestartReclaimsDeadAddress|TestMemberlistKeyringSupportsRollingPrimaryRotation|TestMemberlistPeriodicallyDiscoversAndJoinsPeersAfterStartup)$$' -count=1; \
	PROCTOR_TEST_DATABASE_URL='$(PROCTOR_TEST_DATABASE_URL)' \
		go test -tags=integration ./app -run '^TestWebSocket(Integration|TwoNodeConformance)$$' -count=1

conformance-postgres: integration-postgres

conformance-realtime: integration-realtime
