// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

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
func (f *invitationStoreFake) ReplayIssue(_ context.Context, command *store.CommandIdempotency, _ string, _ int64) (*store.InvitationCommandResult, error) {
	if f.batchDuplicates[command.KeyDigest] {
		return &store.InvitationCommandResult{Duplicate: true, Replayed: true}, nil
	}
	if invitation := f.idempotentIssues[command.KeyDigest]; invitation != nil {
		return &store.InvitationCommandResult{Invitation: invitation, Replayed: true}, nil
	}
	return nil, store.NewErrNotFound("command_outcome", "idempotency_key")
}
func (f *invitationStoreFake) FindCommandOutcome(ctx context.Context, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	return f.ReplayIssue(ctx, command, "", 0)
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
	service.classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.periods = invitationPeriodStoreFake{period: &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour), EndsAt: now.Add(24 * time.Hour)}}
	view, err := service.IssueStudentClass(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		IssueStudentClassInvitationCommand{TargetEmail: "existing@example.edu", ClassID: classID.String(), IdempotencyKey: "import-row",
			onboardingImportID: model.NewOnboardingImportID(), onboardingImportRowNumber: 1})
	if err != nil || !view.NoOp || persistence.issued == nil || persistence.issued.Occurrence != nil || persistence.issued.Delivery != nil || persistence.issued.DeliveryJob != nil {
		t.Fatalf("mail-disabled no-op = %#v / %#v / %v", view, persistence.issued, err)
	}
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

func (f invitationClassStoreFake) Get(context.Context, string) (*model.Class, error) {
	return f.class, nil
}

func (f invitationClassStoreFake) GetAcademicUnitId(context.Context, string) (string, error) {
	return model.NewAcademicUnitID().String(), nil
}

type invitationPeriodStoreFake struct{ period *model.AcademicPeriod }

func (f invitationPeriodStoreFake) Get(context.Context, string) (*model.AcademicPeriod, error) {
	return f.period, nil
}

type invitationAcademicUnitStoreFake struct{ unit *model.AcademicUnit }

func (f invitationAcademicUnitStoreFake) Get(context.Context, string) (*model.AcademicUnit, error) {
	return f.unit, nil
}

type invitationRoleStoreFake struct{ role *model.Role }

func (f invitationRoleStoreFake) Get(context.Context, string) (*model.Role, error) {
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
	if err != nil {
		t.Fatal(err)
	}
	persistence := &invitationStoreFake{invitation: invitation}
	mail := &invitationMailPreparerFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, mail, now.Add(time.Minute))
	state := &model.ExternalLoginState{ID: model.NewExternalLoginStateID(), Provider: "campus",
		Purpose: model.ExternalAuthenticationPurposeInvitationAdmission, InvitationID: invitation.ID,
		ConsumedAt: model.OptionalTimeFrom(now)}
	assertion := &model.ExternalAuthenticationAssertion{ProviderId: "campus", Subject: "opaque-subject",
		Username: "provider-name", Email: " Provider.Mailbox@Example.EDU ", EmailVerified: true,
		DisplayName: "Provider Person"}
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus": {}}}
	result, err := service.AcceptExternalIdentity(context.Background(), state, assertion, capabilities,
		model.RequestMetadata{RequestID: "request-1", IPAddress: "192.0.2.10"}, "oidc")
	if err != nil {
		t.Fatal(err)
	}
	accepted := persistence.externalAccepted
	if accepted == nil || result.User != accepted.User || accepted.ExternalStateID != state.ID ||
		accepted.ProviderEmail != "provider.mailbox@example.edu" || accepted.User.Email != invitation.TargetEmail ||
		!accepted.User.EmailVerified || accepted.Identity.Provider != "campus" || accepted.Identity.Subject != assertion.Subject ||
		accepted.Affiliation == nil || accepted.Affiliation.Kind != model.AffiliationStudent ||
		accepted.ClassMember == nil || accepted.ClassMember.ClassID != classID || accepted.ClassMember.AcademicPeriodID != periodID ||
		accepted.ClassMember.UserID != accepted.User.ID || accepted.Notice == nil ||
		accepted.Notice.Delivery.TargetUserID != accepted.User.ID || accepted.AuditEvent.AuthMethod != "oidc" ||
		!strings.Contains(string(accepted.AuditEvent.Parameters), `"provider":"campus"`) ||
		!slices.Equal(accepted.RequiredActions, []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage}) {
		t.Fatalf("external Invitation package/result = %#v / %#v", accepted, result)
	}
	if mail.directJobType != model.JobTypeMailDeliver {
		t.Fatalf("acceptance notice Job type = %q", mail.directJobType)
	}
	mail.directErr = fmt.Errorf("mail preparation unavailable")
	assertion.Username = strings.Repeat("x", 1_000)
	result, err = service.AcceptExternalIdentity(context.Background(), state, assertion, capabilities,
		model.RequestMetadata{RequestID: "request-2"}, "oidc")
	if err != nil || result == nil {
		t.Fatalf("existing-User candidate fallback result=%#v error=%v", result, err)
	}
	accepted = persistence.externalAccepted
	if accepted == nil || !accepted.User.ID.IsValid() || accepted.Settings != nil || accepted.DefaultProfilePictureJob != nil ||
		accepted.Notice != nil || accepted.ClassMember == nil || accepted.ClassMember.UserID != accepted.User.ID {
		t.Fatalf("existing-User candidate fallback package = %#v", accepted)
	}
}

