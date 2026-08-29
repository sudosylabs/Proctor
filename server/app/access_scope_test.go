// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAccessScopeResolverRejectsArchivedAndIncompatibleResources(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.NewInstitutionID(), ArchivedAt: model.OptionalTimeFrom(time.Now())}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: institution},
		&accessAcademicUnitStoreFake{}, &accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resolver.resolve(context.Background(), model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}); !Is(err, "resource.not_found") {
		t.Fatalf("archived institution error = %v", err)
	}
	if _, err = resolver.resolve(context.Background(), model.Resource{Type: model.ResourceType("future"), ID: model.NewId()}); !Is(err, "authorization.request.invalid") {
		t.Fatalf("incompatible resource error = %v", err)
	}
}

func TestAccessScopeResolverRequiresAcademicPeriodReader(t *testing.T) {
	t.Parallel()

	_, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{}, &accessAcademicUnitStoreFake{}, &accessClassStoreFake{},
		&accessUserStoreFake{}, &accessClassMemberStoreFake{}, &accessExamAuthoringStoreFake{},
		&accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{}, nil,
	)
	if err == nil {
		t.Fatal("newAccessScopeResolver() accepted a nil Academic Period reader")
	}
}

func TestAccessScopeResolverMapsExamToExactAcademicUnit(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	rootID, unitID := model.NewAcademicUnitID(), model.NewAcademicUnitID()
	examID := model.NewExamID()
	exam, err := model.NewExam(examID, unitID, model.NewUserID(), model.NowUTC())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{unitID.String(): {
			{ID: unitID, InstitutionID: institutionID}, {ID: rootID, InstitutionID: institutionID},
		}}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{}, &accessExamAuthoringStoreFake{exam: exam}, &accessExamSittingStoreFake{},
		&accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.resolve(context.Background(), model.Resource{Type: model.ResourceExam, ID: examID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.institutionID != institutionID.String() || resolved.targetAcademicUnitID != unitID.String() || len(resolved.academicUnitID) != 2 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if scope, id := authorizationAuditScope(model.Resource{Type: model.ResourceExam, ID: examID.String()}, resolved); scope != model.RoleScopeAcademicUnit || id != unitID.String() {
		t.Fatalf("audit scope = %s/%s", scope, id)
	}
}

func TestAccessScopeResolverMapsAcademicPeriodOwnerScope(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	rootID, unitID := model.NewAcademicUnitID(), model.NewAcademicUnitID()
	periodID := model.NewAcademicPeriodID()
	period := &model.AcademicPeriod{
		ID: periodID, Owner: model.NewAcademicUnitAcademicPeriodOwner(unitID),
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), Revision: 1,
		Name: "period", DisplayName: "Period", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(2),
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{unitID.String(): {
			{ID: unitID, InstitutionID: institutionID}, {ID: rootID, InstitutionID: institutionID},
		}}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{period: period},
	)
	if err != nil {
		t.Fatal(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: periodID.String()}
	resolved, err := resolver.resolve(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.institutionID != institutionID.String() || resolved.targetAcademicUnitID != unitID.String() || len(resolved.academicUnitID) != 2 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if scope, id := authorizationAuditScope(resource, resolved); scope != model.RoleScopeAcademicUnit || id != unitID.String() {
		t.Fatalf("audit scope = %s/%s", scope, id)
	}
}

func TestAccessScopeResolverMapsProgrammeAndLevelToOwningSubtree(t *testing.T) {
	t.Parallel()

	institutionID := model.NewInstitutionID()
	rootID, unitID := model.NewAcademicUnitID(), model.NewAcademicUnitID()
	programmeID, levelID := model.NewProgrammeID(), model.NewProgrammeLevelID()
	now := model.NowUTC()
	programme := &model.Programme{
		ID: programmeID, AcademicUnitID: unitID, CreatedAt: now, UpdatedAt: now,
		Revision: 1, Name: "computing", DisplayName: "Computing",
	}
	level := &model.ProgrammeLevel{
		ID: levelID, ProgrammeID: programmeID, CreatedAt: now, UpdatedAt: now,
		Revision: 1, Name: "year-one", DisplayName: "Year One",
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			unitID.String(): {{ID: unitID, InstitutionID: institutionID}, {ID: rootID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolver.programmes = &accessProgrammeStoreFake{programme: programme}
	resolver.programmeLevels = &accessProgrammeLevelStoreFake{level: level}

	for _, resource := range []model.Resource{
		{Type: model.ResourceProgramme, ID: programmeID.String()},
		{Type: model.ResourceProgrammeLevel, ID: levelID.String()},
	} {
		resolved, resolveErr := resolver.resolve(context.Background(), resource)
		if resolveErr != nil {
			t.Fatalf("resolve %s: %v", resource.Type, resolveErr)
		}
		if resolved.institutionID != institutionID.String() ||
			resolved.targetAcademicUnitID != unitID.String() ||
			len(resolved.academicUnitID) != 2 {
			t.Fatalf("resolved %s = %#v", resource.Type, resolved)
		}
		if scope, id := authorizationAuditScope(resource, resolved); scope != model.RoleScopeAcademicUnit || id != unitID.String() {
			t.Fatalf("audit scope for %s = %s/%s", resource.Type, scope, id)
		}
	}
}

type accessAcademicPeriodStoreFake struct {
	period  *model.AcademicPeriod
	periods map[string]*model.AcademicPeriod
	gets    int
	err     error
}

func (s *accessAcademicPeriodStoreFake) Get(_ context.Context, id string) (*model.AcademicPeriod, error) {
	s.gets++
	if s.err != nil {
		return nil, s.err
	}
	if period := s.periods[id]; period != nil {
		return period, nil
	}
	if s.period == nil {
		return nil, store.NewErrNotFound("academic_period", "missing")
	}
	return s.period, nil
}

func TestAccessControlAcademicPeriodPreflightDeniesBeforeInspection(t *testing.T) {
	t.Parallel()

	institutionID := model.NewInstitutionID()
	unitID := model.NewAcademicUnitID()
	periods := &accessAcademicPeriodStoreFake{err: errors.New("academic period target must not be read")}
	units := &accessAcademicUnitStoreFake{err: errors.New("academic period owner must not be read")}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}}, units,
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{}, periods,
	)
	if err != nil {
		t.Fatal(err)
	}
	access, err := newAccessControlService(
		&accessRoleStoreFake{}, &accessRoleBindingStoreFake{}, resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.NowUTC(), ClientType: model.SessionClientWeb,
	}
	invocation := NewInvocation(principal, model.RequestMetadata{})
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: model.NewAcademicPeriodID().String()}
	if err := access.authorizeCurrentState(context.Background(), principal, model.ActionAcademicPeriodView, resource, invocation.RequestMetadata()); !Is(err, "authorization.denied") {
		t.Fatalf("target preflight error = %v, want authorization.denied", err)
	}
	period := &model.AcademicPeriod{
		ID: model.NewAcademicPeriodID(), Owner: model.NewAcademicUnitAcademicPeriodOwner(unitID),
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), Revision: 1,
		Name: "period", DisplayName: "Period", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(2),
	}
	if err := access.authorizeAcademicPeriodOwner(context.Background(), invocation, model.ActionAcademicPeriodManage, period); !Is(err, "authorization.denied") {
		t.Fatalf("owner preflight error = %v, want authorization.denied", err)
	}
	if periods.gets != 0 || units.listAncestors != 0 {
		t.Fatalf("preflight inspected target or owner: period gets=%d unit ancestor reads=%d", periods.gets, units.listAncestors)
	}
}

func TestAccessControlMembershipPreflightDeniesBeforeOpaqueTargetInspection(t *testing.T) {
	t.Parallel()

	institutionID := model.NewInstitutionID()
	classes := &accessClassStoreFake{}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}},
		&accessAcademicUnitStoreFake{}, classes, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	access, err := newAccessControlService(
		&accessRoleStoreFake{}, &accessRoleBindingStoreFake{}, resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: model.NowUTC(), ClientType: model.SessionClientWeb,
	}
	invocation := NewInvocation(principal, model.RequestMetadata{})
	if err := access.authorizeResourcePreflight(
		context.Background(), invocation, model.ActionClassMembersManage, model.ResourceClass,
	); !Is(err, "authorization.denied") {
		t.Fatalf("preflight error = %v, want authorization.denied", err)
	}
	if classes.gets != 0 {
		t.Fatalf("preflight inspected %d opaque class targets", classes.gets)
	}
}

