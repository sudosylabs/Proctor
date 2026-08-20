// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const invitationAcceptanceMailLifetime = 24 * time.Hour

type IssueStudentClassInvitationCommand struct {
	TargetEmail, ClassID                                   string
	IntendedStartsAt, IntendedEndsAt                       int64
	SuggestedUsername, SuggestedDisplayName                string
	SuggestedFirstName, SuggestedLastName, SuggestedLocale string
}

type AcceptStudentClassInvitationCommand struct {
	Claim, Password, Username, DisplayName, FirstName, LastName, Locale, Timezone, Source string
}

type IssueTeacherAcademicUnitInvitationCommand struct {
	TargetEmail, AcademicUnitID, RoleID                    string
	IntendedStartsAt, IntendedEndsAt                       int64
	SuggestedUsername, SuggestedDisplayName                string
	SuggestedFirstName, SuggestedLastName, SuggestedLocale string
}

type IssueAcademicUnitRoleInvitationCommand struct {
	TargetEmail, AcademicUnitID, RoleID string
	IntendedStartsAt, IntendedEndsAt    int64
}

type IssueInstitutionRoleInvitationCommand struct {
	TargetEmail, InstitutionID, RoleID string
	IntendedStartsAt, IntendedEndsAt   int64
}

// authorizedScopedRoleInvitationIssue is the scope-specific result of the
// caller's authorization, resource, and delegation checks. The shared issue
// path must not infer or repeat those checks.
type authorizedScopedRoleInvitationIssue struct {
	targetEmail                      string
	purpose                          model.InvitationPurpose
	resource                         model.Resource
	scopeType                        model.RoleScopeType
	scopeID, operation               string
	academicUnitID                   model.AcademicUnitID
	role                             *model.Role
	intendedStartsAt, intendedEndsAt int64
}

type AcceptTeacherAcademicUnitInvitationCommand struct {
	Claim, Password, Username, DisplayName, FirstName, LastName, Locale, Timezone, Source string
}

type AcceptAcademicUnitRoleInvitationCommand struct {
	Claim, Source string
}

type AcceptInstitutionRoleInvitationCommand struct {
	Claim, Source string
}

// InvitationView intentionally excludes the target mailbox, claim digest, and
// raw claim. Delivery is the only boundary allowed to observe the raw claim.
type InvitationView struct {
	ID               model.InvitationID
	Purpose          model.InvitationPurpose
	State            model.InvitationState
	ClassID          model.ClassID
	AcademicPeriodID model.AcademicPeriodID
	AcademicUnitID   model.AcademicUnitID
	RoleID           model.RoleID
	RoleActions      []string
	IntendedStartsAt time.Time
	IntendedEndsAt   model.OptionalTime
	ExpiresAt        time.Time
}

type InvitationAcceptanceView struct {
	Invitation         InvitationView
	User               *model.User
	Affiliation        *model.Affiliation
	ClassMember        *model.ClassMember
	AcademicUnitMember *model.AcademicUnitMember
	RoleBinding        *model.RoleBinding
	Replayed           bool
}

type InvitationDeliveryView struct {
	TemplateKey       model.MailTemplateKey
	State             model.MailDeliveryState
	MaskedRecipient   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Deadline          time.Time
	AcceptedAt        model.OptionalTime
	PublicFailureCode string
}

// InvitationAdministrationView is purpose-built for authorized operators. It
// includes the approved recipient and lifecycle fields while keeping claim
// material, rendered mail, provider identity, and transport internals private.
type InvitationAdministrationView struct {
	InvitationView
	TargetEmail    string
	InviterUserID  model.UserID
	AcceptedUserID model.UserID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Revision       int64
	Delivery       *InvitationDeliveryView
}

type InvitationAdministrationPage struct {
	Items []InvitationAdministrationView
	More  bool
}

type ListInvitationsQuery struct {
	Purpose         model.InvitationPurpose
	State           model.InvitationState
	TargetEmail     string
	TargetID        string
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	BeforeCreatedAt time.Time
	BeforeID        model.InvitationID
	Limit           int
}

type ResendInvitationCommand struct {
	ID               string
	ExpectedRevision int64
}

type RevokeInvitationCommand struct {
	ID               string
	ExpectedRevision int64
}

type ReplaceInvitationCommand struct {
	ID, Purpose, TargetEmail, ClassID, AcademicUnitID, InstitutionID, RoleID string
	ExpectedRevision, IntendedStartsAt, IntendedEndsAt                       int64
	SuggestedUsername, SuggestedDisplayName                                  string
	SuggestedFirstName, SuggestedLastName, SuggestedLocale                   string
}

func (v InvitationView) String() string {
	return fmt.Sprintf("Invitation{%s %s %s %s}", v.ID, v.Purpose, v.State, v.ClassID)
}

type invitationClassReader interface {
	Get(context.Context, string) (*model.Class, error)
}
type invitationPeriodReader interface {
	Get(context.Context, string) (*model.AcademicPeriod, error)
}
type invitationAcademicUnitReader interface {
	Get(context.Context, string) (*model.AcademicUnit, error)
}
type invitationRoleReader interface {
	Get(context.Context, string) (*model.Role, error)
}
type invitationAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	CanDelegateActionsAtScope(context.Context, Invocation, []string, model.RoleScopeType, string) error
	Visibility(context.Context, Invocation, model.Action) (store.InvitationVisibilityScope, error)
}
type invitationPasswordHasher interface{ Hash(string) (string, error) }
type invitationAttemptLimiter interface {
	Check(context.Context, string, string) error
}
type invitationMailPreparer interface {
	Enabled() bool
	PrepareInvitation(*model.Invitation, string) (*preparedDirectMail, error)
	PrepareInvitationResend(*model.Invitation, string, model.UserID, time.Time) (*preparedDirectMail, error)
	PrepareInvitationRevocation(*model.Invitation, model.UserID, time.Time) (*preparedDirectMail, error)
	PrepareDirect(DirectMailPreparation) (*preparedDirectMail, error)
}

