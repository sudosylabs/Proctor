// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each AcademicPeriodStore operation
// so every future persistence adapter can reuse the same tests.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAcademicPeriodStore(t *testing.T, ss store.Store) {
	t.Run("MutationAuditAtomicity", func(t *testing.T) { testAcademicPeriodStoreMutationAuditAtomicity(t, ss) })
	t.Run("AllowsInstitutionDefinedOverlap", func(t *testing.T) { testAcademicPeriodStoreAllowsInstitutionDefinedOverlap(t, ss) })
	t.Run("Save", func(t *testing.T) { testAcademicPeriodStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testAcademicPeriodStoreGet(t, ss) })
	t.Run("GetByName", func(t *testing.T) { testAcademicPeriodStoreGetByName(t, ss) })
	t.Run("ListByInstitution", func(t *testing.T) { testAcademicPeriodStoreListByInstitution(t, ss) })
	t.Run("Update", func(t *testing.T) { testAcademicPeriodStoreUpdate(t, ss) })
	t.Run("RejectUnknownInstitution", func(t *testing.T) {
		testAcademicPeriodStoreRejectUnknownInstitution(t, ss)
	})
	t.Run("EnforceInstitutionNameUniqueness", func(t *testing.T) {
		testAcademicPeriodStoreEnforceInstitutionNameUniqueness(t, ss)
	})
	t.Run("SearchAndArchive", func(t *testing.T) {
		testAcademicPeriodStoreSearchAndArchive(t, ss)
	})
}

func testAcademicPeriodStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	createAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.Id)
	candidate := &model.AcademicPeriod{InstitutionId: institution.Id, Name: "audited-period", DisplayName: "Audited Period", StartAt: 100, EndAt: 200}
	candidate.PrepareCreate(model.NewId(), model.GetMillis())
	created, err := ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{Period: candidate, AuditEventID: createAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("create audit status = %q", completed.Status)
	}

	rolledBackCreate := &model.AcademicPeriod{InstitutionId: institution.Id, Name: "rolled-back-period", DisplayName: "Rolled Back", StartAt: 300, EndAt: 400}
	rolledBackCreate.PrepareCreate(model.NewId(), model.GetMillis())
	if _, err := ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{Period: rolledBackCreate, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("Create() succeeded without its audit attempt")
	}
	if _, err := ss.AcademicPeriod().Get(ctx, rolledBackCreate.Id); !store.IsNotFound(err) {
		t.Fatalf("create survived audit rollback: %v", err)
	}

	updateAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.Id)
	updatedCandidate := *created
	updatedCandidate.EndAt = 250
	updatedCandidate.PrepareUpdate(model.GetMillis())
	updated, err := ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &updatedCandidate, AuditEventID: updateAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err = ss.Audit().Get(ctx, updateAttempt.Id)
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("update audit status = %q", completed.Status)
	}

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.GetMillis())
	if _, err := ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
	}
	persisted, err := ss.AcademicPeriod().Get(ctx, updated.Id)
	requireNoError(t, err)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived audit rollback: %#v", persisted)
	}

	wrongOwner := *updated
	wrongOwner.InstitutionId = model.NewId()
	wrongOwner.PrepareUpdate(model.GetMillis())
	wrongOwnerAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.Id)
	if _, err := ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &wrongOwner, AuditEventID: wrongOwnerAttempt.Id, AuditAt: model.GetMillis()}); !store.IsNotFound(err) {
		t.Fatalf("ownership move error = %v, want not found", err)
	}

	archiveAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.Id)
	archived, err := ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: updated.Id, ArchiveAt: model.GetMillis(), AuditEventID: archiveAttempt.Id, AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}

	archiveRollback := saveAcademicPeriod(t, ctx, ss, institution.Id, "rolled-back-archive", 500)
	if _, err := ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: archiveRollback.Id, ArchiveAt: model.GetMillis(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without its audit attempt")
	}
	if _, err := ss.AcademicPeriod().Get(ctx, archiveRollback.Id); err != nil {
		t.Fatalf("archive survived audit rollback: %v", err)
	}

	unit := saveAcademicUnit(t, ctx, ss, institution.Id, "", "period-dependency-unit")
	programme := saveProgramme(t, ctx, ss, unit.Id, "period-dependency-programme")
	level := saveProgrammeLevel(t, ctx, ss, programme.Id, "period-dependency-level")
	withClass := saveAcademicPeriod(t, ctx, ss, institution.Id, "period-with-class", 1_000)
	saveClass(t, ctx, ss, level.Id, withClass.Id, "period-dependency-class")
	blockedAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.Id)
	if _, err := ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: withClass.Id, ArchiveAt: model.GetMillis(), AuditEventID: blockedAttempt.Id, AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("archive with active class error = %v, want conflict", err)
	}
}

func saveAcademicPeriodAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, institutionID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, Id: institutionID}, ScopeType: model.RoleScopeInstitution, ScopeId: institutionID, Status: model.AuditStatusAttempt, NodeId: "test-node"})
	requireNoError(t, err)
	return attempt
}

func testAcademicPeriodStoreAllowsInstitutionDefinedOverlap(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	first := saveAcademicPeriod(t, ctx, ss, institution.Id, "year", 1_000)
	overlapping := saveAcademicPeriod(t, ctx, ss, institution.Id, "semester", first.StartAt+1)
	adjacent := saveAcademicPeriod(t, ctx, ss, institution.Id, "next", first.EndAt)
	if overlapping.Id == "" || adjacent.Id == "" {
		t.Fatalf("overlap/adjacency not persisted: %#v %#v", overlapping, adjacent)
	}
}

func testAcademicPeriodStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(
		t, ctx, ss, institution.Id, "distinct-summer-term", 2_000_000_000_000,
	)
	found, err := ss.AcademicPeriod().SearchByInstitution(
		ctx, institution.Id, "summer", 10,
	)
	requireNoError(t, err)
	if len(found) != 1 || found[0].Id != period.Id {
		t.Fatalf("SearchByInstitution() = %#v", found)
	}
	archived, err := ss.AcademicPeriod().Delete(ctx, period.Id, model.GetMillis())
	requireNoError(t, err)
	if archived.DeleteAt == 0 {
		t.Fatalf("Delete() = %#v", archived)
	}
}

func testAcademicPeriodStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := &model.AcademicPeriod{
		InstitutionId: institution.Id,
		Name:          "2026-2027",
		DisplayName:   "Academic Year 2026-2027",
		Description:   "Primary academic year",
		StartAt:       1_800_000_000_000,
		EndAt:         1_830_000_000_000,
	}

	saved, err := ss.AcademicPeriod().Save(ctx, period)
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) {
		t.Fatalf("Save() id = %q", saved.Id)
	}
	if period.Id != "" {
		t.Fatalf("Save() mutated input id to %q", period.Id)
	}

	_, err = ss.AcademicPeriod().Save(ctx, saved)
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) {
		t.Fatalf("second Save(saved) error = %v, want invalid input", err)
	}
}

func testAcademicPeriodStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.Id, "2026-2027", 1_800_000_000_000)

	got, err := ss.AcademicPeriod().Get(ctx, period.Id)
	requireNoError(t, err)
	if *got != *period {
		t.Fatalf("Get() = %#v, want %#v", got, period)
	}
	if _, err := ss.AcademicPeriod().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
}

func testAcademicPeriodStoreGetByName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.Id, "2026-2027", 1_800_000_000_000)

	got, err := ss.AcademicPeriod().GetByName(ctx, institution.Id, period.Name)
	requireNoError(t, err)
	if got.Id != period.Id {
		t.Fatalf("GetByName() id = %q, want %q", got.Id, period.Id)
	}
	if _, err := ss.AcademicPeriod().GetByName(ctx, institution.Id, "missing"); !store.IsNotFound(err) {
		t.Fatalf("GetByName(missing) error = %v, want not found", err)
	}
}

func testAcademicPeriodStoreListByInstitution(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	second := saveAcademicPeriod(t, ctx, ss, institution.Id, "2027-2028", 1_840_000_000_000)
	first := saveAcademicPeriod(t, ctx, ss, institution.Id, "2026-2027", 1_800_000_000_000)

	periods, err := ss.AcademicPeriod().ListByInstitution(ctx, institution.Id)
	requireNoError(t, err)
	if len(periods) != 2 || periods[0].Id != first.Id || periods[1].Id != second.Id {
		t.Fatalf("ListByInstitution() = %#v", periods)
	}
	empty, err := ss.AcademicPeriod().ListByInstitution(ctx, model.NewId())
	requireNoError(t, err)
	if len(empty) != 0 {
		t.Fatalf("ListByInstitution(missing) = %#v, want empty", empty)
	}
}

func testAcademicPeriodStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.Id, "2026-2027", 1_800_000_000_000)
	createAt := period.CreateAt

	period.Name = "2026-2027-revised"
	period.DisplayName = "Academic Year 2026-2027 Revised"
	period.EndAt += 1_000
	updated, err := ss.AcademicPeriod().Update(ctx, period)
	requireNoError(t, err)
	if updated.Name != "2026-2027-revised" || updated.EndAt != period.EndAt {
		t.Fatalf("Update() = %#v", updated)
	}
	if updated.CreateAt != createAt || updated.UpdateAt < period.UpdateAt {
		t.Fatalf("Update() timestamps = %#v", updated)
	}

	missing := *updated
	missing.Id = model.NewId()
	_, err = ss.AcademicPeriod().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
}

func testAcademicPeriodStoreRejectUnknownInstitution(t *testing.T, ss store.Store) {
	ctx := context.Background()

	_, err := ss.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		InstitutionId: model.NewId(),
		Name:          "2026-2027",
		DisplayName:   "Academic Year 2026-2027",
		StartAt:       1_800_000_000_000,
		EndAt:         1_830_000_000_000,
	})
	var reference *store.ErrReference
	if !errors.As(err, &reference) ||
		reference.Constraint != "academic_periods_institution_id_fkey" {
		t.Fatalf("Save(unknown institution) error = %v, want reference error", err)
	}
}

func testAcademicPeriodStoreEnforceInstitutionNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	saveAcademicPeriod(t, ctx, ss, institution.Id, "2026-2027", 1_800_000_000_000)

	_, err := ss.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		InstitutionId: institution.Id,
		Name:          "2026-2027",
		DisplayName:   "Duplicate",
		StartAt:       1_900_000_000_000,
		EndAt:         1_930_000_000_000,
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) ||
		conflict.Constraint != "academic_periods_active_name_key" {
		t.Fatalf("duplicate academic period error = %v, want scoped-name conflict", err)
	}
}

func saveAcademicPeriod(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	institutionID string,
	name string,
	startAt int64,
) *model.AcademicPeriod {
	t.Helper()
	period, err := ss.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		InstitutionId: institutionID,
		Name:          name,
		DisplayName:   name,
		StartAt:       startAt,
		EndAt:         startAt + 30_000_000_000,
	})
	requireNoError(t, err)
	return period
}
