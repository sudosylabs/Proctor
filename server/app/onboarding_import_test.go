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

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type onboardingImportClassStoreFake struct {
	store.ClassStore
	value *model.Class
	err   error
}

func (f onboardingImportClassStoreFake) Get(context.Context, string) (*model.Class, error) {
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

func (f onboardingImportUnitStoreFake) Get(context.Context, string) (*model.AcademicUnit, error) {
	return f.value, nil
}
func (f onboardingImportUnitStoreFake) GetByName(_ context.Context, institutionID, name string) (*model.AcademicUnit, error) {
	if f.value == nil || institutionID != f.value.InstitutionID.String() || name != f.value.Name {
		return nil, store.NewErrNotFound("academic_unit", name)
	}
	return f.value, nil
}

type onboardingImportRoleStoreFake struct {
	store.RoleStore
	value *model.Role
}

func (f onboardingImportRoleStoreFake) Get(context.Context, string) (*model.Role, error) {
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

func (f onboardingImportUserStoreFake) Get(_ context.Context, id string) (*model.User, error) {
	if f.value == nil || f.value.ID.String() != id {
		return nil, store.NewErrNotFound("user", id)
	}
	return f.value, nil
}

func (f onboardingImportPersistenceFake) GetOnboardingImport(context.Context, model.OnboardingImportID) (*store.OnboardingImport, error) {
	return f.value, nil
}

func (f onboardingImportInstitutionStoreFake) Get(context.Context, string) (*model.Institution, error) {
	return f.value, nil
}

func (f onboardingImportPeriodStoreFake) Get(context.Context, string) (*model.AcademicPeriod, error) {
	return f.value, nil
}

func TestOnboardingImportCSVBuildsImmutableDuplicatePreview(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	period, err := model.NewAcademicPeriod(model.NewAcademicPeriodID(), model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		"2026", "2026", "", at, at.AddDate(1, 0, 0), at)
	if err != nil {
		t.Fatal(err)
	}
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), period.ID, "a", "A", "", at)
	if err != nil {
		t.Fatal(err)
	}
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, periods: onboardingImportPeriodStoreFake{value: period}, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ActorUserID: model.NewUserID()}
	csv := "\ufeffemail,reference,display_name,misspelled\r\n" +
		"same@example.edu,=formula,\"Student, One\",ignored\r\n" +
		"SAME@example.edu,row-2,Student Two,ignored\r\n" +
		"not-an-email,row-3,Invalid,ignored\r\n"
	rows, ignored, digest, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || len(ignored) != 1 || ignored[0] != "misspelled" || len(digest) != 64 {
		t.Fatalf("preview = %#v, ignored=%v digest=%q", rows, ignored, digest)
	}
	if rows[0].PreviewStatus != model.OnboardingImportRowDuplicate || rows[1].PreviewStatus != model.OnboardingImportRowDuplicate || rows[2].PreviewStatus != model.OnboardingImportRowInvalid {
		t.Fatalf("statuses = %q, %q, %q", rows[0].PreviewStatus, rows[1].PreviewStatus, rows[2].PreviewStatus)
	}
	if rows[0].Email != "same@example.edu" || rows[0].TargetRevision != class.Revision || rows[0].Operation != string(InvitationBatchStudentClassCreate) ||
		rows[0].StartsAt != model.MillisFromTime(period.StartsAt) {
		t.Fatalf("normalized row = %#v", rows[0])
	}
	if got := escapeSpreadsheetFormula(rows[0].Reference); got != "'=formula" {
		t.Fatalf("escaped reference = %q", got)
	}
	current.ID = model.NewOnboardingImportID()
	service.now = func() time.Time { return at.Add(time.Hour) }
	_, _, replayDigest, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(csv))
	if err != nil || replayDigest != digest {
		t.Fatalf("normalized preview digest = %q, %v; want %q", replayDigest, err, digest)
	}
}

