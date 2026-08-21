// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mailStoreFake struct {
	enqueue  *store.MailTestEnqueue
	delivery *model.MailDelivery
	gets     int
	lists    int
	mutates  int
	permit   func() *store.MailSendPermit
	classes  []store.MailSendClass
}

func (s *mailStoreFake) EnqueueTest(_ context.Context, input *store.MailTestEnqueue) (*model.MailDelivery, error) {
	s.enqueue = input
	s.delivery = input.Delivery.Clone()
	return s.delivery.Clone(), nil
}
func (s *mailStoreFake) GetDelivery(_ context.Context, id model.MailDeliveryID) (*model.MailDelivery, error) {
	s.gets++
	if s.delivery == nil || s.delivery.ID != id {
		return nil, store.NewErrNotFound("mail_delivery", id.String())
	}
	return s.delivery.Clone(), nil
}
func (s *mailStoreFake) ListDeliveries(_ context.Context, _ store.MailDeliveryListOptions) ([]*model.MailDelivery, error) {
	s.lists++
	if s.delivery == nil {
		return []*model.MailDelivery{}, nil
	}
	return []*model.MailDelivery{s.delivery.Clone()}, nil
}
func (s *mailStoreFake) CancelDelivery(_ context.Context, input *store.MailDeliveryMutation) (*model.MailDelivery, error) {
	s.mutates++
	if s.delivery == nil || s.delivery.ID != input.ID || s.delivery.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("mail_delivery", "stale", nil)
	}
	next, err := s.delivery.Cancel(time.UnixMilli(input.AuditAt))
	if err == nil {
		s.delivery = next
	}
	return next, err
}
func (s *mailStoreFake) RetryDelivery(_ context.Context, input *store.MailDeliveryMutation) (*model.MailDelivery, error) {
	s.mutates++
	if s.delivery == nil || s.delivery.ID != input.ID || s.delivery.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("mail_delivery", "stale", nil)
	}
	next, err := s.delivery.OperatorRetry(time.UnixMilli(input.AuditAt))
	if err == nil {
		s.delivery = next
	}
	return next, err
}
func (s *mailStoreFake) AcquireSendPermit(_ context.Context, class store.MailSendClass) (*store.MailSendPermit, error) {
	s.classes = append(s.classes, class)
	if s.permit != nil {
		return s.permit(), nil
	}
	return &store.MailSendPermit{Allowed: true}, nil
}

func TestCredentialMailDescriptorUsesDedicatedPoolAndReserve(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, job, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	job.Type = model.JobTypeMailDeliverCredential
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	descriptor := appjobs.NewMailDeliveryDescriptor(persistence, sender, sealer, nil, nil, jobMailDeliveryIsRelevant,
		func() time.Time { return at.Add(time.Second) }, true)
	if descriptor.Type != model.JobTypeMailDeliverCredential || descriptor.Concurrency != 4 {
		t.Fatalf("credential descriptor = type %q concurrency %d", descriptor.Type, descriptor.Concurrency)
	}
	outcome := descriptor.Handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(persistence.classes) != 1 || persistence.classes[0] != store.MailSendCredential {
		t.Fatalf("outcome=%#v permit classes=%#v", outcome, persistence.classes)
	}
}
func (s *mailStoreFake) StartDelivery(_ context.Context, id model.MailDeliveryID, revision int64, at time.Time) (*model.MailDelivery, error) {
	if s.delivery == nil || s.delivery.ID != id || s.delivery.Revision != revision {
		return nil, store.NewErrConflict("mail_delivery", "stale", nil)
	}
	next, err := s.delivery.Start(at)
	if err == nil {
		s.delivery = next
	}
	return next, err
}
func (s *mailStoreFake) CompleteDelivery(_ context.Context, input *store.MailDeliveryCompletion) (*model.MailDelivery, error) {
	if s.delivery == nil || s.delivery.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("mail_delivery", "stale", nil)
	}
	var next *model.MailDelivery
	var err error
	switch input.Kind {
	case store.MailDeliveryCompletionAccepted:
		next, err = s.delivery.Accept(input.At)
	case store.MailDeliveryCompletionRetry:
		next, err = s.delivery.Retry(input.PublicFailureCode, input.At)
	case store.MailDeliveryCompletionFailed:
		next, err = s.delivery.Fail(input.PublicFailureCode, input.At)
	case store.MailDeliveryCompletionExpired:
		next, err = s.delivery.Expire(input.At)
	case store.MailDeliveryCompletionSuppress:
		next, err = s.delivery.Suppress(input.PublicFailureCode, input.At)
	}
	if err == nil {
		s.delivery = next
	}
	return next, err
}

