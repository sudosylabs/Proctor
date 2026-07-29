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
	Delete(context.Context, string, int64) error
}

// AcademicUnitStore persists nodes in the institution's academic-unit tree.
type AcademicUnitStore interface {
	Save(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error)
	Get(context.Context, string) (*model.AcademicUnit, error)
	ListChildren(context.Context, string, string) ([]*model.AcademicUnit, error)
	ListAncestors(context.Context, string) ([]*model.AcademicUnit, error)
	Update(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error)
}

// ProgrammeStore persists courses of study owned by academic units.
type ProgrammeStore interface {
	Save(context.Context, *model.Programme) (*model.Programme, error)
	Get(context.Context, string) (*model.Programme, error)
	GetByName(context.Context, string, string) (*model.Programme, error)
	ListByAcademicUnit(context.Context, string) ([]*model.Programme, error)
	Update(context.Context, *model.Programme) (*model.Programme, error)
}

// ProgrammeLevelStore persists reusable curriculum stages owned by programmes.
type ProgrammeLevelStore interface {
	Save(context.Context, *model.ProgrammeLevel) (*model.ProgrammeLevel, error)
	Get(context.Context, string) (*model.ProgrammeLevel, error)
	GetByName(context.Context, string, string) (*model.ProgrammeLevel, error)
	ListByProgramme(context.Context, string) ([]*model.ProgrammeLevel, error)
	Update(context.Context, *model.ProgrammeLevel) (*model.ProgrammeLevel, error)
}

// AcademicPeriodStore persists institution-wide enrollment periods.
type AcademicPeriodStore interface {
	Save(context.Context, *model.AcademicPeriod) (*model.AcademicPeriod, error)
	Get(context.Context, string) (*model.AcademicPeriod, error)
	GetByName(context.Context, string, string) (*model.AcademicPeriod, error)
	ListByInstitution(context.Context, string) ([]*model.AcademicPeriod, error)
	Update(context.Context, *model.AcademicPeriod) (*model.AcademicPeriod, error)
}

// ClassStore persists concrete programme-level rosters for academic periods.
type ClassStore interface {
	Save(context.Context, *model.Class) (*model.Class, error)
	Get(context.Context, string) (*model.Class, error)
	GetByName(context.Context, string, string, string) (*model.Class, error)
	ListByProgrammeLevel(context.Context, string) ([]*model.Class, error)
	ListByAcademicPeriod(context.Context, string) ([]*model.Class, error)
	GetAcademicUnitId(context.Context, string) (string, error)
	Update(context.Context, *model.Class) (*model.Class, error)
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
	Update(context.Context, *model.User) (*model.User, error)
	UpdateLastLogin(context.Context, string, int64) error
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
