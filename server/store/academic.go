// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

// AcademicUnitCommandResult and AcademicPeriodCommandResult report whether a
// named mutation executed or returned its previously committed outcome.
type AcademicUnitCommandResult struct {
	Value    *model.AcademicUnit
	Replayed bool
}

type AcademicPeriodCommandResult struct {
	Value    *model.AcademicPeriod
	Replayed bool
}

// InstitutionStore persists the institution represented by this installation.
type InstitutionStore interface {
	Save(context.Context, *model.Institution) (*model.Institution, error)
	Get(context.Context, model.InstitutionID) (*model.Institution, error)
	GetSingleton(context.Context) (*model.Institution, error)
	Update(context.Context, *InstitutionUpdate) (*model.Institution, error)
	Archive(context.Context, model.InstitutionID, int64) error
}

type InstitutionUpdate struct {
	Institution  *model.Institution
	AuditEventID model.AuditEventID
	AuditAt      int64
}

// AcademicUnitCreation is the complete durable input for creating an Academic
// Unit and completing its already-persisted mutation audit in one transaction.
type AcademicUnitCreation struct {
	Unit         *model.AcademicUnit
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type AcademicUnitUpdate struct {
	Unit         *model.AcademicUnit
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type AcademicUnitArchive struct {
	// ExpectedRevision is optional for compatibility; positive values fence stale callers.
	ExpectedRevision int64
	ID               model.AcademicUnitID
	ArchiveAt        int64
	AuditEventID     model.AuditEventID
	AuditAt          int64
}

// AcademicUnitStore persists nodes in the institution's academic-unit tree.
type AcademicUnitStore interface {
	Create(context.Context, *AcademicUnitCreation) (*model.AcademicUnit, error)
	CreateIdempotently(context.Context, *AcademicUnitCreation, *CommandIdempotency) (*AcademicUnitCommandResult, error)
	Update(context.Context, *AcademicUnitUpdate) (*model.AcademicUnit, error)
	Archive(context.Context, *AcademicUnitArchive) (*model.AcademicUnit, error)
	Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error)
	GetByName(context.Context, model.InstitutionID, string) (*model.AcademicUnit, error)
	ListChildren(context.Context, model.InstitutionID, model.AcademicUnitID) ([]*model.AcademicUnit, error)
	ListAncestors(context.Context, model.AcademicUnitID) ([]*model.AcademicUnit, error)
	Search(context.Context, model.InstitutionID, string, int) ([]*model.AcademicUnit, error)
}

// ProgrammeStore persists courses of study owned by academic units.
type ProgrammeCreation struct {
	Programme    *model.Programme
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type ProgrammeUpdate struct {
	Programme    *model.Programme
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type ProgrammeArchive struct {
	// ExpectedRevision is optional for compatibility; positive values fence stale callers.
	ExpectedRevision int64
	ID               model.ProgrammeID
	ArchiveAt        int64
	AuditEventID     model.AuditEventID
	AuditAt          int64
}

type ProgrammeStore interface {
	Create(context.Context, *ProgrammeCreation) (*model.Programme, error)
	Update(context.Context, *ProgrammeUpdate) (*model.Programme, error)
	Archive(context.Context, *ProgrammeArchive) (*model.Programme, error)
	Get(context.Context, model.ProgrammeID) (*model.Programme, error)
	GetByName(context.Context, model.AcademicUnitID, string) (*model.Programme, error)
	ListByAcademicUnit(context.Context, model.AcademicUnitID) ([]*model.Programme, error)
	SearchByAcademicUnit(context.Context, model.AcademicUnitID, string, int) ([]*model.Programme, error)
	// SeedUpdate is an unaudited fixture write; production code never calls it.
	SeedUpdate(context.Context, *model.Programme) (*model.Programme, error)
}

// ProgrammeLevelStore persists reusable curriculum stages owned by programmes.
type ProgrammeLevelCreation struct {
	Level        *model.ProgrammeLevel
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type ProgrammeLevelUpdate struct {
	Level        *model.ProgrammeLevel
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type ProgrammeLevelArchive struct {
	// ExpectedRevision is optional for compatibility; positive values fence stale callers.
	ExpectedRevision int64
	ID               model.ProgrammeLevelID
	ArchiveAt        int64
	AuditEventID     model.AuditEventID
	AuditAt          int64
}

type ProgrammeLevelStore interface {
	Create(context.Context, *ProgrammeLevelCreation) (*model.ProgrammeLevel, error)
	Update(context.Context, *ProgrammeLevelUpdate) (*model.ProgrammeLevel, error)
	Archive(context.Context, *ProgrammeLevelArchive) (*model.ProgrammeLevel, error)
	Get(context.Context, model.ProgrammeLevelID) (*model.ProgrammeLevel, error)
	GetByName(context.Context, model.ProgrammeID, string) (*model.ProgrammeLevel, error)
	ListByProgramme(context.Context, model.ProgrammeID) ([]*model.ProgrammeLevel, error)
	SearchByProgramme(context.Context, model.ProgrammeID, string, int) ([]*model.ProgrammeLevel, error)
	// SeedUpdate is an unaudited fixture write; production code never calls it.
	SeedUpdate(context.Context, *model.ProgrammeLevel) (*model.ProgrammeLevel, error)
}

// AcademicPeriodVisibilityScope constrains list/search to institution-owned
// periods plus unit-owned periods visible through authorized subtree roots.
type AcademicPeriodVisibilityScope struct {
	InstitutionID       model.InstitutionID
	InstitutionWide     bool
	AcademicUnitRootIDs []model.AcademicUnitID
}

// AcademicPeriodStore persists institution- or Academic-Unit-owned enrollment
// periods.
type AcademicPeriodCreation struct {
	Period       *model.AcademicPeriod
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type AcademicPeriodUpdate struct {
	Period       *model.AcademicPeriod
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type AcademicPeriodArchive struct {
	// ExpectedRevision is optional for compatibility; positive values fence stale callers.
	ExpectedRevision int64
	ID               model.AcademicPeriodID
	ArchiveAt        int64
	AuditEventID     model.AuditEventID
	AuditAt          int64
}

type AcademicPeriodStore interface {
	Create(context.Context, *AcademicPeriodCreation) (*model.AcademicPeriod, error)
	CreateIdempotently(context.Context, *AcademicPeriodCreation, *CommandIdempotency) (*AcademicPeriodCommandResult, error)
	Update(context.Context, *AcademicPeriodUpdate) (*model.AcademicPeriod, error)
	Archive(context.Context, *AcademicPeriodArchive) (*model.AcademicPeriod, error)
	Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error)
	GetByOwnerName(context.Context, model.Resource, string) (*model.AcademicPeriod, error)
	ListVisible(context.Context, AcademicPeriodVisibilityScope, string, int) ([]*model.AcademicPeriod, error)
}

// ClassStore persists concrete programme-level rosters for academic periods.
type ClassCreation struct {
	Class        *model.Class
	AuditEventID model.AuditEventID
	AuditAt      int64
}

type ClassUpdate struct {
	Class                  *model.Class
	ExpectedAcademicUnitID model.AcademicUnitID
	ExpectedRevision       int64
	AuditEventID           model.AuditEventID
	AuditAt                int64
}

type ClassArchive struct {
	ID                     model.ClassID
	ExpectedAcademicUnitID model.AcademicUnitID
	ExpectedRevision       int64
	ArchiveAt              int64
	AuditEventID           model.AuditEventID
	AuditAt                int64
}

type ClassStore interface {
	Create(context.Context, *ClassCreation) (*model.Class, error)
	Update(context.Context, *ClassUpdate) (*model.Class, error)
	Archive(context.Context, *ClassArchive) (*model.Class, error)
	Get(context.Context, model.ClassID) (*model.Class, error)
	GetByName(context.Context, model.ProgrammeLevelID, model.AcademicPeriodID, string) (*model.Class, error)
	ListByProgrammeLevel(context.Context, model.ProgrammeLevelID) ([]*model.Class, error)
	ListByAcademicPeriod(context.Context, model.AcademicPeriodID) ([]*model.Class, error)
	SearchByAcademicUnit(context.Context, model.AcademicUnitID, string, int) ([]*model.Class, error)
	GetAcademicUnitID(context.Context, model.ClassID) (model.AcademicUnitID, error)
}