type invitationService struct {
	store                   store.InvitationStore
	classes                 invitationClassReader
	periods                 invitationPeriodReader
	academicUnits           invitationAcademicUnitReader
	roles                   invitationRoleReader
	authorization           invitationAuthorizer
	mail                    invitationMailPreparer
	hasher                  invitationPasswordHasher
	audit                   mutationAuditor
	attempts                invitationAttemptLimiter
	nodeID, publicURL       string
	newClaim                func() string
	now                     func() time.Time
	recentAuthenticationTTL time.Duration
}

type invitationAuthorizationAdapter struct{ authorization *accessControlService }

func (a invitationAuthorizationAdapter) Authorize(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource) error {
	if a.authorization == nil {
		return NewError("invitation.unavailable")
	}
	return a.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata())
}

func (a invitationAuthorizationAdapter) CanDelegateActionsAtScope(ctx context.Context, invocation Invocation, actions []string, scopeType model.RoleScopeType, scopeID string) error {
	if a.authorization == nil {
		return NewError("invitation.unavailable")
	}
	allowed, err := a.authorization.canDelegateActionsAtScope(ctx, invocation.Principal(), actions, scopeType, scopeID)
	if err != nil {
		return err
	}
	if !allowed {
		return authorizationDeniedError("invitationAuthorizationAdapter.CanDelegateActionsAtScope")
	}
	return nil
}

func (a invitationAuthorizationAdapter) Visibility(ctx context.Context, invocation Invocation, action model.Action) (store.InvitationVisibilityScope, error) {
	if a.authorization == nil || (action != model.ActionInvitationView && action != model.ActionInvitationManage) {
		return store.InvitationVisibilityScope{}, NewError("invitation.unavailable")
	}
	constraint, err := a.authorization.authorizedScopes(ctx, invocation.Principal(), action, model.ResourceInstitution)
	if err != nil {
		return store.InvitationVisibilityScope{}, err
	}
	institution, err := a.authorization.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return store.InvitationVisibilityScope{}, invitationError(err)
	}
	allowed := constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 || len(constraint.ClassIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if len(constraint.AcademicUnitRootIDs) > 0 {
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: constraint.AcademicUnitRootIDs[0]}
		scopeType, scopeID = model.RoleScopeAcademicUnit, resource.ID
	} else if len(constraint.ClassIDs) > 0 {
		resource = model.Resource{Type: model.ResourceClass, ID: constraint.ClassIDs[0]}
		scopeType, scopeID = model.RoleScopeClass, resource.ID
	}
	if err = a.authorization.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource, scopeType, scopeID, invocation.RequestMetadata(), allowed); err != nil {
		return store.InvitationVisibilityScope{}, err
	}
	if !allowed {
		return store.InvitationVisibilityScope{}, authorizationDeniedError("invitationAuthorizationAdapter.Visibility")
	}
	return store.InvitationVisibilityScope{InstitutionWide: constraint.InstitutionWide,
		AcademicUnitRootIDs: append([]string(nil), constraint.AcademicUnitRootIDs...), ClassIDs: append([]string(nil), constraint.ClassIDs...)}, nil
}

type invitationAuditAdapter struct{ audit mutationAuditAdapter }