func TestAccessControlAcademicPeriodScopeInheritanceAndInstitutionView(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	rootID, childID, siblingID := model.NewAcademicUnitID(), model.NewAcademicUnitID(), model.NewAcademicUnitID()
	unitPeriodID, siblingPeriodID, institutionPeriodID := model.NewAcademicPeriodID(), model.NewAcademicPeriodID(), model.NewAcademicPeriodID()
	now := model.NowUTC()
	periods := map[string]*model.AcademicPeriod{
		unitPeriodID.String(): {
			ID: unitPeriodID, Owner: model.NewAcademicUnitAcademicPeriodOwner(childID), CreatedAt: now, UpdatedAt: now, Revision: 1,
			Name: "unit", DisplayName: "Unit", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(2),
		},
		siblingPeriodID.String(): {
			ID: siblingPeriodID, Owner: model.NewAcademicUnitAcademicPeriodOwner(siblingID), CreatedAt: now, UpdatedAt: now, Revision: 1,
			Name: "sibling", DisplayName: "Sibling", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(2),
		},
		institutionPeriodID.String(): {
			ID: institutionPeriodID, Owner: model.NewInstitutionAcademicPeriodOwner(institutionID), CreatedAt: now, UpdatedAt: now, Revision: 1,
			Name: "institution", DisplayName: "Institution", StartsAt: model.TimeFromMillis(1), EndsAt: model.TimeFromMillis(2),
		},
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			rootID.String():    {{ID: rootID, InstitutionID: institutionID}},
			childID.String():   {{ID: childID, InstitutionID: institutionID}, {ID: rootID, InstitutionID: institutionID}},
			siblingID.String(): {{ID: siblingID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{periods: periods},
	)
	if err != nil {
		t.Fatal(err)
	}
	userID, roleID := model.NewUserID(), model.NewRoleID()
	access, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{{ID: roleID, Permissions: []string{string(model.ActionAcademicPeriodView), string(model.ActionAcademicPeriodManage)}}}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{{RoleID: roleID, UserID: userID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: rootID.String()}}},
		resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: now, ClientType: model.SessionClientWeb,
	}
	unitResource := model.Resource{Type: model.ResourceAcademicPeriod, ID: unitPeriodID.String()}
	if allowed, err := access.Can(context.Background(), principal, model.ActionAcademicPeriodView, unitResource); err != nil || !allowed {
		t.Fatalf("descendant view = %v, %v", allowed, err)
	}
	if allowed, err := access.Can(context.Background(), principal, model.ActionAcademicPeriodManage, unitResource); err != nil || !allowed {
		t.Fatalf("descendant manage = %v, %v", allowed, err)
	}
	siblingResource := model.Resource{Type: model.ResourceAcademicPeriod, ID: siblingPeriodID.String()}
	if allowed, err := access.Can(context.Background(), principal, model.ActionAcademicPeriodView, siblingResource); err != nil || allowed {
		t.Fatalf("cross-subtree view = %v, %v", allowed, err)
	}
	invocation := NewInvocation(principal, model.RequestMetadata{})
	if err := access.authorizeCurrentState(context.Background(), principal, model.ActionAcademicPeriodView, unitResource, invocation.RequestMetadata()); err != nil {
		t.Fatalf("preflight plus terminal descendant view = %v", err)
	}
	if err := access.authorizeCurrentState(context.Background(), principal, model.ActionAcademicPeriodView, siblingResource, invocation.RequestMetadata()); !Is(err, "authorization.denied") {
		t.Fatalf("preflight plus terminal cross-subtree view = %v", err)
	}
	institutionResource := model.Resource{Type: model.ResourceAcademicPeriod, ID: institutionPeriodID.String()}
	if allowed, err := access.Can(context.Background(), principal, model.ActionAcademicPeriodView, institutionResource); err != nil || !allowed {
		t.Fatalf("applicable institution view = %v, %v", allowed, err)
	}
	if allowed, err := access.Can(context.Background(), principal, model.ActionAcademicPeriodManage, institutionResource); err != nil || allowed {
		t.Fatalf("institution manage from unit = %v, %v", allowed, err)
	}
}