func (f *invitationAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action, _ model.Resource) error {
	f.actions = append(f.actions, action)
	if err := f.actionErrors[action]; err != nil {
		return err
	}
	return f.err
}
func (f *invitationAuthorizerFake) CanDelegateActionsAtScope(_ context.Context, _ Invocation, actions []string, scopeType model.RoleScopeType, scopeID string) error {
	f.delegatedActions = append([]string(nil), actions...)
	f.delegatedScopeType = scopeType
	f.delegatedScopeID = scopeID
	return f.err
}
func (f *invitationAuthorizerFake) Visibility(_ context.Context, _ Invocation, action model.Action) (store.InvitationVisibilityScope, error) {
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
	view, err := service.IssueTeacherAcademicUnit(context.Background(),
		NewInvocation(model.Principal{UserID: inviterID}, model.RequestMetadata{}),
		IssueTeacherAcademicUnitInvitationCommand{TargetEmail: "teacher@example.edu", AcademicUnitID: unitID.String(),
			RoleID: roleID.String(), IntendedStartsAt: model.MillisFromTime(now.Add(time.Hour))})
	if err != nil {
		t.Fatalf("IssueTeacherAcademicUnit() error = %v", err)
	}
	if view.AcademicUnitID != unitID || view.RoleID != roleID || persistence.teacherIssued == nil ||
		persistence.teacherIssued.Lifetime != model.InvitationLifetime ||
		!slices.Equal(persistence.teacherIssued.Invitation.RoleActions, []string{string(model.ActionAcademicUnitView), string(model.ActionExamManage), string(model.ActionProgrammeManage)}) {
		t.Fatalf("teacher issue view/input = %#v / %#v", view, persistence.teacherIssued)
	}
	if !slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}) ||
		!slices.Equal(authorizer.delegatedActions, role.Permissions) || authorizer.delegatedScopeType != model.RoleScopeAcademicUnit ||
		authorizer.delegatedScopeID != unitID.String() {
		t.Fatalf("teacher issue authorization = %v / %v / %s", authorizer.actions, authorizer.delegatedActions, authorizer.delegatedScopeID)
	}
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
	service, err := newInvitationService(persistence, invitationClassStoreFake{class: class}, invitationPeriodStoreFake{period: period},
		invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, authorizer, mail, invitationHasherFake{},
		&mutationAttemptAuditorFake{events: &auditEvents}, invitationAttemptLimiterFake{}, "node-a", "https://proctor.example.edu", time.Hour,
		model.NewCredentialToken, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "batch-once", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "row-1", TargetEmail: "first@example.edu"},
			{IdempotencyKey: "row-2", TargetEmail: "invalid@example.edu", RoleID: model.NewRoleID().String()},
			{IdempotencyKey: "row-3", TargetEmail: " FIRST@example.edu "},
		}}
	invocation := NewInvocation(model.Principal{UserID: actorID}, model.RequestMetadata{RequestID: "request-1"})
	first, err := service.RunBatch(context.Background(), invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Succeeded != 1 || first.NoOp != 0 || first.Failed != 2 || len(first.Items) != 3 ||
		first.Items[0].Status != InvitationBatchItemSucceeded || !first.Items[0].InvitationID.IsValid() ||
		first.Items[1].ErrorCode != "request.invalid" || first.Items[2].ErrorCode != "onboarding_batch.duplicate" ||
		persistence.issueExecutions != 1 {
		t.Fatalf("first batch result=%#v executions=%d", first, persistence.issueExecutions)
	}
	if len(authorizer.actions) < 3 || authorizer.actions[0] != model.ActionOnboardingBatchManage ||
		authorizer.actions[1] != model.ActionInvitationCreate || authorizer.actions[2] != model.ActionClassMembersManage {
		t.Fatalf("batch authorization order = %v", authorizer.actions)
	}

	command.Items = []InvitationBatchItemCommand{command.Items[2], command.Items[1], command.Items[0]}
	mail.disabled = true
	second, err := service.RunBatch(context.Background(), invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	if second.Succeeded != 0 || second.NoOp != 1 || second.Failed != 2 ||
		second.Items[0].ErrorCode != "onboarding_batch.duplicate" || second.Items[2].InvitationID != first.Items[0].InvitationID ||
		second.Items[2].Status != InvitationBatchItemNoOp || persistence.issueExecutions != 1 {
		t.Fatalf("replayed batch result=%#v executions=%d", second, persistence.issueExecutions)
	}
}

func TestInvitationBatchAuthorizesBeforeRowsAndRestrictsRoleWorkToInteractiveSessions(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID := model.NewAcademicUnitID()
	authorizer := &invitationAuthorizerFake{err: NewError("authorization.denied")}
	service := newInvitationServiceForTest(t, &invitationStoreFake{}, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		authorizer, &invitationMailPreparerFake{}, now)
	command := RunInvitationBatchCommand{Operation: InvitationBatchTeacherAcademicUnitCreate, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unitID.String(), IdempotencyKey: "authorize-first", Items: []InvitationBatchItemCommand{{IdempotencyKey: "row-1", RoleID: "not-an-id"}}}
	_, err := service.RunBatch(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	if !Is(err, "authorization.denied") || !slices.Equal(authorizer.actions, []model.Action{model.ActionOnboardingBatchManage}) {
		t.Fatalf("batch authorization-before-row error/actions = %v / %v", err, authorizer.actions)
	}

	authorizer.err = nil
	authorizer.actions = nil
	command.Operation = InvitationBatchAcademicUnitRoleCreate
	command.Items = []InvitationBatchItemCommand{{IdempotencyKey: "row-1", TargetEmail: "existing@example.edu", RoleID: model.NewRoleID().String()}}
	_, err = service.RunBatch(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	if !Is(err, "authentication.invalid_token") || !slices.Equal(authorizer.actions, []model.Action{model.ActionOnboardingBatchManage}) {
		t.Fatalf("Role batch assurance error/actions = %v / %v", err, authorizer.actions)
	}

	authorizer.actions = nil
	command.Operation = InvitationBatchTeacherAcademicUnitCreate
	command.Items = make([]InvitationBatchItemCommand, MaximumInvitationBatchItems+1)
	_, err = service.RunBatch(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	if !Is(err, "request.invalid") || len(authorizer.actions) != 0 {
		t.Fatalf("oversized batch error/actions = %v / %v", err, authorizer.actions)
	}
}

func TestInvitationBatchItemIdempotencyKeyUnambiguouslyEncodesTheTuple(t *testing.T) {
	t.Parallel()
	if left, right := invitationBatchItemIdempotencyKey("a.b", "c"), invitationBatchItemIdempotencyKey("a", "b.c"); left == right {
		t.Fatalf("distinct batch/item tuples collided at %q", left)
	}
}

func TestInvitationBatchDuplicatesRetainPerItemAuthorization(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID := model.NewClassID(), model.NewAcademicPeriodID()
	persistence := &invitationStoreFake{}
	authorizer := &invitationAuthorizerFake{actionErrors: map[model.Action]error{
		model.ActionInvitationCreate: NewError("authorization.denied"),
	}}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		authorizer, &invitationMailPreparerFake{}, now)
	service.classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.periods = invitationPeriodStoreFake{period: &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour)}}
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "row-authorization", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "canonical", TargetEmail: "same@example.edu"},
			{IdempotencyKey: "duplicate", TargetEmail: " SAME@example.edu "},
		}}

	result, err := service.RunBatch(context.Background(),
		NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 2 || result.Items[0].ErrorCode != "authorization.denied" ||
		result.Items[1].ErrorCode != "authorization.denied" || len(persistence.batchDuplicates) != 0 {
		t.Fatalf("duplicate row authorization result=%#v durable duplicates=%v", result, persistence.batchDuplicates)
	}
}

