// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mailStoreFake struct {
	enqueue     *store.MailTestEnqueue
	delivery    *model.MailDelivery
	gets        int
	lists       int
	listLimit   int
	listCopies  int
	mutates     int
	permit      func() *store.MailSendPermit
	classes     []store.MailSendClass
	completeErr error
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
func (s *mailStoreFake) ListDeliveries(_ context.Context, options store.MailDeliveryListOptions) ([]*model.MailDelivery, error) {
	s.lists++
	s.listLimit = options.Limit
	if s.delivery == nil {
		return []*model.MailDelivery{}, nil
	}
	deliveries := []*model.MailDelivery{s.delivery.Clone()}
	for range s.listCopies {
		deliveries = append(deliveries, s.delivery.Clone())
	}
	return deliveries, nil
}
func (s *mailStoreFake) CancelDelivery(_ context.Context, input *store.MailDeliveryMutation) (*model.MailDelivery, error) {
	s.mutates++
	if s.delivery == nil || s.delivery.ID != input.ID || s.delivery.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("mail_delivery", "stale", nil)
	}
	next, err := s.delivery.Cancel(model.TimeUTC(input.AuditAt))
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
	next, err := s.delivery.OperatorRetry(model.TimeUTC(input.AuditAt))
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
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	record.Type = model.JobTypeMailDeliverCredential
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	descriptor := jobhandler.NewMailDeliveryDescriptor(persistence, sender, sealer, nil, nil, jobMailDeliveryIsRelevant,
		func() time.Time { return at.Add(time.Second) }, true)
	require.Equal(t, model.JobTypeMailDeliverCredential, descriptor.Type, "credential descriptor = type")
	require.Equal(t, 4, descriptor.Concurrency, "credential descriptor = type")
	outcome := descriptor.Handler.Run(context.Background(), job.NewExecution(record, nil, nil, nil))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v permit classes", outcome)
	require.Len(t, persistence.classes, 1, "outcome=%#v permit classes", outcome)
	require.Equal(t, store.MailSendCredential, persistence.classes[0], "outcome=%#v permit classes", outcome)
}
func (s *mailStoreFake) StartDelivery(_ context.Context, input *store.MailDeliveryStart) (*model.MailDelivery, error) {
	id := input.ID
	revision := input.ExpectedRevision
	at := input.At
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
	if s.completeErr != nil {
		return nil, s.completeErr
	}
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

type jobMailMetricsFake struct {
	values   []jobhandler.MailDeliveryMetric
	attempts []jobhandler.MailAttemptMetric
}

func (r *jobMailMetricsFake) RecordJobMailDelivery(_ context.Context, metric jobhandler.MailDeliveryMetric) {
	r.values = append(r.values, metric)
}

func (r *jobMailMetricsFake) RecordJobMailAttempt(_ context.Context, metric jobhandler.MailAttemptMetric) {
	r.attempts = append(r.attempts, metric)
}

type mailUserStoreFake struct{ user *model.User }

func (s mailUserStoreFake) Get(context.Context, model.UserID) (*model.User, error) {
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

func (a *mailAuthorizerFake) AuthorizeInstitution(_ context.Context, _ invocation.Invocation, action model.Action) (model.Resource, error) {
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

func (a *mailAuditFake) PrepareTest(_ context.Context, invocation invocation.Invocation, resource model.Resource, id model.MailDeliveryID) (*model.AuditEvent, error) {
	a.calls++
	principal := invocation.Principal()
	return &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: id.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: resource.ID, Status: model.AuditStatusSuccess, NodeID: "test", ClientType: string(principal.ClientType), AuthMethod: principal.AuthenticationMethod}, nil
}
func (a *mailAuditFake) PrepareControl(_ context.Context, _ invocation.Invocation, institution model.Resource, delivery *model.MailDelivery, operation string) (model.AuditEventID, error) {
	a.beginCalls++
	if institution.Type != model.ResourceInstitution || delivery == nil || (operation != "cancel" && operation != "retry") {
		return "", errors.New("unsafe mail audit")
	}
	return model.NewAuditEventID(), nil
}
func (a *mailAuditFake) Fail(context.Context, model.AuditEventID, string) error {
	a.failCalls++
	return nil
}

type mailRendererFake struct{ content FrozenMailContent }

func (r mailRendererFake) Render(MailRenderRequest) (FrozenMailContent, error) {
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
	require.NoError(t, err)
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
	require.NoError(t, err)
	prepared, err := preparer.PrepareEmailVerification(AccountTokenMailPreparation{Recipient: user,
		OccurrenceID: model.NewMailOccurrenceID(), ActionURL: actionURL, At: at, Deadline: at.Add(time.Hour)})
	require.NoError(t, err)
	require.Equal(t, model.JobTypeMailDeliverCredential, prepared.Job.Type)
	require.Equal(t, model.MailDeliveryQueued, prepared.Delivery.State)
	require.Equal(t, model.MailTemplateIdentityVerifyEmail, prepared.Delivery.TemplateKey)
	require.NotEmpty(t, prepared.Delivery.EncryptedPayload)
	require.NotContains(t, string(prepared.Delivery.EncryptedPayload), "credential-secret")
	require.NotContains(t, string(prepared.Delivery.EncryptedPayload), user.Email)
	opened, err := mail.OpenDelivery(mailTestSealer(t), prepared.Delivery)
	require.NoError(t, err, "opened payload = %#v", opened)
	require.Equal(t, content.Text, opened.Text, "opened payload = %#v", opened)
	require.Equal(t, user.Email, opened.To.Address, "opened payload = %#v", opened)
}

func TestRelationshipMailComposerSupportsCompleteCatalog(t *testing.T) {
	at := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	user := mailTestUser(mailTestPrincipal(at), at)
	preparer, err := newDirectMailPreparer(
		mailRendererFake{content: FrozenMailContent{Subject: "Relationship changed", Text: "Relationship changed", HTML: "<p>Relationship changed</p>"}},
		&mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}},
		mailTestSealer(t),
	)
	require.NoError(t, err)
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
			require.NoError(t, prepareErr)
			require.Equal(t, key, prepared.Occurrence.TemplateKey)
			require.Equal(t, model.MailOccurrenceAcademicAdministration, prepared.Occurrence.Kind)
			require.Equal(t, key, prepared.Delivery.TemplateKey)
			require.Equal(t, model.MailDeliveryQueued, prepared.Delivery.State)
			require.Equal(t, model.JobTypeMailDeliver, prepared.Job.Type)
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
	require.NoError(t, err)
	preparer, err := newDirectMailPreparer(mailRendererFake{content: FrozenMailContent{Subject: "Invite", Text: "Invite", HTML: "<p>Invite</p>"}},
		&mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}, mailTestSealer(t))
	require.NoError(t, err)
	prepared, err := preparer.PrepareInvitation(invitation, "https://proctor.example.test/join#token=secret")
	require.NoError(t, err)
	require.Equal(t, model.MailTemplateAccessTeacherAcademicUnitInvitation, prepared.Delivery.TemplateKey)
	require.Equal(t, model.MailTemplateAccessTeacherAcademicUnitInvitation, prepared.Occurrence.TemplateKey)
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
			require.NoError(t, err)
			preparer, err := newDirectMailPreparer(mailRendererFake{content: FrozenMailContent{Subject: "Invite", Text: "Invite", HTML: "<p>Invite</p>"}},
				&mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}, mailTestSealer(t))
			require.NoError(t, err)
			prepared, err := preparer.PrepareInvitation(invitation, "https://proctor.example.test/join#token=secret")
			require.NoError(t, err)
			require.Equal(t, test.template, prepared.Delivery.TemplateKey)
			require.Equal(t, test.template, prepared.Occurrence.TemplateKey)
		})
	}
}

