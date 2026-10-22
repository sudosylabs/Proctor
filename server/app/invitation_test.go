// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type invitationStoreFake struct {
	store.InvitationStore
	invitation       *model.Invitation
	issued           *store.StudentClassInvitationIssue
	accepted         *store.StudentClassInvitationAcceptance
	teacherIssued    *store.TeacherAcademicUnitInvitationIssue
	teacherAccepted  *store.TeacherAcademicUnitInvitationAcceptance
	scopedIssued     *store.ScopedRoleInvitationIssue
	scopedAccepted   *store.ScopedRoleInvitationAcceptance
	scopedAcceptErr  error
	listOptions      store.InvitationListOptions
	resent           *store.InvitationResend
	revoked          *store.InvitationRevocation
	replaced         *store.InvitationReplacement
	externalAccepted *store.ExternalIdentityInvitationAcceptance
	events           *[]string
	idempotentIssues map[[32]byte]*model.Invitation
	batchDuplicates  map[[32]byte]bool
	issueExecutions  int
	onboardingNoOp   bool
}

func (f *invitationStoreFake) ResolveOnboardingInvitationNoOp(_ context.Context, candidate *model.Invitation) (*model.Invitation, bool, error) {
	if f.onboardingNoOp {
		return candidate, true, nil
	}
	return nil, false, nil
}

func (f *invitationStoreFake) RecordBatchDuplicate(_ context.Context, _ *store.InvitationBatchDuplicate, command *store.CommandIdempotency) (*store.InvitationBatchCommandResult, error) {
	if invitation := f.idempotentIssues[command.KeyDigest]; invitation != nil {
		return &store.InvitationBatchCommandResult{InvitationID: invitation.ID, Replayed: true}, nil
	}
	if f.batchDuplicates == nil {
		f.batchDuplicates = make(map[[32]byte]bool)
	}
	replayed := f.batchDuplicates[command.KeyDigest]
	f.batchDuplicates[command.KeyDigest] = true
	return &store.InvitationBatchCommandResult{Duplicate: true, Replayed: replayed}, nil
}
func (f *invitationStoreFake) ReplayIssue(_ context.Context, _ *store.InvitationReplay, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	if f.batchDuplicates[command.KeyDigest] {
		return &store.InvitationCommandResult{Duplicate: true, Replayed: true}, nil
	}
	if invitation := f.idempotentIssues[command.KeyDigest]; invitation != nil {
		return &store.InvitationCommandResult{Invitation: invitation, Replayed: true}, nil
	}
	return nil, store.NewErrNotFound("command_outcome", "idempotency_key")
}
func (f *invitationStoreFake) FindCommandOutcome(ctx context.Context, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	return f.ReplayIssue(ctx, &store.InvitationReplay{}, command)
}

func (f *invitationStoreFake) IssueStudentClass(_ context.Context, input *store.StudentClassInvitationIssue) (*model.Invitation, error) {
	f.issued = input
	f.invitation = input.Invitation
	return input.Invitation, nil
}
func (f *invitationStoreFake) IssueStudentClassIdempotently(_ context.Context, input *store.StudentClassInvitationIssue, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	f.issued = input
	if f.onboardingNoOp && input.Occurrence == nil && input.Delivery == nil && input.DeliveryJob == nil {
		return &store.InvitationCommandResult{Invitation: input.Invitation, NoOp: true}, nil
	}
	if f.idempotentIssues == nil {
		f.idempotentIssues = make(map[[32]byte]*model.Invitation)
	}
	if existing := f.idempotentIssues[command.KeyDigest]; existing != nil {
		return &store.InvitationCommandResult{Invitation: existing, Replayed: true}, nil
	}
	f.issueExecutions++
	f.invitation = input.Invitation
	f.idempotentIssues[command.KeyDigest] = input.Invitation
	return &store.InvitationCommandResult{Invitation: input.Invitation}, nil
}

func TestOnboardingInvitationNoOpDoesNotRequireMail(t *testing.T) {
	t.Parallel()
	now := model.TimeFromMillis(1_800_000_000_000)
	periodID, classID := model.NewAcademicPeriodID(), model.NewClassID()
	persistence := &invitationStoreFake{onboardingNoOp: true}
	service := newInvitationServiceForTest(t, persistence,
		invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, &invitationAuthorizerFake{}, &invitationMailPreparerFake{disabled: true}, now)
	service.deps.Classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.deps.Periods = invitationPeriodStoreFake{period: &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour), EndsAt: now.Add(24 * time.Hour)}}
	view, err := service.IssueStudentClassInvitation(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		IssueStudentClassInvitationCommand{TargetEmail: "existing@example.edu", ClassID: classID.String(), IdempotencyKey: "import-row",
			onboardingImportID: model.NewOnboardingImportID(), onboardingImportRowNumber: 1})
	require.NoError(t, err, "mail-disabled no-op = %#v / %#v", view, persistence.issued)
	require.True(t, view.NoOp, "mail-disabled no-op = %#v / %#v", view, persistence.issued)
	require.NotNil(t, persistence.issued, "mail-disabled no-op = %#v / %#v", view, persistence.issued)
	require.Nil(t, persistence.issued.Occurrence, "mail-disabled no-op = %#v / %#v", view, persistence.issued)
	require.Nil(t, persistence.issued.Delivery, "mail-disabled no-op = %#v / %#v", view, persistence.issued)
	require.Nil(t, persistence.issued.DeliveryJob, "mail-disabled no-op = %#v / %#v", view, persistence.issued)
}
func (f *invitationStoreFake) IssueTeacherAcademicUnit(_ context.Context, input *store.TeacherAcademicUnitInvitationIssue) (*model.Invitation, error) {
	f.teacherIssued = input
	f.invitation = input.Invitation
	return input.Invitation, nil
}
func (f *invitationStoreFake) IssueTeacherAcademicUnitIdempotently(_ context.Context, input *store.TeacherAcademicUnitInvitationIssue, _ *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	value, err := f.IssueTeacherAcademicUnit(context.Background(), input)
	return &store.InvitationCommandResult{Invitation: value}, err
}
func (f *invitationStoreFake) Get(context.Context, model.InvitationID) (*model.Invitation, error) {
	return f.invitation, nil
}
func (f *invitationStoreFake) GetByClaimHash(_ context.Context, hash string) (*model.Invitation, error) {
	if f.events != nil {
		*f.events = append(*f.events, "resolve")
	}
	if f.invitation == nil || f.invitation.ClaimHash != hash {
		return nil, store.NewErrNotFound("invitation", "claim")
	}
	return f.invitation, nil
}
func (f *invitationStoreFake) AcceptStudentClass(_ context.Context, input *store.StudentClassInvitationAcceptance) (*store.StudentClassInvitationAcceptanceResult, error) {
	f.accepted = input
	accepted := *f.invitation
	_ = accepted.Accept(input.User.ID, input.Affiliation.ID, input.ClassMember.ID, model.TimeFromMillis(input.AcceptedAt))
	return &store.StudentClassInvitationAcceptanceResult{Invitation: &accepted, User: input.User, Affiliation: input.Affiliation, ClassMember: input.ClassMember}, nil
}
func (f *invitationStoreFake) AcceptTeacherAcademicUnit(_ context.Context, input *store.TeacherAcademicUnitInvitationAcceptance) (*store.TeacherAcademicUnitInvitationAcceptanceResult, error) {
	f.teacherAccepted = input
	accepted := *f.invitation
	_ = accepted.AcceptTeacherAcademicUnit(input.User.ID, input.Affiliation.ID, input.AcademicUnitMember.ID, input.RoleBinding.ID, model.TimeFromMillis(input.AcceptedAt))
	return &store.TeacherAcademicUnitInvitationAcceptanceResult{Invitation: &accepted, User: input.User,
		Affiliation: input.Affiliation, AcademicUnitMember: input.AcademicUnitMember, RoleBinding: input.RoleBinding}, nil
}
func (f *invitationStoreFake) IssueScopedRole(_ context.Context, input *store.ScopedRoleInvitationIssue) (*model.Invitation, error) {
	f.scopedIssued = input
	f.invitation = input.Invitation
	return input.Invitation, nil
}
func (f *invitationStoreFake) IssueScopedRoleIdempotently(_ context.Context, input *store.ScopedRoleInvitationIssue, _ *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	value, err := f.IssueScopedRole(context.Background(), input)
	return &store.InvitationCommandResult{Invitation: value}, err
}
func (f *invitationStoreFake) AcceptScopedRole(_ context.Context, input *store.ScopedRoleInvitationAcceptance) (*store.ScopedRoleInvitationAcceptanceResult, error) {
	if f.events != nil {
		*f.events = append(*f.events, "accept")
	}
	f.scopedAccepted = input
	if f.scopedAcceptErr != nil {
		return nil, f.scopedAcceptErr
	}
	accepted := *f.invitation
	_ = accepted.AcceptScopedRole(input.UserID, input.RoleBinding.ID, model.TimeFromMillis(1_800_000_060_000))
	return &store.ScopedRoleInvitationAcceptanceResult{Invitation: &accepted, User: &model.User{ID: input.UserID}, RoleBinding: input.RoleBinding}, nil
}
func (f *invitationStoreFake) Maintain(context.Context, int) (*store.InvitationMaintenanceResult, error) {
	return &store.InvitationMaintenanceResult{}, nil
}
func (f *invitationStoreFake) List(_ context.Context, options store.InvitationListOptions) (*store.InvitationPage, error) {
	f.listOptions = options
	return &store.InvitationPage{Items: []*store.InvitationAdministrationRecord{{Invitation: f.invitation}}}, nil
}
func (f *invitationStoreFake) GetForAdministration(context.Context, model.InvitationID, store.InvitationVisibilityScope) (*store.InvitationAdministrationRecord, error) {
	return &store.InvitationAdministrationRecord{Invitation: f.invitation}, nil
}
func (f *invitationStoreFake) Resend(_ context.Context, input *store.InvitationResend) (*store.InvitationAdministrationRecord, error) {
	f.resent = input
	result := *f.invitation
	_ = result.Resend(input.ClaimHash, model.TimeFromMillis(input.AuditAt))
	return &store.InvitationAdministrationRecord{Invitation: &result, Delivery: &store.InvitationDeliverySummary{State: model.MailDeliveryQueued}}, nil
}
func (f *invitationStoreFake) ResendIdempotently(ctx context.Context, input *store.InvitationResend, _ *store.CommandIdempotency) (*store.InvitationAdministrationCommandResult, error) {
	record, err := f.Resend(ctx, input)
	return &store.InvitationAdministrationCommandResult{Record: record}, err
}
func (f *invitationStoreFake) Revoke(_ context.Context, input *store.InvitationRevocation) (*store.InvitationAdministrationRecord, error) {
	f.revoked = input
	result := *f.invitation
	_ = result.Revoke(model.TimeFromMillis(input.AuditAt))
	return &store.InvitationAdministrationRecord{Invitation: &result}, nil
}
func (f *invitationStoreFake) RevokeIdempotently(ctx context.Context, input *store.InvitationRevocation, _ *store.CommandIdempotency) (*store.InvitationAdministrationCommandResult, error) {
	record, err := f.Revoke(ctx, input)
	return &store.InvitationAdministrationCommandResult{Record: record}, err
}
func (f *invitationStoreFake) Replace(_ context.Context, input *store.InvitationReplacement) (*store.InvitationAdministrationRecord, error) {
	f.replaced = input
	return &store.InvitationAdministrationRecord{Invitation: input.Replacement}, nil
}
func (f *invitationStoreFake) AcceptExternalIdentity(_ context.Context, input *store.ExternalIdentityInvitationAcceptance) (*store.ExternalIdentityInvitationAcceptanceResult, error) {
	f.externalAccepted = input
	return &store.ExternalIdentityInvitationAcceptanceResult{Invitation: f.invitation, Identity: input.Identity,
		User: input.User, Affiliation: input.Affiliation, ClassMember: input.ClassMember,
		AcademicUnitMember: input.AcademicUnitMember, RoleBinding: input.RoleBinding}, nil
}