func TestInvitationBatchDuplicateIdentityIncludesRole(t *testing.T) {
	t.Parallel()
	firstRole, secondRole := model.NewRoleID(), model.NewRoleID()
	first := InvitationBatchItemCommand{TargetEmail: "teacher@example.edu", RoleID: firstRole.String()}
	caseVariant := InvitationBatchItemCommand{TargetEmail: " TEACHER@example.edu ", RoleID: firstRole.String()}
	second := InvitationBatchItemCommand{TargetEmail: "teacher@example.edu", RoleID: secondRole.String()}
	if firstKey, caseKey := invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, first),
		invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, caseVariant); firstKey != caseKey {
		t.Fatalf("same teacher package produced distinct duplicate keys %q and %q", firstKey, caseKey)
	}
	if firstKey, secondKey := invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, first),
		invitationBatchDuplicateKey(InvitationBatchTeacherAcademicUnitCreate, second); firstKey == secondKey {
		t.Fatalf("different teacher roles produced the same duplicate key %q", firstKey)
	}
	plain := InvitationBatchItemCommand{TargetEmail: "same@example.edu"}
	blockedControl := InvitationBatchItemCommand{TargetEmail: "same@example.edu\u202e"}
	if plainKey, blockedKey := invitationBatchDuplicateKey(InvitationBatchStudentClassCreate, plain),
		invitationBatchDuplicateKey(InvitationBatchStudentClassCreate, blockedControl); plainKey != blockedKey {
		t.Fatalf("model-equivalent mailboxes produced distinct duplicate keys %q and %q", plainKey, blockedKey)
	}
}

func TestInvitationBatchDuplicateStillValidatesItsDomainPackage(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID := model.NewClassID(), model.NewAcademicPeriodID()
	period := &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour), EndsAt: now.Add(90 * 24 * time.Hour)}
	persistence := &invitationStoreFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now)
	service.classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.periods = invitationPeriodStoreFake{period: period}
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "duplicate-validation", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "canonical", TargetEmail: "same@example.edu"},
			{IdempotencyKey: "duplicate", TargetEmail: " SAME@example.edu ", IntendedEndsAt: model.MillisFromTime(now)},
		}}

	result, err := service.RunBatch(context.Background(),
		NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 1 || result.Failed != 1 || result.Items[0].Status != InvitationBatchItemSucceeded ||
		result.Items[1].ErrorCode != "invitation.invalid" || len(persistence.batchDuplicates) != 0 {
		t.Fatalf("invalid duplicate package result=%#v durable duplicates=%v", result, persistence.batchDuplicates)
	}
}

