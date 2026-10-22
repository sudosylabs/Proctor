// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// FixtureAuditAction names the generic attempts that fixture mutations
// complete. It is deliberately outside the model audit action catalog, so no
// application listing or filter can mistake a fixture for a real event.
const FixtureAuditAction = "storetest.fixture"

// beginFixtureAudit persists the actorless attempt an audited Store mutation
// requires and completes. Fixtures use it where they need state rather than an
// assertion about the mutation's own audit record.
func beginFixtureAudit(ctx context.Context, ss store.Store) (model.AuditEventID, time.Time, error) {
	scope := model.NewID()
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:   FixtureAuditAction,
		Resource: model.Resource{Type: model.ResourceInstitution, ID: scope}, ScopeType: model.RoleScopeInstitution,
		ScopeID: scope, Status: model.AuditStatusAttempt, NodeID: "storetest-fixture",
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return event.ID, model.NowUTC(), nil
}

// UpdateInstitution applies a fixture update through the audited Store
// operation, preparing the update instant as callers of the former unaudited
// operation expected.
func UpdateInstitution(ctx context.Context, ss store.Store, institution *model.Institution) (*model.Institution, error) {
	if institution == nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *institution
	candidate.PrepareUpdate(model.NowUTC())
	return ss.Institution().Update(ctx, &store.InstitutionUpdate{Institution: &candidate, AuditEventID: auditID, AuditAt: at})
}

// SaveAcademicUnit creates a fixture Academic Unit through the audited Store
// operation with a Store-chosen identity.
func SaveAcademicUnit(ctx context.Context, ss store.Store, unit *model.AcademicUnit) (*model.AcademicUnit, error) {
	if unit == nil || !unit.ID.IsZero() {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *unit
	candidate.PrepareCreate(model.NewAcademicUnitID(), model.NowUTC())
	return ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{Unit: &candidate, AuditEventID: auditID, AuditAt: at})
}

// UpdateAcademicUnit applies a fixture update through the audited Store operation.
func UpdateAcademicUnit(ctx context.Context, ss store.Store, unit *model.AcademicUnit) (*model.AcademicUnit, error) {
	if unit == nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *unit
	candidate.PrepareUpdate(model.NowUTC())
	return ss.AcademicUnit().Update(ctx, &store.AcademicUnitUpdate{Unit: &candidate, AuditEventID: auditID, AuditAt: at})
}

// ArchiveAcademicUnit archives a fixture Academic Unit without a revision fence.
func ArchiveAcademicUnit(ctx context.Context, ss store.Store, id model.AcademicUnitID, archiveAt time.Time) (*model.AcademicUnit, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.AcademicUnit().Archive(ctx, &store.AcademicUnitArchive{ID: id, ArchiveAt: archiveAt, AuditEventID: auditID, AuditAt: at})
}