func (a invitationAuditAdapter) Begin(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource, operation string, value, prior map[string]any) (string, error) {
	return a.audit.Begin(ctx, invocation, action, resource, operation, value, prior)
}
func (a invitationAuditAdapter) BeginAtScope(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (string, error) {
	return a.audit.BeginAtScope(ctx, invocation, action, resource, scopeType, scopeID, operation, value, prior)
}
func (a invitationAuditAdapter) Fail(ctx context.Context, auditID, errorCode string) error {
	return a.audit.Fail(ctx, auditID, errorCode)
}

func newInvitationService(persistence store.InvitationStore, classes invitationClassReader, periods invitationPeriodReader,
	academicUnits invitationAcademicUnitReader, roles invitationRoleReader,
	authorization invitationAuthorizer, mail invitationMailPreparer, hasher invitationPasswordHasher, audit mutationAuditor,
	attempts invitationAttemptLimiter, nodeID, publicURL string, recentAuthenticationTTL time.Duration, newClaim func() string, now func() time.Time,
) (*invitationService, error) {
	if persistence == nil || classes == nil || periods == nil || academicUnits == nil || roles == nil || authorization == nil || mail == nil || hasher == nil || audit == nil ||
		attempts == nil || nodeID == "" || publicURL == "" || recentAuthenticationTTL <= 0 || newClaim == nil || now == nil {
		return nil, errors.New("invitation service dependencies are invalid")
	}
	return &invitationService{store: persistence, classes: classes, periods: periods, academicUnits: academicUnits, roles: roles, authorization: authorization,
		mail: mail, hasher: hasher, audit: audit, attempts: attempts, nodeID: nodeID, publicURL: publicURL,
		recentAuthenticationTTL: recentAuthenticationTTL, newClaim: newClaim, now: now}, nil
}

func (a *App) IssueTeacherAcademicUnitInvitation(ctx context.Context, invocation Invocation, command IssueTeacherAcademicUnitInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueTeacherAcademicUnit(ctx, invocation, command)
}

func (a *App) IssueAcademicUnitRoleInvitation(ctx context.Context, invocation Invocation, command IssueAcademicUnitRoleInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueAcademicUnitRole(ctx, invocation, command)
}

func (a *App) IssueInstitutionRoleInvitation(ctx context.Context, invocation Invocation, command IssueInstitutionRoleInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueInstitutionRole(ctx, invocation, command)
}

func (a *App) IssueStudentClassInvitation(ctx context.Context, invocation Invocation, command IssueStudentClassInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueStudentClass(ctx, invocation, command)
}

func (a *App) AcceptStudentClassInvitation(ctx context.Context, invocation Invocation, command AcceptStudentClassInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptStudentClass(ctx, invocation, command)
}

func (a *App) AcceptTeacherAcademicUnitInvitation(ctx context.Context, invocation Invocation, command AcceptTeacherAcademicUnitInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptTeacherAcademicUnit(ctx, invocation, command)
}

func (a *App) AcceptAcademicUnitRoleInvitation(ctx context.Context, invocation Invocation, command AcceptAcademicUnitRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptAcademicUnitRole(ctx, invocation, command)
}

func (a *App) AcceptInstitutionRoleInvitation(ctx context.Context, invocation Invocation, command AcceptInstitutionRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptInstitutionRole(ctx, invocation, command)
}

func (a *App) ListInvitations(ctx context.Context, invocation Invocation, query ListInvitationsQuery) (InvitationAdministrationPage, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationPage{}, NewError("invitation.unavailable")
	}
	return a.invitations.List(ctx, invocation, query)
}

func (a *App) GetInvitation(ctx context.Context, invocation Invocation, id string) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.GetAdministration(ctx, invocation, id)
}

func (a *App) ResendInvitation(ctx context.Context, invocation Invocation, command ResendInvitationCommand) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.Resend(ctx, invocation, command)
}

func (a *App) RevokeInvitation(ctx context.Context, invocation Invocation, command RevokeInvitationCommand) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.Revoke(ctx, invocation, command)
}

func (a *App) ReplaceInvitation(ctx context.Context, invocation Invocation, command ReplaceInvitationCommand) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.Replace(ctx, invocation, command)
}

func (s *invitationService) List(ctx context.Context, invocation Invocation, query ListInvitationsQuery) (InvitationAdministrationPage, error) {
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationView)
	if err != nil {
		return InvitationAdministrationPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	targetEmail := strings.ToLower(strings.TrimSpace(query.TargetEmail))
	if query.Limit < 1 || query.Limit > 200 || (query.Purpose != "" && !query.Purpose.IsValid()) ||
		(query.State != "" && !query.State.IsValid()) || (targetEmail != "" && !model.IsValidEmail(targetEmail)) ||
		(query.TargetID != "" && !model.IsValidId(query.TargetID)) ||
		(query.BeforeID.IsValid() != !query.BeforeCreatedAt.IsZero()) {
		return InvitationAdministrationPage{}, NewError("invitation.query.invalid")
	}
	page, err := s.store.List(ctx, store.InvitationListOptions{Visibility: visibility, Purpose: query.Purpose, State: query.State,
		TargetEmail: targetEmail, TargetID: strings.TrimSpace(query.TargetID), CreatedAfter: query.CreatedAfter,
		CreatedBefore: query.CreatedBefore, BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID, Limit: query.Limit})
	if err != nil {
		return InvitationAdministrationPage{}, invitationError(err)
	}
	result := InvitationAdministrationPage{Items: []InvitationAdministrationView{}, More: page.More}
	for _, item := range page.Items {
		result.Items = append(result.Items, invitationAdministrationView(item))
	}
	return result, nil
}

func (s *invitationService) GetAdministration(ctx context.Context, invocation Invocation, rawID string) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(rawID))
	if err != nil {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationView)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	record, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	return invitationAdministrationView(record), nil
}

func (s *invitationService) Resend(ctx context.Context, invocation Invocation, command ResendInvitationCommand) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(command.ID))
	if err != nil || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	if !s.mail.Enabled() {
		return InvitationAdministrationView{}, NewError("invitation.mail_unavailable")
	}
	at := model.TimeUTC(s.now())
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitationResend(current.Invitation, actionURL, invocation.Principal().UserID, at)
	if err != nil {
		return InvitationAdministrationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	result, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "resend", Value: current.Invitation.Auditable()}, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
			return s.store.Resend(ctx, &store.InvitationResend{ID: id, ExpectedRevision: command.ExpectedRevision,
				ClaimHash: model.HashInvitationClaim(rawClaim), Occurrence: prepared.Occurrence, Delivery: prepared.Delivery,
				DeliveryJob: prepared.Job, ActorUserID: invocation.Principal().UserID,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, invitationError)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	return invitationAdministrationView(result), nil
}