func TestInvitationBatchFreshResendDuplicateRequiresMailAvailability(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID, actorID := model.NewClassID(), model.NewAcademicPeriodID(), model.NewUserID()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: "student@example.edu", ClassID: classID, AcademicPeriodID: periodID,
		IntendedStartsAt: now, InviterUserID: actorID, ScopeType: model.RoleScopeClass, ScopeID: classID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &invitationStoreFake{invitation: invitation}
	mail := &invitationMailPreparerFake{disabled: true}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, mail, now.Add(time.Minute))
	_, err = service.Resend(context.Background(),
		NewInvocation(model.Principal{UserID: actorID}, model.RequestMetadata{}),
		ResendInvitationCommand{ID: invitation.ID.String(), ExpectedRevision: invitation.Revision,
			IdempotencyKey: invitationBatchItemIdempotencyKey("mail-disabled", "duplicate"),
			BatchScopeType: model.RoleScopeClass, BatchScopeID: classID.String(), batchDuplicate: true,
			batchCanonicalKey: invitationBatchItemIdempotencyKey("mail-disabled", "canonical")})
	if !Is(err, "invitation.mail_unavailable") || len(persistence.batchDuplicates) != 0 {
		t.Fatalf("fresh resend duplicate with disabled mail error=%v durable duplicates=%v", err, persistence.batchDuplicates)
	}
}

func TestInvitationBatchRejectsEveryRowWithARepeatedItemKey(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID, periodID := model.NewClassID(), model.NewAcademicPeriodID()
	persistence := &invitationStoreFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now)
	service.classes = invitationClassStoreFake{class: &model.Class{ID: classID, AcademicPeriodID: periodID}}
	service.periods = invitationPeriodStoreFake{period: &model.AcademicPeriod{ID: periodID, StartsAt: now.Add(time.Hour)}}
	command := RunInvitationBatchCommand{Operation: InvitationBatchStudentClassCreate, ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), IdempotencyKey: "repeated-item-key", Items: []InvitationBatchItemCommand{
			{IdempotencyKey: "same-key", TargetEmail: "first@example.edu"},
			{IdempotencyKey: "same-key", TargetEmail: "second@example.edu"},
			{IdempotencyKey: "unique-key", TargetEmail: " FIRST@example.edu "},
		}}

	result, err := service.RunBatch(context.Background(),
		NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 1 || result.Failed != 2 || result.Items[0].ErrorCode != "request.invalid" ||
		result.Items[1].ErrorCode != "request.invalid" || result.Items[2].Status != InvitationBatchItemSucceeded ||
		persistence.issueExecutions != 1 {
		t.Fatalf("repeated-key result=%#v executions=%d", result, persistence.issueExecutions)
	}
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
	view, err := service.IssueAcademicUnitRole(context.Background(),
		NewInvocation(model.Principal{UserID: inviterID}, model.RequestMetadata{}),
		IssueAcademicUnitRoleInvitationCommand{TargetEmail: "existing@example.edu", AcademicUnitID: unitID.String(),
			RoleID: roleID.String(), IntendedStartsAt: model.MillisFromTime(now.Add(time.Hour))})
	if err != nil {
		t.Fatalf("IssueAcademicUnitRole() error = %v", err)
	}
	if view.Purpose != model.InvitationPurposeAcademicUnitRole || view.AcademicUnitID != unitID || view.RoleID != roleID ||
		persistence.scopedIssued == nil || persistence.scopedIssued.Lifetime != model.InvitationLifetime {
		t.Fatalf("academic-unit Role issue view/input = %#v / %#v", view, persistence.scopedIssued)
	}
	if !slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}) ||
		!slices.Equal(authorizer.delegatedActions, role.Permissions) || authorizer.delegatedScopeType != model.RoleScopeAcademicUnit ||
		authorizer.delegatedScopeID != unitID.String() {
		t.Fatalf("academic-unit Role issue authorization = %v / %v / %s", authorizer.actions, authorizer.delegatedActions, authorizer.delegatedScopeID)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	auditID := model.NewAuditEventID().String()
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	persistence := &invitationStoreFake{invitation: invitation, events: &events}
	mail := &invitationMailPreparerFake{}
	service, err := newInvitationService(persistence, invitationClassStoreFake{}, invitationPeriodStoreFake{},
		invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, &invitationAuthorizerFake{}, mail,
		invitationHasherFake{}, auditor, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu",
		15*time.Minute, model.NewCredentialToken, func() time.Time { return now.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}
	result, err := service.AcceptAcademicUnitRole(context.Background(), NewInvocation(principal, model.RequestMetadata{RequestID: "request-1"}),
		AcceptAcademicUnitRoleInvitationCommand{Claim: raw, Source: "127.0.0.1"})
	if err != nil {
		t.Fatalf("AcceptAcademicUnitRole() error = %v", err)
	}
	accepted := persistence.scopedAccepted
	if accepted == nil || accepted.UserID != userID || accepted.RoleBinding.UserID != userID ||
		accepted.RoleBinding.RoleID != roleID || accepted.RoleBinding.ScopeType != model.RoleScopeAcademicUnit ||
		accepted.RoleBinding.ScopeID != unitID.String() || accepted.RoleBinding.OriginInvitationID != invitation.ID ||
		accepted.AuditEventID != auditID || accepted.AuditAt != model.MillisFromTime(now.Add(time.Minute)) ||
		result.User == nil || result.User.ID != userID || result.RoleBinding == nil || mail.directJobType != "" {
		t.Fatalf("scoped Role acceptance/result = %#v / %#v", accepted, result)
	}
	if !slices.Equal(events, []string{"resolve", "begin-at-scope", "accept"}) ||
		auditor.attempt.Action != model.Action("invitation.accept") || auditor.attempt.Resource.Type != model.ResourceAcademicUnit ||
		auditor.attempt.Resource.ID != unitID.String() || auditor.attempt.ScopeType != model.RoleScopeAcademicUnit ||
		auditor.attempt.ScopeID != unitID.String() || auditor.attempt.Operation != "accept_scoped_role_invitation" {
		t.Fatalf("scoped Role audit attempt/events = %#v / %v", auditor.attempt, events)
	}
	encoded, encodeErr := model.EncodeAuditData(auditor.attempt.Value)
	if encodeErr != nil || strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), invitation.ClaimHash) {
		t.Fatalf("scoped Role audit attempt leaked claim material: %s / %v", encoded, encodeErr)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	auditID := model.NewAuditEventID().String()
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	persistence := &invitationStoreFake{invitation: invitation, events: &events,
		scopedAcceptErr: store.NewErrConflict("invitation", "invitation_role_binding_conflict", nil)}
	service, err := newInvitationService(persistence, invitationClassStoreFake{}, invitationPeriodStoreFake{},
		invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, &invitationAuthorizerFake{}, &invitationMailPreparerFake{},
		invitationHasherFake{}, auditor, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu",
		15*time.Minute, model.NewCredentialToken, func() time.Time { return now.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}
	_, appErr := service.AcceptAcademicUnitRole(context.Background(), NewInvocation(principal, model.RequestMetadata{}),
		AcceptAcademicUnitRoleInvitationCommand{Claim: raw, Source: "127.0.0.1"})
	if !Is(appErr, "invitation.invalid") || auditor.failID != auditID || auditor.failCode != "invitation.invalid" ||
		!slices.Equal(events, []string{"resolve", "begin-at-scope", "accept", "fail"}) {
		t.Fatalf("scoped Role conflict error/audit/events = %v / %#v / %v", appErr, auditor, events)
	}
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
	principal := model.Principal{UserID: inviterID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: now}
	command := IssueInstitutionRoleInvitationCommand{TargetEmail: "existing@example.edu", InstitutionID: institutionID.String(), RoleID: roleID.String()}
	if _, err := service.IssueInstitutionRole(context.Background(), NewInvocation(principal, model.RequestMetadata{}), command); !Is(err, "authentication.strong_required") {
		t.Fatalf("weak IssueInstitutionRole() error = %v", err)
	}
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	view, err := service.IssueInstitutionRole(context.Background(), NewInvocation(principal, model.RequestMetadata{}), command)
	if err != nil {
		t.Fatalf("strong IssueInstitutionRole() error = %v", err)
	}
	if view.Purpose != model.InvitationPurposeInstitutionRole || view.RoleID != roleID || view.AcademicUnitID.IsValid() ||
		persistence.scopedIssued == nil || persistence.scopedIssued.Invitation.ScopeType != model.RoleScopeInstitution ||
		persistence.scopedIssued.Invitation.ScopeID != institutionID.String() {
		t.Fatalf("institution Role issue = %#v / %#v", view, persistence.scopedIssued)
	}
	if !slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}) ||
		!slices.Equal(authorizer.delegatedActions, role.Permissions) || authorizer.delegatedScopeType != model.RoleScopeInstitution {
		t.Fatalf("institution Role issue authorization = %v / %v", authorizer.actions, authorizer.delegatedActions)
	}
}