// SaveProgramme creates a fixture Programme through the audited Store operation.
func SaveProgramme(ctx context.Context, ss store.Store, programme *model.Programme) (*model.Programme, error) {
	if programme == nil || !programme.ID.IsZero() {
		return nil, store.NewErrInvalidInput("programme", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *programme
	candidate.PrepareCreate(model.NewProgrammeID(), model.NowUTC())
	return ss.Programme().Create(ctx, &store.ProgrammeCreation{Programme: &candidate, AuditEventID: auditID, AuditAt: at})
}

// UpdateProgramme applies a fixture update through the audited Store operation.
func UpdateProgramme(ctx context.Context, ss store.Store, programme *model.Programme) (*model.Programme, error) {
	if programme == nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *programme
	candidate.PrepareUpdate(model.NowUTC())
	return ss.Programme().Update(ctx, &store.ProgrammeUpdate{Programme: &candidate, AuditEventID: auditID, AuditAt: at})
}

// ArchiveProgramme archives a fixture Programme without a revision fence.
func ArchiveProgramme(ctx context.Context, ss store.Store, id model.ProgrammeID, archiveAt time.Time) (*model.Programme, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.Programme().Archive(ctx, &store.ProgrammeArchive{ID: id, ArchiveAt: archiveAt, AuditEventID: auditID, AuditAt: at})
}

// SaveProgrammeLevel creates a fixture Programme Level through the audited
// Store operation.
func SaveProgrammeLevel(ctx context.Context, ss store.Store, level *model.ProgrammeLevel) (*model.ProgrammeLevel, error) {
	if level == nil || !level.ID.IsZero() {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *level
	candidate.PrepareCreate(model.NewProgrammeLevelID(), model.NowUTC())
	return ss.ProgrammeLevel().Create(ctx, &store.ProgrammeLevelCreation{Level: &candidate, AuditEventID: auditID, AuditAt: at})
}

// UpdateProgrammeLevel applies a fixture update through the audited Store operation.
func UpdateProgrammeLevel(ctx context.Context, ss store.Store, level *model.ProgrammeLevel) (*model.ProgrammeLevel, error) {
	if level == nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *level
	candidate.PrepareUpdate(model.NowUTC())
	return ss.ProgrammeLevel().Update(ctx, &store.ProgrammeLevelUpdate{Level: &candidate, AuditEventID: auditID, AuditAt: at})
}

// ArchiveProgrammeLevel archives a fixture Programme Level without a revision fence.
func ArchiveProgrammeLevel(ctx context.Context, ss store.Store, id model.ProgrammeLevelID, archiveAt time.Time) (*model.ProgrammeLevel, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.ProgrammeLevel().Archive(ctx, &store.ProgrammeLevelArchive{ID: id, ArchiveAt: archiveAt, AuditEventID: auditID, AuditAt: at})
}

// SaveAcademicPeriod creates a fixture Academic Period through the audited
// Store operation.
func SaveAcademicPeriod(ctx context.Context, ss store.Store, period *model.AcademicPeriod) (*model.AcademicPeriod, error) {
	if period == nil || !period.ID.IsZero() {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *period
	candidate.PrepareCreate(model.NewAcademicPeriodID(), model.NowUTC())
	return ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{Period: &candidate, AuditEventID: auditID, AuditAt: at})
}

// UpdateAcademicPeriod applies a fixture update through the audited Store operation.
func UpdateAcademicPeriod(ctx context.Context, ss store.Store, period *model.AcademicPeriod) (*model.AcademicPeriod, error) {
	if period == nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *period
	candidate.PrepareUpdate(model.NowUTC())
	return ss.AcademicPeriod().Update(ctx, &store.AcademicPeriodUpdate{Period: &candidate, AuditEventID: auditID, AuditAt: at})
}

// ArchiveAcademicPeriod archives a fixture Academic Period without a revision fence.
func ArchiveAcademicPeriod(ctx context.Context, ss store.Store, id model.AcademicPeriodID, archiveAt time.Time) (*model.AcademicPeriod, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.AcademicPeriod().Archive(ctx, &store.AcademicPeriodArchive{ID: id, ArchiveAt: archiveAt, AuditEventID: auditID, AuditAt: at})
}

// SaveClass creates a fixture Class through the audited Store operation.
func SaveClass(ctx context.Context, ss store.Store, class *model.Class) (*model.Class, error) {
	if class == nil || !class.ID.IsZero() {
		return nil, store.NewErrInvalidInput("class", "value", nil)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *class
	candidate.PrepareCreate(model.NewClassID(), model.NowUTC())
	return ss.Class().Create(ctx, &store.ClassCreation{Class: &candidate, AuditEventID: auditID, AuditAt: at})
}

// UpdateClass applies a fixture update fenced on the caller's revision and
// the Class's current Academic Unit.
func UpdateClass(ctx context.Context, ss store.Store, class *model.Class) (*model.Class, error) {
	if class == nil {
		return nil, store.NewErrInvalidInput("class", "value", nil)
	}
	unitID, err := ss.Class().GetAcademicUnitID(ctx, class.ID)
	if err != nil {
		return nil, err
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	candidate := *class
	candidate.PrepareUpdate(model.NowUTC())
	return ss.Class().Update(ctx, &store.ClassUpdate{
		Class: &candidate, ExpectedAcademicUnitID: unitID, ExpectedRevision: class.Revision,
		AuditEventID: auditID, AuditAt: at,
	})
}

// ArchiveClass archives the current revision of a fixture Class.
func ArchiveClass(ctx context.Context, ss store.Store, id model.ClassID, archiveAt time.Time) (*model.Class, error) {
	current, err := ss.Class().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	unitID, err := ss.Class().GetAcademicUnitID(ctx, id)
	if err != nil {
		return nil, err
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.Class().Archive(ctx, &store.ClassArchive{
		ID: id, ExpectedAcademicUnitID: unitID, ExpectedRevision: current.Revision, ArchiveAt: archiveAt,
		AuditEventID: auditID, AuditAt: at,
	})
}

// EndAffiliation ends a fixture Affiliation through the audited Store operation.
func EndAffiliation(ctx context.Context, ss store.Store, id model.AffiliationID, expectedRevision int64, endAt time.Time) (*model.Affiliation, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.Affiliation().End(ctx, &store.AffiliationEnd{
		ID: id, ExpectedRevision: expectedRevision, EndAt: endAt, AuditEventID: auditID, AuditAt: at,
	})
}

// EndAcademicUnitMember ends a fixture Academic Unit membership through the
// audited Store operation.
func EndAcademicUnitMember(ctx context.Context, ss store.Store, id model.AcademicUnitMemberID, expectedRevision int64, endAt time.Time) (*model.AcademicUnitMember, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.AcademicUnitMember().End(ctx, &store.AcademicUnitMemberEnd{
		ID: id, ExpectedRevision: expectedRevision, EndAt: endAt, AuditEventID: auditID, AuditAt: at,
	})
}

// SaveRole creates a fixture Role through the audited Store operation. The
// protected built-in Role has no audited creation outside installation
// bootstrap, so a built-in fixture uses the dedicated seed operation.
func SaveRole(ctx context.Context, ss store.Store, role *model.Role) (*model.Role, error) {
	if role != nil && role.BuiltIn {
		return ss.Role().SeedBuiltIn(ctx, role)
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.Role().Save(ctx, &store.RoleCreation{Role: role, AuditEventID: auditID, AuditAt: at})
}

// UpdateRole applies a fixture update through the audited Store operation and
// advances the caller's snapshot to the stored Role, as the former unaudited
// operation did, so a later update carries the new concurrency token.
func UpdateRole(ctx context.Context, ss store.Store, role *model.Role) (*model.Role, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	updated, err := ss.Role().Update(ctx, &store.RoleUpdate{Role: role, AuditEventID: auditID, AuditAt: at})
	if err == nil && role != nil && updated != nil {
		*role = *updated.Clone()
	}
	return updated, err
}

// ArchiveRole archives a fixture Role through the audited Store operation.
func ArchiveRole(ctx context.Context, ss store.Store, id model.RoleID, archiveAt time.Time) (*model.Role, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.Role().Archive(ctx, &store.RoleArchive{ID: id, ArchiveAt: archiveAt, AuditEventID: auditID, AuditAt: at})
}

// SaveRoleBinding creates a fixture Role Binding through the audited Store
// operation against the Role's current snapshot.
func SaveRoleBinding(ctx context.Context, ss store.Store, binding *model.RoleBinding) (*model.RoleBinding, error) {
	if binding == nil {
		return nil, store.NewErrInvalidInput("role_binding", "value", nil)
	}
	role, err := ss.Role().Get(ctx, binding.RoleID)
	if err != nil {
		return nil, err
	}
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.RoleBinding().Save(ctx, &store.RoleBindingCreation{
		Binding: binding, ExpectedRoleUpdatedAt: role.UpdatedAt, ExpectedRolePermissions: role.Permissions,
		AuditEventID: auditID, AuditAt: at,
	})
}

// EndRoleBinding ends a fixture Role Binding through the audited Store operation.
func EndRoleBinding(ctx context.Context, ss store.Store, id model.RoleBindingID, endAt time.Time) (*model.RoleBinding, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.RoleBinding().End(ctx, &store.RoleBindingEnd{ID: id, EndAt: endAt, AuditEventID: auditID, AuditAt: at})
}

// RevokeAllUserSessions revokes every Session of a fixture User through the
// audited self-revocation operation, which needs no security notice.
func RevokeAllUserSessions(ctx context.Context, ss store.Store, userID model.UserID, revokedAt time.Time) (*store.UserSessionsRevocationResult, error) {
	auditID, at, err := beginFixtureAudit(ctx, ss)
	if err != nil {
		return nil, err
	}
	return ss.Session().RevokeAllForUser(ctx, &store.UserSessionsRevocation{
		UserID: userID, RevokedAt: revokedAt, Reason: model.SessionRevocationUserAllSessions,
		AuditEventID: auditID, AuditAt: at,
	})
}

// SaveSession creates a fixture Session through the audited creation
// operation, beginning a fixture login attempt when the input names none.
func SaveSession(ctx context.Context, ss store.Store, input *store.SessionCreation) (*model.Session, []*model.SessionCredential, error) {
	if input != nil && input.AuditEventID == "" {
		auditID, at, err := beginFixtureAudit(ctx, ss)
		if err != nil {
			return nil, nil, err
		}
		input.AuditEventID, input.AuditAt = auditID, at
	}
	return ss.Session().Save(ctx, input)
}
