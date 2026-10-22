// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/team_store.go. The
// suite receives the root store and verifies each AcademicPeriodStore operation
// so every future persistence adapter can reuse the same tests.

package storetest

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAcademicPeriodStore(t *testing.T, ss store.Store) {
	t.Run("IdempotentCreate", func(t *testing.T) { testAcademicPeriodStoreIdempotentCreate(t, ss) })
	t.Run("MutationAuditAtomicity", func(t *testing.T) { testAcademicPeriodStoreMutationAuditAtomicity(t, ss) })
	t.Run("AllowsInstitutionDefinedOverlap", func(t *testing.T) { testAcademicPeriodStoreAllowsInstitutionDefinedOverlap(t, ss) })
	t.Run("Save", func(t *testing.T) { testAcademicPeriodStoreSave(t, ss) })
	t.Run("Get", func(t *testing.T) { testAcademicPeriodStoreGet(t, ss) })
	t.Run("GetByOwnerName", func(t *testing.T) { testAcademicPeriodStoreGetByOwnerName(t, ss) })
	t.Run("ListVisible", func(t *testing.T) { testAcademicPeriodStoreListVisible(t, ss) })
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
	candidate := &model.AcademicPeriod{Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "idempotent-period", DisplayName: "Idempotent Period", StartsAt: model.TimeFromMillis(1000), EndsAt: model.TimeFromMillis(2000)}
	candidate.PrepareCreate(model.NewAcademicPeriodID(), model.NowUTC())
	command := &store.CommandIdempotency{UserID: user.ID, Operation: "academic_period.create.v1", KeyDigest: sha256.Sum256([]byte("key")), FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte("command")), OutcomeVersion: 1, Retention: time.Hour, Wait: time.Second}
	firstAudit := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	first, err := ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: candidate, AuditEventID: firstAudit.ID, AuditAt: model.GetMillis()}, command)
	require.NoError(t, err)
	require.False(t, first.Replayed)
	require.Equal(t, candidate.ID, first.Value.ID)

	retryCandidate := *candidate
	retryCandidate.ID = model.NewAcademicPeriodID()
	secondAudit := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	second, err := ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: &retryCandidate, AuditEventID: secondAudit.ID, AuditAt: model.GetMillis()}, command)
	require.NoError(t, err)
	require.True(t, second.Replayed, "replay result = %#v", second)
	require.Equal(t, first.Value.ID, second.Value.ID, "replay result = %#v", second)
	completed, err := ss.Audit().Get(ctx, secondAudit.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)

	conflict := *command
	conflict.Fingerprint = sha256.Sum256([]byte("different"))
	conflictAudit := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	if _, err = ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: &retryCandidate, AuditEventID: conflictAudit.ID, AuditAt: model.GetMillis()}, &conflict); err == nil {
		require.FailNow(t, "different command reused the key")
	} else {
		var target *store.ErrIdempotencyConflict
		require.ErrorAs(t, err, &target)
	}
}