func TestDirectMailPreparerCreatesTerminalSuppressedIntentWhenDisabled(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	preparer, err := newDirectMailPreparer(mailRendererFake{}, &mailSenderFake{enabled: false}, nil)
	require.NoError(t, err)
	prepared, err := preparer.PreparePasswordChanged(NoticeMailPreparation{Recipient: mailTestUser(principal, at), At: at})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, prepared.Delivery.State)
	require.Equal(t, model.MailDeliveryDisabledCode, prepared.Delivery.PublicFailureCode)
	require.Empty(t, prepared.Delivery.EncryptedPayload)
	require.Equal(t, model.JobStatusCanceled, prepared.Job.Status)
}

func TestMailServicePreparesOnlyControlledSelfRecipientAndAtomicIntent(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	persistence := &mailStoreFake{}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@institution.test"}}
	authorizer, auditor := &mailAuthorizerFake{}, &mailAuditFake{}
	content := FrozenMailContent{Subject: "Fixed test", Text: "fixed text", HTML: "<p>fixed html</p>"}
	service, err := newMailService(mailServiceDependencies{Mail: persistence, Users: mailUserStoreFake{mailTestUser(principal, at)}, Authorization: authorizer, Audit: auditor, Attempts: &authenticationAttemptAccounting{deps: authenticationAttemptAccountingDependencies{Cache: &mailAttemptCacheFake{}}}, Renderer: mailRendererFake{content}, Sender: sender, Metrics: NopMailDeliveryRecorder{}, Sealer: mailTestSealer(t), RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return at }})
	require.NoError(t, err)
	woke := 0
	service.wake = func() { woke++ }
	view, err := service.SendTestMail(context.Background(), invocation.New(principal, model.RequestMetadata{}))
	require.NoError(t, err)
	require.NotNil(t, persistence.enqueue, "atomic intent")
	require.Equal(t, principal.UserID, persistence.enqueue.Delivery.TargetUserID, "atomic intent")
	require.Equal(t, principal.UserID, persistence.enqueue.Occurrence.ActorUserID, "atomic intent")
	require.Equal(t, model.JobTypeMailDeliver, persistence.enqueue.Job.Type, "atomic intent")
	require.Equal(t, 1, auditor.calls, "atomic intent")
	require.Equal(t, 1, woke, "atomic intent")
	serialized, _ := json.Marshal(persistence.enqueue)
	require.NotContains(t, string(serialized), "operator@example.test")
	require.NotContains(t, string(serialized), "fixed text")
	require.Equal(t, "o***@example.test", view.MaskedRecipient)
	require.Equal(t, model.MailDeliveryQueued, view.State)
	require.Equal(t, persistence.enqueue.Delivery.MessageID, view.MessageID)
}

