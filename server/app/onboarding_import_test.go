// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type onboardingImportClassStoreFake struct {
	store.ClassStore
	value *model.Class
	err   error
}

func (f onboardingImportClassStoreFake) Get(context.Context, model.ClassID) (*model.Class, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.value, nil
}

type onboardingImportPeriodStoreFake struct {
	store.AcademicPeriodStore
	value *model.AcademicPeriod
}

type onboardingImportUnitStoreFake struct {
	store.AcademicUnitStore
	value *model.AcademicUnit
}

func (f onboardingImportUnitStoreFake) Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error) {
	return f.value, nil
}
func (f onboardingImportUnitStoreFake) GetByName(_ context.Context, institutionID model.InstitutionID, name string) (*model.AcademicUnit, error) {
	if f.value == nil || institutionID != f.value.InstitutionID || name != f.value.Name {
		return nil, store.NewErrNotFound("academic_unit", name)
	}
	return f.value, nil
}

type onboardingImportRoleStoreFake struct {
	store.RoleStore
	value *model.Role
}

func (f onboardingImportRoleStoreFake) Get(context.Context, model.RoleID) (*model.Role, error) {
	return f.value, nil
}
func (f onboardingImportRoleStoreFake) GetByName(context.Context, string) (*model.Role, error) {
	return f.value, nil
}

type onboardingImportInstitutionStoreFake struct {
	store.InstitutionStore
	value *model.Institution
}

type onboardingImportPersistenceFake struct {
	store.OnboardingImportStore
	value *store.OnboardingImport
}

type onboardingImportUserStoreFake struct {
	store.UserStore
	value *model.User
}

func (f onboardingImportUserStoreFake) Get(_ context.Context, id model.UserID) (*model.User, error) {
	if f.value == nil || f.value.ID != id {
		return nil, store.NewErrNotFound("user", id.String())
	}
	return f.value, nil
}

func (f onboardingImportPersistenceFake) GetOnboardingImport(context.Context, model.OnboardingImportID) (*store.OnboardingImport, error) {
	return f.value, nil
}

func (f onboardingImportInstitutionStoreFake) Get(context.Context, model.InstitutionID) (*model.Institution, error) {
	return f.value, nil
}

func (f onboardingImportPeriodStoreFake) Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error) {
	return f.value, nil
}

func TestOnboardingImportCSVBuildsImmutableDuplicatePreview(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	period, err := model.NewAcademicPeriod(model.NewAcademicPeriodID(), model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		"2026", "2026", "", at, at.AddDate(1, 0, 0), at)
	require.NoError(t, err)
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), period.ID, "a", "A", "", at)
	require.NoError(t, err)
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, periods: onboardingImportPeriodStoreFake{value: period}, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ActorUserID: model.NewUserID()}
	csv := "\ufeffemail,reference,display_name,misspelled\r\n" +
		"same@example.edu,=formula,\"Student, One\",ignored\r\n" +
		"SAME@example.edu,row-2,Student Two,ignored\r\n" +
		"not-an-email,row-3,Invalid,ignored\r\n"
	rows, ignored, digest, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rows, 3, "preview")
	require.Len(t, ignored, 1, "preview")
	require.Equal(t, "misspelled", ignored[0], "preview")
	require.Len(t, digest, 64, "preview")
	require.Equal(t, model.OnboardingImportRowDuplicate, rows[0].PreviewStatus, "statuses")
	require.Equal(t, model.OnboardingImportRowDuplicate, rows[1].PreviewStatus, "statuses")
	require.Equal(t, model.OnboardingImportRowInvalid, rows[2].PreviewStatus, "statuses")
	require.Equal(t, "same@example.edu", rows[0].Email)
	require.Equal(t, class.Revision, rows[0].TargetRevision)
	require.Equal(t, string(InvitationBatchStudentClassCreate), rows[0].Operation)
	require.Equal(t, model.MillisFromTime(period.StartsAt), rows[0].StartsAt)
	require.Equal(t, "'=formula", escapeSpreadsheetFormula(rows[0].Reference))
	current.ID = model.NewOnboardingImportID()
	service.now = func() time.Time { return at.Add(time.Hour) }
	_, _, replayDigest, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
	require.NoError(t, err, "normalized preview digest")
	require.Equal(t, digest, replayDigest, "normalized preview digest")
}