func testAcademicPeriodStoreMutationAuditAtomicity(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	createAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	candidate := &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:        "audited-period",
		DisplayName: "Audited Period",
		StartsAt:    model.TimeFromMillis(100),
		EndsAt:      model.TimeFromMillis(200),
	}
	candidate.PrepareCreate(model.AcademicPeriodID(model.NewID()), model.NowUTC())
	created, err := ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{Period: candidate, AuditEventID: createAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	completed, err := ss.Audit().Get(ctx, createAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)

	rolledBackCreate := &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:        "rolled-back-period",
		DisplayName: "Rolled Back",
		StartsAt:    model.TimeFromMillis(300),
		EndsAt:      model.TimeFromMillis(400),
	}
	rolledBackCreate.PrepareCreate(model.AcademicPeriodID(model.NewID()), model.NowUTC())
	_, err = ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{Period: rolledBackCreate, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Create() succeeded without its audit attempt")
	_, err = ss.AcademicPeriod().Get(ctx, rolledBackCreate.ID)
	require.True(t, store.IsNotFound(err), "create survived audit rollback")

	updateAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	updatedCandidate := *created
	updatedCandidate.EndsAt = model.TimeFromMillis(250)
	updatedCandidate.PrepareUpdate(model.NowUTC())
	updated, err := ss.AcademicPeriod().Update(ctx, &store.AcademicPeriodUpdate{Period: &updatedCandidate, AuditEventID: updateAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	completed, err = ss.Audit().Get(ctx, updateAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)
	staleAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	stale := *created
	stale.DisplayName = "Stale Period"
	stale.PrepareUpdate(model.NowUTC())
	_, err = ss.AcademicPeriod().Update(ctx, &store.AcademicPeriodUpdate{Period: &stale, AuditEventID: staleAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "stale Update() error")

	rolledBack := *updated
	rolledBack.DisplayName = "Must Roll Back"
	rolledBack.PrepareUpdate(model.NowUTC())
	_, err = ss.AcademicPeriod().Update(ctx, &store.AcademicPeriodUpdate{Period: &rolledBack, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Update() succeeded without its audit attempt")
	persisted, err := ss.AcademicPeriod().Get(ctx, updated.ID)
	require.NoError(t, err)
	require.Equal(t, updated.DisplayName, persisted.DisplayName)

	wrongOwner := *updated
	wrongOwner.Owner = model.NewAcademicUnitAcademicPeriodOwner(model.NewAcademicUnitID())
	wrongOwner.PrepareUpdate(model.NowUTC())
	wrongOwnerAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	_, err = ss.AcademicPeriod().Update(ctx, &store.AcademicPeriodUpdate{Period: &wrongOwner, AuditEventID: wrongOwnerAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "ownership move error")

	archiveAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	archived, err := ss.AcademicPeriod().Archive(ctx, &store.AcademicPeriodArchive{ID: updated.ID, ArchiveAt: model.GetMillis(), AuditEventID: archiveAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.True(t, archived.IsArchived())

	archiveRollback := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "rolled-back-archive", 500)
	_, err = ss.AcademicPeriod().Archive(ctx, &store.AcademicPeriodArchive{ID: archiveRollback.ID, ArchiveAt: model.GetMillis(), AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Archive() succeeded without its audit attempt")
	_, err = ss.AcademicPeriod().Get(ctx, archiveRollback.ID)
	require.NoError(t, err)

	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "period-dependency-unit")
	programme := saveProgramme(t, ctx, ss, unit.ID.String(), "period-dependency-programme")
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "period-dependency-level")
	withClass := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "period-with-class", 1_000)
	saveClass(t, ctx, ss, level.ID.String(), withClass.ID.String(), "period-dependency-class")
	blockedAttempt := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	_, err = ss.AcademicPeriod().Archive(ctx, &store.AcademicPeriodArchive{ID: withClass.ID, ArchiveAt: model.GetMillis(), AuditEventID: blockedAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "archive with active class error")
}

func saveAcademicPeriodAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, institutionID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionInstitutionManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID}, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID, Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return attempt
}

func testAcademicPeriodStoreAllowsInstitutionDefinedOverlap(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	first := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "year", 1_000)
	overlapping := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "semester", model.MillisFromTime(first.StartsAt)+1)
	adjacent := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "next", model.MillisFromTime(first.EndsAt))
	require.False(t, overlapping.ID.IsZero(), "overlap/adjacency not persisted: %#v %#v", overlapping, adjacent)
	require.False(t, adjacent.ID.IsZero(), "overlap/adjacency not persisted: %#v %#v", overlapping, adjacent)
}

func testAcademicPeriodStoreSearchAndArchive(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(
		t, ctx, ss, institution.ID.String(), "distinct-summer-term", 2_000_000_000_000,
	)
	period.DisplayName = "Literal 50%_! Summer Term"
	period, err := UpdateAcademicPeriod(ctx, ss, period)
	require.NoError(t, err)
	found, err := ss.AcademicPeriod().ListVisible(
		ctx, store.AcademicPeriodVisibilityScope{InstitutionID: institution.ID, InstitutionWide: true}, store.AcademicPeriodListOptions{Term: "summer", Limit: 10},
	)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, period.ID, found[0].ID)
	visibility := store.AcademicPeriodVisibilityScope{InstitutionID: institution.ID, InstitutionWide: true}
	for _, query := range []string{"LITERAL 50%_!", "%", "_", "!"} {
		found, err = ss.AcademicPeriod().ListVisible(ctx, visibility, store.AcademicPeriodListOptions{Term: query, Limit: 10})
		require.NoError(t, err)
		require.Len(t, found, 1, "ListVisible(%q)", query)
		require.Equal(t, period.ID, found[0].ID, "ListVisible(%q)", query)
	}
	archived, err := ArchiveAcademicPeriod(ctx, ss, period.ID, model.GetMillis())
	require.NoError(t, err)
	require.True(t, archived.IsArchived())
}

func testAcademicPeriodStoreSave(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:        "2026-2027",
		DisplayName: "Academic Year 2026-2027",
		Description: "Primary academic year",
		StartsAt:    model.TimeFromMillis(1_800_000_000_000),
		EndsAt:      model.TimeFromMillis(1_830_000_000_000),
	}

	saved, err := SaveAcademicPeriod(ctx, ss, period)
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid())
	require.True(t, period.ID.IsZero())

	_, err = SaveAcademicPeriod(ctx, ss, saved)
	var invalid *store.ErrInvalidInput
	require.ErrorAs(t, err, &invalid, "second Save(saved) error")
}

func testAcademicPeriodStoreGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	got, err := ss.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	require.Equal(t, *period, *got, "Get() = %#v, want %#v", got, period)
	_, err = ss.AcademicPeriod().Get(ctx, model.NewAcademicPeriodID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")
}

func testAcademicPeriodStoreGetByOwnerName(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	period := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	owner := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	got, err := ss.AcademicPeriod().GetByOwnerName(ctx, owner, period.Name)
	require.NoError(t, err)
	require.Equal(t, period.ID, got.ID, "GetByOwnerName() id")
	_, err = ss.AcademicPeriod().GetByOwnerName(ctx, owner, "missing")
	require.True(t, store.IsNotFound(err), "GetByOwnerName(missing) error")
}

func testAcademicPeriodStoreListVisible(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	root := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "visible-root")
	child := saveAcademicUnit(t, ctx, ss, institution.ID.String(), root.ID.String(), "visible-child")
	sibling := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "hidden-root")
	institutionPeriod := saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "institution-period", 1_800_000_000_000)
	rootPeriod := saveAcademicUnitPeriod(t, ctx, ss, root.ID, "root-period", 1_810_000_000_000)
	childPeriod := saveAcademicUnitPeriod(t, ctx, ss, child.ID, "child-period", 1_820_000_000_000)
	saveAcademicUnitPeriod(t, ctx, ss, sibling.ID, "hidden-period", 1_830_000_000_000)

	periods, err := ss.AcademicPeriod().ListVisible(ctx, store.AcademicPeriodVisibilityScope{
		InstitutionID: institution.ID, AcademicUnitRootIDs: []model.AcademicUnitID{root.ID},
	}, store.AcademicPeriodListOptions{Limit: 100})
	require.NoError(t, err)
	require.Len(t, periods, 3)
	require.Equal(t, institutionPeriod.ID, periods[0].ID)
	require.Equal(t, rootPeriod.ID, periods[1].ID)
	require.Equal(t, childPeriod.ID, periods[2].ID)
	empty, err := ss.AcademicPeriod().ListVisible(ctx, store.AcademicPeriodVisibilityScope{
		InstitutionID: institution.ID,
	}, store.AcademicPeriodListOptions{Limit: 100})
	require.NoError(t, err)
	require.Empty(t, empty, "ListVisible(no grants)")
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
	updated, err := UpdateAcademicPeriod(ctx, ss, period)
	require.NoError(t, err)
	require.Equal(t, "2026-2027-revised", updated.Name)
	require.True(t, updated.EndsAt.Equal(period.EndsAt))
	require.True(t, updated.CreatedAt.Equal(createAt))
	require.False(t, updated.UpdatedAt.Before(previousUpdateAt))

	missing := *updated
	missing.ID = model.AcademicPeriodID(model.NewID())
	_, err = UpdateAcademicPeriod(ctx, ss, &missing)
	require.True(t, store.IsNotFound(err), "Update(missing) error")
}