func TestAccessScopeResolverMapsExamSittingThroughOwningExam(t *testing.T) {
	t.Parallel()
	institutionID, unitID := model.NewInstitutionID(), model.NewAcademicUnitID()
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	exam, err := model.NewExam(examID, unitID, model.NewUserID(), model.NowUTC())
	if err != nil {
		t.Fatal(err)
	}
	sitting, err := model.NewExamSitting(sittingID, examID, model.NewExamRevisionID(), model.NewClassID(), model.NowUTC().Add(time.Hour), model.NowUTC().Add(2*time.Hour), model.NowUTC())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{unitID.String(): {{ID: unitID, InstitutionID: institutionID}}}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{exam: exam}, &accessExamSittingStoreFake{snapshot: &store.ExamSittingSnapshot{Sitting: sitting}},
		&accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}
	resolved, err := resolver.resolve(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.institutionID != institutionID.String() || resolved.targetAcademicUnitID != unitID.String() {
		t.Fatalf("resolved = %#v", resolved)
	}
	if scope, id := authorizationAuditScope(resource, resolved); scope != model.RoleScopeAcademicUnit || id != unitID.String() {
		t.Fatalf("audit scope = %s/%s", scope, id)
	}
}

func TestAccessScopeResolverMapsSubmissionToOwningAcademicUnit(t *testing.T) {
	t.Parallel()

	institutionID, unitID := model.NewInstitutionID(), model.NewAcademicUnitID()
	submissionID := model.NewSubmissionID()
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			unitID.String(): {{ID: unitID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{},
		&accessExamSubmissionStoreFake{authorization: &store.ExamSubmissionAuthorization{
			SubmissionID: submissionID, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
			AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(), AcademicUnitID: unitID,
		}},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	resource := model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}
	resolved, err := resolver.resolve(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.institutionID != institutionID.String() || resolved.targetAcademicUnitID != unitID.String() ||
		len(resolved.academicUnitID) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if scope, id := authorizationAuditScope(resource, resolved); scope != model.RoleScopeAcademicUnit || id != unitID.String() {
		t.Fatalf("audit scope = %s/%s", scope, id)
	}
}

func TestAccessControlGrantsExamViewThroughAcademicUnitScope(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	unitID, examID, userID, roleID := model.NewAcademicUnitID(), model.NewExamID(), model.NewUserID(), model.NewRoleID()
	exam, err := model.NewExam(examID, unitID, model.NewUserID(), model.NowUTC())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			unitID.String(): {{ID: unitID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{}, &accessExamAuthoringStoreFake{exam: exam}, &accessExamSittingStoreFake{},
		&accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	access, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{{ID: roleID, Permissions: []string{string(model.ActionExamView)}}}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{{RoleID: roleID, UserID: userID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String()}}},
		resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: userID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, SessionID: model.NewSessionID(),
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: model.NowUTC(), ClientType: model.SessionClientWeb,
	}
	allowed, err := access.Can(context.Background(), principal, model.ActionExamView, model.Resource{Type: model.ResourceExam, ID: examID.String()})
	if err != nil || !allowed {
		t.Fatalf("exam view = %v, %v", allowed, err)
	}
}

func TestAccessControlGrantsExamCreateOverrideThroughInstitutionScope(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	unitID, userID, roleID := model.NewAcademicUnitID(), model.NewUserID(), model.NewRoleID()
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			unitID.String(): {{ID: unitID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{}, &accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{},
		&accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	access, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{{ID: roleID, Permissions: []string{string(model.ActionExamCreateOverride)}}}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{{RoleID: roleID, UserID: userID, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String()}}},
		resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: userID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, SessionID: model.NewSessionID(),
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: model.NowUTC(), ClientType: model.SessionClientWeb,
	}
	allowed, err := access.Can(context.Background(), principal, model.ActionExamCreateOverride, model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()})
	if err != nil || !allowed {
		t.Fatalf("exam create override = %v, %v", allowed, err)
	}
}

func TestAccessScopeConstraintsAreBoundedAndRespectPATCeiling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	institutionID := model.NewInstitutionID()
	rootID := model.NewAcademicUnitID()
	childID := model.NewAcademicUnitID()
	roleID := model.RoleID(model.NewId())
	userID := model.NewUserID()
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			childID.String(): {{ID: rootID, InstitutionID: institutionID}, {ID: childID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{}, &accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{},
		&accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{{ID: roleID, Permissions: []string{string(model.ActionAcademicUnitView)}}}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{{RoleID: roleID, UserID: userID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: childID.String()}}},
		resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization.now = func() time.Time { return now }
	principal := model.Principal{
		UserID: userID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType:       model.CredentialPersonalAccessToken,
		AuthenticationMethod: "personal_access_token", ClientType: model.SessionClientCLI,
		CredentialScopes: []string{string(model.ActionAcademicUnitView)}, AcademicUnitID: rootID,
	}
	query, err := authorization.authorizedScopes(
		context.Background(), principal, model.ActionAcademicUnitView,
		model.ResourceAcademicUnit,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(query.AcademicUnitRootIDs) != 1 || query.AcademicUnitRootIDs[0] != childID.String() || query.InstitutionWide {
		t.Fatalf("query = %#v", query)
	}
	principal.CredentialScopes = []string{string(model.ActionClassView)}
	query, err = authorization.authorizedScopes(context.Background(), principal, model.ActionAcademicUnitView, model.ResourceAcademicUnit)
	if err != nil || query.InstitutionWide || len(query.AcademicUnitRootIDs) != 0 {
		t.Fatalf("ceiling query=%#v err=%v", query, err)
	}
	if _, err = authorization.authorizedScopes(context.Background(), principal, model.ActionAcademicUnitView, model.ResourceClass); !Is(err, "authorization.request.invalid") {
		t.Fatalf("compatibility error = %v", err)
	}
}

func TestUserVisibilityScopeKeepsRelationshipAndClassMemberAuthoritySeparate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	institutionID := model.NewInstitutionID()
	unitID := model.NewAcademicUnitID()
	userID := model.NewUserID()
	userViewRoleID, classMembersRoleID := model.NewRoleID(), model.NewRoleID()
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			unitID.String(): {{ID: unitID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{
			{ID: userViewRoleID, Permissions: []string{string(model.ActionUserView)}},
			{ID: classMembersRoleID, Permissions: []string{string(model.ActionClassMembersView)}},
		}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{
			{RoleID: userViewRoleID, UserID: userID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String()},
			{RoleID: classMembersRoleID, UserID: userID, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String()},
		}},
		resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization.now = func() time.Time { return now }
	principal := model.Principal{
		UserID: userID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, SessionID: model.NewSessionID(),
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: now, ClientType: model.SessionClientWeb,
	}

	visibility, err := authorization.userVisibilityScope(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if visibility.InstitutionWide || !visibility.ClassMemberInstitutionWide ||
		!reflect.DeepEqual(visibility.AcademicUnitRootIDs, []string{unitID.String()}) ||
		len(visibility.ClassMemberAcademicUnitRootIDs) != 0 || len(visibility.ClassIDs) != 0 ||
		visibility.ActiveAt != now.UnixMilli() {
		t.Fatalf("user visibility = %#v", visibility)
	}
}

func TestUserReadMatchesInstitutionWideClassMemberVisibility(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	institutionID := model.NewInstitutionID()
	viewerID, targetID := model.NewUserID(), model.NewUserID()
	roleID := model.NewRoleID()
	classID := model.NewClassID()
	users := &accessUserStoreFake{match: store.UserVisibilityMatch{
		ScopeType: model.RoleScopeClass, ScopeID: classID.String(),
	}}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}},
		&accessAcademicUnitStoreFake{}, &accessClassStoreFake{}, users, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{{
			ID: roleID, Permissions: []string{string(model.ActionClassMembersView)},
		}}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{{
			RoleID: roleID, UserID: viewerID, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(),
		}}},
		resolver, accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization.now = func() time.Time { return now }
	principal := model.Principal{
		UserID: viewerID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, SessionID: model.NewSessionID(),
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: now, ClientType: model.SessionClientWeb,
	}

	fullProfile, err := authorization.authorizeUserRead(
		context.Background(), NewInvocation(principal, model.RequestMetadata{}), targetID.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if fullProfile || users.matchCalls != 1 || !users.matchVisibility.ClassMemberInstitutionWide ||
		users.matchVisibility.InstitutionWide {
		t.Fatalf("fullProfile=%t calls=%d visibility=%#v", fullProfile, users.matchCalls, users.matchVisibility)
	}
}

func TestDelegationCeilingRequiresOwnedActionAndContainingScope(t *testing.T) {
	t.Parallel()

	institutionID := model.NewInstitutionID()
	rootID, childID, siblingID := model.NewAcademicUnitID(), model.NewAcademicUnitID(), model.NewAcademicUnitID()
	userID, roleID := model.NewUserID(), model.NewRoleID()
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: &model.Institution{ID: institutionID}},
		&accessAcademicUnitStoreFake{ancestors: map[string][]*model.AcademicUnit{
			rootID.String():    {{ID: rootID, InstitutionID: institutionID}},
			childID.String():   {{ID: childID, InstitutionID: institutionID}, {ID: rootID, InstitutionID: institutionID}},
			siblingID.String(): {{ID: siblingID, InstitutionID: institutionID}},
		}},
		&accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	access, err := newAccessControlService(
		&accessRoleStoreFake{roles: []*model.Role{{ID: roleID, Permissions: []string{
			string(model.ActionProgrammeManage), string(model.ActionRoleBindingManage),
		}}}},
		&accessRoleBindingStoreFake{bindings: []*model.RoleBinding{{
			UserID: userID, RoleID: roleID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: rootID.String(),
		}}},
		resolver,
		accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: userID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, SessionID: model.NewSessionID(),
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: model.NowUTC(), ClientType: model.SessionClientWeb,
	}

	for name, test := range map[string]struct {
		actions []string
		target  model.AcademicUnitID
		want    bool
	}{
		"ordinary descendant":  {[]string{string(model.ActionProgrammeManage)}, childID, true},
		"protected descendant": {[]string{string(model.ActionRoleBindingManage)}, childID, true},
		"protected same scope": {[]string{string(model.ActionRoleBindingManage)}, rootID, false},
		"sibling":              {[]string{string(model.ActionProgrammeManage)}, siblingID, false},
		"unowned action":       {[]string{string(model.ActionClassManage)}, childID, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, delegationErr := access.canDelegateActionsAtScope(
				context.Background(), principal, test.actions,
				model.RoleScopeAcademicUnit, test.target.String(),
			)
			if delegationErr != nil || got != test.want {
				t.Fatalf("delegation = %v, %v; want %v", got, delegationErr, test.want)
			}
		})
	}

	principal.CredentialType = model.CredentialPersonalAccessToken
	principal.SessionID = ""
	principal.AuthenticationMethod = "personal_access_token"
	principal.AuthenticationStrength = ""
	principal.AuthenticatedAt = time.Time{}
	principal.ClientType = model.SessionClientCLI
	principal.CredentialScopes = []string{string(model.ActionProgrammeManage)}
	principal.AcademicUnitID = rootID
	if allowed, delegationErr := access.canDelegateActionsAtScope(
		context.Background(), principal, []string{string(model.ActionProgrammeManage)},
		model.RoleScopeAcademicUnit, childID.String(),
	); delegationErr != nil || !allowed {
		t.Fatalf("PAT descendant delegation = %v, %v", allowed, delegationErr)
	}
	principal.CredentialScopes = []string{string(model.ActionClassView)}
	if allowed, delegationErr := access.canDelegateActionsAtScope(
		context.Background(), principal, []string{string(model.ActionProgrammeManage)},
		model.RoleScopeAcademicUnit, childID.String(),
	); delegationErr != nil || allowed {
		t.Fatalf("PAT action ceiling = %v, %v", allowed, delegationErr)
	}
}