func TestOnboardingImportCSVBuildsExistingUserAcademicPreview(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), model.NewAcademicPeriodID(), "a", "A", "", at)
	require.NoError(t, err)
	user := &model.User{ID: model.NewUserID(), Username: "existing", Email: "existing@example.edu"}
	authorization := &invitationAuthorizerFake{actionErrors: map[model.Action]error{}}
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, users: onboardingImportUserStoreFake{value: user}, authorization: authorization, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportAcademicAdministration,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), ActorUserID: model.NewUserID(), CreatedAt: at}
	csv := "operation,user_id,reference\nclass.enroll," + user.ID.String() + ",first\nclass.enroll," + user.ID.String() + ",second\n"
	rows, ignored, digest, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rows, 2, "academic administration preview")
	require.Empty(t, ignored, "academic administration preview")
	require.Len(t, digest, 64, "academic administration preview")
	require.Equal(t, string(AcademicAdministrationClassEnroll), rows[0].Operation, "academic administration preview")
	require.Equal(t, class.ID.String(), rows[0].ScopeID, "academic administration preview")
	require.Equal(t, class.Revision, rows[0].TargetRevision, "academic administration preview")
	require.Equal(t, user.ID, rows[0].UserID, "academic administration preview")
	require.Equal(t, at.UnixMilli(), rows[0].StartsAt, "academic administration preview")
	require.Equal(t, model.OnboardingImportRowDuplicate, rows[0].PreviewStatus, "academic administration preview")
	require.Equal(t, model.OnboardingImportRowDuplicate, rows[1].PreviewStatus, "academic administration preview")
	service.now = func() time.Time { return at.Add(time.Hour) }
	_, _, retryDigest, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
	require.NoError(t, err, "academic administration retry digest")
	require.Equal(t, digest, retryDigest, "academic administration retry digest")
}

func TestOnboardingImportCSVRejectsInvitationFieldsForAcademicAdministration(t *testing.T) {
	t.Parallel()
	service := &onboardingImportService{now: time.Now}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportAcademicAdministration,
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), ActorUserID: model.NewUserID()}
	for _, header := range []string{"email", "username", "display_name", "academic_unit", "class", "role"} {
		csv := "operation,user_id," + header + "\nuser.enable," + model.NewUserID().String() + ",ignored\n"
		_, _, _, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
		require.ErrorIs(t, err, errOnboardingImportInvalidFile, "header %q error", header)
	}
}

func TestOnboardingImportCSVPreservesDistinctAffiliationHistories(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	institution := &model.Institution{ID: model.NewInstitutionID(), Revision: 2}
	user := &model.User{ID: model.NewUserID(), Username: "existing", Email: "existing@example.edu"}
	service := &onboardingImportService{institutions: onboardingImportInstitutionStoreFake{value: institution},
		users: onboardingImportUserStoreFake{value: user}, authorization: &invitationAuthorizerFake{actionErrors: map[model.Action]error{}}, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportAcademicAdministration,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), ActorUserID: model.NewUserID(), CreatedAt: at}
	csv := "operation,user_id,affiliation_kind,start_at,reference\n" +
		"affiliation.add," + user.ID.String() + ",staff,2026-09-01T00:00:00Z,staff\n" +
		"affiliation.add," + user.ID.String() + ",teacher,2026-10-01T00:00:00Z,teacher\n"
	rows, _, _, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
	require.NoError(t, err, "distinct affiliation preview")
	require.Len(t, rows, 2, "distinct affiliation preview")
	require.Equal(t, model.OnboardingImportRowValid, rows[0].PreviewStatus, "distinct affiliation preview")
	require.Equal(t, model.OnboardingImportRowValid, rows[1].PreviewStatus, "distinct affiliation preview")
}

func TestOnboardingImportCSVRejectsUnsafeEncodingAndShape(t *testing.T) {
	t.Parallel()
	service := &onboardingImportService{now: time.Now}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ActorUserID: model.NewUserID()}
	for name, csv := range map[string]string{
		"nul":              "email\nstudent@example.edu\x00\n",
		"duplicate_header": "email,email\na@example.edu,b@example.edu\n",
		"uneven":           "email,reference\na@example.edu\n",
		"empty":            "email\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
			require.Error(t, err, "expected invalid CSV")
		})
	}
}

func TestOnboardingImportRetriesAcademicAdministrationMailOutages(t *testing.T) {
	t.Parallel()
	require.True(t, onboardingImportRetryablePublicCode("mail.unavailable"), "academic administration mail outage was classified as a terminal row failure")
}