func TestMailMetricsRequiresMailViewAuthorization(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	authorizer := &mailAuthorizerFake{}
	metrics := &recordingMailMetrics{health: []MailHealthMetric{{Code: MailHealthHealthy}}}
	service, err := newMailService(mailServiceDependencies{Mail: &mailStoreFake{}, Users: mailUserStoreFake{mailTestUser(principal, at)}, Authorization: authorizer, Audit: &mailAuditFake{}, Attempts: &authenticationAttemptAccounting{deps: authenticationAttemptAccountingDependencies{Cache: &mailAttemptCacheFake{}}}, Renderer: mailRendererFake{}, Sender: &mailSenderFake{}, Metrics: metrics, Sealer: nil, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return at }})
	require.NoError(t, err)
	_, err = service.GetMailMetrics(context.Background(), invocation.New(principal, model.RequestMetadata{}))
	require.NoError(t, err)
	require.Len(t, authorizer.actions, 1)
	require.Equal(t, model.ActionMailView, authorizer.actions[0])
}

func TestMailServiceRejectsPATUnverifiedAndRateLimitedBeforePersistence(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	user := mailTestUser(principal, at)
	persistence := &mailStoreFake{}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	content := FrozenMailContent{Subject: "test", Text: "test"}
	cache := &mailAttemptCacheFake{}
	service, err := newMailService(mailServiceDependencies{Mail: persistence, Users: mailUserStoreFake{user}, Authorization: &mailAuthorizerFake{}, Audit: &mailAuditFake{}, Attempts: &authenticationAttemptAccounting{deps: authenticationAttemptAccountingDependencies{Cache: cache}}, Renderer: mailRendererFake{content}, Sender: sender, Metrics: NopMailDeliveryRecorder{}, Sealer: mailTestSealer(t), RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return at }})
	require.NoError(t, err)
	pat := principal
	pat.SessionID = ""
	pat.CredentialType = model.CredentialPersonalAccessToken
	pat.CredentialID = model.PrincipalCredentialID(model.NewPersonalAccessTokenID())
	pat.AuthenticationStrength = ""
	pat.AuthenticatedAt = time.Time{}
	pat.ClientType = model.SessionClientCLI
	pat.CredentialScopes = []string{string(model.ActionMailManage)}
	_, err = service.SendTestMail(context.Background(), invocation.New(pat, model.RequestMetadata{}))
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "PAT error = %v", err)
	stale := principal
	stale.AuthenticatedAt = at.Add(-16 * time.Minute)
	_, err = service.SendTestMail(context.Background(), invocation.New(stale, model.RequestMetadata{}))
	require.True(t, apperror.Is(err, "authentication.reauthentication_required"), "stale Session error = %v", err)
	user.EmailVerified = false
	_, err = service.SendTestMail(context.Background(), invocation.New(principal, model.RequestMetadata{}))
	require.True(t, apperror.Is(err, "mail.recipient_unverified"), "unverified error = %v", err)
	user.EmailVerified = true
	for index := 1; index < mailTestRateLimitMaximum; index++ {
		_, err = service.SendTestMail(context.Background(), invocation.New(principal, model.RequestMetadata{}))
		require.NoError(t, err)
	}
	_, err = service.SendTestMail(context.Background(), invocation.New(principal, model.RequestMetadata{}))
	require.True(t, apperror.Is(err, "mail.test.rate_limited"), "rate limit error = %v", err)
}

