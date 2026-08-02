// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package store defines Proctor's durable persistence contracts.
package store

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

// Store is the root persistence contract used by the application and platform.
// Concrete adapters expose each model store through this interface so callers
// do not depend on PostgreSQL implementation types.
type Store interface {
	Institution() InstitutionStore
	AcademicUnit() AcademicUnitStore
	Programme() ProgrammeStore
	ProgrammeLevel() ProgrammeLevelStore
	AcademicPeriod() AcademicPeriodStore
	Class() ClassStore
	User() UserStore
	ExternalIdentity() ExternalIdentityStore
	ExternalLoginState() ExternalLoginStateStore
	UserToken() UserTokenStore
	PersonalAccessToken() PersonalAccessTokenStore
	MFA() MFAStore
	Affiliation() AffiliationStore
	AcademicUnitMember() AcademicUnitMemberStore
	ClassMember() ClassMemberStore
	PasswordCredential() PasswordCredentialStore
	Session() SessionStore
	SessionCredential() SessionCredentialStore
	Role() RoleStore
	RoleBinding() RoleBindingStore
	Audit() AuditStore
	Installation() InstallationStore

	Ping(context.Context) error
	GetDBSchemaVersion(context.Context) (int, error)
	GetLocalSchemaVersion() (int, error)
	ValidateSchema(context.Context) error
	Close() error
}

// InstitutionStore persists the institution represented by this installation.
type InstitutionStore interface {
	Save(context.Context, *model.Institution) (*model.Institution, error)
	Get(context.Context, string) (*model.Institution, error)
	GetSingleton(context.Context) (*model.Institution, error)
	Update(context.Context, *model.Institution) (*model.Institution, error)
	UpdateWithAudit(context.Context, *InstitutionUpdate) (*model.Institution, error)
	Delete(context.Context, string, int64) error
}

type InstitutionUpdate struct {
	Institution  *model.Institution
	AuditEventID string
	AuditAt      int64
}

// AcademicUnitCreation is the complete durable input for creating an Academic
// Unit and completing its already-persisted mutation audit in one transaction.
type AcademicUnitCreation struct {
	Unit         *model.AcademicUnit
	AuditEventID string
	AuditAt      int64
}

type AcademicUnitUpdate struct {
	Unit         *model.AcademicUnit
	AuditEventID string
	AuditAt      int64
}

type AcademicUnitArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

// AcademicUnitStore persists nodes in the institution's academic-unit tree.
type AcademicUnitStore interface {
	Create(context.Context, *AcademicUnitCreation) (*model.AcademicUnit, error)
	UpdateWithAudit(context.Context, *AcademicUnitUpdate) (*model.AcademicUnit, error)
	ArchiveWithAudit(context.Context, *AcademicUnitArchive) (*model.AcademicUnit, error)
	Save(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error)
	Get(context.Context, string) (*model.AcademicUnit, error)
	ListChildren(context.Context, string, string) ([]*model.AcademicUnit, error)
	ListAncestors(context.Context, string) ([]*model.AcademicUnit, error)
	Search(context.Context, string, string, int) ([]*model.AcademicUnit, error)
	Update(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error)
	Delete(context.Context, string, int64) (*model.AcademicUnit, error)
}

// ProgrammeStore persists courses of study owned by academic units.
type ProgrammeCreation struct {
	Programme    *model.Programme
	AuditEventID string
	AuditAt      int64
}

type ProgrammeUpdate struct {
	Programme    *model.Programme
	AuditEventID string
	AuditAt      int64
}

type ProgrammeArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

type ProgrammeStore interface {
	Create(context.Context, *ProgrammeCreation) (*model.Programme, error)
	UpdateWithAudit(context.Context, *ProgrammeUpdate) (*model.Programme, error)
	ArchiveWithAudit(context.Context, *ProgrammeArchive) (*model.Programme, error)
	Save(context.Context, *model.Programme) (*model.Programme, error)
	Get(context.Context, string) (*model.Programme, error)
	GetByName(context.Context, string, string) (*model.Programme, error)
	ListByAcademicUnit(context.Context, string) ([]*model.Programme, error)
	SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Programme, error)
	Update(context.Context, *model.Programme) (*model.Programme, error)
	Delete(context.Context, string, int64) (*model.Programme, error)
}

// ProgrammeLevelStore persists reusable curriculum stages owned by programmes.
type ProgrammeLevelCreation struct {
	Level        *model.ProgrammeLevel
	AuditEventID string
	AuditAt      int64
}

