// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/store.go. Proctor
// retains Mattermost's root SQL store, per-model store registry, centralized
// query builder, connection lifecycle, and interface-returning accessors.
// Proctor is PostgreSQL-only and omits replica, search, license, and transparent
// store-layer machinery until those capabilities have an actual consumer.

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store"
)

const minimumPostgresVersion = 140000

type Settings struct {
	DataSource            string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
	QueryTimeout          time.Duration
	MigrationTimeout      time.Duration
}

func SettingsFromConfig(cfg config.Database) Settings {
	return Settings{
		DataSource:            cfg.DataSource,
		MaxOpenConnections:    cfg.MaxOpenConnections,
		MaxIdleConnections:    cfg.MaxIdleConnections,
		ConnectionMaxLifetime: cfg.ConnectionMaxLifetime.Duration,
		ConnectionMaxIdleTime: cfg.ConnectionMaxIdleTime.Duration,
		QueryTimeout:          cfg.QueryTimeout.Duration,
		MigrationTimeout:      cfg.MigrationTimeout.Duration,
	}
}

func (s Settings) validate() error {
	if s.DataSource == "" {
		return errors.New("database data source is required")
	}
	if s.MaxOpenConnections <= 0 {
		return errors.New("database max open connections must be greater than zero")
	}
	if s.MaxIdleConnections < 0 || s.MaxIdleConnections > s.MaxOpenConnections {
		return errors.New("database max idle connections must be between zero and max open connections")
	}
	if s.ConnectionMaxLifetime <= 0 || s.ConnectionMaxIdleTime <= 0 ||
		s.QueryTimeout <= 0 || s.MigrationTimeout <= 0 {
		return errors.New("database timeouts and connection lifetimes must be greater than zero")
	}
	return nil
}

// SQLStoreStores holds the concrete adapters behind the model-store contracts.
// Keeping this registry separate from SQLStore mirrors Mattermost's composition
// flow and makes future store decorators possible without changing callers.
type SQLStoreStores struct {
	institution          store.InstitutionStore
	academicUnit         store.AcademicUnitStore
	programme            store.ProgrammeStore
	programmeLevel       store.ProgrammeLevelStore
	academicPeriod       store.AcademicPeriodStore
	examAuthoring        store.ExamAuthoringStore
	examRevision         store.ExamRevisionStore
	examSitting          store.ExamSittingStore
	examAttempt          store.ExamAttemptStore
	examAttemptWorkspace store.ExamAttemptWorkspaceStore
	examSubmission       store.ExamSubmissionStore
	examIntegrityReview  store.ExamIntegrityReviewStore
	examResource         store.ExamResourceStore
	examCorrection       store.ExamCorrectionStore
	examStarterWorkspace store.ExamStarterWorkspaceStore
	class                store.ClassStore
	user                 store.UserStore
	userSettings         store.UserSettingsStore
	file                 store.FileStore
	job                  store.JobStore
	mail                 store.MailStore
	externalIdentity     store.ExternalIdentityStore
	externalLoginState   store.ExternalLoginStateStore
	desktopAuthorization store.DesktopAuthorizationStore
	userToken            store.UserTokenStore
	invitation           store.InvitationStore
	personalAccessToken  store.PersonalAccessTokenStore
	mfa                  store.MFAStore
	affiliation          store.AffiliationStore
	academicUnitMember   store.AcademicUnitMemberStore
	classMember          store.ClassMemberStore
	passwordCredential   store.PasswordCredentialStore
	session              store.SessionStore
	sessionCredential    store.SessionCredentialStore
	role                 store.RoleStore
	roleBinding          store.RoleBindingStore
	audit                store.AuditStore
	installation         store.InstallationStore
	accessPolicy         store.AccessPolicyStore
	clusterDiscovery     store.ClusterDiscoveryStore
	commandOutcome       store.CommandOutcomeStore
}

// SQLStore owns PostgreSQL connections and all concrete model stores.
type SQLStore struct {
	masterX  *sqlxDBWrapper
	stores   SQLStoreStores
	settings Settings
}