type invitationClassStoreFake struct{ class *model.Class }

func (f invitationClassStoreFake) Get(context.Context, model.ClassID) (*model.Class, error) {
	return f.class, nil
}

func (f invitationClassStoreFake) GetAcademicUnitID(context.Context, string) (string, error) {
	return model.NewAcademicUnitID().String(), nil
}

type invitationPeriodStoreFake struct{ period *model.AcademicPeriod }

func (f invitationPeriodStoreFake) Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error) {
	return f.period, nil
}

type invitationAcademicUnitStoreFake struct{ unit *model.AcademicUnit }

func (f invitationAcademicUnitStoreFake) Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error) {
	return f.unit, nil
}

type invitationRoleStoreFake struct{ role *model.Role }

func (f invitationRoleStoreFake) Get(context.Context, model.RoleID) (*model.Role, error) {
	return f.role, nil
}

type invitationAuthorizerFake struct {
	actions            []model.Action
	delegatedActions   []string
	delegatedScopeType model.RoleScopeType
	delegatedScopeID   string
	actionErrors       map[model.Action]error
	err                error
}

func TestInvitationExternalIdentityAcceptancePreparesTheExactTerminalPackage(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID, inviterID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "invited@example.edu", ClassID: classID, AcademicPeriodID: periodID,
		IntendedStartsAt: now.Add(time.Hour), InviterUserID: inviterID, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: now})
	require.NoError(t, err)
	persistence := &invitationStoreFake{invitation: invitation}
	mail := &invitationMailPreparerFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, mail, now.Add(time.Minute))
	state := &model.ExternalLoginState{ID: model.NewExternalLoginStateID(), Provider: "campus",
		Purpose: model.ExternalAuthenticationPurposeInvitationAdmission, InvitationID: invitation.ID,
		ConsumedAt: model.OptionalTimeFrom(now)}
	assertion := &model.ExternalAuthenticationAssertion{ProviderID: "campus", Subject: "opaque-subject",
		Username: "provider-name", Email: " Provider.Mailbox@Example.EDU ", EmailVerified: true,
		DisplayName: "Provider Person"}
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {}}}
	result, err := service.acceptExternalIdentity(context.Background(), state, assertion, capabilities,
		model.RequestMetadata{RequestID: "request-1", IPAddress: "192.0.2.10"}, "oidc")
	require.NoError(t, err)
	accepted := persistence.externalAccepted
	require.NotNil(t, accepted, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Same(t, accepted.User, result.User, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, state.ID, accepted.ExternalStateID, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, "provider.mailbox@example.edu", accepted.ProviderEmail, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, invitation.TargetEmail, accepted.User.Email, "external Invitation package/result = %#v / %#v", accepted, result)
	require.True(t, accepted.User.EmailVerified, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, "campus", accepted.Identity.Provider, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, assertion.Subject, accepted.Identity.Subject, "external Invitation package/result = %#v / %#v", accepted, result)
	require.NotNil(t, accepted.Affiliation, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, model.AffiliationStudent, accepted.Affiliation.Kind, "external Invitation package/result = %#v / %#v", accepted, result)
	require.NotNil(t, accepted.ClassMember, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, classID, accepted.ClassMember.ClassID, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, periodID, accepted.ClassMember.AcademicPeriodID, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, accepted.User.ID, accepted.ClassMember.UserID, "external Invitation package/result = %#v / %#v", accepted, result)
	require.NotNil(t, accepted.Notice, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, accepted.User.ID, accepted.Notice.Delivery.TargetUserID, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, "oidc", accepted.AuditEvent.AuthMethod, "external Invitation package/result = %#v / %#v", accepted, result)
	require.Contains(t, string(accepted.AuditEvent.Parameters), `"provider":"campus"`, "external Invitation package/result = %#v / %#v", accepted, result)
	require.True(t, slices.Equal(accepted.RequiredActions, []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage}), "external Invitation package/result = %#v / %#v", accepted, result)
	require.Equal(t, model.JobTypeMailDeliver, mail.directJobType)
	mail.directErr = fmt.Errorf("mail preparation unavailable")
	assertion.Username = strings.Repeat("x", 1_000)
	result, err = service.acceptExternalIdentity(context.Background(), state, assertion, capabilities,
		model.RequestMetadata{RequestID: "request-2"}, "oidc")
	require.NoError(t, err, "existing-User candidate fallback result")
	require.NotNil(t, result, "existing-User candidate fallback result")
	accepted = persistence.externalAccepted
	require.NotNil(t, accepted)
	require.True(t, accepted.User.ID.IsValid())
	require.Nil(t, accepted.Settings)
	require.Nil(t, accepted.DefaultProfilePictureJob)
	require.Nil(t, accepted.Notice)
	require.NotNil(t, accepted.ClassMember)
	require.Equal(t, accepted.User.ID, accepted.ClassMember.UserID)
}