func (s *invitationService) Revoke(ctx context.Context, invocation Invocation, command RevokeInvitationCommand) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(command.ID))
	if err != nil || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	at := model.TimeUTC(s.now())
	prepared, err := s.mail.PrepareInvitationRevocation(current.Invitation, invocation.Principal().UserID, at)
	if err != nil {
		return InvitationAdministrationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	result, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "revoke", Value: current.Invitation.Auditable()}, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
			return s.store.Revoke(ctx, &store.InvitationRevocation{ID: id, ExpectedRevision: command.ExpectedRevision,
				ActorUserID: invocation.Principal().UserID, RevocationNotice: &store.PreparedMail{Occurrence: prepared.Occurrence,
					Delivery: prepared.Delivery, Job: prepared.Job}, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, invitationError)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	return invitationAdministrationView(result), nil
}

func (s *invitationService) Replace(ctx context.Context, invocation Invocation, command ReplaceInvitationCommand) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(command.ID))
	if err != nil || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	if !s.mail.Enabled() {
		return InvitationAdministrationView{}, NewError("invitation.mail_unavailable")
	}
	replacement, rawClaim, err := s.prepareReplacement(ctx, invocation, command)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(replacement, actionURL)
	if err != nil {
		return InvitationAdministrationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	currentAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "supersede", Value: current.Invitation.Auditable()}
	replacementAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate,
		Resource: invitationResource(replacement), ScopeType: replacement.ScopeType, ScopeID: replacement.ScopeID,
		Operation: "replacement_issue", Value: replacement.Auditable()}
	result, err := runAuditedMutation(ctx, s.audit, currentAttempt, s.now,
		func(ctx context.Context, currentReference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
			return runAuditedMutation(ctx, s.audit, replacementAttempt,
				func() time.Time { return model.TimeFromMillis(currentReference.MutationAtMillis) },
				func(ctx context.Context, replacementReference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
					return s.store.Replace(ctx, &store.InvitationReplacement{CurrentID: id, ExpectedCurrentRevision: command.ExpectedRevision,
						Replacement: replacement, Lifetime: model.InvitationLifetime, Occurrence: prepared.Occurrence,
						Delivery: prepared.Delivery, DeliveryJob: prepared.Job, ActorUserID: invocation.Principal().UserID,
						CurrentAuditEventID: currentReference.ID, ReplacementAuditEventID: replacementReference.ID,
						AuditAt: currentReference.MutationAtMillis})
				}, invitationMutationError)
		}, invitationMutationError)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	return invitationAdministrationView(result), nil
}

func (s *invitationService) prepareReplacement(ctx context.Context, invocation Invocation, command ReplaceInvitationCommand) (*model.Invitation, string, error) {
	purpose := model.InvitationPurpose(strings.TrimSpace(command.Purpose))
	if !purpose.IsValid() {
		return nil, "", NewError("request.invalid").WithField("field", "purpose")
	}
	at := model.TimeUTC(s.now())
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return nil, "", NewError("invitation.unavailable")
	}
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	endsAt := model.OptionalTimeFromMillis(command.IntendedEndsAt)
	actor := invocation.Principal().UserID
	switch purpose {
	case model.InvitationPurposeStudentClass:
		classID, err := model.ParseClassID(strings.TrimSpace(command.ClassID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "class_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceClass, ID: classID.String()}
		for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage} {
			if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		class, err := s.classes.Get(ctx, classID.String())
		if err != nil || class.ID != classID {
			return nil, "", invitationError(err)
		}
		period, err := s.periods.Get(ctx, class.AcademicPeriodID.String())
		if err != nil || period.ID != class.AcademicPeriodID {
			return nil, "", invitationError(err)
		}
		if startsAt.IsZero() {
			startsAt = period.StartsAt
		}
		value, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
			TargetEmail: command.TargetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
			IntendedStartsAt: startsAt, IntendedEndsAt: endsAt,
			Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
				FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale},
			InviterUserID: actor, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
			ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if err != nil {
			return nil, "", domainInvalid("invitation.invalid", err)
		}
		return value, rawClaim, nil
	case model.InvitationPurposeTeacherAcademicUnit, model.InvitationPurposeAcademicUnitRole:
		unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
		}
		roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "role_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
		required := []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}
		if purpose == model.InvitationPurposeAcademicUnitRole {
			required[1] = model.ActionRoleBindingManage
		}
		for _, action := range required {
			if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		unit, err := s.academicUnits.Get(ctx, unitID.String())
		if err != nil || unit.ID != unitID || unit.IsArchived() {
			return nil, "", invitationError(err)
		}
		role, err := s.roles.Get(ctx, roleID.String())
		if err != nil || role.ID != roleID || role.IsArchived() {
			return nil, "", invitationError(err)
		}
		if err = validateInvitationDelegableRole(role, model.ResourceAcademicUnit); err != nil {
			return nil, "", err
		}
		if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
			return nil, "", err
		}
		if startsAt.IsZero() {
			startsAt = at
		}
		if purpose == model.InvitationPurposeTeacherAcademicUnit {
			value, modelErr := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{ID: model.NewInvitationID(),
				TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
				IntendedStartsAt: startsAt, IntendedEndsAt: endsAt,
				Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
					FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale},
				InviterUserID: actor, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
				ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
			if modelErr != nil {
				return nil, "", domainInvalid("invitation.invalid", modelErr)
			}
			return value, rawClaim, nil
		}
		value, modelErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose,
			TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
			IntendedStartsAt: startsAt, IntendedEndsAt: endsAt, InviterUserID: actor,
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if modelErr != nil {
			return nil, "", domainInvalid("invitation.invalid", modelErr)
		}
		return value, rawClaim, nil
	case model.InvitationPurposeInstitutionRole:
		if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
			return nil, "", err
		}
		institutionID, err := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "institution_id").Wrap(err)
		}
		roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "role_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}
		for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
			if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		role, err := s.roles.Get(ctx, roleID.String())
		if err != nil || role.ID != roleID || role.IsArchived() {
			return nil, "", invitationError(err)
		}
		if err = validateInvitationDelegableRole(role, model.ResourceInstitution); err != nil {
			return nil, "", err
		}
		if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeInstitution, institutionID.String()); err != nil {
			return nil, "", err
		}
		if startsAt.IsZero() {
			startsAt = at
		}
		value, modelErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose,
			TargetEmail: command.TargetEmail, RoleID: role.ID, RoleActions: role.Permissions, IntendedStartsAt: startsAt,
			IntendedEndsAt: endsAt, InviterUserID: actor, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(),
			ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if modelErr != nil {
			return nil, "", domainInvalid("invitation.invalid", modelErr)
		}
		return value, rawClaim, nil
	default:
		return nil, "", NewError("request.invalid").WithField("field", "purpose")
	}
}