func New(ctx context.Context, settings Settings) (*SQLStore, error) {
	if err := settings.validate(); err != nil {
		return nil, err
	}

	db, err := sqlx.Open("postgres", settings.DataSource)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	db.SetMaxOpenConns(settings.MaxOpenConnections)
	db.SetMaxIdleConns(settings.MaxIdleConnections)
	db.SetConnMaxLifetime(settings.ConnectionMaxLifetime)
	db.SetConnMaxIdleTime(settings.ConnectionMaxIdleTime)

	sqlStore := &SQLStore{
		masterX:  newSqlxDBWrapper(db, settings.QueryTimeout),
		settings: settings,
	}
	if err := sqlStore.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := sqlStore.verifyPostgresVersion(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	sqlStore.stores.institution = newSQLInstitutionStore(sqlStore)
	sqlStore.stores.academicUnit = newSQLAcademicUnitStore(sqlStore)
	sqlStore.stores.programme = newSQLProgrammeStore(sqlStore)
	sqlStore.stores.programmeLevel = newSQLProgrammeLevelStore(sqlStore)
	sqlStore.stores.academicPeriod = newSQLAcademicPeriodStore(sqlStore)
	sqlStore.stores.examAuthoring = newSQLExamAuthoringStore(sqlStore)
	sqlStore.stores.examRevision = newSQLExamRevisionStore(sqlStore)
	sqlStore.stores.examSitting = newSQLExamSittingStore(sqlStore)
	sqlStore.stores.examAttempt = newSQLExamAttemptStore(sqlStore)
	sqlStore.stores.examAttemptWorkspace = NewSQLExamAttemptWorkspaceStore(sqlStore)
	sqlStore.stores.examSubmission = NewSQLExamSubmissionStore(sqlStore)
	sqlStore.stores.examIntegrityReview = NewSQLExamIntegrityReviewStore(sqlStore)
	sqlStore.stores.examResource = newSQLExamResourceStore(sqlStore)
	sqlStore.stores.examCorrection = newSQLExamCorrectionStore(sqlStore)
	sqlStore.stores.examStarterWorkspace = NewSQLExamStarterWorkspaceStore(sqlStore)
	sqlStore.stores.class = newSQLClassStore(sqlStore)
	sqlStore.stores.user = newSQLUserStore(sqlStore)
	sqlStore.stores.userSettings = newSQLUserSettingsStore(sqlStore)
	sqlStore.stores.file = newSQLFileStore(sqlStore)
	sqlStore.stores.job = newSQLJobStore(sqlStore)
	sqlStore.stores.mail = newSQLMailStore(sqlStore)
	sqlStore.stores.externalIdentity = newSQLExternalIdentityStore(sqlStore)
	sqlStore.stores.externalLoginState = newSQLExternalLoginStateStore(sqlStore)
	sqlStore.stores.desktopAuthorization = newSQLDesktopAuthorizationStore(sqlStore)
	sqlStore.stores.userToken = newSQLUserTokenStore(sqlStore)
	sqlStore.stores.invitation = newSQLInvitationStore(sqlStore)
	sqlStore.stores.personalAccessToken = newSQLPersonalAccessTokenStore(sqlStore)
	sqlStore.stores.mfa = newSQLMFAStore(sqlStore)
	sqlStore.stores.affiliation = newSQLAffiliationStore(sqlStore)
	sqlStore.stores.academicUnitMember = newSQLAcademicUnitMemberStore(sqlStore)
	sqlStore.stores.classMember = newSQLClassMemberStore(sqlStore)
	sqlStore.stores.passwordCredential = newSQLPasswordCredentialStore(sqlStore)
	sqlStore.stores.session = newSQLSessionStore(sqlStore)
	sqlStore.stores.sessionCredential = newSQLSessionCredentialStore(sqlStore)
	sqlStore.stores.role = newSQLRoleStore(sqlStore)
	sqlStore.stores.roleBinding = newSQLRoleBindingStore(sqlStore)
	sqlStore.stores.audit = newSQLAuditStore(sqlStore)
	sqlStore.stores.installation = newSQLInstallationStore(sqlStore)
	sqlStore.stores.accessPolicy = newSQLAccessPolicyStore(sqlStore)
	sqlStore.stores.clusterDiscovery = newSQLClusterDiscoveryStore(sqlStore)
	sqlStore.stores.commandOutcome = newSQLCommandOutcomeStore(sqlStore)
	return sqlStore, nil
}

func (ss *SQLStore) Close() error {
	if ss == nil || ss.masterX == nil {
		return nil
	}
	return ss.masterX.Close()
}

func (ss *SQLStore) Ping(ctx context.Context) error {
	return ss.GetMaster().Ping(ctx)
}

func (ss *SQLStore) Stats() sql.DBStats {
	return ss.GetMaster().Stats()
}

func (ss *SQLStore) Institution() store.InstitutionStore {
	return ss.stores.institution
}

func (ss *SQLStore) AcademicUnit() store.AcademicUnitStore {
	return ss.stores.academicUnit
}

func (ss *SQLStore) Programme() store.ProgrammeStore {
	return ss.stores.programme
}

func (ss *SQLStore) ProgrammeLevel() store.ProgrammeLevelStore {
	return ss.stores.programmeLevel
}

func (ss *SQLStore) AcademicPeriod() store.AcademicPeriodStore {
	return ss.stores.academicPeriod
}

func (ss *SQLStore) ExamAuthoring() store.ExamAuthoringStore {
	return ss.stores.examAuthoring
}

func (ss *SQLStore) ExamRevision() store.ExamRevisionStore {
	return ss.stores.examRevision
}

func (ss *SQLStore) ExamSitting() store.ExamSittingStore {
	return ss.stores.examSitting
}

func (ss *SQLStore) ExamAttempt() store.ExamAttemptStore {
	return ss.stores.examAttempt
}

func (ss *SQLStore) ExamAttemptWorkspace() store.ExamAttemptWorkspaceStore {
	return ss.stores.examAttemptWorkspace
}

func (ss *SQLStore) ExamSubmission() store.ExamSubmissionStore {
	return ss.stores.examSubmission
}

func (ss *SQLStore) ExamIntegrityReview() store.ExamIntegrityReviewStore {
	return ss.stores.examIntegrityReview
}

func (ss *SQLStore) ExamResource() store.ExamResourceStore {
	return ss.stores.examResource
}

func (ss *SQLStore) ExamCorrection() store.ExamCorrectionStore {
	return ss.stores.examCorrection
}

func (ss *SQLStore) ExamStarterWorkspace() store.ExamStarterWorkspaceStore {
	return ss.stores.examStarterWorkspace
}

func (ss *SQLStore) Class() store.ClassStore {
	return ss.stores.class
}

func (ss *SQLStore) User() store.UserStore {
	return ss.stores.user
}

func (ss *SQLStore) UserSettings() store.UserSettingsStore {
	return ss.stores.userSettings
}

func (ss *SQLStore) File() store.FileStore { return ss.stores.file }

func (ss *SQLStore) Job() store.JobStore { return ss.stores.job }

func (ss *SQLStore) Mail() store.MailStore { return ss.stores.mail }

func (ss *SQLStore) ExternalIdentity() store.ExternalIdentityStore {
	return ss.stores.externalIdentity
}

func (ss *SQLStore) ExternalLoginState() store.ExternalLoginStateStore {
	return ss.stores.externalLoginState
}

func (ss *SQLStore) DesktopAuthorization() store.DesktopAuthorizationStore {
	return ss.stores.desktopAuthorization
}

func (ss *SQLStore) UserToken() store.UserTokenStore {
	return ss.stores.userToken
}

func (ss *SQLStore) Invitation() store.InvitationStore {
	return ss.stores.invitation
}

func (ss *SQLStore) PersonalAccessToken() store.PersonalAccessTokenStore {
	return ss.stores.personalAccessToken
}

func (ss *SQLStore) MFA() store.MFAStore {
	return ss.stores.mfa
}

func (ss *SQLStore) Affiliation() store.AffiliationStore {
	return ss.stores.affiliation
}

func (ss *SQLStore) AcademicUnitMember() store.AcademicUnitMemberStore {
	return ss.stores.academicUnitMember
}

func (ss *SQLStore) ClassMember() store.ClassMemberStore {
	return ss.stores.classMember
}

func (ss *SQLStore) PasswordCredential() store.PasswordCredentialStore {
	return ss.stores.passwordCredential
}

func (ss *SQLStore) Session() store.SessionStore {
	return ss.stores.session
}

func (ss *SQLStore) SessionCredential() store.SessionCredentialStore {
	return ss.stores.sessionCredential
}

func (ss *SQLStore) Role() store.RoleStore {
	return ss.stores.role
}

func (ss *SQLStore) RoleBinding() store.RoleBindingStore {
	return ss.stores.roleBinding
}

func (ss *SQLStore) Audit() store.AuditStore {
	return ss.stores.audit
}

func (ss *SQLStore) Installation() store.InstallationStore {
	return ss.stores.installation
}

func (ss *SQLStore) AccessPolicy() store.AccessPolicyStore {
	return ss.stores.accessPolicy
}

func (ss *SQLStore) ClusterDiscovery() store.ClusterDiscoveryStore {
	return ss.stores.clusterDiscovery
}

func (ss *SQLStore) CommandOutcome() store.CommandOutcomeStore { return ss.stores.commandOutcome }

func (ss *SQLStore) GetMaster() *sqlxDBWrapper {
	return ss.masterX
}

func (ss *SQLStore) getQueryBuilder() sq.StatementBuilderType {
	return sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
}

func (ss *SQLStore) verifyPostgresVersion(ctx context.Context) error {
	var raw string
	if err := ss.GetMaster().Get(ctx, &raw, "SHOW server_version_num"); err != nil {
		return fmt.Errorf("read PostgreSQL version: %w", err)
	}
	version, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("parse PostgreSQL version %q: %w", raw, err)
	}
	if version < minimumPostgresVersion {
		return fmt.Errorf(
			"PostgreSQL %s is unsupported; version 14.0 or newer is required",
			postgresVersionString(version),
		)
	}
	return nil
}

func postgresVersionString(version int) string {
	return strconv.Itoa(version/10000) + "." + strconv.Itoa(version%10000)
}

var _ store.Store = (*SQLStore)(nil)
