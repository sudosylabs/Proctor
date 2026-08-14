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
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAcademicPeriodStore(t *testing.T, ss store.Store) {
	t.Run("IdempotentCreate", func(t *testing.T) { testAcademicPeriodStoreIdempotentCreate(t, ss) })
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

func testAcademicPeriodStoreIdempotentCreate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	candidate := &model.AcademicPeriod{InstitutionID: institution.ID, Name: "idempotent-period", DisplayName: "Idempotent Period", StartsAt: model.TimeFromMillis(1000), EndsAt: model.TimeFromMillis(2000)}
	candidate.PrepareCreate(model.NewAcademicPeriodID(), model.NowUTC())
	command := &store.CommandIdempotency{UserID: user.ID, Operation: "academic_period.create.v1", KeyDigest: sha256.Sum256([]byte("key")), FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte("command")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
	firstAudit := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	first, err := ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: candidate, AuditEventID: firstAudit.ID.String(), AuditAt: model.GetMillis()}, command)
	requireNoError(t, err)
	if first.Replayed || first.Value.ID != candidate.ID {
		t.Fatalf("first result = %#v", first)
	}

	retryCandidate := *candidate
	retryCandidate.ID = model.NewAcademicPeriodID()
	secondAudit := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	second, err := ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: &retryCandidate, AuditEventID: secondAudit.ID.String(), AuditAt: model.GetMillis()}, command)
	requireNoError(t, err)
	if !second.Replayed || second.Value.ID != first.Value.ID {
		t.Fatalf("replay result = %#v, want %s", second, first.Value.ID)
	}
	completed, err := ss.Audit().Get(ctx, secondAudit.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("replay audit status = %q", completed.Status)
	}

	conflict := *command
	conflict.Fingerprint = sha256.Sum256([]byte("different"))
	conflictAudit := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	if _, err = ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: &retryCandidate, AuditEventID: conflictAudit.ID.String(), AuditAt: model.GetMillis()}, &conflict); err == nil {
		t.Fatal("different command reused the key")
	} else {
		var target *store.ErrIdempotencyConflict
		if !errors.As(err, &target) {
			t.Fatalf("conflict error = %v", err)
		}
	}
}

func testAcademicPeriodStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	createAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	candidate := &model.AcademicPeriod{
		InstitutionID: institution.ID,
		Name:          "audited-period",
		DisplayName:   "Audited Period",
		StartsAt:      model.TimeFromMillis(100),
		EndsAt:        model.TimeFromMillis(200),
	}
	candidate.PrepareCreate(model.AcademicPeriodID(model.NewId()), model.NowUTC())
	created, err := ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{Period: candidate, AuditEventID: createAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("create audit status = %q", completed.Status)
	}

	rolledBackCreate := &model.AcademicPeriod{
		InstitutionID: institution.ID,
		Name:          "rolled-back-period",
		DisplayName:   "Rolled Back",
		StartsAt:      model.TimeFromMillis(300),
		EndsAt:        model.TimeFromMillis(400),
	}
	rolledBackCreate.PrepareCreate(model.AcademicPeriodID(model.NewId()), model.NowUTC())
	if _, err := ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{Period: rolledBackCreate, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("Create() succeeded without its audit attempt")
	}
	if _, err := ss.AcademicPeriod().Get(ctx, rolledBackCreate.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("create survived audit rollback: %v", err)
	}

	updateAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	updatedCandidate := *created
	updatedCandidate.EndsAt = model.TimeFromMillis(250)
	updatedCandidate.PrepareUpdate(model.NowUTC())
	updated, err := ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &updatedCandidate, AuditEventID: updateAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	completed, err = ss.Audit().Get(ctx, updateAttempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("update audit status = %q", completed.Status)
	}
	staleAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	stale := *created
	stale.DisplayName = "Stale Period"
	stale.PrepareUpdate(model.NowUTC())
	if _, err := ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &stale, AuditEventID: staleAttempt.ID.String(), AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateWithAudit() error = %v, want conflict", err)
	}

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	if _, err := ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &rolledBack, AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("UpdateWithAudit() succeeded without its audit attempt")
	}
	persisted, err := ss.AcademicPeriod().Get(ctx, updated.ID.String())
	requireNoError(t, err)
	if persisted.DisplayName != updated.DisplayName {
		t.Fatalf("update survived audit rollback: %#v", persisted)
	}

	wrongOwner := *updated
	wrongOwner.InstitutionID = model.InstitutionID(model.NewId())
	wrongOwner.PrepareUpdate(model.NowUTC())
	wrongOwnerAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	if _, err := ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: &wrongOwner, AuditEventID: wrongOwnerAttempt.ID.String(), AuditAt: model.GetMillis()}); !store.IsNotFound(err) {
		t.Fatalf("ownership move error = %v, want not found", err)
	}

	archiveAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	archived, err := ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: updated.ID.String(), ArchiveAt: model.GetMillis(), AuditEventID: archiveAttempt.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if !archived.IsArchived() {
		t.Fatalf("ArchiveWithAudit() = %#v", archived)
	}

	archiveRollback := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "rolled-back-archive", 500)
	if _, err := ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: archiveRollback.ID.String(), ArchiveAt: model.GetMillis(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}); err == nil {
		t.Fatal("ArchiveWithAudit() succeeded without its audit attempt")
	}
	if _, err := ss.AcademicPeriod().Get(ctx, archiveRollback.ID.String()); err != nil {
		t.Fatalf("archive survived audit rollback: %v", err)
	}

	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "period-dependency-unit")
	programme := saveProgramme(t, ctx, ss, unit.ID.String(), "period-dependency-programme")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "period-dependency-level")
	withClass := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "period-with-class", 1_000)
	saveClass(t, ctx, ss, level.ID.String(), withClass.ID.String(), "period-dependency-class")
	blockedAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	if _, err := ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: withClass.ID.String(), ArchiveAt: model.GetMillis(), AuditEventID: blockedAttempt.ID.String(), AuditAt: model.GetMillis()}); !store.IsConflict(err) {
		t.Fatalf("archive with active class error = %v, want conflict", err)
	}
}

func saveAcademicPeriodAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, institutionID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID}, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID, Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return attempt
}

func testAcademicPeriodStoreAllowsInstitutionDefinedOverlap(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	first := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "year", 1_000)
	overlapping := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "semester", model.MillisFromTime(first.StartsAt)+1)
	adjacent := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "next", model.MillisFromTime(first.EndsAt))
	if overlapping.ID.IsZero() || adjacent.ID.IsZero() {
		t.Fatalf("overlap/adjacency not persisted: %#v %#v", overlapping, adjacent)
	}
}

func testAcademicPeriodStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(
		t, ctx, ss, institution.ID.String(), "distinct-summer-term", 2_000_000_000_000,
	)
	found, err := ss.AcademicPeriod().SearchByInstitution(
		ctx, institution.ID.String(), "summer", 10,
	)
	requireNoError(t, err)
	if len(found) != 1 || found[0].ID != period.ID {
		t.Fatalf("SearchByInstitution() = %#v", found)
	}
	archived, err := ss.AcademicPeriod().Archive(ctx, period.ID.String(), model.GetMillis())
	requireNoError(t, err)
	if !archived.IsArchived() {
		t.Fatalf("Archive() = %#v", archived)
	}
}

func testAcademicPeriodStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := &model.AcademicPeriod{
		InstitutionID: institution.ID,
		Name:          "2026-2027",
		DisplayName:   "Academic Year 2026-2027",
		Description:   "Primary academic year",
		StartsAt:      model.TimeFromMillis(1_800_000_000_000),
		EndsAt:        model.TimeFromMillis(1_830_000_000_000),
	}

	saved, err := ss.AcademicPeriod().Save(ctx, period)
	requireNoError(t, err)
	if !saved.ID.IsValid() {
		t.Fatalf("Save() id = %q", saved.ID)
	}
	if !period.ID.IsZero() {
		t.Fatalf("Save() mutated input id to %q", period.ID)
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
	period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	got, err := ss.AcademicPeriod().Get(ctx, period.ID.String())
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
	period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	got, err := ss.AcademicPeriod().GetByName(ctx, institution.ID.String(), period.Name)
	requireNoError(t, err)
	if got.ID != period.ID {
		t.Fatalf("GetByName() id = %q, want %q", got.ID, period.ID)
	}
	if _, err := ss.AcademicPeriod().GetByName(ctx, institution.ID.String(), "missing"); !store.IsNotFound(err) {
		t.Fatalf("GetByName(missing) error = %v, want not found", err)
	}
}

func testAcademicPeriodStoreListByInstitution(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	second := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2027-2028", 1_840_000_000_000)
	first := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	periods, err := ss.AcademicPeriod().ListByInstitution(ctx, institution.ID.String())
	requireNoError(t, err)
	if len(periods) != 2 || periods[0].ID != first.ID || periods[1].ID != second.ID {
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
	period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)
	createAt := period.CreatedAt
	previousUpdateAt := period.UpdatedAt

	period.Name = "2026-2027-revised"
	period.DisplayName = "Academic Year 2026-2027 Revised"
	period.EndsAt = period.EndsAt.Add(time.Second)
	updated, err := ss.AcademicPeriod().Update(ctx, period)
	requireNoError(t, err)
	if updated.Name != "2026-2027-revised" || !updated.EndsAt.Equal(period.EndsAt) {
		t.Fatalf("Update() = %#v", updated)
	}
	if !updated.CreatedAt.Equal(createAt) || updated.UpdatedAt.Before(previousUpdateAt) {
		t.Fatalf("Update() timestamps = %#v", updated)
	}

	missing := *updated
	missing.ID = model.AcademicPeriodID(model.NewId())
	_, err = ss.AcademicPeriod().Update(ctx, &missing)
	if !store.IsNotFound(err) {
		t.Fatalf("Update(missing) error = %v, want not found", err)
	}
}

func testAcademicPeriodStoreRejectUnknownInstitution(t *testing.T, ss store.Store) {
	ctx := context.Background()

	_, err := ss.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		InstitutionID: model.InstitutionID(model.NewId()),
		Name:          "2026-2027",
		DisplayName:   "Academic Year 2026-2027",
		StartsAt:      model.TimeFromMillis(1_800_000_000_000),
		EndsAt:        model.TimeFromMillis(1_830_000_000_000),
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
	saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	_, err := ss.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		InstitutionID: institution.ID,
		Name:          "2026-2027",
		DisplayName:   "Duplicate",
		StartsAt:      model.TimeFromMillis(1_900_000_000_000),
		EndsAt:        model.TimeFromMillis(1_930_000_000_000),
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) ||
		conflict.Constraint != "academic_periods_institution_id_name_key" {
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
		InstitutionID: model.InstitutionID(institutionID),
		Name:          name,
		DisplayName:   name,
		StartsAt:      model.TimeFromMillis(startAt),
		EndsAt:        model.TimeFromMillis(startAt + 30_000_000_000),
	})
	requireNoError(t, err)
	return period
}