func TestStudentProgressionPreviewRecoversUnknownCommittedCompletion(t *testing.T) {
	t.Parallel()
	id := model.NewOnboardingImportID()
	service := &onboardingImportService{imports: onboardingImportPersistenceFake{value: &store.OnboardingImport{
		ID: id, Mode: model.OnboardingImportStudentProgression, State: model.OnboardingImportPreviewReady,
	}}}
	require.NoError(t, service.previewProgression(context.Background(), id))
}

func TestStudentProgressionRosterInstantUsesEffectiveTimeForSamePeriod(t *testing.T) {
	t.Parallel()
	periodID := model.NewAcademicPeriodID()
	effectiveAt := time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)
	period := &model.AcademicPeriod{ID: periodID, EndsAt: effectiveAt.AddDate(0, 2, 0)}
	value := &store.OnboardingImport{SourcePeriodID: periodID, DestinationPeriodID: periodID, EffectiveAt: effectiveAt}
	got := studentProgressionRosterInstant(value, period)
	require.True(t, got.Equal(effectiveAt), "same-Period roster instant = %s", got)
	value.DestinationPeriodID = model.NewAcademicPeriodID()
	got, want := studentProgressionRosterInstant(value, period), period.EndsAt.Add(-time.Millisecond)
	require.True(t, got.Equal(want), "cross-Period roster instant = %s", got)
}

func TestStudentProgressionPreviewRejectsUnexecutableTransferAndMissingAffiliation(t *testing.T) {
	t.Parallel()
	effectiveAt := time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)
	member := &model.ClassMember{StartsAt: effectiveAt}
	require.True(t, studentProgressionTransferEffectiveDateConflict(member, effectiveAt), "transfer beginning at the effective instant was accepted")
	member.StartsAt = effectiveAt.Add(-time.Hour)
	member.EndsAt = model.OptionalTimeFrom(effectiveAt.Add(time.Hour))
	require.True(t, studentProgressionTransferEffectiveDateConflict(member, effectiveAt), "bounded source transfer was accepted")
	member.EndsAt = model.OptionalTime{}
	require.False(t, studentProgressionTransferEffectiveDateConflict(member, effectiveAt), "open earlier source transfer was rejected")
	require.False(t, studentProgressionEligibleAffiliation([]*model.Affiliation{{Kind: model.AffiliationStaff}}), "non-Student affiliation was accepted")
	require.True(t, studentProgressionEligibleAffiliation([]*model.Affiliation{{Kind: model.AffiliationStudent}}), "Student affiliation was rejected")
	require.False(t, studentProgressionEligibleAffiliation([]*model.Affiliation{{Kind: model.AffiliationStudent, EndsAt: model.OptionalTimeFrom(effectiveAt.Add(time.Hour))}}), "bounded Student affiliation was accepted")
}

func TestOnboardingImportInvalidBoundsRetainSafeTargetProjection(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	period, err := model.NewAcademicPeriod(model.NewAcademicPeriodID(), model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		"2026", "2026", "", at, at.AddDate(1, 0, 0), at)
	require.NoError(t, err)
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), period.ID, "a", "A", "", at)
	require.NoError(t, err)
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ActorUserID: model.NewUserID()}
	rows, _, _, err := (&onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, periods: onboardingImportPeriodStoreFake{value: period}, now: func() time.Time { return at }}).parseCSV(context.Background(), invocation.Invocation{}, current,
		strings.NewReader("email,start_at,end_at\nstudent@example.edu,200,100\n"))
	require.NoError(t, err, "invalid row projection")
	require.Len(t, rows, 1, "invalid row projection")
	require.Equal(t, model.OnboardingImportRowInvalid, rows[0].PreviewStatus, "invalid row projection")
	require.Equal(t, current.ScopeType, rows[0].ScopeType, "invalid row projection")
	require.Equal(t, current.ScopeID, rows[0].ScopeID, "invalid row projection")
}

func TestOnboardingImportInvalidDuplicatePoisonsValidTwin(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	period, err := model.NewAcademicPeriod(model.NewAcademicPeriodID(), model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		"2026", "2026", "", at, at.AddDate(1, 0, 0), at)
	require.NoError(t, err)
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), period.ID, "a", "A", "", at)
	require.NoError(t, err)
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, periods: onboardingImportPeriodStoreFake{value: period}, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ActorUserID: model.NewUserID()}
	rows, _, _, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(
		"email,start_at,end_at\nsame@example.edu,2026-08-21T00:00:00Z,2026-08-20T00:00:00Z\nsame@example.edu,,\n"))
	require.NoError(t, err, "duplicate preview")
	require.Len(t, rows, 2, "duplicate preview")
	require.Equal(t, model.OnboardingImportRowDuplicate, rows[0].PreviewStatus, "duplicate preview")
	require.Equal(t, model.OnboardingImportRowDuplicate, rows[1].PreviewStatus, "duplicate preview")
}