type mailUserStoreFake struct{ user *model.User }

func (s mailUserStoreFake) Get(context.Context, string) (*model.User, error) {
	if s.user == nil {
		return nil, store.NewErrNotFound("user", "missing")
	}
	copy := *s.user
	return &copy, nil
}

type mailAuthorizerFake struct {
	actions []model.Action
	err     error
}

func (a *mailAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action) (model.Resource, error) {
	a.actions = append(a.actions, action)
	if a.err != nil {
		return model.Resource{}, a.err
	}
	return model.Resource{Type: model.ResourceInstitution, ID: model.NewInstitutionID().String()}, nil
}

type mailAuditFake struct {
	calls      int
	beginCalls int
	failCalls  int
}

func (a *mailAuditFake) PrepareTest(_ context.Context, invocation Invocation, resource model.Resource, id model.MailDeliveryID) (*model.AuditEvent, error) {
	a.calls++
	principal := invocation.Principal()
	return &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: id.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: resource.ID, Status: model.AuditStatusSuccess, NodeID: "test", ClientType: string(principal.ClientType), AuthMethod: principal.AuthenticationMethod}, nil
}
func (a *mailAuditFake) PrepareControl(_ context.Context, _ Invocation, institution model.Resource, delivery *model.MailDelivery, operation string) (string, error) {
	a.beginCalls++
	if institution.Type != model.ResourceInstitution || delivery == nil || (operation != "cancel" && operation != "retry") {
		return "", errors.New("unsafe mail audit")
	}
	return model.NewId(), nil
}
func (a *mailAuditFake) Fail(context.Context, string, string) error { a.failCalls++; return nil }

type mailRendererFake struct{ content FrozenMailContent }

func (r mailRendererFake) Render(model.MailTemplateKey, string, string) (FrozenMailContent, error) {
	return r.content, nil
}

func (r mailRendererFake) RenderPersonalAccessTokenSecurityNotice(model.MailTemplateKey, string, PersonalAccessTokenMailDetails) (FrozenMailContent, error) {
	return r.content, nil
}

func (r mailRendererFake) RenderExamManagerNotice(model.MailTemplateKey, string, ExamManagerMailDetails) (FrozenMailContent, error) {
	return r.content, nil
}

func (r mailRendererFake) RenderClassTransitionNotice(model.MailTemplateKey, string, ClassTransitionMailDetails) (FrozenMailContent, error) {
	return r.content, nil
}

func (r mailRendererFake) RenderSubmissionReceipt(model.MailTemplateKey, string, SubmissionReceiptMailDetails) (FrozenMailContent, error) {
	return r.content, nil
}

func (r mailRendererFake) RenderResultRelease(model.MailTemplateKey, string, ResultReleaseMailDetails) (FrozenMailContent, error) {
	return r.content, nil
}

type mailSenderFake struct {
	enabled   bool
	from      MailAddress
	messages  []OutboundMail
	err       error
	outcome   MailTransportOutcome
	afterSend func()
}

func (s *mailSenderFake) Enabled() bool     { return s.enabled }
func (s *mailSenderFake) From() MailAddress { return s.from }
func (s *mailSenderFake) Send(_ context.Context, message OutboundMail) (MailTransportOutcome, error) {
	s.messages = append(s.messages, message)
	if s.afterSend != nil {
		s.afterSend()
	}
	if s.err != nil {
		return s.outcome, s.err
	}
	return MailTransportUnknown, nil
}

type mailAttemptCacheFake struct{ counts map[string]int64 }