func TestMailOperatorReadsAuthorizeBeforeInspectionAndReturnOnlySafeViews(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	delivery := queuedMailDeliveryFixtureForApp(at)
	persistence := &mailStoreFake{delivery: delivery}
	authorizer := &mailAuthorizerFake{}
	service, err := newMailService(mailServiceDependencies{Mail: persistence, Users: mailUserStoreFake{mailTestUser(principal, at)}, Authorization: authorizer, Audit: &mailAuditFake{}, Attempts: &authenticationAttemptAccounting{deps: authenticationAttemptAccountingDependencies{Cache: &mailAttemptCacheFake{}}}, Renderer: mailRendererFake{}, Sender: &mailSenderFake{}, Metrics: NopMailDeliveryRecorder{}, Sealer: nil, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return at }})
	require.NoError(t, err)
	page, appErr := service.ListMailDeliveries(context.Background(), invocation.New(principal, model.RequestMetadata{}), ListMailDeliveriesQuery{States: []model.MailDeliveryState{model.MailDeliveryQueued}, Limit: 20})
	require.NoError(t, appErr, "List() page=%#v err=%v lists=%d actions=%v", page, appErr, persistence.lists, authorizer.actions)
	require.Len(t, page.Items, 1, "List() page=%#v err=%v lists=%d actions=%v", page, appErr, persistence.lists, authorizer.actions)
	require.Equal(t, 1, persistence.lists, "List() page=%#v err=%v lists=%d actions=%v", page, appErr, persistence.lists, authorizer.actions)
	require.Equal(t, model.ActionMailView, authorizer.actions[0], "List() page=%#v err=%v lists=%d actions=%v", page, appErr, persistence.lists, authorizer.actions)
	require.Equal(t, 21, persistence.listLimit, "the read probes one past the page")
	require.False(t, page.HasMore)
	full, appErr := service.ListMailDeliveries(context.Background(), invocation.New(principal, model.RequestMetadata{}), ListMailDeliveriesQuery{Limit: 1})
	require.NoError(t, appErr)
	require.Len(t, full.Items, 1)
	require.False(t, full.HasMore, "an exactly full final page has no successor")
	persistence.listCopies = 1
	more, appErr := service.ListMailDeliveries(context.Background(), invocation.New(principal, model.RequestMetadata{}), ListMailDeliveriesQuery{Limit: 1})
	require.NoError(t, appErr)
	require.Len(t, more.Items, 1)
	require.True(t, more.HasMore)
	persistence.listCopies = 0
	authorizer.err = apperror.New("authorization.denied")
	_, appErr = service.GetMailDelivery(context.Background(), invocation.New(principal, model.RequestMetadata{}), delivery.ID)
	require.True(t, apperror.Is(appErr, "authorization.denied"), "denied Get() err=%v gets", appErr)
	require.Equal(t, 0, persistence.gets, "denied Get() err=%v gets", appErr)
	encoded, _ := json.Marshal(page)
	for _, forbidden := range []string{"ciphertext", "secret", "encrypted_payload", "operator@example.test"} {
		require.NotContains(t, string(encoded), forbidden, "safe page leaked")
	}
}