func TestInvitationServiceRejectsInertTeacherRoleAction(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	unitID, roleID := model.NewAcademicUnitID(), model.NewRoleID()
	persistence := &invitationStoreFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{unit: &model.AcademicUnit{ID: unitID}},
		invitationRoleStoreFake{role: &model.Role{ID: roleID, Name: "inert", DisplayName: "Inert", Permissions: []string{string(model.ActionRoleManage)}}},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now)
	_, err := service.IssueTeacherAcademicUnit(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		IssueTeacherAcademicUnitInvitationCommand{TargetEmail: "teacher@example.edu", AcademicUnitID: unitID.String(), RoleID: roleID.String()})
	if !Is(err, "invitation.role_not_delegable") || persistence.teacherIssued != nil {
		t.Fatalf("IssueTeacherAcademicUnit() = %v / %#v", err, persistence.teacherIssued)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	persistence := &invitationStoreFake{invitation: invitation}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{},
		&invitationAuthorizerFake{}, &invitationMailPreparerFake{}, now.Add(time.Minute))
	result, err := service.AcceptTeacherAcademicUnit(context.Background(), Invocation{}, AcceptTeacherAcademicUnitInvitationCommand{
		Claim: raw, Password: "correct horse battery staple", Username: "teacher-one", Source: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("AcceptTeacherAcademicUnit() error = %v", err)
	}
	accepted := persistence.teacherAccepted
	if accepted == nil || accepted.Affiliation.Kind != model.AffiliationTeacher || accepted.AcademicUnitMember.AcademicUnitID != unitID ||
		accepted.RoleBinding.RoleID != roleID || accepted.RoleBinding.ScopeType != model.RoleScopeAcademicUnit ||
		accepted.RoleBinding.ScopeID != unitID.String() || accepted.RoleBinding.OriginInvitationID != invitation.ID ||
		result.AcademicUnitMember == nil || result.RoleBinding == nil {
		t.Fatalf("teacher acceptance artifacts/result = %#v / %#v", accepted, result)
	}
}