func (f *invitationAuthorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, _ model.Resource) error {
	f.actions = append(f.actions, action)
	if err := f.actionErrors[action]; err != nil {
		return err
	}
	return f.err
}
func (f *invitationAuthorizerFake) CanDelegateActionsAtScope(_ context.Context, _ invocation.Invocation, actions []string, scopeType model.RoleScopeType, scopeID string) error {
	f.delegatedActions = append([]string(nil), actions...)
	f.delegatedScopeType = scopeType
	f.delegatedScopeID = scopeID
	return f.err
}
func (f *invitationAuthorizerFake) Visibility(_ context.Context, _ invocation.Invocation, action model.Action) (store.InvitationVisibilityScope, error) {
	f.actions = append(f.actions, action)
	return store.InvitationVisibilityScope{InstitutionWide: true}, f.err
}

func TestInvitationServiceIssuesTeacherPackageThroughDelegationCeiling(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID, roleID, inviterID := model.NewAcademicUnitID(), model.NewRoleID(), model.NewUserID()
	role := &model.Role{ID: roleID, Name: "teacher", DisplayName: "Teacher",
		Permissions: []string{string(model.ActionProgrammeManage), string(model.ActionAcademicUnitView), string(model.ActionExamManage)}}
	persistence := &invitationStoreFake{}
	authorizer := &invitationAuthorizerFake{}
	mail := &invitationMailPreparerFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{unit: &model.AcademicUnit{ID: unitID}},
		invitationRoleStoreFake{role: role}, authorizer, mail, now)
	view, err := service.IssueTeacherAcademicUnitInvitation(context.Background(),
		invocation.New(model.Principal{UserID: inviterID}, model.RequestMetadata{}),
		IssueTeacherAcademicUnitInvitationCommand{TargetEmail: "teacher@example.edu", AcademicUnitID: unitID.String(),
			RoleID: roleID.String(), IntendedStartsAt: model.MillisFromTime(now.Add(time.Hour))})
	require.NoError(t, err)
	require.Equal(t, unitID, view.AcademicUnitID, "teacher issue view/input = %#v", view)
	require.Equal(t, roleID, view.RoleID, "teacher issue view/input = %#v", view)
	require.NotNil(t, persistence.teacherIssued, "teacher issue view/input = %#v", view)
	require.Equal(t, model.InvitationLifetime, persistence.teacherIssued.Lifetime, "teacher issue view/input = %#v", view)
	require.True(t, slices.Equal(persistence.teacherIssued.Invitation.RoleActions, []string{string(model.ActionAcademicUnitView), string(model.ActionExamManage), string(model.ActionProgrammeManage)}), "teacher issue view/input = %#v", view)
	require.True(t, slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}), "teacher issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.True(t, slices.Equal(authorizer.delegatedActions, role.Permissions), "teacher issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.Equal(t, model.RoleScopeAcademicUnit, authorizer.delegatedScopeType, "teacher issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.Equal(t, unitID.String(), authorizer.delegatedScopeID, "teacher issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
}

func TestInvitationBatchRunsIndependentRowsAndRecoversCommittedItems(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID, actorID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	class := &model.Class{ID: classID, AcademicPeriodID: periodID}
	period := &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour), EndsAt: now.Add(90 * 24 * time.Hour)}
	persistence := &invitationStoreFake{}
	authorizer := &invitationAuthorizerFake{}
	mail := &invitationMailPreparerFake{}
	auditEvents := []string{}
	service, err := newInvitationService(invitationServiceDependencies{Store: persistence, Classes: invitationClassStoreFake{class: class}, Periods: invitationPeriodStoreFake{period: period}, AcademicUnits: invitationAcademicUnitStoreFake{}, Roles: invitationRoleStoreFake{}, Authorization: authorizer, Mail: mail, Hasher: invitationHasherFake{}, Audit: &mutationAttemptAuditorFake{events: &auditEvents}, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-a", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: time.Hour, NewClaim: model.NewCredentialToken, Now: func() time.Time { return now }})
	require.NoError(t, err)
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "batch-once", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "row-1", TargetEmail: "first@example.edu"},
			{IdempotencyKey: "row-2", TargetEmail: "invalid@example.edu", RoleID: model.NewRoleID().String()},
			{IdempotencyKey: "row-3", TargetEmail: " FIRST@example.edu "},
		}}
	invocation := invocation.New(model.Principal{UserID: actorID}, model.RequestMetadata{RequestID: "request-1"})
	first, err := service.RunInvitationBatch(context.Background(), invocation, command)
	require.NoError(t, err)
	require.Equal(t, 1, first.Succeeded, "first batch result=%#v executions", first)
	require.Equal(t, 0, first.NoOp, "first batch result=%#v executions", first)
	require.Equal(t, 2, first.Failed, "first batch result=%#v executions", first)
	require.Len(t, first.Items, 3, "first batch result=%#v executions", first)
	require.Equal(t, InvitationBatchItemSucceeded, first.Items[0].Status, "first batch result=%#v executions", first)
	require.True(t, first.Items[0].InvitationID.IsValid(), "first batch result=%#v executions", first)
	require.Equal(t, "request.invalid", first.Items[1].ErrorCode, "first batch result=%#v executions", first)
	require.Equal(t, "onboarding_batch.duplicate", first.Items[2].ErrorCode, "first batch result=%#v executions", first)
	require.Equal(t, 1, persistence.issueExecutions, "first batch result=%#v executions", first)
	require.GreaterOrEqual(t, len(authorizer.actions), 3)
	require.Equal(t, model.ActionOnboardingBatchManage, authorizer.actions[0])
	require.Equal(t, model.ActionInvitationCreate, authorizer.actions[1])
	require.Equal(t, model.ActionClassMembersManage, authorizer.actions[2])

	command.Items = []InvitationBatchItemCommand{command.Items[2], command.Items[1], command.Items[0]}
	mail.disabled = true
	second, err := service.RunInvitationBatch(context.Background(), invocation, command)
	require.NoError(t, err)
	require.Equal(t, 0, second.Succeeded, "replayed batch result=%#v executions", second)
	require.Equal(t, 1, second.NoOp, "replayed batch result=%#v executions", second)
	require.Equal(t, 2, second.Failed, "replayed batch result=%#v executions", second)
	require.Equal(t, "onboarding_batch.duplicate", second.Items[0].ErrorCode, "replayed batch result=%#v executions", second)
	require.Equal(t, first.Items[0].InvitationID, second.Items[2].InvitationID, "replayed batch result=%#v executions", second)
	require.Equal(t, InvitationBatchItemNoOp, second.Items[2].Status, "replayed batch result=%#v executions", second)
	require.Equal(t, 1, persistence.issueExecutions, "replayed batch result=%#v executions", second)
}

func TestInvitationBatchAuthorizesBeforeRowsAndRestrictsRoleWorkToInteractiveSessions(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID := model.NewAcademicUnitID()
	authorizer := &invitationAuthorizerFake{err: apperror.New("authorization.denied")}
	service := newInvitationServiceForTest(t, &invitationStoreFake{}, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		authorizer, &invitationMailPreparerFake{}, now)
	command := RunInvitationBatchCommand{Operation: InvitationBatchTeacherAcademicUnitCreate, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unitID.String(), IdempotencyKey: "authorize-first", Items: []InvitationBatchItemCommand{{IdempotencyKey: "row-1", RoleID: "not-an-id"}}}
	_, err := service.RunInvitationBatch(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	require.True(t, apperror.Is(err, "authorization.denied"), "batch authorization-before-row error/actions = %v / %v", err, authorizer.actions)
	require.True(t, slices.Equal(authorizer.actions, []model.Action{model.ActionOnboardingBatchManage}), "batch authorization-before-row error/actions = %v / %v", err, authorizer.actions)

	authorizer.err = nil
	authorizer.actions = nil
	command.Operation = InvitationBatchAcademicUnitRoleCreate
	command.Items = []InvitationBatchItemCommand{{IdempotencyKey: "row-1", TargetEmail: "existing@example.edu", RoleID: model.NewRoleID().String()}}
	_, err = service.RunInvitationBatch(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "Role batch assurance error/actions = %v / %v", err, authorizer.actions)
	require.True(t, slices.Equal(authorizer.actions, []model.Action{model.ActionOnboardingBatchManage}), "Role batch assurance error/actions = %v / %v", err, authorizer.actions)

	authorizer.actions = nil
	command.Operation = InvitationBatchTeacherAcademicUnitCreate
	command.Items = make([]InvitationBatchItemCommand, MaximumInvitationBatchItems+1)
	_, err = service.RunInvitationBatch(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	require.True(t, apperror.Is(err, "request.invalid"), "oversized batch error/actions = %v", err)
	require.Empty(t, authorizer.actions, "oversized batch error/actions = %v", err)
}

func TestInvitationBatchItemIdempotencyKeyUnambiguouslyEncodesTheTuple(t *testing.T) {
	t.Parallel()
	require.NotEqual(t, invitationBatchItemIdempotencyKey("a", "b.c"), invitationBatchItemIdempotencyKey("a.b", "c"))
}

func TestInvitationBatchDuplicatesRetainPerItemAuthorization(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID := model.NewClassID(), model.NewAcademicPeriodID()
	persistence := &invitationStoreFake{}
	authorizer := &invitationAuthorizerFake{actionErrors: map[model.Action]error{
		model.ActionInvitationCreate: apperror.New("authorization.denied"),
	}}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		authorizer, &invitationMailPreparerFake{}, now)
	service.deps.Classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.deps.Periods = invitationPeriodStoreFake{period: &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour)}}
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "row-authorization", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "canonical", TargetEmail: "same@example.edu"},
			{IdempotencyKey: "duplicate", TargetEmail: " SAME@example.edu "},
		}}

	result, err := service.RunInvitationBatch(context.Background(),
		invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	require.NoError(t, err)
	require.Equal(t, 2, result.Failed, "duplicate row authorization result=%#v durable duplicates", result)
	require.Equal(t, "authorization.denied", result.Items[0].ErrorCode, "duplicate row authorization result=%#v durable duplicates", result)
	require.Equal(t, "authorization.denied", result.Items[1].ErrorCode, "duplicate row authorization result=%#v durable duplicates", result)
	require.Empty(t, persistence.batchDuplicates, "duplicate row authorization result=%#v durable duplicates", result)
}