type ProgrammeLevelUpdate struct {
	Level        *model.ProgrammeLevel
	AuditEventID string
	AuditAt      int64
}

type ProgrammeLevelArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

type ProgrammeLevelStore interface {
	Create(context.Context, *ProgrammeLevelCreation) (*model.ProgrammeLevel, error)
	UpdateWithAudit(context.Context, *ProgrammeLevelUpdate) (*model.ProgrammeLevel, error)
	ArchiveWithAudit(context.Context, *ProgrammeLevelArchive) (*model.ProgrammeLevel, error)
	Save(context.Context, *model.ProgrammeLevel) (*model.ProgrammeLevel, error)
	Get(context.Context, string) (*model.ProgrammeLevel, error)
	GetByName(context.Context, string, string) (*model.ProgrammeLevel, error)
	ListByProgramme(context.Context, string) ([]*model.ProgrammeLevel, error)
	SearchByProgramme(context.Context, string, string, int) ([]*model.ProgrammeLevel, error)
	Update(context.Context, *model.ProgrammeLevel) (*model.ProgrammeLevel, error)
	Delete(context.Context, string, int64) (*model.ProgrammeLevel, error)
}

// AcademicPeriodStore persists institution-wide enrollment periods.
type AcademicPeriodStore interface {
	Save(context.Context, *model.AcademicPeriod) (*model.AcademicPeriod, error)
	Get(context.Context, string) (*model.AcademicPeriod, error)
	GetByName(context.Context, string, string) (*model.AcademicPeriod, error)
	ListByInstitution(context.Context, string) ([]*model.AcademicPeriod, error)
	SearchByInstitution(context.Context, string, string, int) ([]*model.AcademicPeriod, error)
	Update(context.Context, *model.AcademicPeriod) (*model.AcademicPeriod, error)
	Delete(context.Context, string, int64) (*model.AcademicPeriod, error)
}

// ClassStore persists concrete programme-level rosters for academic periods.
type ClassStore interface {
	Save(context.Context, *model.Class) (*model.Class, error)
	Get(context.Context, string) (*model.Class, error)
	GetByName(context.Context, string, string, string) (*model.Class, error)
	ListByProgrammeLevel(context.Context, string) ([]*model.Class, error)
	ListByAcademicPeriod(context.Context, string) ([]*model.Class, error)
	SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Class, error)
	GetAcademicUnitId(context.Context, string) (string, error)
	Update(context.Context, *model.Class) (*model.Class, error)
	Delete(context.Context, string, int64) (*model.Class, error)
}

type UserListOptions struct {
	Query           string
	AfterUsername   string
	AfterId         string
	Limit           int
	IncludeDisabled bool
}

// UserStore persists login-capable accounts without their credentials.
type UserStore interface {
	Save(context.Context, *model.User) (*model.User, error)
	SaveWithPassword(
		context.Context,
		*model.User,
		*model.PasswordCredential,
	) (*model.User, *model.PasswordCredential, error)
	Get(context.Context, string) (*model.User, error)
	GetByUsername(context.Context, string) (*model.User, error)
	GetByEmail(context.Context, string) (*model.User, error)
	List(context.Context, UserListOptions) ([]*model.User, error)
	Update(context.Context, *model.User) (*model.User, error)
	SetDisabled(context.Context, string, int64, int64) (*model.User, error)
	DisableAndRevokeSessions(
		context.Context,
		string,
		int64,
		string,
	) (*model.User, []*model.Session, []string, error)
	UpdateLastLogin(context.Context, string, int64) error
}

type ExternalIdentityResolution struct {
	Identity    *model.ExternalIdentity
	User        *model.User
	Provisioned bool
}

// ExternalIdentityStore persists provider-subject links and owns the
// transaction that either resolves an existing link or provisions a new user
// and link without email-based account merging.
type ExternalIdentityStore interface {
	Save(context.Context, *model.ExternalIdentity) (*model.ExternalIdentity, error)
	Get(context.Context, string) (*model.ExternalIdentity, error)
	GetByProviderSubject(context.Context, string, string) (*model.ExternalIdentity, error)
	ListByUser(context.Context, string) ([]*model.ExternalIdentity, error)
	ResolveOrProvision(
		context.Context,
		*model.ExternalIdentity,
		*model.User,
		bool,
		*model.AuditEvent,
	) (*ExternalIdentityResolution, error)
}