func invitationResource(invitation *model.Invitation) model.Resource {
	if invitation == nil {
		return model.Resource{}
	}
	typeByScope := map[model.RoleScopeType]model.ResourceType{model.RoleScopeInstitution: model.ResourceInstitution,
		model.RoleScopeAcademicUnit: model.ResourceAcademicUnit, model.RoleScopeClass: model.ResourceClass}
	return model.Resource{Type: typeByScope[invitation.ScopeType], ID: invitation.ScopeID}
}

func invitationAdministrationView(record *store.InvitationAdministrationRecord) InvitationAdministrationView {
	if record == nil || record.Invitation == nil {
		return InvitationAdministrationView{}
	}
	invitation := record.Invitation
	result := InvitationAdministrationView{InvitationView: invitationView(invitation), TargetEmail: invitation.TargetEmail,
		InviterUserID: invitation.InviterUserID, AcceptedUserID: invitation.AcceptedUserID,
		CreatedAt: invitation.CreatedAt, UpdatedAt: invitation.UpdatedAt, Revision: invitation.Revision}
	if record.Delivery != nil {
		delivery := record.Delivery
		result.Delivery = &InvitationDeliveryView{TemplateKey: delivery.TemplateKey, State: delivery.State,
			MaskedRecipient: delivery.MaskedRecipient, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt,
			Deadline: delivery.Deadline, AcceptedAt: delivery.AcceptedAt, PublicFailureCode: delivery.PublicFailureCode}
	}
	return result
}

func (s *invitationService) IssueStudentClass(ctx context.Context, invocation Invocation, command IssueStudentClassInvitationCommand) (InvitationView, error) {
	classID := strings.TrimSpace(command.ClassID)
	parsedClassID, err := model.ParseClassID(classID)
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "class_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceClass, ID: classID}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionInvitationCreate, resource); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionClassMembersManage, resource); err != nil {
		return InvitationView{}, err
	}
	if !s.mail.Enabled() {
		return InvitationView{}, NewError("invitation.mail_unavailable")
	}
	class, err := s.classes.Get(ctx, classID)
	if err != nil {
		return InvitationView{}, invitationError(err)
	}
	period, err := s.periods.Get(ctx, class.AcademicPeriodID.String())
	if err != nil {
		return InvitationView{}, invitationError(err)
	}
	if class.ID != parsedClassID || period.ID != class.AcademicPeriodID {
		return InvitationView{}, NewError("invitation.class_period_invalid")
	}
	issuedAt := model.TimeUTC(s.now())
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	if startsAt.IsZero() {
		startsAt = period.StartsAt
	}
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
		IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
		Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
			FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale},
		InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
		ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: issuedAt,
	})
	if err != nil {
		return InvitationView{}, domainInvalid("invitation.invalid", err)
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	created, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate,
		Resource: resource, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), Operation: "issue_student_class", Value: invitation.Auditable()},
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*model.Invitation, error) {
			return s.store.IssueStudentClass(ctx, &store.StudentClassInvitationIssue{Invitation: invitation, Occurrence: prepared.Occurrence,
				Delivery: prepared.Delivery, DeliveryJob: prepared.Job, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, invitationError)
	if err != nil {
		return InvitationView{}, err
	}
	return invitationView(created), nil
}

func (s *invitationService) IssueTeacherAcademicUnit(ctx context.Context, invocation Invocation, command IssueTeacherAcademicUnitInvitationCommand) (InvitationView, error) {
	unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionInvitationCreate, resource); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitMembersManage, resource); err != nil {
		return InvitationView{}, err
	}
	unit, err := s.academicUnits.Get(ctx, unitID.String())
	if err != nil || unit.ID != unitID || unit.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	role, err := s.roles.Get(ctx, roleID.String())
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	if role.BuiltIn || role.Name == model.SystemAdministratorRoleName || len(role.Permissions) == 0 {
		return InvitationView{}, NewError("invitation.role_not_delegable")
	}
	for _, action := range role.Permissions {
		definition, ok := model.DefinitionForAction(model.Action(action))
		if !ok || definition.RelationshipOnly || !definition.AcceptsResource(model.ResourceAcademicUnit) {
			return InvitationView{}, NewError("invitation.role_not_delegable")
		}
	}
	if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
		return InvitationView{}, err
	}
	if !s.mail.Enabled() {
		return InvitationView{}, NewError("invitation.mail_unavailable")
	}
	issuedAt := model.TimeUTC(s.now())
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	if startsAt.IsZero() {
		startsAt = issuedAt
	}
	invitation, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID,
		RoleActions: role.Permissions, IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
		Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
			FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale},
		InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: issuedAt,
	})
	if err != nil {
		return InvitationView{}, domainInvalid("invitation.invalid", err)
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	created, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate,
		Resource: resource, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), Operation: "issue_teacher_academic_unit", Value: invitation.Auditable()},
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*model.Invitation, error) {
			return s.store.IssueTeacherAcademicUnit(ctx, &store.TeacherAcademicUnitInvitationIssue{Invitation: invitation,
				Lifetime:   model.InvitationLifetime,
				Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, invitationError)
	if err != nil {
		return InvitationView{}, err
	}
	return invitationView(created), nil
}

func (s *invitationService) IssueAcademicUnitRole(ctx context.Context, invocation Invocation, command IssueAcademicUnitRoleInvitationCommand) (InvitationView, error) {
	unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return InvitationView{}, err
		}
	}
	unit, err := s.academicUnits.Get(ctx, unitID.String())
	if err != nil || unit.ID != unitID || unit.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	role, err := s.roles.Get(ctx, roleID.String())
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	if err = validateInvitationDelegableRole(role, model.ResourceAcademicUnit); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
		return InvitationView{}, err
	}
	return s.issueAuthorizedScopedRole(ctx, invocation, authorizedScopedRoleInvitationIssue{
		targetEmail: command.TargetEmail, purpose: model.InvitationPurposeAcademicUnitRole,
		resource: resource, scopeType: model.RoleScopeAcademicUnit, scopeID: unit.ID.String(), operation: "issue_academic_unit_role",
		academicUnitID: unit.ID, role: role, intendedStartsAt: command.IntendedStartsAt, intendedEndsAt: command.IntendedEndsAt,
	})
}