func (c *mailAttemptCacheFake) Add(_ context.Context, key string, delta int64, _ time.Duration) (int64, error) {
	if c.counts == nil {
		c.counts = map[string]int64{}
	}
	c.counts[key] += delta
	return c.counts[key], nil
}
func (c *mailAttemptCacheFake) Delete(context.Context, string) error { return nil }

func mailTestSealer(t *testing.T) *secretseal.Sealer {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytesRepeat(7, 32))
	value, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: secretseal.MaximumPlaintextBytes})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
func mailTestPrincipal(at time.Time) model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewSessionCredentialID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: at}
}
func mailTestUser(principal model.Principal, at time.Time) *model.User {
	user := &model.User{Email: "operator@example.test", DisplayName: "Operator", EmailVerified: true, Locale: "en", Timezone: "UTC"}
	user.PrepareCreate(principal.UserID, at.Add(-time.Hour))
	return user
}

func TestDirectMailPreparerFreezesEncryptedCredentialPayload(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	user := mailTestUser(principal, at)
	actionURL := "https://proctor.example.test/account/verify-email#token=credential-secret"
	content := FrozenMailContent{Subject: "Verify", Text: "Use " + actionURL, HTML: "<a href=\"" + actionURL + "\">Verify</a>"}
	preparer, err := newDirectMailPreparer(mailRendererFake{content: content}, &mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PrepareDirect(DirectMailPreparation{Recipient: user, OccurrenceID: model.NewMailOccurrenceID(),
		Kind: model.MailOccurrenceAccountToken, TemplateKey: model.MailTemplateIdentityVerifyEmail,
		ActionURL: actionURL, At: at, Deadline: at.Add(time.Hour), JobType: model.JobTypeMailDeliverCredential})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Job.Type != model.JobTypeMailDeliverCredential || prepared.Delivery.State != model.MailDeliveryQueued ||
		prepared.Delivery.TemplateKey != model.MailTemplateIdentityVerifyEmail || len(prepared.Delivery.EncryptedPayload) == 0 {
		t.Fatalf("prepared direct mail = %#v", prepared)
	}
	if strings.Contains(string(prepared.Delivery.EncryptedPayload), "credential-secret") || strings.Contains(string(prepared.Delivery.EncryptedPayload), user.Email) {
		t.Fatalf("persisted payload exposes credential or recipient: %s", prepared.Delivery.EncryptedPayload)
	}
	opened, err := appjobs.OpenFrozenMailPayload(mailTestSealer(t), prepared.Delivery)
	if err != nil || opened.Text != content.Text || opened.RecipientAddress != user.Email {
		t.Fatalf("opened payload = %#v, %v", opened, err)
	}
}

func TestRelationshipMailComposerSupportsCompleteCatalog(t *testing.T) {
	at := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	user := mailTestUser(mailTestPrincipal(at), at)
	preparer, err := newDirectMailPreparer(
		mailRendererFake{content: FrozenMailContent{Subject: "Relationship changed", Text: "Relationship changed", HTML: "<p>Relationship changed</p>"}},
		&mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}},
		mailTestSealer(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	keys := []model.MailTemplateKey{
		model.MailTemplateAcademicUnitAssigned,
		model.MailTemplateAcademicUnitAssignmentEnded,
		model.MailTemplateAuthorizationScopedRoleAssigned,
		model.MailTemplateAuthorizationScopedRoleEnded,
		model.MailTemplateAuthorizationInstitutionRoleAssigned,
		model.MailTemplateAuthorizationInstitutionRoleEnded,
	}
	for _, key := range keys {
		key := key
		t.Run(string(key), func(t *testing.T) {
			prepared, prepareErr := preparer.PrepareRelationshipTransition(relationshipTransitionMailPreparation{
				Recipient: user, OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: key, ActionAt: at,
			})
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			if prepared.Occurrence.TemplateKey != key || prepared.Occurrence.Kind != model.MailOccurrenceAcademicAdministration ||
				prepared.Delivery.TemplateKey != key || prepared.Delivery.State != model.MailDeliveryQueued ||
				prepared.Job.Type != model.JobTypeMailDeliver {
				t.Fatalf("prepared relationship mail = %#v", prepared)
			}
		})
	}
}

func TestDirectMailPreparerSelectsTeacherInvitationTemplate(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	unitID := model.NewAcademicUnitID()
	invitation, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: "teacher@example.test", AcademicUnitID: unitID,
		RoleID: model.NewRoleID(), RoleActions: []string{string(model.ActionAcademicUnitView)}, IntendedStartsAt: at,
		InviterUserID: model.NewUserID(), ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	preparer, err := newDirectMailPreparer(mailRendererFake{content: FrozenMailContent{Subject: "Invite", Text: "Invite", HTML: "<p>Invite</p>"}},
		&mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}, mailTestSealer(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PrepareInvitation(invitation, "https://proctor.example.test/join#token=secret")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Delivery.TemplateKey != model.MailTemplateAccessTeacherAcademicUnitInvitation ||
		prepared.Occurrence.TemplateKey != model.MailTemplateAccessTeacherAcademicUnitInvitation {
		t.Fatalf("teacher Invitation mail = %#v", prepared)
	}
}

func TestDirectMailPreparerSelectsScopedRoleInvitationTemplates(t *testing.T) {
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	unitID, institutionID := model.NewAcademicUnitID(), model.NewInstitutionID()
	for _, test := range []struct {
		name       string
		purpose    model.InvitationPurpose
		unitID     model.AcademicUnitID
		scopeType  model.RoleScopeType
		scopeID    string
		template   model.MailTemplateKey
		permission model.Action
	}{
		{name: "academic unit", purpose: model.InvitationPurposeAcademicUnitRole, unitID: unitID,
			scopeType: model.RoleScopeAcademicUnit, scopeID: unitID.String(), template: model.MailTemplateAccessAcademicUnitRoleInvitation,
			permission: model.ActionAcademicAuditView},
		{name: "institution", purpose: model.InvitationPurposeInstitutionRole,
			scopeType: model.RoleScopeInstitution, scopeID: institutionID.String(), template: model.MailTemplateAccessInstitutionRoleInvitation,
			permission: model.ActionAuditView},
	} {
		t.Run(test.name, func(t *testing.T) {
			invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: test.purpose,
				TargetEmail: "existing@example.test", AcademicUnitID: test.unitID, RoleID: model.NewRoleID(), RoleActions: []string{string(test.permission)},
				IntendedStartsAt: at, InviterUserID: model.NewUserID(), ScopeType: test.scopeType, ScopeID: test.scopeID,
				ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), IssuedAt: at})
			if err != nil {
				t.Fatal(err)
			}
			preparer, err := newDirectMailPreparer(mailRendererFake{content: FrozenMailContent{Subject: "Invite", Text: "Invite", HTML: "<p>Invite</p>"}},
				&mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}, mailTestSealer(t))
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := preparer.PrepareInvitation(invitation, "https://proctor.example.test/join#token=secret")
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Delivery.TemplateKey != test.template || prepared.Occurrence.TemplateKey != test.template {
				t.Fatalf("scoped Role Invitation mail = %#v", prepared)
			}
		})
	}
}