func TestMailOperatorMutationsRequireRecentSessionAndAuditSameDelivery(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	delivery := queuedMailDeliveryFixtureForApp(at.Add(-time.Minute))
	persistence := &mailStoreFake{delivery: delivery}
	auditor := &mailAuditFake{}
	service, err := newMailService(mailServiceDependencies{Mail: persistence, Users: mailUserStoreFake{mailTestUser(principal, at)}, Authorization: &mailAuthorizerFake{}, Audit: auditor, Attempts: &authenticationAttemptAccounting{deps: authenticationAttemptAccountingDependencies{Cache: &mailAttemptCacheFake{}}}, Renderer: mailRendererFake{}, Sender: &mailSenderFake{}, Metrics: NopMailDeliveryRecorder{}, Sealer: nil, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return at }})
	require.NoError(t, err)
	pat := principal
	pat.SessionID = ""
	pat.CredentialType = model.CredentialPersonalAccessToken
	pat.CredentialID = model.PrincipalCredentialID(model.NewPersonalAccessTokenID())
	pat.AuthenticationStrength, pat.AuthenticatedAt = "", time.Time{}
	_, appErr := service.CancelMailDelivery(context.Background(), invocation.New(pat, model.RequestMetadata{}), delivery.ID)
	require.True(t, apperror.Is(appErr, "authentication.token_invalid"), "Cancel(PAT) err=%v gets", appErr)
	require.Equal(t, 0, persistence.gets, "Cancel(PAT) err=%v gets", appErr)
	stale := principal
	stale.AuthenticatedAt = at.Add(-16 * time.Minute)
	_, appErr = service.CancelMailDelivery(context.Background(), invocation.New(stale, model.RequestMetadata{}), delivery.ID)
	require.True(t, apperror.Is(appErr, "authentication.reauthentication_required"), "Cancel(stale) err=%v gets", appErr)
	require.Equal(t, 0, persistence.gets, "Cancel(stale) err=%v gets", appErr)
	view, appErr := service.CancelMailDelivery(context.Background(), invocation.New(principal, model.RequestMetadata{}), delivery.ID)
	require.NoError(t, appErr, "Cancel() view=%#v err=%v mutates=%d audit", view, appErr, persistence.mutates)
	require.Equal(t, model.MailDeliveryCanceled, view.State, "Cancel() view=%#v err=%v mutates=%d audit", view, appErr, persistence.mutates)
	require.Equal(t, 1, persistence.mutates, "Cancel() view=%#v err=%v mutates=%d audit", view, appErr, persistence.mutates)
	require.Equal(t, 1, auditor.beginCalls, "Cancel() view=%#v err=%v mutates=%d audit", view, appErr, persistence.mutates)
}

func queuedMailDeliveryFixtureForApp(at time.Time) *model.MailDelivery {
	return &model.MailDelivery{ID: model.NewMailDeliveryID(), OccurrenceID: model.NewMailOccurrenceID(), JobID: model.NewJobID(), TargetUserID: model.NewUserID(), TemplateKey: model.MailTemplateSystemTest, TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "o***@example.test", State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: at.Add(24 * time.Hour), MessageID: "<mail." + model.NewID() + "@example.test>", EncryptedPayload: json.RawMessage(`{"ciphertext":"secret"}`), Revision: 1}
}

func TestMailServiceRejectsEnabledDeliveryWithoutSecretSealer(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	content := FrozenMailContent{Subject: "test", Text: "test"}
	_, err := newMailService(mailServiceDependencies{Mail: &mailStoreFake{}, Users: mailUserStoreFake{mailTestUser(principal, at)}, Authorization: &mailAuthorizerFake{}, Audit: &mailAuditFake{}, Attempts: &authenticationAttemptAccounting{deps: authenticationAttemptAccountingDependencies{Cache: &mailAttemptCacheFake{}}}, Renderer: mailRendererFake{content}, Sender: &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}, Metrics: NopMailDeliveryRecorder{}, Sealer: nil, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return at }})
	require.Error(t, err)
	require.Contains(t, err.Error(), "enabled mail requires secret sealing")
}