func (s *invitationService) IssueInstitutionRole(ctx context.Context, invocation Invocation, command IssueInstitutionRoleInvitationCommand) (InvitationView, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
		return InvitationView{}, err
	}
	institutionID, err := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "institution_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return InvitationView{}, err
		}
	}
	role, err := s.roles.Get(ctx, roleID.String())
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	if err = validateInvitationDelegableRole(role, model.ResourceInstitution); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeInstitution, institutionID.String()); err != nil {
		return InvitationView{}, err
	}
	return s.issueAuthorizedScopedRole(ctx, invocation, authorizedScopedRoleInvitationIssue{
		targetEmail: command.TargetEmail, purpose: model.InvitationPurposeInstitutionRole,
		resource: resource, scopeType: model.RoleScopeInstitution, scopeID: institutionID.String(), operation: "issue_institution_role",
		role: role, intendedStartsAt: command.IntendedStartsAt, intendedEndsAt: command.IntendedEndsAt,
	})
}

func (s *invitationService) issueAuthorizedScopedRole(ctx context.Context, invocation Invocation, issue authorizedScopedRoleInvitationIssue) (InvitationView, error) {
	if !s.mail.Enabled() {
		return InvitationView{}, NewError("invitation.mail_unavailable")
	}
	issuedAt := model.TimeUTC(s.now())
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	startsAt := model.TimeFromMillis(issue.intendedStartsAt)
	if startsAt.IsZero() {
		startsAt = issuedAt
	}
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
		ID: model.NewInvitationID(), Purpose: issue.purpose,
		TargetEmail: issue.targetEmail, AcademicUnitID: issue.academicUnitID, RoleID: issue.role.ID, RoleActions: issue.role.Permissions,
		IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(issue.intendedEndsAt),
		InviterUserID: invocation.Principal().UserID, ScopeType: issue.scopeType, ScopeID: issue.scopeID,
		ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: issuedAt,
	})
	if err != nil {
		return InvitationView{}, domainInvalid("invitation.invalid", err)
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	created, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate,
		Resource: issue.resource, ScopeType: issue.scopeType, ScopeID: issue.scopeID, Operation: issue.operation, Value: invitation.Auditable()},
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*model.Invitation, error) {
			return s.store.IssueScopedRole(ctx, &store.ScopedRoleInvitationIssue{Invitation: invitation, Lifetime: model.InvitationLifetime,
				Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, invitationError)
	if err != nil {
		return InvitationView{}, err
	}
	return invitationView(created), nil
}

func validateInvitationDelegableRole(role *model.Role, resourceType model.ResourceType) error {
	if role == nil || role.IsArchived() || role.BuiltIn || role.Name == model.SystemAdministratorRoleName || len(role.Permissions) == 0 {
		return NewError("invitation.role_not_delegable")
	}
	for _, action := range role.Permissions {
		definition, ok := model.DefinitionForAction(model.Action(action))
		if !ok || definition.RelationshipOnly || !definition.AcceptsResource(resourceType) {
			return NewError("invitation.role_not_delegable")
		}
	}
	return nil
}

func (s *invitationService) AcceptStudentClass(ctx context.Context, invocation Invocation, command AcceptStudentClassInvitationCommand) (*InvitationAcceptanceView, error) {
	if err := s.attempts.Check(ctx, model.HashInvitationClaim(command.Claim), command.Source); err != nil {
		return nil, err
	}
	if !model.IsValidCredentialToken(command.Claim) {
		return nil, NewError("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(command.Claim)
	invitation, err := s.store.GetByClaimHash(ctx, claimHash)
	if err != nil {
		return nil, invalidInvitationError(err)
	}
	at := model.TimeUTC(s.now())
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
	}
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{Username: command.Username, Email: invitation.TargetEmail,
		EmailVerified: true, DisplayName: command.DisplayName, FirstName: command.FirstName, LastName: command.LastName,
		Locale: command.Locale, Timezone: command.Timezone}, at)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: hash}
	credential.PrepareCreate(model.NewPasswordCredentialID(), at)
	settings, err := prepareInitialUserSettingsDocument(user)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	effectiveStart := invitation.EffectiveStartsAt(at)
	affiliation := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: effectiveStart}
	affiliation.PrepareCreate(model.NewAffiliationID(), at)
	member := &model.ClassMember{ClassID: invitation.ClassID, AcademicPeriodID: invitation.AcademicPeriodID,
		UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
	member.PrepareCreate(model.NewClassMemberID(), at)
	prepared, err := s.mail.PrepareDirect(DirectMailPreparation{
		Recipient: user, OccurrenceID: model.NewMailOccurrenceID(), Kind: model.MailOccurrenceInvitation,
		TemplateKey: model.MailTemplateAccessInvitationAccepted, At: at,
		Deadline: at.Add(invitationAcceptanceMailLifetime), JobType: model.JobTypeMailDeliver,
	})
	if err != nil {
		return nil, NewError("invitation.mail_unavailable").Wrap(err)
	}
	metadata := invocation.RequestMetadata()
	event := &model.AuditEvent{ActorID: user.ID, Action: "invitation.accept",
		Resource: model.Resource{Type: model.ResourceClass, ID: invitation.ClassID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: invitation.ClassID.String(), Status: model.AuditStatusSuccess, RequestID: metadata.RequestID,
		NodeID: s.nodeID, ClientType: "web", AuthMethod: "invitation", IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent}
	result, err := s.store.AcceptStudentClass(ctx, &store.StudentClassInvitationAcceptance{ClaimHash: claimHash,
		AcceptedAt: model.MillisFromTime(at), User: user, Settings: settings, PasswordCredential: credential,
		DefaultProfilePictureJob: defaultJob, Affiliation: affiliation, ClassMember: member,
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job, AuditEvent: event,
		RequiredActions: []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage}})
	if err != nil {
		return nil, invalidInvitationError(err)
	}
	return &InvitationAcceptanceView{Invitation: invitationView(result.Invitation), User: result.User,
		Affiliation: result.Affiliation, ClassMember: result.ClassMember, Replayed: result.Replayed}, nil
}