func TestOnboardingImportBoundsRequireRFC3339(t *testing.T) {
	t.Parallel()
	_, _, err := parseOnboardingImportBounds("1800000000000", "")
	require.Error(t, err, "Unix-millisecond CSV timestamp was accepted")
	start := "2026-08-20T09:00:00+02:00"
	got, _, err := parseOnboardingImportBounds(start, "")
	require.NoError(t, err, "RFC3339 bound")
	require.Equal(t, model.MillisFromTime(time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)), got, "RFC3339 bound")
}

func TestOnboardingImportCSVValidatesTeacherAndInstitutionRolePackages(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	institution, err := model.NewInstitution(model.NewInstitutionID(), "institution", "Institution", "", at)
	require.NoError(t, err)
	unit, err := model.NewAcademicUnit(model.NewAcademicUnitID(), institution.ID, "", "science", "Science", "", at)
	require.NoError(t, err)
	role := &model.Role{Name: "auditor", DisplayName: "Auditor", Permissions: []string{string(model.ActionAuditView)}}
	role.PrepareCreate(model.NewRoleID(), at)
	err = role.Validate()
	require.NoError(t, err)
	service := &onboardingImportService{units: onboardingImportUnitStoreFake{value: unit}, roles: onboardingImportRoleStoreFake{value: role},
		institutions: onboardingImportInstitutionStoreFake{value: institution}, authorization: &invitationAuthorizerFake{actionErrors: map[model.Action]error{}}, now: func() time.Time { return at }}

	teacher := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportTeacherAcademicUnit, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), RoleID: role.ID, ActorUserID: model.NewUserID()}
	rows, _, _, err := service.parseCSV(context.Background(), invocation.Invocation{}, teacher, strings.NewReader("email,timezone\nteacher@example.edu,Europe/Paris\n"))
	require.NoError(t, err, "teacher preview")
	require.Len(t, rows, 1, "teacher preview")
	require.Equal(t, model.OnboardingImportRowValid, rows[0].PreviewStatus, "teacher preview")
	require.Equal(t, string(InvitationBatchTeacherAcademicUnitCreate), rows[0].Operation, "teacher preview")
	require.Equal(t, role.ID, rows[0].RoleID, "teacher preview")
	require.Equal(t, "Europe/Paris", rows[0].Timezone, "teacher preview")
	require.Equal(t, model.MillisFromTime(at), rows[0].StartsAt, "teacher preview")

	institutionImport := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportInstitution, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), ActorUserID: model.NewUserID()}
	rows, _, _, err = service.parseCSV(context.Background(), invocation.Invocation{}, institutionImport, strings.NewReader("email,kind,role\nrole@example.edu,institution_role,auditor\n"))
	require.NoError(t, err, "institution Role preview")
	require.Len(t, rows, 1, "institution Role preview")
	require.Equal(t, model.OnboardingImportRowValid, rows[0].PreviewStatus, "institution Role preview")
	require.Equal(t, string(InvitationBatchInstitutionRoleCreate), rows[0].Operation, "institution Role preview")
	require.Equal(t, institution.ID.String(), rows[0].ScopeID, "institution Role preview")
	require.Equal(t, model.MillisFromTime(at), rows[0].StartsAt, "institution Role preview")
	role.Permissions = []string{string(model.ActionAcademicUnitView)}
	rows, _, _, err = service.parseCSV(context.Background(), invocation.Invocation{}, institutionImport,
		strings.NewReader("email,kind,academic_unit,role\nunit-role@example.edu,academic_unit_role,science,auditor\n"))
	require.NoError(t, err, "Institution Academic Unit preview")
	require.Len(t, rows, 1, "Institution Academic Unit preview")
	require.Equal(t, model.OnboardingImportRowValid, rows[0].PreviewStatus, "Institution Academic Unit preview")
	require.Equal(t, unit.ID.String(), rows[0].ScopeID, "Institution Academic Unit preview")
}