type invitationMailPreparerFake struct {
	issueURL      string
	disabled      bool
	directJobType model.JobType
	directErr     error
}

func (f *invitationMailPreparerFake) Enabled() bool { return !f.disabled }
func (f *invitationMailPreparerFake) PrepareInvitation(invitation *model.Invitation, actionURL string) (*preparedDirectMail, error) {
	f.issueURL = actionURL
	return invitationPreparedMail(invitation.InviterUserID, "", invitation.ID, model.MailOccurrenceID(invitation.ID.String()), model.MailTemplateAccessStudentClassInvitation, invitation.CreatedAt, invitation.ExpiresAt), nil
}
func (f *invitationMailPreparerFake) PrepareInvitationResend(invitation *model.Invitation, actionURL string, actor model.UserID, at time.Time) (*preparedDirectMail, error) {
	f.issueURL = actionURL
	return invitationPreparedMail(actor, "", invitation.ID, model.NewMailOccurrenceID(), model.MailTemplateAccessStudentClassInvitation, at, invitation.ExpiresAt), nil
}
func (f *invitationMailPreparerFake) PrepareInvitationRevocation(invitation *model.Invitation, actor model.UserID, at time.Time) (*preparedDirectMail, error) {
	prepared := invitationPreparedMail(actor, "", invitation.ID, model.NewMailOccurrenceID(), model.MailTemplateAccessInvitationRevoked, at, at.Add(24*time.Hour))
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: prepared.Delivery.ID})
	prepared.Job, _ = model.NewJob(prepared.Job.ID, model.JobTypeMailDeliver, 1, command, prepared.Delivery.ID.String(), at, at, model.MailMaximumAttempts)
	return prepared, nil
}
func (f *invitationMailPreparerFake) PrepareInvitationAccepted(request NoticeMailPreparation) (*preparedDirectMail, error) {
	if f.directErr != nil {
		return nil, f.directErr
	}
	f.directJobType = model.JobTypeMailDeliver
	prepared := invitationPreparedMail(request.Recipient.ID, request.Recipient.ID, "", model.NewMailOccurrenceID(),
		model.MailTemplateAccessInvitationAccepted, request.At, request.At.Add(24*time.Hour))
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: prepared.Delivery.ID})
	prepared.Job, _ = model.NewJob(prepared.Job.ID, model.JobTypeMailDeliver, 1, command, prepared.Delivery.ID.String(), request.At, request.At, model.MailMaximumAttempts)
	if f.disabled {
		prepared.Job, _ = prepared.Job.RequestCancellation(request.At)
		prepared.Delivery.State = model.MailDeliverySuppressed
		prepared.Delivery.PublicFailureCode = model.MailDeliveryDisabledCode
		prepared.Delivery.EncryptedPayload = nil
	}
	return prepared, nil
}