func TestInvitationBatchDuplicateIdentityIncludesRole(t *testing.T) {
	t.Parallel()
	firstRole, secondRole := model.NewRoleID(), model.NewRoleID()
	first := InvitationBatchItemCommand{TargetEmail: "teacher@example.edu", RoleID: firstRole.String()}
	caseVariant := InvitationBatchItemCommand{TargetEmail: " TEACHER@example.edu ", RoleID: firstRole.String()}
	second := InvitationBatchItemCommand{TargetEmail: "teacher@example.edu", RoleID: secondRole.String()}
	require.Equal(t, invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, caseVariant), invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, first), "same teacher package produced distinct duplicate keys")
	require.NotEqual(t, invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, second), invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, first))
	plain := InvitationBatchItemCommand{TargetEmail: "same@example.edu"}
	blockedControl := InvitationBatchItemCommand{TargetEmail: "same@example.edu\u202e"}
	require.Equal(t, invitationBatchDuplicateKey(InvitationBatchStudentClassCreate, blockedControl), invitationBatchDuplicateKey(InvitationBatchStudentClassCreate, plain), "model-equivalent mailboxes produced distinct duplicate keys")
}

func TestInvitationBatchDuplicateStillValidatesItsDomainPackage(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID := model.NewClassID(), model.NewAcademicPeriodID()
	period := &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour), EndsAt: now.Add(90 * 24 * time.Hour)}
	persistence := &invitationStoreFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now)
	service.deps.Classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.deps.Periods = invitationPeriodStoreFake{period: period}
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "duplicate-validation", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "canonical", TargetEmail: "same@example.edu"},
			{IdempotencyKey: "duplicate", TargetEmail: " SAME@example.edu ", IntendedEndsAt: model.MillisFromTime(now)},
		}}

	result, err := service.RunInvitationBatch(context.Background(),
		invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	require.NoError(t, err)
	require.Equal(t, 1, result.Succeeded, "invalid duplicate package result=%#v durable duplicates", result)
	require.Equal(t, 1, result.Failed, "invalid duplicate package result=%#v durable duplicates", result)
	require.Equal(t, InvitationBatchItemSucceeded, result.Items[0].Status, "invalid duplicate package result=%#v durable duplicates", result)
	require.Equal(t, "invitation.invalid", result.Items[1].ErrorCode, "invalid duplicate package result=%#v durable duplicates", result)
	require.Empty(t, persistence.batchDuplicates, "invalid duplicate package result=%#v durable duplicates", result)
}

func TestInvitationBatchFreshResendDuplicateRequiresMailAvailability(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID, actorID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: "student@example.edu", ClassID: classID, AcademicPeriodID: periodID,
		IntendedStartsAt: now, InviterUserID: actorID, ScopeType: model.RoleScopeClass, ScopeID: classID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: now,
	})
	require.NoError(t, err)
	persistence := &invitationStoreFake{invitation: invitation}
	mail := &invitationMailPreparerFake{disabled: true}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, mail, now.Add(time.Minute))
	_, err = service.ResendInvitation(context.Background(),
		invocation.New(model.Principal{UserID: actorID}, model.RequestMetadata{}),
		ResendInvitationCommand{ID: invitation.ID, ExpectedRevision: invitation.Revision,
			IdempotencyKey: invitationBatchItemIdempotencyKey("mail-disabled", "duplicate"),
			BatchScopeType: model.RoleScopeClass, BatchScopeID: classID.String(), batchDuplicate: true,
			batchCanonicalKey: invitationBatchItemIdempotencyKey("mail-disabled", "canonical")})
	require.True(t, apperror.Is(err, "invitation.mail_unavailable"), "fresh resend duplicate with disabled mail error=%v durable duplicates", err)
	require.Empty(t, persistence.batchDuplicates, "fresh resend duplicate with disabled mail error=%v durable duplicates", err)
}

func TestInvitationBatchRejectsEveryRowWithARepeatedItemKey(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID := model.NewClassID(), model.NewAcademicPeriodID()
	persistence := &invitationStoreFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now)
	service.deps.Classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.deps.Periods = invitationPeriodStoreFake{period: &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour)}}
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "repeated-item-key", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "same-key", TargetEmail: "first@example.edu"},
			{IdempotencyKey: "same-key", TargetEmail: "second@example.edu"},
			{IdempotencyKey: "unique-key", TargetEmail: " FIRST@example.edu "},
		}}

	result, err := service.RunInvitationBatch(context.Background(),
		invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	require.NoError(t, err)
	require.Equal(t, 1, result.Succeeded, "repeated-key result=%#v executions", result)
	require.Equal(t, 2, result.Failed, "repeated-key result=%#v executions", result)
	require.Equal(t, "request.invalid", result.Items[0].ErrorCode, "repeated-key result=%#v executions", result)
	require.Equal(t, "request.invalid", result.Items[1].ErrorCode, "repeated-key result=%#v executions", result)
	require.Equal(t, InvitationBatchItemSucceeded, result.Items[2].Status, "repeated-key result=%#v executions", result)
	require.Equal(t, 1, persistence.issueExecutions, "repeated-key result=%#v executions", result)
}

func TestInvitationServiceIssuesAcademicUnitRoleForExistingUser(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID, roleID, inviterID := model.NewAcademicUnitID(), model.NewRoleID(), model.NewUserID()
	role := &model.Role{ID: roleID, Name: "unit-reviewer", DisplayName: "Unit Reviewer",
		Permissions: []string{string(model.ActionProgrammeManage), string(model.ActionAcademicUnitView)}}
	persistence := &invitationStoreFake{}
	authorizer := &invitationAuthorizerFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{unit: &model.AcademicUnit{ID: unitID}},
		invitationRoleStoreFake{role: role}, authorizer, &invitationMailPreparerFake{}, now)
	view, err := service.IssueAcademicUnitRoleInvitation(context.Background(),
		invocation.New(model.Principal{UserID: inviterID}, model.RequestMetadata{}),
		IssueAcademicUnitRoleInvitationCommand{TargetEmail: "existing@example.edu", AcademicUnitID: unitID.String(),
			RoleID: roleID.String(), IntendedStartsAt: model.MillisFromTime(now.Add(time.Hour))})
	require.NoError(t, err)
	require.Equal(t, model.InvitationPurposeAcademicUnitRole, view.Purpose, "academic-unit Role issue view/input = %#v", view)
	require.Equal(t, unitID, view.AcademicUnitID, "academic-unit Role issue view/input = %#v", view)
	require.Equal(t, roleID, view.RoleID, "academic-unit Role issue view/input = %#v", view)
	require.NotNil(t, persistence.scopedIssued, "academic-unit Role issue view/input = %#v", view)
	require.Equal(t, model.InvitationLifetime, persistence.scopedIssued.Lifetime, "academic-unit Role issue view/input = %#v", view)
	require.True(t, slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}), "academic-unit Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.True(t, slices.Equal(authorizer.delegatedActions, role.Permissions), "academic-unit Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.Equal(t, model.RoleScopeAcademicUnit, authorizer.delegatedScopeType, "academic-unit Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.Equal(t, unitID.String(), authorizer.delegatedScopeID, "academic-unit Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
}