func testAcademicPeriodStoreRejectUnknownInstitution(t *testing.T, ss store.Store) {
	ctx := context.Background()

	_, err := SaveAcademicPeriod(ctx, ss, &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(model.InstitutionID(model.NewID())),
		Name:        "2026-2027",
		DisplayName: "Academic Year 2026-2027",
		StartsAt:    model.TimeFromMillis(1_800_000_000_000),
		EndsAt:      model.TimeFromMillis(1_830_000_000_000),
	})
	var reference *store.ErrReference
	require.ErrorAs(t, err, &reference, "Save(unknown institution) error")
	require.Equal(t, "academic_periods_institution_id_fkey", reference.Constraint, "Save(unknown institution) error")
}

func testAcademicPeriodStoreEnforceInstitutionNameUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	saveAcademicPeriod(t, ctx, ss, institution.ID.String(), "2026-2027", 1_800_000_000_000)

	_, err := SaveAcademicPeriod(ctx, ss, &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(institution.ID),
		Name:        "2026-2027",
		DisplayName: "Duplicate",
		StartsAt:    model.TimeFromMillis(1_900_000_000_000),
		EndsAt:      model.TimeFromMillis(1_930_000_000_000),
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "duplicate academic period error")
	require.Equal(t, "academic_periods_institution_id_name_key", conflict.Constraint, "duplicate academic period error")
	firstUnit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "period-owner-one")
	secondUnit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "period-owner-two")
	saveAcademicUnitPeriod(t, ctx, ss, firstUnit.ID, "2026-2027", 1_900_000_000_000)
	saveAcademicUnitPeriod(t, ctx, ss, secondUnit.ID, "2026-2027", 1_900_000_000_000)
	_, err = SaveAcademicPeriod(ctx, ss, &model.AcademicPeriod{
		Owner: model.NewAcademicUnitAcademicPeriodOwner(firstUnit.ID), Name: "2026-2027", DisplayName: "Duplicate",
		StartsAt: model.TimeFromMillis(1_900_000_000_000), EndsAt: model.TimeFromMillis(1_930_000_000_000),
	})
	require.ErrorAs(t, err, &conflict, "duplicate Academic Unit owner name")
	require.Equal(t, "academic_periods_academic_unit_id_name_key", conflict.Constraint, "unit duplicate, want unit-scoped-name conflict")
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
	period, err := SaveAcademicPeriod(ctx, ss, &model.AcademicPeriod{
		Owner:       model.NewInstitutionAcademicPeriodOwner(model.InstitutionID(institutionID)),
		Name:        name,
		DisplayName: name,
		StartsAt:    model.TimeFromMillis(startAt),
		EndsAt:      model.TimeFromMillis(startAt + 30_000_000_000),
	})
	require.NoError(t, err)
	return period
}

func saveAcademicUnitPeriod(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	academicUnitID model.AcademicUnitID,
	name string,
	startAt int64,
) *model.AcademicPeriod {
	t.Helper()
	period, err := SaveAcademicPeriod(ctx, ss, &model.AcademicPeriod{
		Owner: model.NewAcademicUnitAcademicPeriodOwner(academicUnitID),
		Name:  name, DisplayName: name,
		StartsAt: model.TimeFromMillis(startAt), EndsAt: model.TimeFromMillis(startAt + 30_000_000_000),
	})
	require.NoError(t, err)
	return period
}