func (s *invitationService) AcceptTeacherAcademicUnit(ctx context.Context, invocation Invocation, command AcceptTeacherAcademicUnitInvitationCommand) (*InvitationAcceptanceView, error) {
	if err := s.attempts.Check(ctx, model.HashInvitationClaim(command.Claim), command.Source); err != nil {
		return nil, err
	}
	if !model.IsValidCredentialToken(command.Claim) {
		return nil, NewError("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(command.Claim)
	invitation, err := s.store.GetByClaimHash(ctx, claimHash)
	if err != nil || invitation.Purpose != model.InvitationPurposeTeacherAcademicUnit {
		return nil, invalidInvitationError(err)
	}
	at := model.TimeUTC(s.now())
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
	}
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{Username: command.Username, Email: invitation.TargetEmail,
		EmailVerified: true, DisplayName: command.DisplayName, FirstName: command.FirstName, LastName: command.LastName,
		Locale: command.Locale, Timezone: command.Timezone}, at)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: hash}
	credential.PrepareCreate(model.NewPasswordCredentialID(), at)
	settings, err := prepareInitialUserSettingsDocument(user)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	effectiveStart := invitation.EffectiveStartsAt(at)
	affiliation := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationTeacher, StartsAt: effectiveStart}
	affiliation.PrepareCreate(model.NewAffiliationID(), at)
	member := &model.AcademicUnitMember{AcademicUnitID: invitation.AcademicUnitID, UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
	member.PrepareCreate(model.NewAcademicUnitMemberID(), at)
	binding := &model.RoleBinding{UserID: user.ID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
		OriginAcademicUnitMemberID: member.ID,
		ScopeType:                  model.RoleScopeAcademicUnit, ScopeID: invitation.AcademicUnitID.String(), StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
	binding.PrepareCreate(model.NewRoleBindingID(), at)
	prepared, err := s.mail.PrepareDirect(DirectMailPreparation{Recipient: user, OccurrenceID: model.NewMailOccurrenceID(),
		Kind: model.MailOccurrenceInvitation, TemplateKey: model.MailTemplateAccessInvitationAccepted, At: at,
		Deadline: at.Add(invitationAcceptanceMailLifetime), JobType: model.JobTypeMailDeliver})
	if err != nil {
		return nil, NewError("invitation.mail_unavailable").Wrap(err)
	}
	metadata := invocation.RequestMetadata()
	event := &model.AuditEvent{ActorID: user.ID, Action: "invitation.accept",
		Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: invitation.AcademicUnitID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: invitation.AcademicUnitID.String(), Status: model.AuditStatusSuccess, RequestID: metadata.RequestID,
		NodeID: s.nodeID, ClientType: "web", AuthMethod: "invitation", IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent}
	result, err := s.store.AcceptTeacherAcademicUnit(ctx, &store.TeacherAcademicUnitInvitationAcceptance{ClaimHash: claimHash,
		AcceptedAt: model.MillisFromTime(at), User: user, Settings: settings, PasswordCredential: credential,
		DefaultProfilePictureJob: defaultJob, Affiliation: affiliation, AcademicUnitMember: member, RoleBinding: binding,
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job, AuditEvent: event,
		RequiredActions: []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}})
	if err != nil {
		return nil, invalidInvitationError(err)
	}
	return &InvitationAcceptanceView{Invitation: invitationView(result.Invitation), User: result.User, Affiliation: result.Affiliation,
		AcademicUnitMember: result.AcademicUnitMember, RoleBinding: result.RoleBinding, Replayed: result.Replayed}, nil
}