func TestOnboardingImportReportRequiresTerminalState(t *testing.T) {
	t.Parallel()
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), State: model.OnboardingImportPreviewReady,
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String()}
	service := &onboardingImportService{imports: onboardingImportPersistenceFake{value: current}, authorization: &invitationAuthorizerFake{}}
	err := service.OnboardingImportReport(context.Background(), invocation.Invocation{}, current.ID, &strings.Builder{})
	require.True(t, apperror.Is(err, "onboarding_import.conflict"), "preview report error = %v", err)
}

func TestOnboardingImportTransientRowCodesRemainRetryable(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"administration.unavailable", "authorization.unavailable", "audit.unavailable", "invitation.mail_unavailable", "invitation.unavailable"} {
		require.True(t, onboardingImportRetryablePublicCode(code))
	}
	require.False(t, onboardingImportRetryablePublicCode("invitation.conflict"), "semantic conflict was made retryable")
}

func TestOnboardingImportTargetReadFailuresRemainRetryable(t *testing.T) {
	t.Parallel()
	dependencyErr := errors.New("database unavailable")
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{err: dependencyErr}, now: time.Now}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: model.NewClassID().String(), ActorUserID: model.NewUserID()}
	_, _, _, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader("email\nstudent@example.edu\n"))
	require.True(t, onboardingImportRetryableError(err), "parse target error")
	row := store.OnboardingImportRow{Operation: string(InvitationBatchStudentClassCreate), ScopeType: model.RoleScopeClass, ScopeID: current.ScopeID}
	err = service.revalidateFrozenTarget(context.Background(), row)
	require.True(t, onboardingImportRetryableError(err), "execution target error")
}

func TestOnboardingImportReportEscapesEverySpreadsheetFormulaPrefix(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"=x", "+x", "-x", "@x", "\tx", "\rx"} {
		require.Equal(t, "'"+value, escapeSpreadsheetFormula(value), "escapeSpreadsheetFormula(%q)", value)
	}
	require.Equal(t, "safe", escapeSpreadsheetFormula("safe"))
}

func TestOnboardingImportExecutionJobResumesSafeTerminalCheckpointAcrossWorkers(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	id := model.NewOnboardingImportID()
	job, err := newOnboardingImportJob(model.JobTypeOnboardingImportExecute, id, at)
	require.NoError(t, err)
	job.Checkpoint = json.RawMessage(`{"after_row":41,"processed":37}`)
	service := &onboardingImportService{imports: onboardingImportPersistenceFake{value: &store.OnboardingImport{ID: id, State: model.OnboardingImportCompleted, ValidRows: 37}}}
	handler := jobhandler.NewOnboardingImportExecuteDescriptor(onboardingImportJobs{service: service}).Handler
	for worker := 0; worker < 2; worker++ {
		outcome := handler.Run(context.Background(), jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()}, func(context.Context, jobengine.CheckpointValue) error {
			require.FailNow(t, "terminal recovery must not write another checkpoint")
			return nil
		}, nil))
		require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "worker %d outcome = %#v", worker, outcome)
		require.Equal(t, `{"after_row":41,"processed":37}`, string(outcome.Result), "worker %d outcome = %#v", worker, outcome)
	}

	job.Checkpoint = json.RawMessage(`{"after_row":-1,"processed":37}`)
	outcome := handler.Run(context.Background(), jobengine.Execution{Job: job})
	require.Equal(t, jobengine.OutcomePermanentFailure, outcome.Kind)
	require.Equal(t, "job.checkpoint.invalid", outcome.PublicErrorCode)
}

func TestOnboardingImportCSVMarksControlCharacterNamesInvalid(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	period, err := model.NewAcademicPeriod(model.NewAcademicPeriodID(), model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		"2026", "2026", "", at, at.AddDate(1, 0, 0), at)
	require.NoError(t, err)
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), period.ID, "a", "A", "", at)
	require.NoError(t, err)
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, periods: onboardingImportPeriodStoreFake{value: period}, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ActorUserID: model.NewUserID()}
	csv := "email,display_name,first_name\r\n" +
		"one@example.edu,\"Student\r\nBcc: other@example.test\",One\r\n" +
		"two@example.edu,Student Two,\"Tab\tName\"\r\n" +
		"three@example.edu,Student Three,Three\r\n"
	rows, _, _, err := service.parseCSV(context.Background(), invocation.Invocation{}, current, strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, model.OnboardingImportRowInvalid, rows[0].PreviewStatus)
	require.Equal(t, model.OnboardingImportRowInvalid, rows[1].PreviewStatus)
	require.Equal(t, model.OnboardingImportRowValid, rows[2].PreviewStatus)
}