func TestInvitationServiceAcceptsAcademicUnitRoleForAuthenticatedExistingUserWithoutMail(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID, roleID, inviterID, userID := model.NewAcademicUnitID(), model.NewRoleID(), model.NewUserID(), model.NewUserID()
	raw := model.NewCredentialToken()
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
		ID: model.NewInvitationID(), Purpose: model.InvitationPurposeAcademicUnitRole, TargetEmail: "other-mailbox@example.edu",
		AcademicUnitID: unitID, RoleID: roleID, RoleActions: []string{string(model.ActionAcademicUnitView)},
		IntendedStartsAt: now, InviterUserID: inviterID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: model.HashInvitationClaim(raw), IssuedAt: now,
	})
	require.NoError(t, err)
	events := []string{}
	auditID := model.NewAuditEventID().String()
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	persistence := &invitationStoreFake{invitation: invitation, events: &events}
	mail := &invitationMailPreparerFake{}
	service, err := newInvitationService(invitationServiceDependencies{Store: persistence, Classes: invitationClassStoreFake{}, Periods: invitationPeriodStoreFake{}, AcademicUnits: invitationAcademicUnitStoreFake{}, Roles: invitationRoleStoreFake{}, Authorization: &invitationAuthorizerFake{}, Mail: mail, Hasher: invitationHasherFake{}, Audit: auditor, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-1", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: 15 * time.Minute, NewClaim: model.NewCredentialToken, Now: func() time.Time { return now.Add(time.Minute) }})
	require.NoError(t, err)
	principal := model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}
	result, err := service.AcceptAcademicUnitRoleInvitation(context.Background(), invocation.New(principal, model.RequestMetadata{RequestID: "request-1"}),
		AcceptAcademicUnitRoleInvitationCommand{Claim: raw, Source: "127.0.0.1"})
	require.NoError(t, err)
	accepted := persistence.scopedAccepted
	require.NotNil(t, accepted, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, userID, accepted.UserID, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, userID, accepted.RoleBinding.UserID, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, roleID, accepted.RoleBinding.RoleID, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, model.RoleScopeAcademicUnit, accepted.RoleBinding.ScopeType, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, unitID.String(), accepted.RoleBinding.ScopeID, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, invitation.ID, accepted.RoleBinding.OriginInvitationID, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, model.AuditEventID(auditID), accepted.AuditEventID, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, model.MillisFromTime(now.Add(time.Minute)), accepted.AuditAt, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.NotNil(t, result.User, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Equal(t, userID, result.User.ID, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.NotNil(t, result.RoleBinding, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.Empty(t, mail.directJobType, "scoped Role acceptance/result = %#v / %#v", accepted, result)
	require.True(t, slices.Equal(events, []string{"resolve", "begin-at-scope", "accept"}), "scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	require.Equal(t, model.Action("invitation.accept"), auditor.attempt.Action, "scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	require.Equal(t, model.ResourceAcademicUnit, auditor.attempt.Resource.Type, "scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	require.Equal(t, unitID.String(), auditor.attempt.Resource.ID, "scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	require.Equal(t, model.RoleScopeAcademicUnit, auditor.attempt.ScopeType, "scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	require.Equal(t, unitID.String(), auditor.attempt.ScopeID, "scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	require.Equal(t, "accept_scoped_role_invitation", auditor.attempt.Operation, "scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	encoded, encodeErr := model.EncodeAuditData(auditor.attempt.Value)
	require.NoError(t, encodeErr, "scoped Role audit attempt leaked claim material")
	require.NotContains(t, string(encoded), raw, "scoped Role audit attempt leaked claim material")
	require.NotContains(t, string(encoded), invitation.ClaimHash, "scoped Role audit attempt leaked claim material")
}

func TestInvitationServiceTerminalizesScopedRoleAcceptanceConflict(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID, roleID, inviterID, userID := model.NewAcademicUnitID(), model.NewRoleID(), model.NewUserID(), model.NewUserID()
	raw := model.NewCredentialToken()
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
		ID: model.NewInvitationID(), Purpose: model.InvitationPurposeAcademicUnitRole, TargetEmail: "other-mailbox@example.edu",
		AcademicUnitID: unitID, RoleID: roleID, RoleActions: []string{string(model.ActionAcademicUnitView)}, IntendedStartsAt: now,
		InviterUserID: inviterID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: model.HashInvitationClaim(raw), IssuedAt: now,
	})
	require.NoError(t, err)
	events := []string{}
	auditID := model.NewAuditEventID().String()
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	persistence := &invitationStoreFake{invitation: invitation, events: &events,
		scopedAcceptErr: store.NewErrConflict("invitation", "invitation_role_binding_conflict", nil)}
	service, err := newInvitationService(invitationServiceDependencies{Store: persistence, Classes: invitationClassStoreFake{}, Periods: invitationPeriodStoreFake{}, AcademicUnits: invitationAcademicUnitStoreFake{}, Roles: invitationRoleStoreFake{}, Authorization: &invitationAuthorizerFake{}, Mail: &invitationMailPreparerFake{}, Hasher: invitationHasherFake{}, Audit: auditor, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-1", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: 15 * time.Minute, NewClaim: model.NewCredentialToken, Now: func() time.Time { return now.Add(time.Minute) }})
	require.NoError(t, err)
	principal := model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}
	_, appErr := service.AcceptAcademicUnitRoleInvitation(context.Background(), invocation.New(principal, model.RequestMetadata{}),
		AcceptAcademicUnitRoleInvitationCommand{Claim: raw, Source: "127.0.0.1"})
	require.True(t, apperror.Is(appErr, "invitation.invalid"), "scoped Role conflict error/audit/events = %v / %#v / %v", appErr, auditor, events)
	require.Equal(t, auditID, auditor.failID, "scoped Role conflict error/audit/events = %v / %#v / %v", appErr, auditor, events)
	require.Equal(t, "invitation.invalid", auditor.failCode, "scoped Role conflict error/audit/events = %v / %#v / %v", appErr, auditor, events)
	require.True(t, slices.Equal(events, []string{"resolve", "begin-at-scope", "accept", "fail"}), "scoped Role conflict error/audit/events = %v / %#v / %v", appErr, auditor, events)
}

func TestInvitationServiceInstitutionRoleRequiresStrongRecentInteractiveSession(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	institutionID, roleID, inviterID := model.NewInstitutionID(), model.NewRoleID(), model.NewUserID()
	role := &model.Role{ID: roleID, Name: "institution-reviewer", DisplayName: "Institution Reviewer",
		Permissions: []string{string(model.ActionAuditView)}}
	persistence := &invitationStoreFake{}
	authorizer := &invitationAuthorizerFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{},
		invitationRoleStoreFake{role: role}, authorizer, &invitationMailPreparerFake{}, now)
	principal := model.Principal{UserID: inviterID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}
	command := IssueInstitutionRoleInvitationCommand{TargetEmail: "existing@example.edu", InstitutionID: institutionID.String(), RoleID: roleID.String()}
	_, err := service.IssueInstitutionRoleInvitation(context.Background(), invocation.New(principal, model.RequestMetadata{}), command)
	require.True(t, apperror.Is(err, "authentication.strong_required"), "weak IssueInstitutionRole() error = %v", err)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	view, err := service.IssueInstitutionRoleInvitation(context.Background(), invocation.New(principal, model.RequestMetadata{}), command)
	require.NoError(t, err)
	require.Equal(t, model.InvitationPurposeInstitutionRole, view.Purpose, "institution Role issue = %#v", view)
	require.Equal(t, roleID, view.RoleID, "institution Role issue = %#v", view)
	require.False(t, view.AcademicUnitID.IsValid(), "institution Role issue = %#v", view)
	require.NotNil(t, persistence.scopedIssued, "institution Role issue = %#v", view)
	require.Equal(t, model.RoleScopeInstitution, persistence.scopedIssued.Invitation.ScopeType, "institution Role issue = %#v", view)
	require.Equal(t, institutionID.String(), persistence.scopedIssued.Invitation.ScopeID, "institution Role issue = %#v", view)
	require.True(t, slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}), "institution Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.True(t, slices.Equal(authorizer.delegatedActions, role.Permissions), "institution Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	require.Equal(t, model.RoleScopeInstitution, authorizer.delegatedScopeType, "institution Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
}

func TestInvitationServiceRejectsInertTeacherRoleAction(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID, roleID := model.NewAcademicUnitID(), model.NewRoleID()
	persistence := &invitationStoreFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{unit: &model.AcademicUnit{ID: unitID}},
		invitationRoleStoreFake{role: &model.Role{ID: roleID, Name: "inert", DisplayName: "Inert", Permissions: []string{string(model.ActionRoleManage)}}},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now)
	_, err := service.IssueTeacherAcademicUnitInvitation(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		IssueTeacherAcademicUnitInvitationCommand{TargetEmail: "teacher@example.edu", AcademicUnitID: unitID.String(), RoleID: roleID.String()})
	require.True(t, apperror.Is(err, "invitation.role_not_delegable"), "IssueTeacherAcademicUnit() = %v", err)
	require.Nil(t, persistence.teacherIssued, "IssueTeacherAcademicUnit() = %v", err)
}

func TestInvitationServiceAcceptsTeacherPackageArtifacts(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID, roleID, inviterID := model.NewAcademicUnitID(), model.NewRoleID(), model.NewUserID()
	raw := model.NewCredentialToken()
	invitation, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: "teacher@example.edu", AcademicUnitID: unitID, RoleID: roleID,
		RoleActions: []string{string(model.ActionAcademicUnitView)}, IntendedStartsAt: now,
		InviterUserID: inviterID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: model.HashInvitationClaim(raw), IssuedAt: now,
	})
	require.NoError(t, err)
	persistence := &invitationStoreFake{invitation: invitation}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now.Add(time.Minute))
	result, err := service.AcceptTeacherAcademicUnitInvitation(context.Background(), invocation.Invocation{}, AcceptTeacherAcademicUnitInvitationCommand{
		Claim: raw, Password: "correct horse battery staple", Username: "teacher-one", Source: "127.0.0.1",
	})
	require.NoError(t, err)
	accepted := persistence.teacherAccepted
	require.NotNil(t, accepted, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.Equal(t, model.AffiliationTeacher, accepted.Affiliation.Kind, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.Equal(t, unitID, accepted.AcademicUnitMember.AcademicUnitID, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.Equal(t, roleID, accepted.RoleBinding.RoleID, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.Equal(t, model.RoleScopeAcademicUnit, accepted.RoleBinding.ScopeType, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.Equal(t, unitID.String(), accepted.RoleBinding.ScopeID, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.Equal(t, invitation.ID, accepted.RoleBinding.OriginInvitationID, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.NotNil(t, result.AcademicUnitMember, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	require.NotNil(t, result.RoleBinding, "teacher acceptance artifacts/result = %#v / %#v", accepted, result)
}

type invitationMailPreparerFake struct {
	issueURL      string
	disabled      bool
	directJobType model.JobType
	directErr     error
}

func (f *invitationMailPreparerFake) Enabled() bool { return !f.disabled }
func (f *invitationMailPreparerFake) PrepareInvitation(invitation *model.Invitation, actionURL string) (*store.PreparedMail, error) {
	f.issueURL = actionURL
	return invitationPreparedMail(invitation.InviterUserID, "", invitation.ID, model.MailOccurrenceID(invitation.ID.String()), model.MailTemplateAccessStudentClassInvitation, invitation.CreatedAt, invitation.ExpiresAt), nil
}
func (f *invitationMailPreparerFake) PrepareInvitationResend(invitation *model.Invitation, actionURL string, actor model.UserID, at time.Time) (*store.PreparedMail, error) {
	f.issueURL = actionURL
	return invitationPreparedMail(actor, "", invitation.ID, model.NewMailOccurrenceID(), model.MailTemplateAccessStudentClassInvitation, at, invitation.ExpiresAt), nil
}
func (f *invitationMailPreparerFake) PrepareInvitationRevocation(invitation *model.Invitation, actor model.UserID, at time.Time) (*store.PreparedMail, error) {
	prepared := invitationPreparedMail(actor, "", invitation.ID, model.NewMailOccurrenceID(), model.MailTemplateAccessInvitationRevoked, at, at.Add(24*time.Hour))
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: prepared.Delivery.ID})
	prepared.Job, _ = model.NewJob(prepared.Job.ID, model.JobTypeMailDeliver, command, prepared.Delivery.ID.String(), at, at, model.MailMaximumAttempts)
	return prepared, nil
}
func (f *invitationMailPreparerFake) PrepareInvitationAccepted(request NoticeMailPreparation) (*store.PreparedMail, error) {
	if f.directErr != nil {
		return nil, f.directErr
	}
	f.directJobType = model.JobTypeMailDeliver
	prepared := invitationPreparedMail(request.Recipient.ID, request.Recipient.ID, "", model.NewMailOccurrenceID(),
		model.MailTemplateAccessInvitationAccepted, request.At, request.At.Add(24*time.Hour))
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: prepared.Delivery.ID})
	prepared.Job, _ = model.NewJob(prepared.Job.ID, model.JobTypeMailDeliver, command, prepared.Delivery.ID.String(), request.At, request.At, model.MailMaximumAttempts)
	if f.disabled {
		prepared.Job, _ = prepared.Job.RequestCancellation(request.At)
		prepared.Delivery.State = model.MailDeliverySuppressed
		prepared.Delivery.PublicFailureCode = model.MailDeliveryDisabledCode
		prepared.Delivery.EncryptedPayload = nil
	}
	return prepared, nil
}

func invitationPreparedMail(actor, targetUser model.UserID, targetInvitation model.InvitationID, occurrenceID model.MailOccurrenceID, key model.MailTemplateKey, at, deadline time.Time) *store.PreparedMail {
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommand{DeliveryID: deliveryID})
	job, _ := model.NewJob(jobID, model.JobTypeMailDeliverCredential, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	return &store.PreparedMail{
		Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceInvitation, TemplateKey: key, ActorUserID: actor, CreatedAt: at},
		Delivery: &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: targetUser,
			TargetInvitationID: targetInvitation, TemplateKey: key, TemplateDigest: strings.Repeat("d", 64),
			MaskedRecipient: "s***@example.edu", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at,
			MessageDate: at, Deadline: deadline, MessageID: "<invite." + deliveryID.String() + "@example.test>",
			EncryptedPayload: []byte(`{"version":1,"key_id":"11111111111111111111111111111111","ciphertext":"sealed"}`), Revision: 1},
		Job: job,
	}
}

func newInvitationServiceForTest(t *testing.T, persistence store.InvitationStore, units invitationAcademicUnitReader,
	roles invitationRoleReader, authorization invitationAuthorizer, mail invitationMailPreparer, now time.Time,
) *invitationService {
	t.Helper()
	events := []string{}
	service, err := newInvitationService(invitationServiceDependencies{Store: persistence, Classes: invitationClassStoreFake{}, Periods: invitationPeriodStoreFake{}, AcademicUnits: units, Roles: roles, Authorization: authorization, Mail: mail, Hasher: invitationHasherFake{}, Audit: &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-1", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: 15 * time.Minute, NewClaim: model.NewCredentialToken, Now: func() time.Time { return now }})
	require.NoError(t, err)
	return service
}

func TestInvitationAdministrationUsesAuthorizedSafeLifecycleCommands(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID := model.NewClassID()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "student@example.edu", ClassID: classID, AcademicPeriodID: model.NewAcademicPeriodID(),
		IntendedStartsAt: now, InviterUserID: model.NewUserID(), ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: now})
	require.NoError(t, err)
	persistence := &invitationStoreFake{invitation: invitation}
	authorizer := &invitationAuthorizerFake{}
	mail := &invitationMailPreparerFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, authorizer, mail, now.Add(time.Hour))
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})

	page, err := service.ListInvitations(context.Background(), invocation, ListInvitationsQuery{TargetEmail: "STUDENT@example.edu", Limit: 25})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "Invitation list = %#v / %#v", page, persistence.listOptions)
	require.Equal(t, invitation.TargetEmail, page.Items[0].TargetEmail, "Invitation list = %#v / %#v", page, persistence.listOptions)
	require.Equal(t, invitation.TargetEmail, persistence.listOptions.TargetEmail, "Invitation list = %#v / %#v", page, persistence.listOptions)
	require.Equal(t, 25, persistence.listOptions.Limit, "Invitation list = %#v / %#v", page, persistence.listOptions)

	resent, err := service.ResendInvitation(context.Background(), invocation, ResendInvitationCommand{ID: invitation.ID, ExpectedRevision: invitation.Revision})
	require.NoError(t, err)
	require.NotNil(t, persistence.resent, "Invitation resend = %#v / %#v", resent, persistence.resent)
	require.NotEqual(t, invitation.ClaimHash, persistence.resent.ClaimHash, "Invitation resend = %#v / %#v", resent, persistence.resent)
	require.NotContains(t, resent.String(), persistence.resent.ClaimHash, "Invitation resend = %#v / %#v", resent, persistence.resent)
	require.Contains(t, mail.issueURL, "/join#token=", "Invitation resend = %#v / %#v", resent, persistence.resent)

	revoked, err := service.RevokeInvitation(context.Background(), invocation, RevokeInvitationCommand{ID: invitation.ID, ExpectedRevision: invitation.Revision})
	require.NoError(t, err)
	require.NotNil(t, persistence.revoked, "Invitation revoke = %#v", revoked)
	require.Equal(t, model.InvitationRevoked, revoked.State, "Invitation revoke = %#v", revoked)
	require.NotNil(t, persistence.revoked.RevocationNotice, "Invitation revoke = %#v", revoked)
	require.Equal(t, model.MailTemplateAccessInvitationRevoked, persistence.revoked.RevocationNotice.Delivery.TemplateKey, "Invitation revoke = %#v", revoked)
	require.True(t, slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationView, model.ActionInvitationManage, model.ActionInvitationManage}), "Invitation administration authorization = %v", authorizer.actions)
}