func TestMailDeliveryHandlerUsesStableMessageIDAndRecordsAcceptance(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, 24*time.Hour)
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "new-sender@example.test"}}
	now := at.Add(time.Second)
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { now = now.Add(time.Second); return now }, record)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Len(t, sender.messages, 1, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, delivery.MessageID, sender.messages[0].MessageID, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.True(t, sender.messages[0].Date.Equal(at), "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, "no-reply@example.test", sender.messages[0].From.Address, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, "auto-generated", sender.messages[0].Headers["Auto-Submitted"][0], "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, "All", sender.messages[0].Headers["X-Auto-Response-Suppress"][0], "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliveryAccepted, persistence.delivery.State, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, persistence.delivery.EncryptedPayload, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	second := runMailDeliveryJob(persistence, sender, sealer, time.Now, record)
	require.Equal(t, job.OutcomeSucceeded, second.Kind)
	require.Len(t, sender.messages, 1)
}

func TestMailDeliveryHandlerRecordsAttemptBeforeCompletionFailure(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	completionErr := errors.New("completion unavailable")
	persistence := &mailStoreFake{delivery: delivery, completeErr: completionErr}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	recorder := &jobMailMetricsFake{}
	descriptor := jobhandler.NewMailDeliveryDescriptor(
		persistence, sender, sealer, recorder, nil, jobMailDeliveryIsRelevant,
		func() time.Time { return at.Add(time.Second) }, false,
	)
	outcome := descriptor.Handler.Run(context.Background(), job.NewExecution(record, nil, nil, nil))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
	require.ErrorIs(t, outcome.Err, completionErr)
	require.Equal(t, model.MailDeliverySending, persistence.delivery.State)
	require.Equal(t, 1, persistence.delivery.AttemptCount)
	require.Empty(t, recorder.values, "mail metrics = deliveries")
	require.Len(t, recorder.attempts, 1, "mail metrics = deliveries")
	require.Equal(t, model.MailDeliverySending, recorder.attempts[0].State, "mail metrics = deliveries")
	require.Equal(t, delivery.TemplateKey, recorder.attempts[0].TemplateKey, "mail metrics = deliveries")
}

func TestMailDeliveryHandlerExpiresQueuedDeliveryWithoutSending(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Second)
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return delivery.Deadline }, record)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, sender.messages, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliverySuppressed, persistence.delivery.State, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliveryExpiredCode, persistence.delivery.PublicFailureCode, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, persistence.delivery.EncryptedPayload, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
}

func TestMailDeliveryHandlerSuppressesDisabledWorkWithoutSealerOrSend(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	_, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: false}
	outcome := runMailDeliveryJob(persistence, sender, nil, func() time.Time { return at.Add(time.Second) }, record)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, sender.messages, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliverySuppressed, persistence.delivery.State, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliveryDisabledCode, persistence.delivery.PublicFailureCode, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, persistence.delivery.EncryptedPayload, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
}

func TestMailDeliveryHandlerRechecksDisabledStateAfterWaitingForPermit(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	sender := &mailSenderFake{enabled: true}
	persistence := &mailStoreFake{delivery: delivery}
	persistence.permit = func() *store.MailSendPermit {
		sender.enabled = false
		return &store.MailSendPermit{Allowed: true}
	}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(time.Second) }, record)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, sender.messages, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliverySuppressed, persistence.delivery.State, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliveryDisabledCode, persistence.delivery.PublicFailureCode, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, persistence.delivery.EncryptedPayload, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
}