func TestOnboardingImportCSVBuildsExistingUserAcademicPreview(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), model.NewAcademicPeriodID(), "a", "A", "", at)
	if err != nil {
		t.Fatal(err)
	}
	user := &model.User{ID: model.NewUserID(), Username: "existing", Email: "existing@example.edu"}
	authorization := &invitationAuthorizerFake{actionErrors: map[model.Action]error{}}
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, users: onboardingImportUserStoreFake{value: user}, authorization: authorization, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportAcademicAdministration,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), ActorUserID: model.NewUserID(), CreatedAt: at}
	csv := "operation,user_id,reference\nclass.enroll," + user.ID.String() + ",first\nclass.enroll," + user.ID.String() + ",second\n"
	rows, ignored, digest, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(ignored) != 0 || len(digest) != 64 || rows[0].Operation != string(AcademicAdministrationClassEnroll) ||
		rows[0].ScopeID != class.ID.String() || rows[0].TargetRevision != class.Revision || rows[0].UserID != user.ID ||
		rows[0].StartsAt != at.UnixMilli() || rows[0].PreviewStatus != model.OnboardingImportRowDuplicate || rows[1].PreviewStatus != model.OnboardingImportRowDuplicate {
		t.Fatalf("academic administration preview = %#v ignored=%v digest=%q", rows, ignored, digest)
	}
	service.now = func() time.Time { return at.Add(time.Hour) }
	_, _, retryDigest, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(csv))
	if err != nil || retryDigest != digest {
		t.Fatalf("academic administration retry digest = %q, %v; want %q", retryDigest, err, digest)
	}
}

func TestOnboardingImportCSVRejectsInvitationFieldsForAcademicAdministration(t *testing.T) {
	t.Parallel()
	service := &onboardingImportService{now: time.Now}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportAcademicAdministration,
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(), ActorUserID: model.NewUserID()}
	for _, header := range []string{"email", "username", "display_name", "academic_unit", "class", "role"} {
		csv := "operation,user_id," + header + "\nuser.enable," + model.NewUserID().String() + ",ignored\n"
		if _, _, _, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(csv)); !errors.Is(err, errOnboardingImportInvalidFile) {
			t.Fatalf("header %q error = %v, want invalid file", header, err)
		}
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
	rows, _, _, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(csv))
	if err != nil || len(rows) != 2 || rows[0].PreviewStatus != model.OnboardingImportRowValid || rows[1].PreviewStatus != model.OnboardingImportRowValid {
		t.Fatalf("distinct affiliation preview=%#v error=%v", rows, err)
	}
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
			if _, _, _, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(csv)); err == nil {
				t.Fatal("expected invalid CSV")
			}
		})
	}
}

func TestOnboardingImportRetriesAcademicAdministrationMailOutages(t *testing.T) {
	t.Parallel()
	if !onboardingImportRetryablePublicCode("mail.unavailable") {
		t.Fatal("academic administration mail outage was classified as a terminal row failure")
	}
}

func TestStudentProgressionPreviewRecoversUnknownCommittedCompletion(t *testing.T) {
	t.Parallel()
	id := model.NewOnboardingImportID()
	service := &onboardingImportService{imports: onboardingImportPersistenceFake{value: &store.OnboardingImport{
		ID: id, Mode: model.OnboardingImportStudentProgression, State: model.OnboardingImportPreviewReady,
	}}}
	if err := service.previewProgression(context.Background(), id); err != nil {
		t.Fatalf("recovered preview completion = %v", err)
	}
}

func TestStudentProgressionRosterInstantUsesEffectiveTimeForSamePeriod(t *testing.T) {
	t.Parallel()
	periodID := model.NewAcademicPeriodID()
	effectiveAt := time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)
	period := &model.AcademicPeriod{ID: periodID, EndsAt: effectiveAt.AddDate(0, 2, 0)}
	value := &store.OnboardingImport{SourcePeriodID: periodID, DestinationPeriodID: periodID, EffectiveAt: effectiveAt}
	if got := studentProgressionRosterInstant(value, period); !got.Equal(effectiveAt) {
		t.Fatalf("same-Period roster instant = %s; want %s", got, effectiveAt)
	}
	value.DestinationPeriodID = model.NewAcademicPeriodID()
	if got, want := studentProgressionRosterInstant(value, period), period.EndsAt.Add(-time.Millisecond); !got.Equal(want) {
		t.Fatalf("cross-Period roster instant = %s; want %s", got, want)
	}
}

