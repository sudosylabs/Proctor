// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAffiliationStore(t *testing.T, ss store.Store) {
	t.Run("EndBeforeModification", func(t *testing.T) { testAffiliationEndBeforeModification(t, ss) })
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	start := model.GetMillis() + 1000
	saved, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID:   user.ID,
		Kind:     model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(start),
	})
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid())
	got, err := ss.Affiliation().Get(ctx, saved.ID)
	require.NoError(t, err)
	require.Equal(t, *saved, *got, "Get() = %#v, want %#v", got, saved)
	active, err := ss.Affiliation().ListActiveByUser(ctx, user.ID, start+1)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, saved.ID, active[0].ID)
	teacher, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(start),
	})
	require.NoError(t, err)
	active, err = ss.Affiliation().ListActiveByUser(ctx, user.ID, start+1)
	require.NoError(t, err)
	require.Len(t, active, 2)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start + 2),
	})
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	ended, err := EndAffiliation(ctx, ss, saved.ID, saved.Revision, start+10)
	require.NoError(t, err)
	require.Equal(t, start+10, ended.EndsAt.Millis())
	_, err = EndAffiliation(ctx, ss, saved.ID, saved.Revision, start+11)
	require.True(t, store.IsConflict(err), "stale End() error")
	active, err = ss.Affiliation().ListActiveByUser(ctx, user.ID, start+11)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, teacher.ID, active[0].ID)
	history, err := ss.Affiliation().ListByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, history, 2)
	next, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(start + 10),
	})
	require.NoError(t, err)
	require.NotEqual(t, saved.ID, next.ID)
	createAttempt := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), user.ID.String())
	candidate := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStaff, StartsAt: model.TimeFromMillis(start)}
	candidate.PrepareCreate(model.NewAffiliationID(), model.NowUTC())
	created, err := ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: candidate, AuditEventID: createAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	completedCreate, err := ss.Audit().Get(ctx, createAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completedCreate.Status)
	rolledBack := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationExternal, StartsAt: model.TimeFromMillis(start)}
	rolledBack.PrepareCreate(model.NewAffiliationID(), model.NowUTC())
	_, err = ss.Affiliation().Create(ctx, &store.AffiliationCreation{Affiliation: rolledBack, AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()})
	require.Error(t, err, "Create() succeeded without an audit attempt")
	_, err = ss.Affiliation().Get(ctx, rolledBack.ID)
	require.True(t, store.IsNotFound(err), "create survived audit rollback")
	endAttempt := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), user.ID.String())
	endedWithAudit, err := ss.Affiliation().End(ctx, &store.AffiliationEnd{ID: created.ID, ExpectedRevision: created.Revision, EndAt: start + 20, AuditEventID: endAttempt.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)
	require.Equal(t, created.Revision+1, endedWithAudit.Revision)
	require.Equal(t, start+20, endedWithAudit.EndsAt.Millis())
	completedEnd, err := ss.Audit().Get(ctx, endAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completedEnd.Status)
	staleAttempt := saveAffiliationAuditAttempt(t, ctx, ss, institution.ID.String(), user.ID.String())
	_, err = ss.Affiliation().End(ctx, &store.AffiliationEnd{ID: created.ID, ExpectedRevision: created.Revision, EndAt: start + 21, AuditEventID: staleAttempt.ID, AuditAt: model.GetMillis()})
	require.True(t, store.IsConflict(err), "stale End() error")
	unit, err := SaveAcademicUnit(ctx, ss, &model.AcademicUnit{InstitutionID: institution.ID, Name: "affiliation-unit", DisplayName: "Affiliation Unit"})
	require.NoError(t, err)
	programme, err := SaveProgramme(ctx, ss, &model.Programme{AcademicUnitID: unit.ID, Name: "affiliation-programme", DisplayName: "Affiliation Programme"})
	require.NoError(t, err)
	level, err := SaveProgrammeLevel(ctx, ss, &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "affiliation-level", DisplayName: "Affiliation Level"})
	require.NoError(t, err)
	period, err := SaveAcademicPeriod(ctx, ss, &model.AcademicPeriod{Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "affiliation-period", DisplayName: "Affiliation Period", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(start + 10_000)})
	require.NoError(t, err)
	class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "affiliation-class")
	enrolledUser := saveUser(t, ctx, ss)
	student, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: model.UserID(enrolledUser.ID.String()), Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(1)})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID:  class.ID,
		UserID:   model.UserID(enrolledUser.ID.String()),
		StartsAt: model.TimeFromMillis(start),
	})
	require.NoError(t, err)
	_, err = EndAffiliation(ctx, ss, student.ID, student.Revision, start+1)
	require.True(t, store.IsConflict(err), "End() with active enrollment error")
}

// testAffiliationEndBeforeModification ends a backdated Affiliation at an
// effective instant earlier than the row's creation, as a millisecond
// mutation instant or a trailing peer clock can.
func testAffiliationEndBeforeModification(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	now := model.GetMillis()
	saved, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: user.ID, Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(now - 2_000_000),
	})
	require.NoError(t, err)
	endAt := now - 1_000_000
	ended, err := EndAffiliation(ctx, ss, saved.ID, saved.Revision, endAt)
	require.NoError(t, err)
	require.Equal(t, endAt, ended.EndsAt.Millis(), "End(before creation) = %#v, created %#v", ended, saved)
	require.True(t, ended.UpdatedAt.Equal(saved.UpdatedAt), "End(before creation) = %#v, created %#v", ended, saved)
	stored, err := ss.Affiliation().Get(ctx, saved.ID)
	require.NoError(t, err)
	require.Equal(t, *ended, *stored, "Get() = %#v, want %#v", stored, ended)
}

func saveAffiliationAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, institutionID, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, ID: userID}, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID, Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return attempt
}
