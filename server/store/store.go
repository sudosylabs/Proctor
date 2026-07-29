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
	UpdateActivity(context.Context, string, int64, int64) error
	Revoke(context.Context, string, int64, string) ([]string, error)
	RevokeAllForUser(context.Context, string, int64, string) ([]string, error)
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