func TestMailDeliveryHandlerExpiresRetryWhenSMTPReturnsAfterDeadline(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, 2*time.Second)
	persistence := &mailStoreFake{delivery: delivery}
	now := at.Add(time.Second)
	sender := &mailSenderFake{
		enabled: true, from: MailAddress{Address: "no-reply@example.test"},
		outcome: MailTransportTemporary, err: errors.New("temporary SMTP failure"),
		afterSend: func() { now = delivery.Deadline },
	}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return now }, record)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Len(t, sender.messages, 1, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliverySuppressed, persistence.delivery.State, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Equal(t, model.MailDeliveryExpiredCode, persistence.delivery.PublicFailureCode, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	require.Empty(t, persistence.delivery.EncryptedPayload, "outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
}

func mailDeliveryHandlerFixture(t *testing.T, at time.Time, deadlineAfter time.Duration) (*secretseal.Sealer, *model.Job, *model.MailDelivery) {
	t.Helper()
	sealer := mailTestSealer(t)
	composer, err := mail.NewComposer(mail.ComposerDependencies{Renderer: mailRendererFake{FrozenMailContent{Subject: "test", Text: "text", HTML: "<p>html</p>"}}, Sender: &mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, Sealer: sealer})
	require.NoError(t, err)
	prepared, err := composer.PrepareOperatorTest(mail.NoticePreparation{Recipient: mailTestUser(mailTestPrincipal(at), at), At: at})
	require.NoError(t, err)
	prepared.Delivery.Deadline = at.Add(deadlineAfter)
	return sealer, prepared.Job, prepared.Delivery
}

func runMailDeliveryJob(persistence jobhandler.MailDeliveryLifecycleStore, sender MailDeliverySender,
	sealer *secretseal.Sealer, now func() time.Time, record *model.Job,
) job.Outcome {
	descriptor := jobhandler.NewMailDeliveryDescriptor(persistence, sender, sealer, nil, nil, jobMailDeliveryIsRelevant, now, false)
	return descriptor.Handler.Run(context.Background(), job.NewExecution(record, nil, nil, nil))
}

func TestMailDeliveryHandlerClassifiesRetryableAndPermanentFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		outcome MailTransportOutcome
		code    string
		kind    job.OutcomeKind
		state   model.MailDeliveryState
	}{
		{"temporary", MailTransportTemporary, "mail.transport.temporary", job.OutcomeRetryableFailure, model.MailDeliveryQueued},
		{"permanent", MailTransportPermanent, "mail.transport.permanent", job.OutcomePermanentFailure, model.MailDeliveryFailed},
		{"unknown", MailTransportUnknown, "mail.transport.unknown", job.OutcomeRetryableFailure, model.MailDeliveryQueued},
		{"acceptance uncertain", MailTransportAcceptanceUncertain, "mail.transport.acceptance_uncertain", job.OutcomeRetryableFailure, model.MailDeliveryQueued},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
			sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
			persistence := &mailStoreFake{delivery: delivery}
			sendErr := errors.New("private provider detail")
			sender := &mailSenderFake{enabled: true, outcome: test.outcome, err: sendErr}
			outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(time.Second) }, record)
			require.Equal(t, test.kind, outcome.Kind, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			require.Equal(t, test.code, outcome.PublicErrorCode, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			require.ErrorIs(t, outcome.Err, sendErr, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			require.Equal(t, test.state, persistence.delivery.State, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			require.Equal(t, test.code, persistence.delivery.PublicFailureCode, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			require.Equal(t, 1, persistence.delivery.AttemptCount, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			require.NotEmpty(t, persistence.delivery.EncryptedPayload, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			require.Len(t, sender.messages, 1, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
			// Both automatic retries and a repeated terminal Job preserve the
			// first send's identity; only retryable work may reach Sender again.
			sender.err = nil
			sender.from = MailAddress{Address: "changed@example.test"}
			second := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(2 * time.Second) }, record)
			if test.state == model.MailDeliveryFailed {
				require.Equal(t, job.OutcomePermanentFailure, second.Kind, "permanent failure was resent: %#v, sends", second)
				require.Len(t, sender.messages, 1, "permanent failure was resent: %#v, sends", second)
				return
			}
			require.Equal(t, job.OutcomeSucceeded, second.Kind, "retry=%#v delivery=%#v messages", second, persistence.delivery)
			require.Equal(t, model.MailDeliveryAccepted, persistence.delivery.State, "retry=%#v delivery=%#v messages", second, persistence.delivery)
			require.Empty(t, persistence.delivery.EncryptedPayload, "retry=%#v delivery=%#v messages", second, persistence.delivery)
			require.Len(t, sender.messages, 2, "retry=%#v delivery=%#v messages", second, persistence.delivery)
			require.Equal(t, sender.messages[1], sender.messages[0], "retry=%#v delivery=%#v messages", second, persistence.delivery)
		})
	}
}

func TestMailDeliveryHandlerFailsTemporaryFailureOnLastJobAttempt(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	record.AttemptCount = record.MaximumAttempts
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, outcome: MailTransportTemporary, err: errors.New("try later")}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(time.Second) }, record)
	require.Equal(t, job.OutcomePermanentFailure, outcome.Kind, "outcome=%#v", outcome)
	require.Equal(t, model.MailDeliveryFailed, persistence.delivery.State, "delivery left without a Job")
	require.Equal(t, "mail.transport.temporary", persistence.delivery.PublicFailureCode)
}

