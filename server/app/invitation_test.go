// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type invitationStoreFake struct {
	invitation *model.Invitation
	issued     *store.StudentClassInvitationIssue
	accepted   *store.StudentClassInvitationAcceptance
}

func (f *invitationStoreFake) IssueStudentClass(_ context.Context, input *store.StudentClassInvitationIssue) (*model.Invitation, error) {
	f.issued = input
	f.invitation = input.Invitation
	return input.Invitation, nil
}
func (f *invitationStoreFake) Get(context.Context, model.InvitationID) (*model.Invitation, error) {
	return f.invitation, nil
}
func (f *invitationStoreFake) GetByClaimHash(_ context.Context, hash string) (*model.Invitation, error) {
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
func (f *invitationStoreFake) Maintain(context.Context, int) (*store.InvitationMaintenanceResult, error) {
	return &store.InvitationMaintenanceResult{}, nil
}

type invitationClassStoreFake struct{ class *model.Class }

func (f invitationClassStoreFake) Get(context.Context, string) (*model.Class, error) {
	return f.class, nil
}

type invitationPeriodStoreFake struct{ period *model.AcademicPeriod }

func (f invitationPeriodStoreFake) Get(context.Context, string) (*model.AcademicPeriod, error) {
	return f.period, nil
}

type invitationAuthorizerFake struct {
	actions []model.Action
	err     error
}

func (f *invitationAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action, _ model.Resource) error {
	f.actions = append(f.actions, action)
	return f.err
}

type invitationMailPreparerFake struct {
	issueURL      string
	disabled      bool
	directJobType model.JobType
}

func (f *invitationMailPreparerFake) Enabled() bool { return !f.disabled }
func (f *invitationMailPreparerFake) PrepareInvitation(invitation *model.Invitation, actionURL string) (*preparedDirectMail, error) {
	f.issueURL = actionURL
	return invitationPreparedMail(invitation.InviterUserID, "", invitation.ID, model.MailOccurrenceID(invitation.ID.String()), model.MailTemplateAccessStudentClassInvitation, invitation.CreatedAt, invitation.ExpiresAt), nil
}
func (f *invitationMailPreparerFake) PrepareDirect(request DirectMailPreparation) (*preparedDirectMail, error) {
	f.directJobType = request.JobType
	prepared := invitationPreparedMail(request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, request.TemplateKey, request.At, request.Deadline)
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: prepared.Delivery.ID})
	prepared.Job, _ = model.NewJob(prepared.Job.ID, request.JobType, 1, command, prepared.Delivery.ID.String(), request.At, request.At, model.MailMaximumAttempts)
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

func TestInvitationIssueAuthorizesBeforeInspectingMailCapability(t *testing.T) {
	now := model.TimeFromMillis(1_800_000_000_000)
	periodID, classID := model.NewAcademicPeriodID(), model.NewClassID()
	authorizer := &invitationAuthorizerFake{err: NewError("authorization.denied")}
	mail := &invitationMailPreparerFake{disabled: true}
	service, err := newInvitationService(&invitationStoreFake{}, invitationClassStoreFake{&model.Class{ID: classID, AcademicPeriodID: periodID}},
		invitationPeriodStoreFake{&model.AcademicPeriod{ID: periodID, StartsAt: now, EndsAt: now.Add(24 * time.Hour)}}, authorizer, mail,
		invitationHasherFake{}, &mutationAttemptAuditorFake{}, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu",
		model.NewCredentialToken, func() time.Time { return now })
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
	service, err := newInvitationService(persistence, invitationClassStoreFake{}, invitationPeriodStoreFake{}, &invitationAuthorizerFake{}, mail,
		invitationHasherFake{}, &mutationAttemptAuditorFake{}, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu",
		model.NewCredentialToken, func() time.Time { return now.Add(time.Minute) })
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
		authorizer, mail, invitationHasherFake{}, auditor, invitationAttemptLimiterFake{}, "node-1", "https://proctor.example.edu", func() string { return raw }, func() time.Time { return now })
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
	if err = limiter.Check(context.Background(), model.HashInvitationClaim("malformed"), "127.0.0.1"); err != nil {
		t.Fatalf("first Check() error = %v", err)
	}
	err = limiter.Check(context.Background(), model.HashInvitationClaim("malformed"), "127.0.0.1")
	failure, ok := As(err)
	if !ok || failure.Code() != "authentication.rate_limited" {
		t.Fatalf("second Check() error = %v", err)
	}
}