func TestAccessScopeResolutionFailsClosedOnPersistenceFailure(t *testing.T) {
	t.Parallel()

	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{err: errors.New("database unavailable")},
		&accessAcademicUnitStoreFake{}, &accessClassStoreFake{}, &accessUserStoreFake{}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.resolve(context.Background(), model.Resource{Type: model.ResourceInstitution, ID: model.NewId()})
	if !Is(err, "authorization.unavailable") {
		t.Fatalf("error = %v, want authorization.unavailable", err)
	}
}

func TestAccessControlPreservesIntrinsicSelfRead(t *testing.T) {
	t.Parallel()

	institution := &model.Institution{ID: model.NewInstitutionID()}
	user := &model.User{
		ID: model.NewUserID(), Username: "self-reader", Email: "self@example.edu",
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), Revision: 1,
	}
	resolver, err := newAccessScopeResolver(
		&accessInstitutionStoreFake{institution: institution},
		&accessAcademicUnitStoreFake{}, &accessClassStoreFake{},
		&accessUserStoreFake{user: user}, &accessClassMemberStoreFake{},
		&accessExamAuthoringStoreFake{}, &accessExamSittingStoreFake{}, &accessExamSubmissionStoreFake{},
		&accessAcademicPeriodStoreFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	access, err := newAccessControlService(
		&accessRoleStoreFake{}, &accessRoleBindingStoreFake{}, resolver,
		accessDecisionAuditFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: user.ID, CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess,
		SessionID:      model.SessionID(model.NewId()), AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        model.NowUTC(), ClientType: model.SessionClientWeb,
	}
	resource := model.Resource{Type: model.ResourceUser, ID: user.ID.String()}
	allowed, err := access.Can(context.Background(), principal, model.ActionUserView, resource)
	if err != nil || !allowed {
		t.Fatalf("self read = %v, %v", allowed, err)
	}
	allowed, err = access.Can(context.Background(), principal, model.ActionUserManage, resource)
	if err != nil || allowed {
		t.Fatalf("self management = %v, %v", allowed, err)
	}
}