func invitationPreparedMail(actor, targetUser model.UserID, targetInvitation model.InvitationID, occurrenceID model.MailOccurrenceID, key model.MailTemplateKey, at, deadline time.Time) *preparedDirectMail {
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	job, _ := model.NewJob(jobID, model.JobTypeMailDeliverCredential, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	return &preparedDirectMail{
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
	service, err := newInvitationService(persistence, invitationClassStoreFake{}, invitationPeriodStoreFake{}, units, roles,
		authorization, mail, invitationHasherFake{}, &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}, invitationAttemptLimiterFake{},
		"node-1", "https://proctor.example.edu", 15*time.Minute, model.NewCredentialToken, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestInvitationAdministrationUsesAuthorizedSafeLifecycleCommands(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	classID := model.NewClassID()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
		TargetEmail: "student@example.edu", ClassID: classID, AcademicPeriodID: model.NewAcademicPeriodID(),
		IntendedStartsAt: now, InviterUserID: model.NewUserID(), ScopeType: model.RoleScopeClass,
		ScopeID: classID.String(), ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &invitationStoreFake{invitation: invitation}
	authorizer := &invitationAuthorizerFake{}
	mail := &invitationMailPreparerFake{}
	service := newInvitationServiceForTest(t, persistence, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, authorizer, mail, now.Add(time.Hour))
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})

	page, err := service.List(context.Background(), invocation, ListInvitationsQuery{TargetEmail: "STUDENT@example.edu", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].TargetEmail != invitation.TargetEmail ||
		persistence.listOptions.TargetEmail != invitation.TargetEmail || persistence.listOptions.Limit != 25 {
		t.Fatalf("Invitation list = %#v / %#v", page, persistence.listOptions)
	}

	resent, err := service.Resend(context.Background(), invocation, ResendInvitationCommand{ID: invitation.ID.String(), ExpectedRevision: invitation.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.resent == nil || persistence.resent.ClaimHash == invitation.ClaimHash ||
		strings.Contains(resent.String(), persistence.resent.ClaimHash) || !strings.Contains(mail.issueURL, "/join#token=") {
		t.Fatalf("Invitation resend = %#v / %#v / %q", resent, persistence.resent, mail.issueURL)
	}

	revoked, err := service.Revoke(context.Background(), invocation, RevokeInvitationCommand{ID: invitation.ID.String(), ExpectedRevision: invitation.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.revoked == nil || revoked.State != model.InvitationRevoked ||
		persistence.revoked.RevocationNotice == nil || persistence.revoked.RevocationNotice.Delivery.TemplateKey != model.MailTemplateAccessInvitationRevoked {
		t.Fatalf("Invitation revoke = %#v / %#v", revoked, persistence.revoked)
	}
	if !slices.Equal(authorizer.actions, []model.Action{model.ActionInvitationView, model.ActionInvitationManage, model.ActionInvitationManage}) {
		t.Fatalf("Invitation administration authorization = %v", authorizer.actions)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	persistence := &invitationStoreFake{invitation: invitation}
	authorizer := &invitationAuthorizerFake{}
	mail := &invitationMailPreparerFake{}
	events := []string{}
	auditIDs := []string{model.NewAuditEventID().String(), model.NewAuditEventID().String()}
	auditor := &mutationAttemptAuditorFake{events: &events, beginIDs: auditIDs}
	service, err := newInvitationService(persistence, invitationClassStoreFake{class: class}, invitationPeriodStoreFake{period: period},
		invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, authorizer, mail, invitationHasherFake{},
		auditor, invitationAttemptLimiterFake{},
		"node-1", "https://proctor.example.edu", 15*time.Minute, model.NewCredentialToken, func() time.Time { return now.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	actor := model.NewUserID()
	result, err := service.Replace(context.Background(), NewInvocation(model.Principal{UserID: actor}, model.RequestMetadata{}),
		ReplaceInvitationCommand{ID: invitation.ID.String(), ExpectedRevision: invitation.Revision,
			Purpose: string(model.InvitationPurposeStudentClass), TargetEmail: "new@example.edu", ClassID: class.ID.String(),
			SuggestedUsername: "new-student"})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.replaced == nil || result.ID == invitation.ID || result.TargetEmail != "new@example.edu" ||
		persistence.replaced.Replacement.InviterUserID != actor || persistence.replaced.Replacement.ClassID != class.ID ||
		persistence.replaced.Replacement.AcademicPeriodID != period.ID || persistence.replaced.Replacement.ClaimHash == invitation.ClaimHash ||
		persistence.replaced.ExpectedCurrentRevision != invitation.Revision ||
		persistence.replaced.CurrentAuditEventID != auditIDs[0] || persistence.replaced.ReplacementAuditEventID != auditIDs[1] ||
		!strings.Contains(mail.issueURL, "/join#token=") {
		t.Fatalf("Invitation replacement = %#v / %#v / %q", result, persistence.replaced, mail.issueURL)
	}
	wantActions := []model.Action{model.ActionInvitationManage, model.ActionInvitationCreate, model.ActionClassMembersManage}
	if !slices.Equal(authorizer.actions, wantActions) {
		t.Fatalf("Invitation replacement authorization = %v, want %v", authorizer.actions, wantActions)
	}
	if len(auditor.attempts) != 2 || auditor.attempts[0].ScopeID != currentClassID.String() ||
		auditor.attempts[1].ScopeID != class.ID.String() ||
		strings.Contains(fmt.Sprint(auditor.attempts[0].Value), class.ID.String()) ||
		strings.Contains(fmt.Sprint(auditor.attempts[1].Value), currentClassID.String()) {
		t.Fatalf("Invitation replacement audit attempts = %#v", auditor.attempts)
	}
}

func TestInvitationIssueAuthorizesBeforeInspectingMailCapability(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	periodID, classID := model.NewAcademicPeriodID(), model.NewClassID()
	authorizer := &invitationAuthorizerFake{err: NewError("authorization.denied")}
	mail := &invitationMailPreparerFake{disabled: true}
	service, err := newInvitationService(&invitationStoreFake{}, invitationClassStoreFake{&model.Class{ID: classID, AcademicPeriodID: periodID}},
		invitationPeriodStoreFake{&model.AcademicPeriod{ID: periodID, StartsAt: now, EndsAt: now.Add(24 * time.Hour)}},
		invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, authorizer, mail,
		invitationHasherFake{}, &mutationAttemptAuditorFake{}, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu",
		15*time.Minute, model.NewCredentialToken, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	_, appErr := service.IssueStudentClass(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		IssueStudentClassInvitationCommand{ClassID: classID.String(), TargetEmail: "student@example.edu"})
	if !Is(appErr, "authorization.denied") || len(authorizer.actions) != 1 || authorizer.actions[0] != model.ActionInvitationCreate {
		t.Fatalf("IssueStudentClass() error/actions = %v / %v", appErr, authorizer.actions)
	}
}

func TestInvitationAcceptanceCommitsWithTerminalNoticeWhenMailIsDisabled(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	periodID, classID, inviterID := model.NewAcademicPeriodID(), model.NewClassID(), model.NewUserID()
	raw := model.NewCredentialToken()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(), TargetEmail: "student@example.edu",
		ClassID: classID, AcademicPeriodID: periodID, IntendedStartsAt: now, InviterUserID: inviterID,
		ScopeType: model.RoleScopeClass, ScopeID: classID.String(), ClaimHash: model.HashInvitationClaim(raw), IssuedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	persistence := &invitationStoreFake{invitation: invitation}
	mail := &invitationMailPreparerFake{disabled: true}
	service, err := newInvitationService(persistence, invitationClassStoreFake{}, invitationPeriodStoreFake{}, invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, &invitationAuthorizerFake{}, mail,
		invitationHasherFake{}, &mutationAttemptAuditorFake{}, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu",
		15*time.Minute, model.NewCredentialToken, func() time.Time { return now.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	if _, appErr := service.AcceptStudentClass(context.Background(), Invocation{}, AcceptStudentClassInvitationCommand{Claim: raw,
		Password: "correct horse battery staple", Username: "student-one", Source: "127.0.0.1"}); appErr != nil {
		t.Fatalf("AcceptStudentClass() error = %v", appErr)
	}
	if mail.directJobType != model.JobTypeMailDeliver || persistence.accepted.Delivery.State != model.MailDeliverySuppressed ||
		persistence.accepted.Delivery.PublicFailureCode != model.MailDeliveryDisabledCode || len(persistence.accepted.Delivery.EncryptedPayload) != 0 ||
		persistence.accepted.DeliveryJob.Status != model.JobStatusCanceled {
		t.Fatalf("disabled acceptance mail = %#v / %#v / %s", persistence.accepted.Delivery, persistence.accepted.DeliveryJob, mail.directJobType)
	}
}

type invitationHasherFake struct{}

func (invitationHasherFake) Hash(value string) (string, error) { return "encoded:" + value, nil }

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
	service, err := newInvitationService(persistence, invitationClassStoreFake{class}, invitationPeriodStoreFake{period},
		invitationAcademicUnitStoreFake{}, invitationRoleStoreFake{}, authorizer, mail, invitationHasherFake{}, auditor, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu", 15*time.Minute, func() string { return raw }, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newInvitationService() error = %v", err)
	}
	invocation := NewInvocation(model.Principal{UserID: inviterID}, model.RequestMetadata{})
	view, err := service.IssueStudentClass(context.Background(), invocation, IssueStudentClassInvitationCommand{
		TargetEmail: " Student@Example.edu ", ClassID: classID.String(), IntendedStartsAt: model.MillisFromTime(period.StartsAt),
		IntendedEndsAt: model.MillisFromTime(period.EndsAt), SuggestedUsername: "student-one", SuggestedDisplayName: "Student One", SuggestedLocale: "en",
	})
	if err != nil {
		t.Fatalf("IssueStudentClass() error = %v", err)
	}
	if view.ID != persistence.invitation.ID || strings.Contains(view.String(), raw) || persistence.issued.Invitation.ClaimHash != model.HashInvitationClaim(raw) ||
		strings.Contains(string(persistence.issued.Delivery.EncryptedPayload), raw) || !strings.Contains(mail.issueURL, "/join#token=") {
		t.Fatalf("unsafe or incomplete issue: view=%#v input=%#v url=%q", view, persistence.issued, mail.issueURL)
	}
	if len(authorizer.actions) != 2 || authorizer.actions[0] != model.ActionInvitationCreate || authorizer.actions[1] != model.ActionClassMembersManage {
		t.Fatalf("authorization actions = %v", authorizer.actions)
	}
	result, err := service.AcceptStudentClass(context.Background(), Invocation{}, AcceptStudentClassInvitationCommand{
		Claim: raw, Password: "correct horse battery staple", Username: "student-one", DisplayName: "Student One", Locale: "en",
	})
	if err != nil {
		t.Fatalf("AcceptStudentClass() error = %v", err)
	}
	if result.User == nil || result.User.Email != "student@example.edu" || !result.User.EmailVerified ||
		persistence.accepted.PasswordCredential.PasswordHash != "encoded:correct horse battery staple" ||
		persistence.accepted.ClaimHash != model.HashInvitationClaim(raw) {
		t.Fatalf("acceptance result/input = %#v / %#v", result, persistence.accepted)
	}
}

func TestInvitationAttemptAccountingLimitsClaimAndSourceBeforeValidation(t *testing.T) {
	now := time.Now()
	accounting, err := newAuthenticationAttemptAccounting(newExpiringAuthenticationAttemptCache(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	limiter := invitationAttemptAccounting{attempts: accounting, policy: LoginRateLimitPolicy{
		Window: time.Minute, MaximumAttempts: 1, MaximumSourceAttempts: 2,
	}}
	identity := model.HashInvitationClaim("malformed")
	if err = limiter.Check(context.Background(), identity, "127.0.0.1"); err != nil {
		t.Fatalf("first Check() error = %v", err)
	}
	purpose, _ := authenticationAttemptPurposeInvitation.keySegment()
	identityLimit := authenticationAttemptLimit{dimension: authenticationAttemptDimensionIdentity, identity: identity}
	intent := authenticationAttemptIntent{purpose: authenticationAttemptPurposeInvitation, qualifier: invitationAcceptanceAttemptQualifier}
	expectedKey := "authentication/attempts/" + purpose + "/identity/" + digestAuthenticationAttempt(intent, identityLimit)
	legacyIntent := intent
	legacyIntent.qualifier = "student-class-accept"
	legacyKey := "authentication/attempts/" + purpose + "/identity/" + digestAuthenticationAttempt(legacyIntent, identityLimit)
	entries := accounting.cache.(*expiringAuthenticationAttemptCache).snapshot()
	if _, ok := entries[expectedKey]; !ok {
		t.Fatalf("purpose-neutral Invitation acceptance counter was not recorded: %#v", entries)
	}
	if _, ok := entries[legacyKey]; ok {
		t.Fatalf("teacher-capable Invitation accounting used legacy student qualifier: %#v", entries)
	}
	err = limiter.Check(context.Background(), identity, "127.0.0.1")
	failure, ok := As(err)
	if !ok || failure.Code() != "authentication.rate_limited" {
		t.Fatalf("second Check() error = %v", err)
	}
}
