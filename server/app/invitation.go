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

// InvitationView intentionally excludes the target mailbox, claim digest, and
// raw claim. Delivery is the only boundary allowed to observe the raw claim.
type InvitationView struct {
	ID               model.InvitationID
	Purpose          model.InvitationPurpose
	State            model.InvitationState
	ClassID          model.ClassID
	AcademicPeriodID model.AcademicPeriodID
	IntendedStartsAt time.Time
	IntendedEndsAt   model.OptionalTime
	ExpiresAt        time.Time
}

type InvitationAcceptanceView struct {
	Invitation  InvitationView
	User        *model.User
	Affiliation *model.Affiliation
	ClassMember *model.ClassMember
	Replayed    bool
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
type invitationAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}
type invitationPasswordHasher interface{ Hash(string) (string, error) }
type invitationAttemptLimiter interface {
	Check(context.Context, string, string) error
}
type invitationMailPreparer interface {
	Enabled() bool
	PrepareInvitation(*model.Invitation, string) (*preparedDirectMail, error)
	PrepareDirect(DirectMailPreparation) (*preparedDirectMail, error)
}

type invitationService struct {
	store             store.InvitationStore
	classes           invitationClassReader
	periods           invitationPeriodReader
	authorization     invitationAuthorizer
	mail              invitationMailPreparer
	hasher            invitationPasswordHasher
	audit             mutationAuditor
	attempts          invitationAttemptLimiter
	nodeID, publicURL string
	newClaim          func() string
	now               func() time.Time
}

type invitationAuthorizationAdapter struct{ authorization *accessControlService }

func (a invitationAuthorizationAdapter) Authorize(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource) error {
	if a.authorization == nil {
		return NewError("invitation.unavailable")
	}
	return a.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata())
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
	authorization invitationAuthorizer, mail invitationMailPreparer, hasher invitationPasswordHasher, audit mutationAuditor,
	attempts invitationAttemptLimiter, nodeID, publicURL string, newClaim func() string, now func() time.Time,
) (*invitationService, error) {
	if persistence == nil || classes == nil || periods == nil || authorization == nil || mail == nil || hasher == nil || audit == nil ||
		attempts == nil || nodeID == "" || publicURL == "" || newClaim == nil || now == nil {
		return nil, errors.New("invitation service dependencies are invalid")
	}
	return &invitationService{store: persistence, classes: classes, periods: periods, authorization: authorization,
		mail: mail, hasher: hasher, audit: audit, attempts: attempts, nodeID: nodeID, publicURL: publicURL, newClaim: newClaim, now: now}, nil
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

func invitationView(invitation *model.Invitation) InvitationView {
	if invitation == nil {
		return InvitationView{}
	}
	return InvitationView{ID: invitation.ID, Purpose: invitation.Purpose, State: invitation.State, ClassID: invitation.ClassID,
		AcademicPeriodID: invitation.AcademicPeriodID, IntendedStartsAt: invitation.IntendedStartsAt,
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

func invalidInvitationError(err error) error { return NewError("invitation.invalid").Wrap(err) }

type invitationAttemptAccounting struct {
	attempts *authenticationAttemptAccounting
	policy   LoginRateLimitPolicy
}

func (a invitationAttemptAccounting) Check(ctx context.Context, identity, source string) error {
	if a.attempts == nil || a.policy.Window <= 0 || a.policy.MaximumAttempts <= 0 || a.policy.MaximumSourceAttempts <= 0 {
		return rateLimitUnavailableAppError(errors.New("invitation attempt accounting is unavailable"))
	}
	_, limited, err := a.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeInvitation, qualifier: "student-class-accept", window: a.policy.Window,
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