func (s *invitationService) AcceptAcademicUnitRole(ctx context.Context, invocation Invocation, command AcceptAcademicUnitRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	return s.acceptScopedRole(ctx, invocation, command.Claim, command.Source, model.InvitationPurposeAcademicUnitRole)
}

func (s *invitationService) AcceptInstitutionRole(ctx context.Context, invocation Invocation, command AcceptInstitutionRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	return s.acceptScopedRole(ctx, invocation, command.Claim, command.Source, model.InvitationPurposeInstitutionRole)
}

func (s *invitationService) acceptScopedRole(ctx context.Context, invocation Invocation, claim, source string, purpose model.InvitationPurpose) (*InvitationAcceptanceView, error) {
	if err := s.attempts.Check(ctx, model.HashInvitationClaim(claim), source); err != nil {
		return nil, err
	}
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, invalidTokenAppError()
	}
	if !model.IsValidCredentialToken(claim) {
		return nil, NewError("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(claim)
	invitation, err := s.store.GetByClaimHash(ctx, claimHash)
	if err != nil || invitation.Purpose != purpose {
		return nil, invalidInvitationError(err)
	}
	at := model.TimeUTC(s.now())
	binding := &model.RoleBinding{UserID: principal.UserID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, StartsAt: invitation.EffectiveStartsAt(at), EndsAt: invitation.IntendedEndsAt}
	binding.PrepareCreate(model.NewRoleBindingID(), at)
	if err = binding.Validate(); err != nil {
		return nil, NewError("invitation.invalid").Wrap(err)
	}
	resourceType := model.ResourceAcademicUnit
	if purpose == model.InvitationPurposeInstitutionRole {
		resourceType = model.ResourceInstitution
	}
	result, err := runAuditedMutation(ctx, s.audit, mutationAttempt{
		Invocation: invocation, Action: model.Action("invitation.accept"),
		Resource:  model.Resource{Type: resourceType, ID: invitation.ScopeID},
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID,
		Operation: "accept_scoped_role_invitation", Value: invitation.Auditable(),
	}, s.now, func(ctx context.Context, attempt mutationAttemptReference) (*store.ScopedRoleInvitationAcceptanceResult, error) {
		return s.store.AcceptScopedRole(ctx, &store.ScopedRoleInvitationAcceptance{ClaimHash: claimHash,
			UserID: principal.UserID, RoleBinding: binding, AuditEventID: attempt.ID, AuditAt: attempt.MutationAtMillis,
			RequiredActions: []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}})
	}, invalidInvitationError)
	if err != nil {
		return nil, err
	}
	return &InvitationAcceptanceView{Invitation: invitationView(result.Invitation), User: result.User,
		RoleBinding: result.RoleBinding, Replayed: result.Replayed}, nil
}

func invitationView(invitation *model.Invitation) InvitationView {
	if invitation == nil {
		return InvitationView{}
	}
	return InvitationView{ID: invitation.ID, Purpose: invitation.Purpose, State: invitation.State, ClassID: invitation.ClassID,
		AcademicPeriodID: invitation.AcademicPeriodID, AcademicUnitID: invitation.AcademicUnitID, RoleID: invitation.RoleID,
		RoleActions: append([]string(nil), invitation.RoleActions...), IntendedStartsAt: invitation.IntendedStartsAt,
		IntendedEndsAt: invitation.IntendedEndsAt, ExpiresAt: invitation.ExpiresAt}
}

func invitationError(err error) error {
	if store.IsNotFound(err) {
		return NewError("resource.not_found").Wrap(err)
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) {
		return NewError("invitation.conflict").Wrap(err)
	}
	var invalid *store.ErrInvalidInput
	if errors.As(err, &invalid) {
		return NewError("invitation.invalid").Wrap(err)
	}
	return NewError("invitation.unavailable").Wrap(err)
}

func invitationMutationError(err error) error {
	if _, ok := As(err); ok {
		return err
	}
	return invitationError(err)
}

func invalidInvitationError(err error) error { return NewError("invitation.invalid").Wrap(err) }

type invitationAttemptAccounting struct {
	attempts *authenticationAttemptAccounting
	policy   LoginRateLimitPolicy
}

const invitationAcceptanceAttemptQualifier = "claim-accept"

func (a invitationAttemptAccounting) Check(ctx context.Context, identity, source string) error {
	if a.attempts == nil || a.policy.Window <= 0 || a.policy.MaximumAttempts <= 0 || a.policy.MaximumSourceAttempts <= 0 {
		return rateLimitUnavailableAppError(errors.New("invitation attempt accounting is unavailable"))
	}
	_, limited, err := a.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeInvitation, qualifier: invitationAcceptanceAttemptQualifier, window: a.policy.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentity, maximum: a.policy.MaximumAttempts, identity: identity},
			{dimension: authenticationAttemptDimensionSource, maximum: a.policy.MaximumSourceAttempts, source: source},
		},
	})
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if limited {
		return NewError("authentication.rate_limited")
	}
	return nil
}