type accessInstitutionStoreFake struct {
	store.InstitutionStore
	institution *model.Institution
	err         error
}

func (s *accessInstitutionStoreFake) Get(context.Context, string) (*model.Institution, error) {
	return s.institution, s.err
}
func (s *accessInstitutionStoreFake) GetSingleton(context.Context) (*model.Institution, error) {
	return s.institution, s.err
}

type accessAcademicUnitStoreFake struct {
	store.AcademicUnitStore
	ancestors     map[string][]*model.AcademicUnit
	listAncestors int
	err           error
}

func (s *accessAcademicUnitStoreFake) ListAncestors(_ context.Context, id string) ([]*model.AcademicUnit, error) {
	s.listAncestors++
	return s.ancestors[id], s.err
}

type accessClassStoreFake struct {
	store.ClassStore
	gets int
}

func (s *accessClassStoreFake) Get(context.Context, string) (*model.Class, error) {
	s.gets++
	return nil, store.NewErrNotFound("class", "")
}

type accessProgrammeStoreFake struct {
	store.ProgrammeStore
	programme *model.Programme
}

func (s *accessProgrammeStoreFake) Get(context.Context, string) (*model.Programme, error) {
	if s.programme == nil {
		return nil, store.NewErrNotFound("programme", "")
	}
	return s.programme, nil
}