func TestDirectMailPreparerCreatesTerminalSuppressedIntentWhenDisabled(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	preparer, err := newDirectMailPreparer(mailRendererFake{}, &mailSenderFake{enabled: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PrepareDirect(DirectMailPreparation{Recipient: mailTestUser(principal, at),
		OccurrenceID: model.NewMailOccurrenceID(), Kind: model.MailOccurrenceSecurityNotice,
		TemplateKey: model.MailTemplateIdentityPasswordChanged, At: at,
		Deadline: at.Add(24 * time.Hour), JobType: model.JobTypeMailDeliver})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Delivery.State != model.MailDeliverySuppressed ||
		prepared.Delivery.PublicFailureCode != model.MailDeliveryDisabledCode ||
		len(prepared.Delivery.EncryptedPayload) != 0 || prepared.Job.Status != model.JobStatusCanceled {
		t.Fatalf("disabled direct mail = %#v", prepared)
	}
}

func TestMailServicePreparesOnlyControlledSelfRecipientAndAtomicIntent(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	persistence := &mailStoreFake{}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@institution.test"}}
	authorizer, auditor := &mailAuthorizerFake{}, &mailAuditFake{}
	content := FrozenMailContent{Subject: "Fixed test", Text: "fixed text", HTML: "<p>fixed html</p>"}
	service, err := newMailService(persistence, mailUserStoreFake{mailTestUser(principal, at)}, authorizer, auditor, &authenticationAttemptAccounting{cache: &mailAttemptCacheFake{}}, mailRendererFake{content}, sender, NopMailDeliveryRecorder{}, mailTestSealer(t), 15*time.Minute, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	woke := 0
	service.wake = func() { woke++ }
	view, err := service.SendTest(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	if err != nil {
		t.Fatal(err)
	}
	if persistence.enqueue == nil || persistence.enqueue.Delivery.TargetUserID != principal.UserID || persistence.enqueue.Occurrence.ActorUserID != principal.UserID || persistence.enqueue.Job.Type != model.JobTypeMailDeliver || auditor.calls != 1 || woke != 1 {
		t.Fatalf("atomic intent = %#v audit=%d wake=%d", persistence.enqueue, auditor.calls, woke)
	}
	serialized, _ := json.Marshal(persistence.enqueue)
	if strings.Contains(string(serialized), "operator@example.test") || strings.Contains(string(serialized), "fixed text") {
		t.Fatalf("durable intent leaked plaintext: %s", serialized)
	}
	if view.MaskedRecipient != "o***@example.test" || view.State != model.MailDeliveryQueued || view.MessageID != persistence.enqueue.Delivery.MessageID {
		t.Fatalf("view = %#v", view)
	}
}

func TestMailMetricsRequiresMailViewAuthorization(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	authorizer := &mailAuthorizerFake{}
	metrics := &recordingMailMetrics{health: []MailHealthMetric{{Code: MailHealthHealthy}}}
	service, err := newMailService(
		&mailStoreFake{}, mailUserStoreFake{mailTestUser(principal, at)}, authorizer, &mailAuditFake{},
		&authenticationAttemptAccounting{cache: &mailAttemptCacheFake{}}, mailRendererFake{},
		&mailSenderFake{}, metrics, nil, 15*time.Minute, func() time.Time { return at },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Metrics(context.Background(), NewInvocation(principal, model.RequestMetadata{})); err != nil {
		t.Fatal(err)
	}
	if len(authorizer.actions) != 1 || authorizer.actions[0] != model.ActionMailView {
		t.Fatalf("metric authorization actions = %#v", authorizer.actions)
	}
}

func TestMaskMailAddressNeverReturnsACompleteAddress(t *testing.T) {
	for input, want := range map[string]string{
		"a@example.test":        "***@example.test",
		"operator@example.test": "o***@example.test",
	} {
		if got := maskMailAddress(input); got != want || got == input {
			t.Fatalf("maskMailAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMailServiceRejectsPATUnverifiedAndRateLimitedBeforePersistence(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	user := mailTestUser(principal, at)
	persistence := &mailStoreFake{}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	content := FrozenMailContent{Subject: "test", Text: "test"}
	cache := &mailAttemptCacheFake{}
	service, err := newMailService(persistence, mailUserStoreFake{user}, &mailAuthorizerFake{}, &mailAuditFake{}, &authenticationAttemptAccounting{cache: cache}, mailRendererFake{content}, sender, NopMailDeliveryRecorder{}, mailTestSealer(t), 15*time.Minute, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	pat := principal
	pat.SessionID = ""
	pat.CredentialType = model.CredentialPersonalAccessToken
	pat.CredentialID = model.PrincipalCredentialID(model.NewPersonalAccessTokenID())
	pat.AuthenticationStrength = ""
	pat.AuthenticatedAt = time.Time{}
	pat.ClientType = model.SessionClientCLI
	pat.CredentialScopes = []string{string(model.ActionMailManage)}
	if _, err = service.SendTest(context.Background(), NewInvocation(pat, model.RequestMetadata{})); !Is(err, "authentication.invalid_token") {
		t.Fatalf("PAT error = %v", err)
	}
	stale := principal
	stale.AuthenticatedAt = at.Add(-16 * time.Minute)
	if _, err = service.SendTest(context.Background(), NewInvocation(stale, model.RequestMetadata{})); !Is(err, "authentication.reauthentication_required") {
		t.Fatalf("stale Session error = %v", err)
	}
	user.EmailVerified = false
	if _, err = service.SendTest(context.Background(), NewInvocation(principal, model.RequestMetadata{})); !Is(err, "mail.recipient_unverified") {
		t.Fatalf("unverified error = %v", err)
	}
	user.EmailVerified = true
	for index := 1; index < mailTestRateLimitMaximum; index++ {
		if _, err = service.SendTest(context.Background(), NewInvocation(principal, model.RequestMetadata{})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.SendTest(context.Background(), NewInvocation(principal, model.RequestMetadata{})); !Is(err, "mail.test.rate_limited") {
		t.Fatalf("rate limit error = %v", err)
	}
}

func TestMailOperatorReadsAuthorizeBeforeInspectionAndReturnOnlySafeViews(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	delivery := queuedMailDeliveryFixtureForApp(at)
	persistence := &mailStoreFake{delivery: delivery}
	authorizer := &mailAuthorizerFake{}
	service, err := newMailService(persistence, mailUserStoreFake{mailTestUser(principal, at)}, authorizer, &mailAuditFake{}, &authenticationAttemptAccounting{cache: &mailAttemptCacheFake{}}, mailRendererFake{}, &mailSenderFake{}, NopMailDeliveryRecorder{}, nil, 15*time.Minute, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	page, appErr := service.List(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ListMailDeliveriesQuery{States: []model.MailDeliveryState{model.MailDeliveryQueued}, Limit: 20})
	if appErr != nil || len(page.Items) != 1 || persistence.lists != 1 || authorizer.actions[0] != model.ActionMailView {
		t.Fatalf("List() page=%#v err=%v lists=%d actions=%v", page, appErr, persistence.lists, authorizer.actions)
	}
	authorizer.err = NewError("authorization.denied")
	if _, appErr = service.Get(context.Background(), NewInvocation(principal, model.RequestMetadata{}), delivery.ID); !Is(appErr, "authorization.denied") || persistence.gets != 0 {
		t.Fatalf("denied Get() err=%v gets=%d", appErr, persistence.gets)
	}
	encoded, _ := json.Marshal(page)
	for _, forbidden := range []string{"ciphertext", "secret", "encrypted_payload", "operator@example.test"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("safe page leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestMailOperatorMutationsRequireRecentSessionAndAuditSameDelivery(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	delivery := queuedMailDeliveryFixtureForApp(at.Add(-time.Minute))
	persistence := &mailStoreFake{delivery: delivery}
	auditor := &mailAuditFake{}
	service, err := newMailService(persistence, mailUserStoreFake{mailTestUser(principal, at)}, &mailAuthorizerFake{}, auditor, &authenticationAttemptAccounting{cache: &mailAttemptCacheFake{}}, mailRendererFake{}, &mailSenderFake{}, NopMailDeliveryRecorder{}, nil, 15*time.Minute, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	pat := principal
	pat.SessionID = ""
	pat.CredentialType = model.CredentialPersonalAccessToken
	pat.CredentialID = model.PrincipalCredentialID(model.NewPersonalAccessTokenID())
	pat.AuthenticationStrength, pat.AuthenticatedAt = "", time.Time{}
	if _, appErr := service.Cancel(context.Background(), NewInvocation(pat, model.RequestMetadata{}), delivery.ID); !Is(appErr, "authentication.invalid_token") || persistence.gets != 0 {
		t.Fatalf("Cancel(PAT) err=%v gets=%d", appErr, persistence.gets)
	}
	stale := principal
	stale.AuthenticatedAt = at.Add(-16 * time.Minute)
	if _, appErr := service.Cancel(context.Background(), NewInvocation(stale, model.RequestMetadata{}), delivery.ID); !Is(appErr, "authentication.reauthentication_required") || persistence.gets != 0 {
		t.Fatalf("Cancel(stale) err=%v gets=%d", appErr, persistence.gets)
	}
	view, appErr := service.Cancel(context.Background(), NewInvocation(principal, model.RequestMetadata{}), delivery.ID)
	if appErr != nil || view.State != model.MailDeliveryCanceled || persistence.mutates != 1 || auditor.beginCalls != 1 {
		t.Fatalf("Cancel() view=%#v err=%v mutates=%d audit=%d", view, appErr, persistence.mutates, auditor.beginCalls)
	}
}

func queuedMailDeliveryFixtureForApp(at time.Time) *model.MailDelivery {
	return &model.MailDelivery{ID: model.NewMailDeliveryID(), OccurrenceID: model.NewMailOccurrenceID(), JobID: model.NewJobID(), TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour), MessageID: "<mail." + model.NewId() + "@example.test>", EncryptedPayload: json.RawMessage(`{"ciphertext":"secret"}`), Revision: 1}
}

func TestMailServiceRejectsEnabledDeliveryWithoutSecretSealer(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	content := FrozenMailContent{Subject: "test", Text: "test"}
	_, err := newMailService(
		&mailStoreFake{}, mailUserStoreFake{mailTestUser(principal, at)}, &mailAuthorizerFake{}, &mailAuditFake{},
		&authenticationAttemptAccounting{cache: &mailAttemptCacheFake{}}, mailRendererFake{content},
		&mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}, NopMailDeliveryRecorder{}, nil,
		15*time.Minute, func() time.Time { return at },
	)
	if err == nil || !strings.Contains(err.Error(), "enabled mail requires secret sealing") {
		t.Fatalf("newMailService(enabled without sealer) error = %v", err)
	}
}

func TestMailDeliveryHandlerUsesStableMessageIDAndRecordsAcceptance(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer := mailTestSealer(t)
	id := model.NewMailDeliveryID()
	payload := frozenMailPayloadV1{Version: 1, RecipientName: "Operator", RecipientAddress: "operator@example.test", FromName: "Proctor", FromAddress: "no-reply@example.test", Subject: "test", Text: "text", HTML: "<p>html</p>", AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, _ := json.Marshal(payload)
	envelope, _ := sealer.Seal(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: id.String()}, plaintext)
	encrypted, _ := json.Marshal(envelope)
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: id})
	job, _ := model.NewJob(model.NewJobID(), model.JobTypeMailDeliver, 1, command, id.String(), at, at, model.MailMaximumAttempts)
	delivery := &model.MailDelivery{ID: id, OccurrenceID: model.NewMailOccurrenceID(), JobID: job.ID, TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour), MessageID: "<mail." + id.String() + "@example.test>", EncryptedPayload: encrypted, Revision: 1}
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: payload.FromAddress}}
	now := at.Add(time.Second)
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { now = now.Add(time.Second); return now }, job)
	if outcome.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 1 || sender.messages[0].MessageID != delivery.MessageID ||
		sender.messages[0].Headers["Auto-Submitted"][0] != payload.AutoSubmitted || sender.messages[0].Headers["X-Auto-Response-Suppress"][0] != payload.AutoResponseSuppress ||
		persistence.delivery.State != model.MailDeliveryAccepted || len(persistence.delivery.EncryptedPayload) != 0 {
		t.Fatalf("outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	}
	second := runMailDeliveryJob(persistence, sender, sealer, time.Now, job)
	if second.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 1 {
		t.Fatalf("terminal replay resent: %#v", second)
	}
}

func TestMailDeliveryHandlerExpiresQueuedDeliveryWithoutSending(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, job, delivery := mailDeliveryHandlerFixture(t, at, time.Second)
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return delivery.Deadline }, job)
	if outcome.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 0 || persistence.delivery.State != model.MailDeliverySuppressed ||
		persistence.delivery.PublicFailureCode != model.MailDeliveryExpiredCode || len(persistence.delivery.EncryptedPayload) != 0 {
		t.Fatalf("outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	}
}

func TestMailDeliveryHandlerSuppressesDisabledWorkWithoutSealerOrSend(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	_, job, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: false}
	outcome := runMailDeliveryJob(persistence, sender, nil, func() time.Time { return at.Add(time.Second) }, job)
	if outcome.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 0 || persistence.delivery.State != model.MailDeliverySuppressed ||
		persistence.delivery.PublicFailureCode != model.MailDeliveryDisabledCode || len(persistence.delivery.EncryptedPayload) != 0 {
		t.Fatalf("outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	}
}

func TestMailDeliveryHandlerRechecksDisabledStateAfterWaitingForPermit(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, job, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	sender := &mailSenderFake{enabled: true}
	persistence := &mailStoreFake{delivery: delivery}
	persistence.permit = func() *store.MailSendPermit {
		sender.enabled = false
		return &store.MailSendPermit{Allowed: true}
	}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(time.Second) }, job)
	if outcome.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 0 || persistence.delivery.State != model.MailDeliverySuppressed ||
		persistence.delivery.PublicFailureCode != model.MailDeliveryDisabledCode || len(persistence.delivery.EncryptedPayload) != 0 {
		t.Fatalf("outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	}
}

func TestMailDeliveryHandlerExpiresRetryWhenSMTPReturnsAfterDeadline(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, job, delivery := mailDeliveryHandlerFixture(t, at, 2*time.Second)
	persistence := &mailStoreFake{delivery: delivery}
	now := at.Add(time.Second)
	sender := &mailSenderFake{
		enabled: true, from: MailAddress{Address: "no-reply@example.test"},
		outcome: MailTransportTemporary, err: errors.New("temporary SMTP failure"),
		afterSend: func() { now = delivery.Deadline },
	}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return now }, job)
	if outcome.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 1 || persistence.delivery.State != model.MailDeliverySuppressed ||
		persistence.delivery.PublicFailureCode != model.MailDeliveryExpiredCode || len(persistence.delivery.EncryptedPayload) != 0 {
		t.Fatalf("outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	}
}

func mailDeliveryHandlerFixture(t *testing.T, at time.Time, deadlineAfter time.Duration) (*secretseal.Sealer, *model.Job, *model.MailDelivery) {
	t.Helper()
	sealer := mailTestSealer(t)
	id := model.NewMailDeliveryID()
	payload := frozenMailPayloadV1{Version: 1, RecipientName: "Operator", RecipientAddress: "operator@example.test", FromName: "Proctor", FromAddress: "no-reply@example.test", Subject: "test", Text: "text", AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, _ := json.Marshal(payload)
	envelope, _ := sealer.Seal(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: id.String()}, plaintext)
	encrypted, _ := json.Marshal(envelope)
	command, _ := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: id})
	job, _ := model.NewJob(model.NewJobID(), model.JobTypeMailDeliver, 1, command, id.String(), at, at, model.MailMaximumAttempts)
	delivery := &model.MailDelivery{ID: id, OccurrenceID: model.NewMailOccurrenceID(), JobID: job.ID, TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(deadlineAfter), MessageID: "<mail." + id.String() + "@example.test>", EncryptedPayload: encrypted, Revision: 1}
	return sealer, job, delivery
}

func runMailDeliveryJob(persistence appjobs.MailDeliveryLifecycleStore, sender MailDeliverySender,
	sealer *secretseal.Sealer, now func() time.Time, job *model.Job,
) jobengine.Outcome {
	descriptor := appjobs.NewMailDeliveryDescriptor(persistence, sender, sealer, nil, nil, jobMailDeliveryIsRelevant, now, false)
	return descriptor.Handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
}

func TestMailDeliveryHandlerClassifiesRetryableAndPermanentFailures(t *testing.T) {
	if appjobs.MailTransportFailureCode(MailTransportAcceptanceUncertain) != "mail.transport.acceptance_uncertain" {
		t.Fatal("uncertain outcome lost")
	}
	for outcome, want := range map[MailTransportOutcome]string{MailTransportTemporary: "mail.transport.temporary", MailTransportPermanent: "mail.transport.permanent", MailTransportUnknown: "mail.transport.unknown"} {
		if got := appjobs.MailTransportFailureCode(outcome); got != want {
			t.Fatalf("code(%s)=%s", outcome, got)
		}
	}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}, outcome: MailTransportTemporary, err: errors.New("secret provider detail")}
	if outcome, err := sender.Send(context.Background(), OutboundMail{}); outcome != MailTransportTemporary || err == nil {
		t.Fatal("portable classification lost across application port")
	}
}