// ExternalLoginStateStore persists hashed, browser-bound, one-use login
// transactions so any node may receive the provider callback.
type ExternalLoginStateStore interface {
	Save(context.Context, *model.ExternalLoginState) (*model.ExternalLoginState, error)
	GetByStateHash(context.Context, string) (*model.ExternalLoginState, error)
	Consume(
		context.Context,
		string,
		string,
		string,
		int64,
	) (*model.ExternalLoginState, error)
}

type EmailVerificationResult struct {
	Token *model.UserToken
	User  *model.User
}

type PasswordResetResult struct {
	Token               *model.UserToken
	User                *model.User
	PasswordCredential  *model.PasswordCredential
	RevokedSessions     []*model.Session
	RevokedAccessHashes []string
}

// UserTokenStore owns issuance and single-use consumption of purpose-specific
// account credentials. Consumption methods include their account mutation,
// session revocation where applicable, and terminal audit in one transaction.
type UserTokenStore interface {
	Issue(
		context.Context,
		*model.UserToken,
		*model.AuditEvent,
	) (*model.UserToken, error)
	GetByHash(
		context.Context,
		string,
		model.UserTokenPurpose,
	) (*model.UserToken, error)
	ConsumeEmailVerification(
		context.Context,
		string,
		int64,
		*model.AuditEvent,
	) (*EmailVerificationResult, error)
	ConsumePasswordReset(
		context.Context,
		string,
		string,
		int64,
		string,
		*model.AuditEvent,
	) (*PasswordResetResult, error)
}

type PersonalAccessTokenResolution struct {
	Token *model.PersonalAccessToken
	User  *model.User
}

// PersonalAccessTokenStore persists hashed, explicitly scoped credentials.
// Resolve is authoritative and also performs the debounced last-used update.
type PersonalAccessTokenStore interface {
	Save(context.Context, *model.PersonalAccessToken, int) (*model.PersonalAccessToken, error)
	Get(context.Context, string) (*model.PersonalAccessToken, error)
	ListByUser(context.Context, string) ([]*model.PersonalAccessToken, error)
	Resolve(context.Context, string, int64, int64) (*PersonalAccessTokenResolution, error)
	SetDisabled(context.Context, string, string, bool, int64, int) (*model.PersonalAccessToken, error)
	Revoke(context.Context, string, string, int64) (*model.PersonalAccessToken, error)
}

type MFAActivationResult struct {
	Credential        *model.MFACredential
	Session           *model.Session
	AccessTokenHashes []string
}

type MFADisableResult struct {
	AccessTokenHashes []string
}

// MFAStore owns the encrypted TOTP credential, hashed recovery codes, replay
// prevention, and the session-strength changes coupled to MFA lifecycle.
type MFAStore interface {
	SavePending(context.Context, *model.MFACredential) (*model.MFACredential, error)
	GetByUser(context.Context, string) (*model.MFACredential, error)
	Activate(
		context.Context,
		string,
		string,
		int64,
		[]*model.MFARecoveryCode,
		string,
		int64,
	) (*MFAActivationResult, error)
	ConsumeSecondFactor(context.Context, string, int64, string, int64) error
	UpgradeSession(context.Context, string, string, int64) ([]string, error)
	ReplaceRecoveryCodes(context.Context, string, []*model.MFARecoveryCode, int64) error
	CountRecoveryCodes(context.Context, string) (int, error)
	Disable(context.Context, string, int64) (*MFADisableResult, error)
}

// AffiliationStore persists non-exclusive institution relationships.
type AffiliationStore interface {
	Save(context.Context, *model.Affiliation) (*model.Affiliation, error)
	Get(context.Context, string) (*model.Affiliation, error)
	ListByUser(context.Context, string) ([]*model.Affiliation, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.Affiliation, error)
	End(context.Context, string, int64) (*model.Affiliation, error)
}