func TestMailDeliveryHandlerFailsExhaustedSendingDeliveryWithoutSending(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	sending, err := delivery.Start(at)
	require.NoError(t, err)
	sending.AttemptCount = model.MailMaximumAttempts
	persistence := &mailStoreFake{delivery: sending}
	sender := &mailSenderFake{enabled: true}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(time.Second) }, record)
	require.Equal(t, job.OutcomePermanentFailure, outcome.Kind, "outcome=%#v", outcome)
	require.Equal(t, "mail.transport.acceptance_uncertain", outcome.PublicErrorCode)
	require.Equal(t, model.MailDeliveryFailed, persistence.delivery.State, "exhausted delivery stayed Sending")
	require.Empty(t, sender.messages, "exhausted delivery was sent again")
}

func TestMailDeliveryHandlerFailsUnavailablePayloadWithoutSending(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	_, _, otherDelivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	// A valid envelope from another delivery must fail the owner binding.
	delivery.EncryptedPayload = otherDelivery.EncryptedPayload
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(time.Second) }, record)
	require.Equal(t, job.OutcomePermanentFailure, outcome.Kind, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Equal(t, "mail.payload.unavailable", outcome.PublicErrorCode, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Equal(t, model.MailDeliveryFailed, persistence.delivery.State, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Equal(t, 1, persistence.delivery.AttemptCount, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Empty(t, sender.messages, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
}

func TestMailDeliveryHandlerFailsInvalidMessageWithoutSending(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, record, delivery := mailDeliveryHandlerFixture(t, at, time.Hour)
	persistence := &mailInvalidMessageMetadataStoreFake{mailStoreFake: &mailStoreFake{delivery: delivery}}
	sender := &mailSenderFake{enabled: true}
	outcome := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(time.Second) }, record)
	require.Equal(t, job.OutcomePermanentFailure, outcome.Kind, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Equal(t, "mail.message.invalid", outcome.PublicErrorCode, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.ErrorIs(t, outcome.Err, mail.ErrInvalidMessage, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Equal(t, model.MailDeliveryFailed, persistence.delivery.State, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Equal(t, "mail.message.invalid", persistence.delivery.PublicFailureCode, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Equal(t, 1, persistence.delivery.AttemptCount, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.Empty(t, sender.messages, "outcome=%#v delivery=%#v sends", outcome, persistence.delivery)
	require.NotContains(t, outcome.Err.Error(), "hidden@example.test", "worker error disclosed invalid message metadata")
	failedRevision := persistence.delivery.Revision
	second := runMailDeliveryJob(persistence, sender, sealer, func() time.Time { return at.Add(2 * time.Second) }, record)
	require.Equal(t, job.OutcomePermanentFailure, second.Kind, "terminal replay changed invalid delivery: outcome=%#v delivery=%#v sends", second, persistence.delivery)
	require.Equal(t, "mail.message.invalid", second.PublicErrorCode, "terminal replay changed invalid delivery: outcome=%#v delivery=%#v sends", second, persistence.delivery)
	require.Equal(t, failedRevision, persistence.delivery.Revision, "terminal replay changed invalid delivery: outcome=%#v delivery=%#v sends", second, persistence.delivery)
	require.Equal(t, 1, persistence.delivery.AttemptCount, "terminal replay changed invalid delivery: outcome=%#v delivery=%#v sends", second, persistence.delivery)
	require.Empty(t, sender.messages, "terminal replay changed invalid delivery: outcome=%#v delivery=%#v sends", second, persistence.delivery)
}

type mailInvalidMessageMetadataStoreFake struct {
	*mailStoreFake
}

func (s *mailInvalidMessageMetadataStoreFake) StartDelivery(ctx context.Context, input *store.MailDeliveryStart) (*model.MailDelivery, error) {
	id := input.ID
	revision := input.ExpectedRevision
	at := input.At
	delivery, err := s.mailStoreFake.StartDelivery(ctx, &store.MailDeliveryStart{ID: id, ExpectedRevision: revision, At: at})
	if err != nil {
		return nil, err
	}
	// Keep the stored record valid while exercising the worker's defensive
	// validation of returned metadata without interpreting Mail's payload.
	delivery = delivery.Clone()
	delivery.MessageID += "\r\nBcc: hidden@example.test"
	return delivery, nil
}
