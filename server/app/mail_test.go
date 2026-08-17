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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mailStoreFake struct {
	enqueue  *store.MailTestEnqueue
	delivery *model.MailDelivery
	gets     int
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

type mailAuthorizerFake struct{ actions []model.Action }

func (a *mailAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action) (model.Resource, error) {
	a.actions = append(a.actions, action)
	return model.Resource{Type: model.ResourceInstitution, ID: model.NewInstitutionID().String()}, nil
}

type mailAuditFake struct{ calls int }

func (a *mailAuditFake) PrepareTest(_ context.Context, invocation Invocation, resource model.Resource, id model.MailDeliveryID) (*model.AuditEvent, error) {
	a.calls++
	principal := invocation.Principal()
	return &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: id.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: resource.ID, Status: model.AuditStatusSuccess, NodeID: "test", ClientType: string(principal.ClientType), AuthMethod: principal.AuthenticationMethod}, nil
}

type mailRendererFake struct{ content FrozenMailContent }

func (r mailRendererFake) RenderSystemMailTest(string, string) (FrozenMailContent, error) {
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

func TestMailServicePreparesOnlyControlledSelfRecipientAndAtomicIntent(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	persistence := &mailStoreFake{}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@institution.test"}}
	authorizer, auditor := &mailAuthorizerFake{}, &mailAuditFake{}
	content := FrozenMailContent{Subject: "Fixed test", Text: "fixed text", HTML: "<p>fixed html</p>"}
	service, err := newMailService(persistence, mailUserStoreFake{mailTestUser(principal, at)}, authorizer, auditor, &authenticationAttemptAccounting{cache: &mailAttemptCacheFake{}}, mailRendererFake{content}, sender, mailTestSealer(t), 15*time.Minute, func() time.Time { return at })
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
	service, err := newMailService(persistence, mailUserStoreFake{user}, &mailAuthorizerFake{}, &mailAuditFake{}, &authenticationAttemptAccounting{cache: cache}, mailRendererFake{content}, sender, mailTestSealer(t), 15*time.Minute, func() time.Time { return at })
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

func TestMailServiceRejectsEnabledDeliveryWithoutSecretSealer(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	content := FrozenMailContent{Subject: "test", Text: "test"}
	_, err := newMailService(
		&mailStoreFake{}, mailUserStoreFake{mailTestUser(principal, at)}, &mailAuthorizerFake{}, &mailAuditFake{},
		&authenticationAttemptAccounting{cache: &mailAttemptCacheFake{}}, mailRendererFake{content},
		&mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}, nil,
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
	outcome := (mailDeliveryHandler{deliveries: persistence, sender: sender, sealer: sealer, now: func() time.Time { now = now.Add(time.Second); return now }}).Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 1 || sender.messages[0].MessageID != delivery.MessageID ||
		sender.messages[0].Headers["Auto-Submitted"][0] != payload.AutoSubmitted || sender.messages[0].Headers["X-Auto-Response-Suppress"][0] != payload.AutoResponseSuppress ||
		persistence.delivery.State != model.MailDeliveryAccepted || len(persistence.delivery.EncryptedPayload) != 0 {
		t.Fatalf("outcome=%#v messages=%#v delivery=%#v", outcome, sender.messages, persistence.delivery)
	}
	second := (mailDeliveryHandler{deliveries: persistence, sender: sender, sealer: sealer, now: time.Now}).Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	if second.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 1 {
		t.Fatalf("terminal replay resent: %#v", second)
	}
}

func TestMailDeliveryHandlerExpiresQueuedDeliveryWithoutSending(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sealer, job, delivery := mailDeliveryHandlerFixture(t, at, time.Second)
	persistence := &mailStoreFake{delivery: delivery}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}}
	outcome := (mailDeliveryHandler{deliveries: persistence, sender: sender, sealer: sealer, now: func() time.Time { return delivery.Deadline }}).Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(sender.messages) != 0 || persistence.delivery.State != model.MailDeliverySuppressed ||
		persistence.delivery.PublicFailureCode != model.MailDeliveryExpiredCode || len(persistence.delivery.EncryptedPayload) != 0 {
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
	outcome := (mailDeliveryHandler{deliveries: persistence, sender: sender, sealer: sealer, now: func() time.Time { return now }}).Run(context.Background(), jobengine.NewExecution(job, nil, nil, nil))
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

func TestMailDeliveryHandlerClassifiesRetryableAndPermanentFailures(t *testing.T) {
	if mailTransportFailureCode(MailTransportAcceptanceUncertain) != "mail.transport.acceptance_uncertain" {
		t.Fatal("uncertain outcome lost")
	}
	for outcome, want := range map[MailTransportOutcome]string{MailTransportTemporary: "mail.transport.temporary", MailTransportPermanent: "mail.transport.permanent", MailTransportUnknown: "mail.transport.unknown"} {
		if got := mailTransportFailureCode(outcome); got != want {
			t.Fatalf("code(%s)=%s", outcome, got)
		}
	}
	sender := &mailSenderFake{enabled: true, from: MailAddress{Address: "no-reply@example.test"}, outcome: MailTransportTemporary, err: errors.New("secret provider detail")}
	if outcome, err := sender.Send(context.Background(), OutboundMail{}); outcome != MailTransportTemporary || err == nil {
		t.Fatal("portable classification lost across application port")
	}
}