// AcademicUnitMemberStore persists organizational membership without roles.
type AcademicUnitMemberStore interface {
	Save(context.Context, *model.AcademicUnitMember) (*model.AcademicUnitMember, error)
	Get(context.Context, string) (*model.AcademicUnitMember, error)
	ListByUser(context.Context, string) ([]*model.AcademicUnitMember, error)
	ListByAcademicUnit(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
	End(context.Context, string, int64) (*model.AcademicUnitMember, error)
}

type ClassEnrollmentResult struct {
	Membership *model.ClassMember
	Previous   *model.ClassMember
}

// ClassMemberStore owns transactional student enrollment and history.
type ClassMemberStore interface {
	Enroll(context.Context, *model.ClassMember) (*ClassEnrollmentResult, error)
	Get(context.Context, string) (*model.ClassMember, error)
	ListByUser(context.Context, string) ([]*model.ClassMember, error)
	ListByClass(context.Context, string, int64) ([]*model.ClassMember, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.ClassMember, error)
	End(context.Context, string, int64) (*model.ClassMember, error)
}

// PasswordCredentialStore persists one encoded password hash per local user.
type PasswordCredentialStore interface {
	Save(context.Context, *model.PasswordCredential) (*model.PasswordCredential, error)
	GetByUser(context.Context, string) (*model.PasswordCredential, error)
	Update(context.Context, *model.PasswordCredential) (*model.PasswordCredential, error)
}

// SessionStore persists sessions and owns atomic session lifecycle changes.
type SessionStore interface {
	Save(
		context.Context,
		*model.Session,
		[]*model.SessionCredential,
		int,
	) (*model.Session, []*model.SessionCredential, error)
	Get(context.Context, string) (*model.Session, error)
	ListByUser(context.Context, string) ([]*model.Session, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.Session, error)
	UpdateActivity(context.Context, string, int64, int64) error
	Revoke(context.Context, string, string, int64, string) ([]string, error)
	RevokeAllForUser(
		context.Context,
		string,
		int64,
		string,
	) ([]*model.Session, []string, error)
}

type SessionRotation struct {
	Session             *model.Session
	AccessCredential    *model.SessionCredential
	RefreshCredential   *model.SessionCredential
	RevokedAccessHashes []string
	ReplayDetected      bool
}

// SessionCredentialStore resolves bearer credentials and atomically rotates
// refresh credentials with replay detection.
type SessionCredentialStore interface {
	GetSessionByTokenHash(
		context.Context,
		string,
		model.SessionCredentialKind,
	) (*model.SessionCredential, *model.Session, error)
	RotateRefresh(
		context.Context,
		string,
		*model.SessionCredential,
		*model.SessionCredential,
		int64,
		int64,
	) (*SessionRotation, error)
}

// RoleStore persists reusable permission sets independently of their scopes.
type RoleStore interface {
	Save(context.Context, *model.Role) (*model.Role, error)
	Get(context.Context, string) (*model.Role, error)
	GetByName(context.Context, string) (*model.Role, error)
	GetByIds(context.Context, []string) ([]*model.Role, error)
	List(context.Context) ([]*model.Role, error)
	Update(context.Context, *model.Role) (*model.Role, error)
	Delete(context.Context, string, int64) (*model.Role, error)
}

// RoleBindingStore persists time-bounded role assignments. Scope references
// are validated transactionally because PostgreSQL cannot express a foreign
// key whose target table depends on scope_type.
type RoleBindingStore interface {
	Save(context.Context, *model.RoleBinding) (*model.RoleBinding, error)
	Get(context.Context, string) (*model.RoleBinding, error)
	ListByUser(context.Context, string) ([]*model.RoleBinding, error)
	ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.RoleBinding, error)
	End(context.Context, string, int64) (*model.RoleBinding, error)
}

type AuditListOptions struct {
	ActorId    string
	Action     string
	Resource   *model.Resource
	BeforeTime int64
	BeforeId   string
	Limit      int
}

// AuditStore owns authoritative security records. Events are append-oriented;
// Complete permits only the terminal transition from attempt to success/fail.
type AuditStore interface {
	Save(context.Context, *model.AuditEvent) (*model.AuditEvent, error)
	Get(context.Context, string) (*model.AuditEvent, error)
	Complete(context.Context, string, model.AuditStatus, string, []byte, int64) (*model.AuditEvent, error)
	List(context.Context, AuditListOptions) ([]*model.AuditEvent, error)
}

// InstallationBootstrap is the complete durable input for the one-time
// installation bootstrap transaction. PasswordHash is already encoded by the
// application password hasher and must never be logged or audited.
type InstallationBootstrap struct {
	Institution   *model.Institution
	Administrator *model.User
	PasswordHash  string
	Role          *model.Role
	RoleBinding   *model.RoleBinding
	AuditEvent    *model.AuditEvent
}

// InstallationStore owns the cross-model transaction that makes a pristine
// database into an initialized logical Proctor installation.
type InstallationStore interface {
	Get(context.Context) (*model.InstallationState, error)
	Bootstrap(context.Context, *InstallationBootstrap) (*model.InstallationBootstrapResult, error)
}