type accessProgrammeLevelStoreFake struct {
	store.ProgrammeLevelStore
	level *model.ProgrammeLevel
}

func (s *accessProgrammeLevelStoreFake) Get(context.Context, string) (*model.ProgrammeLevel, error) {
	if s.level == nil {
		return nil, store.NewErrNotFound("programme_level", "")
	}
	return s.level, nil
}

type accessUserStoreFake struct {
	store.UserStore
	user            *model.User
	match           store.UserVisibilityMatch
	matchVisibility store.UserVisibilityScope
	matchCalls      int
}

func (s *accessUserStoreFake) Get(context.Context, string) (*model.User, error) {
	if s.user == nil {
		return nil, store.NewErrNotFound("user", "")
	}
	return s.user, nil
}

func (s *accessUserStoreFake) MatchVisibility(_ context.Context, _ string, visibility store.UserVisibilityScope) (store.UserVisibilityMatch, error) {
	s.matchCalls++
	s.matchVisibility = visibility
	return s.match, nil
}

type accessClassMemberStoreFake struct{ store.ClassMemberStore }
type accessExamAuthoringStoreFake struct {
	store.ExamAuthoringStore
	exam *model.Exam
}

func (s *accessExamAuthoringStoreFake) Resolve(context.Context, model.ExamID) (*model.Exam, error) {
	return s.exam, nil
}