func TestStudentProgressionPreviewRejectsUnexecutableTransferAndMissingAffiliation(t *testing.T) {
	t.Parallel()
	effectiveAt := time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)
	member := &model.ClassMember{StartsAt: effectiveAt}
	if !studentProgressionTransferEffectiveDateConflict(member, effectiveAt) {
		t.Fatal("transfer beginning at the effective instant was accepted")
	}
	member.StartsAt = effectiveAt.Add(-time.Hour)
	member.EndsAt = model.OptionalTimeFrom(effectiveAt.Add(time.Hour))
	if !studentProgressionTransferEffectiveDateConflict(member, effectiveAt) {
		t.Fatal("bounded source transfer was accepted")
	}
	member.EndsAt = model.OptionalTime{}
	if studentProgressionTransferEffectiveDateConflict(member, effectiveAt) {
		t.Fatal("open earlier source transfer was rejected")
	}
	if studentProgressionEligibleAffiliation([]*model.Affiliation{{Kind: model.AffiliationStaff}}) {
		t.Fatal("non-Student affiliation was accepted")
	}
	if !studentProgressionEligibleAffiliation([]*model.Affiliation{{Kind: model.AffiliationStudent}}) {
		t.Fatal("Student affiliation was rejected")
	}
	if studentProgressionEligibleAffiliation([]*model.Affiliation{{Kind: model.AffiliationStudent, EndsAt: model.OptionalTimeFrom(effectiveAt.Add(time.Hour))}}) {
		t.Fatal("bounded Student affiliation was accepted")
	}
}

func TestOnboardingImportInvalidBoundsRetainSafeTargetProjection(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	period, err := model.NewAcademicPeriod(model.NewAcademicPeriodID(), model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		"2026", "2026", "", at, at.AddDate(1, 0, 0), at)
	if err != nil {
		t.Fatal(err)
	}
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), period.ID, "a", "A", "", at)
	if err != nil {
		t.Fatal(err)
	}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ActorUserID: model.NewUserID()}
	rows, _, _, err := (&onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, periods: onboardingImportPeriodStoreFake{value: period}, now: func() time.Time { return at }}).parseCSV(context.Background(), Invocation{}, current,
		strings.NewReader("email,start_at,end_at\nstudent@example.edu,200,100\n"))
	if err != nil || len(rows) != 1 || rows[0].PreviewStatus != model.OnboardingImportRowInvalid || rows[0].ScopeType != current.ScopeType || rows[0].ScopeID != current.ScopeID {
		t.Fatalf("invalid row projection = %#v, %v", rows, err)
	}
}

func TestOnboardingImportInvalidDuplicatePoisonsValidTwin(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	period, err := model.NewAcademicPeriod(model.NewAcademicPeriodID(), model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		"2026", "2026", "", at, at.AddDate(1, 0, 0), at)
	if err != nil {
		t.Fatal(err)
	}
	class, err := model.NewClass(model.NewClassID(), model.NewProgrammeLevelID(), period.ID, "a", "A", "", at)
	if err != nil {
		t.Fatal(err)
	}
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{value: class}, periods: onboardingImportPeriodStoreFake{value: period}, now: func() time.Time { return at }}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ActorUserID: model.NewUserID()}
	rows, _, _, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader(
		"email,start_at,end_at\nsame@example.edu,2026-08-21T00:00:00Z,2026-08-20T00:00:00Z\nsame@example.edu,,\n"))
	if err != nil || len(rows) != 2 || rows[0].PreviewStatus != model.OnboardingImportRowDuplicate || rows[1].PreviewStatus != model.OnboardingImportRowDuplicate {
		t.Fatalf("duplicate preview = %#v, %v", rows, err)
	}
}