func TestInvitationReplacementReauthorizesAndBuildsANewImmutablePackage(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	period := &model.AcademicPeriod{ID: model.NewAcademicPeriodID(), StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(180 * 24 * time.Hour)}
	currentClassID := model.NewClassID()
	class := &model.Class{ID: model.NewClassID(), AcademicPeriodID: period.ID}
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "old@example.edu", ClassID: currentClassID, AcademicPeriodID: period.ID,
		IntendedStartsAt: period.StartsAt, InviterUserID: model.NewUserID(), ScopeType: model.RoleScopeClass,
		ScopeID: currentClassID.String(), ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: now})
	require.NoError(t, err)
	persistence := &invitationStoreFake{invitation: invitation}
	authorizer := &invitationAuthorizerFake{}
	mail := &invitationMailPreparerFake{}
	events := []string{}
	auditIDs := []string{model.NewAuditEventID().String(), model.NewAuditEventID().String()}
	auditor := &mutationAttemptAuditorFake{events: &events, beginIDs: auditIDs}
	service, err := newInvitationService(invitationServiceDependencies{Store: persistence, Classes: invitationClassStoreFake{class: class}, Periods: invitationPeriodStoreFake{period: period}, AcademicUnits: invitationAcademicUnitStoreFake{}, Roles: invitationRoleStoreFake{}, Authorization: authorizer, Mail: mail, Hasher: invitationHasherFake{}, Audit: auditor, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-1", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: 15 * time.Minute, NewClaim: model.NewCredentialToken, Now: func() time.Time { return now.Add(time.Hour) }})
	require.NoError(t, err)
	actor := model.NewUserID()
	result, err := service.ReplaceInvitation(context.Background(), invocation.New(model.Principal{UserID: actor}, model.RequestMetadata{}),
		ReplaceInvitationCommand{ID: invitation.ID, ExpectedRevision: invitation.Revision,
			Purpose: string(model.InvitationPurposeStudentClass), TargetEmail: "new@example.edu", ClassID: class.ID.String(),
			SuggestedUsername: "new-student"})
	require.NoError(t, err)
	require.NotNil(t, persistence.replaced, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.NotEqual(t, invitation.ID, result.ID, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Equal(t, "new@example.edu", result.TargetEmail, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Equal(t, actor, persistence.replaced.Replacement.InviterUserID, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Equal(t, class.ID, persistence.replaced.Replacement.ClassID, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Equal(t, period.ID, persistence.replaced.Replacement.AcademicPeriodID, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.NotEqual(t, invitation.ClaimHash, persistence.replaced.Replacement.ClaimHash, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Equal(t, invitation.Revision, persistence.replaced.ExpectedCurrentRevision, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Equal(t, model.AuditEventID(auditIDs[0]), persistence.replaced.CurrentAuditEventID, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Equal(t, model.AuditEventID(auditIDs[1]), persistence.replaced.ReplacementAuditEventID, "Invitation replacement = %#v / %#v", result, persistence.replaced)
	require.Contains(t, mail.issueURL, "/join#token=", "Invitation replacement = %#v / %#v", result, persistence.replaced)
	wantActions := []model.Action{model.ActionInvitationManage, model.ActionInvitationCreate, model.ActionClassMembersManage}
	require.True(t, slices.Equal(authorizer.actions, wantActions), "Invitation replacement authorization = %v, want %v", authorizer.actions, wantActions)
	require.Len(t, auditor.attempts, 2)
	require.Equal(t, currentClassID.String(), auditor.attempts[0].ScopeID)
	require.Equal(t, class.ID.String(), auditor.attempts[1].ScopeID)
	require.NotContains(t, fmt.Sprint(auditor.attempts[0].Value), class.ID.String())
	require.NotContains(t, fmt.Sprint(auditor.attempts[1].Value), currentClassID.String())
}

func TestInvitationIssueAuthorizesBeforeInspectingMailCapability(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	periodID, classID := model.NewAcademicPeriodID(), model.NewClassID()
	authorizer := &invitationAuthorizerFake{err: apperror.New("authorization.denied")}
	mail := &invitationMailPreparerFake{disabled: true}
	service, err := newInvitationService(invitationServiceDependencies{Store: &invitationStoreFake{}, Classes: invitationClassStoreFake{&model.Class{ID: classID, AcademicPeriodID: periodID}}, Periods: invitationPeriodStoreFake{&model.AcademicPeriod{ID: periodID, StartsAt: now, EndsAt: now.Add(24 * time.Hour)}}, AcademicUnits: invitationAcademicUnitStoreFake{}, Roles: invitationRoleStoreFake{}, Authorization: authorizer, Mail: mail, Hasher: invitationHasherFake{}, Audit: &mutationAttemptAuditorFake{}, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-1", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: 15 * time.Minute, NewClaim: model.NewCredentialToken, Now: func() time.Time { return now }})
	require.NoError(t, err)
	_, appErr := service.IssueStudentClassInvitation(context.Background(), invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		IssueStudentClassInvitationCommand{ClassID: classID.String(), TargetEmail: "student@example.edu"})
	require.True(t, apperror.Is(appErr, "authorization.denied"), "IssueStudentClass() error/actions = %v", appErr)
	require.Len(t, authorizer.actions, 1, "IssueStudentClass() error/actions = %v", appErr)
	require.Equal(t, model.ActionInvitationCreate, authorizer.actions[0], "IssueStudentClass() error/actions = %v", appErr)
}

func TestInvitationAcceptanceCommitsWithTerminalNoticeWhenMailIsDisabled(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	periodID, classID, inviterID := model.NewAcademicPeriodID(), model.NewClassID(), model.NewUserID()
	raw := model.NewCredentialToken()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(), TargetEmail: "student@example.edu",
		ClassID: classID, AcademicPeriodID: periodID, IntendedStartsAt: now, InviterUserID: inviterID,
		ScopeType: model.RoleScopeClass, ScopeID: classID.String(), ClaimHash: model.HashInvitationClaim(raw), IssuedAt: now})
	require.NoError(t, err)
	persistence := &invitationStoreFake{invitation: invitation}
	mail := &invitationMailPreparerFake{disabled: true}
	service, err := newInvitationService(invitationServiceDependencies{Store: persistence, Classes: invitationClassStoreFake{}, Periods: invitationPeriodStoreFake{}, AcademicUnits: invitationAcademicUnitStoreFake{}, Roles: invitationRoleStoreFake{}, Authorization: &invitationAuthorizerFake{}, Mail: mail, Hasher: invitationHasherFake{}, Audit: &mutationAttemptAuditorFake{}, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-1", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: 15 * time.Minute, NewClaim: model.NewCredentialToken, Now: func() time.Time { return now.Add(time.Minute) }})
	require.NoError(t, err)
	_, appErr := service.AcceptStudentClassInvitation(context.Background(), invocation.Invocation{}, AcceptStudentClassInvitationCommand{Claim: raw,
		Password: "correct horse battery staple", Username: "student-one", Source: "127.0.0.1"})
	require.NoError(t, appErr)
	require.Equal(t, model.JobTypeMailDeliver, mail.directJobType, "disabled acceptance mail = %#v / %#v", persistence.accepted.Delivery, persistence.accepted.DeliveryJob)
	require.Equal(t, model.MailDeliverySuppressed, persistence.accepted.Delivery.State, "disabled acceptance mail = %#v / %#v", persistence.accepted.Delivery, persistence.accepted.DeliveryJob)
	require.Equal(t, model.MailDeliveryDisabledCode, persistence.accepted.Delivery.PublicFailureCode, "disabled acceptance mail = %#v / %#v", persistence.accepted.Delivery, persistence.accepted.DeliveryJob)
	require.Empty(t, persistence.accepted.Delivery.EncryptedPayload, "disabled acceptance mail = %#v / %#v", persistence.accepted.Delivery, persistence.accepted.DeliveryJob)
	require.Equal(t, model.JobStatusCanceled, persistence.accepted.DeliveryJob.Status, "disabled acceptance mail = %#v / %#v", persistence.accepted.Delivery, persistence.accepted.DeliveryJob)
}

func TestInvitationPasswordWorkFailuresStopBeforeAcceptance(t *testing.T) {
	for _, purpose := range []model.InvitationPurpose{model.InvitationPurposeStudentClass, model.InvitationPurposeTeacherAcademicUnit} {
		t.Run(string(purpose), func(t *testing.T) {
			testPasswordHashFailures(t, "invitation.unavailable", func(t *testing.T, ctx context.Context, hasher passwordHash) error {
				raw := model.NewCredentialToken()
				invitation := &model.Invitation{Purpose: purpose, ClaimHash: model.HashInvitationClaim(raw), TargetEmail: "invited@example.edu"}
				persistence := &invitationStoreFake{invitation: invitation}
				mail := &invitationMailPreparerFake{}
				service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
					&invitationAuthorizerFake{}, mail, time.Now())
				service.deps.Hasher = hasher
				var result *InvitationAcceptanceView
				var err error
				if purpose == model.InvitationPurposeStudentClass {
					result, err = service.AcceptStudentClassInvitation(ctx, invocation.Invocation{}, AcceptStudentClassInvitationCommand{
						Claim: raw, Username: "invited", Password: "correct horse battery staple", Source: "192.0.2.36",
					})
				} else {
					result, err = service.AcceptTeacherAcademicUnitInvitation(ctx, invocation.Invocation{}, AcceptTeacherAcademicUnitInvitationCommand{
						Claim: raw, Username: "invited", Password: "correct horse battery staple", Source: "192.0.2.36",
					})
				}
				require.Nil(t, result, "failed password work accepted an Invitation or prepared mail")
				require.Nil(t, persistence.accepted, "failed password work accepted an Invitation or prepared mail")
				require.Nil(t, persistence.teacherAccepted, "failed password work accepted an Invitation or prepared mail")
				require.Empty(t, mail.directJobType, "failed password work accepted an Invitation or prepared mail")
				return err
			})
		})
	}
}

type invitationHasherFake struct{}

func (invitationHasherFake) Hash(_ context.Context, value string) (string, error) {
	return "encoded:" + value, nil
}

type invitationAttemptLimiterFake struct{}

func (invitationAttemptLimiterFake) Check(context.Context, string, string) error { return nil }

func TestInvitationServiceIssuesAndAcceptsWithoutPersistingRawClaim(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	periodID, classID, inviterID := model.NewAcademicPeriodID(), model.NewClassID(), model.NewUserID()
	period := &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour), EndsAt: now.Add(180 * 24 * time.Hour)}
	class := &model.Class{ID: classID, AcademicPeriodID: periodID}
	persistence := &invitationStoreFake{}
	authorizer := &invitationAuthorizerFake{}
	mail := &invitationMailPreparerFake{}
	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}
	raw := model.NewCredentialToken()
	service, err := newInvitationService(invitationServiceDependencies{Store: persistence, Classes: invitationClassStoreFake{class}, Periods: invitationPeriodStoreFake{period}, AcademicUnits: invitationAcademicUnitStoreFake{}, Roles: invitationRoleStoreFake{}, Authorization: authorizer, Mail: mail, Hasher: invitationHasherFake{}, Audit: auditor, Attempts: invitationAttemptLimiterFake{}, MembershipEffects: &classMemberEffectsFake{}, NodeID: "node-1", PublicURL: "https://proctor.example.edu", RecentAuthenticationTTL: 15 * time.Minute, NewClaim: func() string { return raw }, Now: func() time.Time { return now }})
	require.NoError(t, err)
	call := invocation.New(model.Principal{UserID: inviterID}, model.RequestMetadata{})
	view, err := service.IssueStudentClassInvitation(context.Background(), call, IssueStudentClassInvitationCommand{
		TargetEmail: " Student@Example.edu ", ClassID: classID.String(), IntendedStartsAt: model.MillisFromTime(period.StartsAt),
		IntendedEndsAt: model.MillisFromTime(period.EndsAt), SuggestedUsername: "student-one", SuggestedDisplayName: "Student One", SuggestedLocale: "en",
	})
	require.NoError(t, err)
	require.Equal(t, persistence.invitation.ID, view.ID, "unsafe or incomplete issue: view=%#v input=%#v url", view, persistence.issued)
	require.NotContains(t, view.String(), raw, "unsafe or incomplete issue: view=%#v input=%#v url", view, persistence.issued)
	require.Equal(t, model.HashInvitationClaim(raw), persistence.issued.Invitation.ClaimHash, "unsafe or incomplete issue: view=%#v input=%#v url", view, persistence.issued)
	require.NotContains(t, string(persistence.issued.Delivery.EncryptedPayload), raw, "unsafe or incomplete issue: view=%#v input=%#v url", view, persistence.issued)
	require.Contains(t, mail.issueURL, "/join#token=", "unsafe or incomplete issue: view=%#v input=%#v url", view, persistence.issued)
	require.Len(t, authorizer.actions, 2)
	require.Equal(t, model.ActionInvitationCreate, authorizer.actions[0])
	require.Equal(t, model.ActionClassMembersManage, authorizer.actions[1])
	result, err := service.AcceptStudentClassInvitation(context.Background(), invocation.Invocation{}, AcceptStudentClassInvitationCommand{
		Claim: raw, Password: "correct horse battery staple", Username: "student-one", DisplayName: "Student One", Locale: "en",
	})
	require.NoError(t, err)
	require.NotNil(t, result.User, "acceptance result/input = %#v / %#v", result, persistence.accepted)
	require.Equal(t, "student@example.edu", result.User.Email, "acceptance result/input = %#v / %#v", result, persistence.accepted)
	require.True(t, result.User.EmailVerified, "acceptance result/input = %#v / %#v", result, persistence.accepted)
	require.Equal(t, "encoded:correct horse battery staple", persistence.accepted.PasswordCredential.PasswordHash, "acceptance result/input = %#v / %#v", result, persistence.accepted)
	require.Equal(t, model.HashInvitationClaim(raw), persistence.accepted.ClaimHash, "acceptance result/input = %#v / %#v", result, persistence.accepted)
}

func TestInvitationAttemptAccountingLimitsClaimAndSourceBeforeValidation(t *testing.T) {
	now := time.Now()
	accounting, err := newAuthenticationAttemptAccounting(authenticationAttemptAccountingDependencies{Cache: newExpiringAuthenticationAttemptCache(func() time.Time { return now })})
	require.NoError(t, err)
	limiter := invitationAttemptAccounting{attempts: accounting, policy: LoginRateLimitPolicy{
		Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 2,
	}}
	identity := model.HashInvitationClaim("malformed")
	err = limiter.Check(context.Background(), identity, "127.0.0.1")
	require.NoError(t, err)
	purpose, _ := authenticationAttemptPurposeInvitation.keySegment()
	identityLimit := authenticationAttemptLimit{dimension: authenticationAttemptDimensionIdentity, identity: identity}
	intent := authenticationAttemptIntent{purpose: authenticationAttemptPurposeInvitation, qualifier: invitationAcceptanceAttemptQualifier}
	expectedKey := "authentication/attempts/" + purpose + "/identity/" + digestAuthenticationAttempt(intent, identityLimit)
	legacyIntent := intent
	legacyIntent.qualifier = "student-class-accept"
	legacyKey := "authentication/attempts/" + purpose + "/identity/" + digestAuthenticationAttempt(legacyIntent, identityLimit)
	entries := accounting.deps.Cache.(*expiringAuthenticationAttemptCache).snapshot()
	_, ok := entries[expectedKey]
	require.True(t, ok, "purpose-neutral Invitation acceptance counter was not recorded: %#v", entries)
	_, ok = entries[legacyKey]
	require.False(t, ok, "teacher-capable Invitation accounting used legacy student qualifier: %#v", entries)
	err = limiter.Check(context.Background(), identity, "127.0.0.1")
	failure, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "authentication.rate_limited", failure.Code())
}