type accessExamSittingStoreFake struct {
	store.ExamSittingStore
	snapshot *store.ExamSittingSnapshot
}

type accessExamSubmissionStoreFake struct {
	store.ExamSubmissionStore
	authorization *store.ExamSubmissionAuthorization
}

func (s *accessExamSubmissionStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamSubmissionAuthorization, error) {
	if s.authorization == nil {
		return nil, store.NewErrNotFound("submission", "")
	}
	return s.authorization, nil
}

func (s *accessExamSittingStoreFake) Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	if s.snapshot == nil {
		return nil, store.NewErrNotFound("exam_sitting", "")
	}
	return s.snapshot, nil
}

type accessRoleStoreFake struct {
	store.RoleStore
	roles []*model.Role
}

func (s *accessRoleStoreFake) GetByIds(context.Context, []string) ([]*model.Role, error) {
	return s.roles, nil
}

type accessRoleBindingStoreFake struct {
	store.RoleBindingStore
	bindings []*model.RoleBinding
}

func (s *accessRoleBindingStoreFake) ListActiveByUser(context.Context, string, int64) ([]*model.RoleBinding, error) {
	return s.bindings, nil
}

type accessDecisionAuditFake struct{}

func (accessDecisionAuditFake) RecordAuthorizationDecision(context.Context, model.Principal, model.Action, model.Resource, model.RoleScopeType, string, model.RequestMetadata, bool) error {
	return nil
}

func (accessDecisionAuditFake) RecordUserSearchDecision(context.Context, model.Principal, model.Resource, model.RoleScopeType, string, model.RequestMetadata, bool) error {
	return nil
}