func TestOnboardingImportBoundsRequireRFC3339(t *testing.T) {
	t.Parallel()
	if _, _, err := parseOnboardingImportBounds("1800000000000", ""); err == nil {
		t.Fatal("Unix-millisecond CSV timestamp was accepted")
	}
	start := "2026-08-20T09:00:00+02:00"
	got, _, err := parseOnboardingImportBounds(start, "")
	if err != nil || got != model.MillisFromTime(time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC3339 bound = %d, %v", got, err)
	}
}

func TestOnboardingImportCSVValidatesTeacherAndInstitutionRolePackages(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	institution, err := model.NewInstitution(model.NewInstitutionID(), "institution", "Institution", "", at)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := model.NewAcademicUnit(model.NewAcademicUnitID(), institution.ID, "", "science", "Science", "", at)
	if err != nil {
		t.Fatal(err)
	}
	role := &model.Role{Name: "auditor", DisplayName: "Auditor", Permissions: []string{string(model.ActionAuditView)}}
	role.PrepareCreate(model.NewRoleID(), at)
	if err = role.Validate(); err != nil {
		t.Fatal(err)
	}
	service := &onboardingImportService{units: onboardingImportUnitStoreFake{value: unit}, roles: onboardingImportRoleStoreFake{value: role},
		institutions: onboardingImportInstitutionStoreFake{value: institution}, authorization: &invitationAuthorizerFake{actionErrors: map[model.Action]error{}}, now: func() time.Time { return at }}

	teacher := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportTeacherAcademicUnit, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), RoleID: role.ID, ActorUserID: model.NewUserID()}
	rows, _, _, err := service.parseCSV(context.Background(), Invocation{}, teacher, strings.NewReader("email,timezone\nteacher@example.edu,Europe/Paris\n"))
	if err != nil || len(rows) != 1 || rows[0].PreviewStatus != model.OnboardingImportRowValid || rows[0].Operation != string(InvitationBatchTeacherAcademicUnitCreate) || rows[0].RoleID != role.ID || rows[0].Timezone != "Europe/Paris" || rows[0].StartsAt != model.MillisFromTime(at) {
		t.Fatalf("teacher preview = %#v, %v", rows, err)
	}

	institutionImport := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportInstitution, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), ActorUserID: model.NewUserID()}
	rows, _, _, err = service.parseCSV(context.Background(), Invocation{}, institutionImport, strings.NewReader("email,kind,role\nrole@example.edu,institution_role,auditor\n"))
	if err != nil || len(rows) != 1 || rows[0].PreviewStatus != model.OnboardingImportRowValid || rows[0].Operation != string(InvitationBatchInstitutionRoleCreate) || rows[0].ScopeID != institution.ID.String() || rows[0].StartsAt != model.MillisFromTime(at) {
		t.Fatalf("institution Role preview = %#v, %v", rows, err)
	}
	role.Permissions = []string{string(model.ActionAcademicUnitView)}
	rows, _, _, err = service.parseCSV(context.Background(), Invocation{}, institutionImport,
		strings.NewReader("email,kind,academic_unit,role\nunit-role@example.edu,academic_unit_role,science,auditor\n"))
	if err != nil || len(rows) != 1 || rows[0].PreviewStatus != model.OnboardingImportRowValid || rows[0].ScopeID != unit.ID.String() {
		t.Fatalf("Institution Academic Unit preview = %#v, %v", rows, err)
	}
}

func TestOnboardingImportReportRequiresTerminalState(t *testing.T) {
	t.Parallel()
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), State: model.OnboardingImportPreviewReady,
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String()}
	service := &onboardingImportService{imports: onboardingImportPersistenceFake{value: current}, authorization: &invitationAuthorizerFake{}}
	if err := service.Report(context.Background(), Invocation{}, current.ID.String(), &strings.Builder{}); !Is(err, "onboarding_import.conflict") {
		t.Fatalf("preview report error = %v", err)
	}
}

func TestOnboardingImportTransientRowCodesRemainRetryable(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"administration.unavailable", "authorization.unavailable", "audit.unavailable", "invitation.mail_unavailable", "invitation.unavailable"} {
		if !onboardingImportRetryablePublicCode(code) {
			t.Fatalf("%q was terminalized", code)
		}
	}
	if onboardingImportRetryablePublicCode("invitation.conflict") {
		t.Fatal("semantic conflict was made retryable")
	}
}

func TestOnboardingImportTargetReadFailuresRemainRetryable(t *testing.T) {
	t.Parallel()
	dependencyErr := errors.New("database unavailable")
	service := &onboardingImportService{classes: onboardingImportClassStoreFake{err: dependencyErr}, now: time.Now}
	current := &store.OnboardingImport{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentClass, ScopeType: model.RoleScopeClass,
		ScopeID: model.NewClassID().String(), ActorUserID: model.NewUserID()}
	_, _, _, err := service.parseCSV(context.Background(), Invocation{}, current, strings.NewReader("email\nstudent@example.edu\n"))
	if !onboardingImportRetryableError(err) {
		t.Fatalf("parse target error = %v", err)
	}
	row := store.OnboardingImportRow{Operation: string(InvitationBatchStudentClassCreate), ScopeType: model.RoleScopeClass, ScopeID: current.ScopeID}
	if err = service.revalidateFrozenTarget(context.Background(), row); !onboardingImportRetryableError(err) {
		t.Fatalf("execution target error = %v", err)
	}
}

func TestOnboardingImportReportEscapesEverySpreadsheetFormulaPrefix(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"=x", "+x", "-x", "@x", "\tx", "\rx"} {
		if escaped := escapeSpreadsheetFormula(value); escaped != "'"+value {
			t.Fatalf("escapeSpreadsheetFormula(%q) = %q", value, escaped)
		}
	}
	if escaped := escapeSpreadsheetFormula("safe"); escaped != "safe" {
		t.Fatalf("safe = %q", escaped)
	}
}

func TestOnboardingImportExecutionJobResumesSafeTerminalCheckpointAcrossWorkers(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	id := model.NewOnboardingImportID()
	job, err := newOnboardingImportJob(model.JobTypeOnboardingImportExecute, id, at)
	if err != nil {
		t.Fatal(err)
	}
	job.CheckpointVersion = 1
	job.Checkpoint = json.RawMessage(`{"after_row":41,"processed":37}`)
	service := &onboardingImportService{imports: onboardingImportPersistenceFake{value: &store.OnboardingImport{ID: id, State: model.OnboardingImportCompleted, ValidRows: 37}}}
	handler := appjobs.NewOnboardingImportExecuteDescriptor(onboardingImportJobs{service: service}).Handler
	for worker := 0; worker < 2; worker++ {
		outcome := handler.Run(context.Background(), jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()}, func(context.Context, jobengine.CheckpointValue) error {
			t.Fatal("terminal recovery must not write another checkpoint")
			return nil
		}, nil))
		if outcome.Kind != jobengine.OutcomeSucceeded || outcome.ResultVersion != 1 || string(outcome.Result) != `{"after_row":41,"processed":37}` {
			t.Fatalf("worker %d outcome = %#v", worker, outcome)
		}
	}

	job.Checkpoint = json.RawMessage(`{"after_row":-1,"processed":37}`)
	outcome := handler.Run(context.Background(), jobengine.Execution{Job: job})
	if outcome.Kind != jobengine.OutcomePermanentFailure || outcome.PublicErrorCode != "job.checkpoint.invalid" {
		t.Fatalf("invalid checkpoint outcome = %#v", outcome)
	}
}
