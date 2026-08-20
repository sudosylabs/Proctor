// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mailDeliverySealingPurpose = "mail.delivery"
	mailTestRateLimitWindow    = time.Hour
	mailTestRateLimitMaximum   = 3
)

type FrozenMailContent struct{ Subject, Text, HTML string }

// DirectMailPreparation is the application-owned description of one mail
// intent addressed to an existing User. The preparer freezes the rendered
// content and returns the occurrence, delivery, and Job that the caller must
// persist in its named aggregate transaction.
type DirectMailPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	Kind         model.MailOccurrenceKind
	TemplateKey  model.MailTemplateKey
	ActionURL    string
	At           time.Time
	Deadline     time.Time
	JobType      model.JobType
}

type MailAddress struct {
	Name    string
	Address string
}

type OutboundMail struct {
	From         MailAddress
	EnvelopeFrom string
	To           MailAddress
	Subject      string
	Text         string
	HTML         string
	Headers      map[string][]string
	MessageID    string
	Date         time.Time
}

type MailTransportOutcome string

const (
	MailTransportUnknown             MailTransportOutcome = "unknown"
	MailTransportTemporary           MailTransportOutcome = "temporary"
	MailTransportPermanent           MailTransportOutcome = "permanent"
	MailTransportAcceptanceUncertain MailTransportOutcome = "acceptance_uncertain"
)

type MailTemplateRenderer interface {
	Render(key model.MailTemplateKey, recipientLocale, installationLocale, actionURL string) (FrozenMailContent, error)
}

// PersonalAccessTokenMailDetails is the bounded, non-secret context allowed in
// PAT security notices. It intentionally excludes the one-time credential,
// stored hash, and complete action list.
type PersonalAccessTokenMailDetails struct {
	Description        string
	ExpiresAt          time.Time
	ActionAt           time.Time
	ActionCount        int
	AcademicUnitScoped bool
}

// ExamManagerMailDetails is the bounded, actor-free context allowed in Exam
// relationship notices.
type ExamManagerMailDetails struct {
	Title        string
	Relationship string
	ActionAt     time.Time
}

// DirectMailTemplateRenderer makes every rendering capability used by the
// direct-mail preparer explicit at construction. A partially capable renderer
// must fail composition rather than a security-notice request at runtime.
type DirectMailTemplateRenderer interface {
	MailTemplateRenderer
	RenderPersonalAccessTokenSecurityNotice(
		model.MailTemplateKey,
		string,
		string,
		PersonalAccessTokenMailDetails,
	) (FrozenMailContent, error)
	RenderExamManagerNotice(model.MailTemplateKey, string, string, ExamManagerMailDetails) (FrozenMailContent, error)
}

type MailDeliverySender interface {
	Enabled() bool
	From() MailAddress
	Send(context.Context, OutboundMail) (MailTransportOutcome, error)
}

type frozenMailPayloadV1 struct {
	Version              int    `json:"version"`
	RecipientName        string `json:"recipient_name"`
	RecipientAddress     string `json:"recipient_address"`
	FromName             string `json:"from_name"`
	FromAddress          string `json:"from_address"`
	Subject              string `json:"subject"`
	Text                 string `json:"text"`
	HTML                 string `json:"html"`
	AutoSubmitted        string `json:"auto_submitted"`
	AutoResponseSuppress string `json:"auto_response_suppress"`
}

type MailDeliveryView struct {
	ID                                          model.MailDeliveryID
	OccurrenceID                                model.MailOccurrenceID
	TargetUserID                                model.UserID
	TemplateKey                                 model.MailTemplateKey
	TemplateDigest                              string
	MaskedRecipient                             string
	State                                       model.MailDeliveryState
	CreatedAt, UpdatedAt, MessageDate, Deadline time.Time
	MessageID                                   string
	AttemptCount                                int
	AcceptedAt, FailedAt                        model.OptionalTime
	PublicFailureCode                           string
}

type MailDeliveryPage struct{ Items []MailDeliveryView }

type ListMailDeliveriesQuery struct {
	States          []model.MailDeliveryState
	TemplateKeys    []model.MailTemplateKey
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	BeforeCreatedAt time.Time
	BeforeID        model.MailDeliveryID
	Limit           int
}

type mailStore interface {
	EnqueueTest(context.Context, *store.MailTestEnqueue) (*model.MailDelivery, error)
	GetDelivery(context.Context, model.MailDeliveryID) (*model.MailDelivery, error)
	ListDeliveries(context.Context, store.MailDeliveryListOptions) ([]*model.MailDelivery, error)
	CancelDelivery(context.Context, *store.MailDeliveryMutation) (*model.MailDelivery, error)
	RetryDelivery(context.Context, *store.MailDeliveryMutation) (*model.MailDelivery, error)
}

type mailUserStore interface {
	Get(context.Context, string) (*model.User, error)
}

type mailAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action) (model.Resource, error)
}

type mailInstitutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
}

type mailAuthorizationAdapter struct {
	authorization *accessControlService
	institutions  mailInstitutionStore
}

func (a mailAuthorizationAdapter) Authorize(ctx context.Context, invocation Invocation, action model.Action) (model.Resource, error) {
	if a.authorization == nil || a.institutions == nil {
		return model.Resource{}, NewError("mail.unavailable")
	}
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return model.Resource{}, NewError("mail.unavailable").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err = a.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata()); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

type mailAuditPreparer interface {
	PrepareTest(context.Context, Invocation, model.Resource, model.MailDeliveryID) (*model.AuditEvent, error)
	PrepareControl(context.Context, Invocation, model.Resource, *model.MailDelivery, string) (string, error)
	Fail(context.Context, string, string) error
}

type mailService struct {
	mail                    mailStore
	rekey                   mailRekeyStarter
	keyState                mailKeyInspector
	rekeyJobs               mailRekeyJobReader
	rekeyAudit              mutationAuditor
	users                   mailUserStore
	authorization           mailAuthorizer
	audit                   mailAuditPreparer
	attempts                *authenticationAttemptAccounting
	renderer                MailTemplateRenderer
	sender                  MailDeliverySender
	metrics                 MailDeliveryRecorder
	sealer                  *secretseal.Sealer
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
	wake                    func()
}

type directMailPreparer struct {
	renderer DirectMailTemplateRenderer
	sender   MailDeliverySender
	sealer   *secretseal.Sealer
}

func newDirectMailPreparer(renderer DirectMailTemplateRenderer, sender MailDeliverySender, sealer *secretseal.Sealer) (*directMailPreparer, error) {
	if renderer == nil || sender == nil || (sender.Enabled() && sealer == nil) {
		return nil, errors.New("direct mail preparer dependencies are invalid")
	}
	return &directMailPreparer{renderer: renderer, sender: sender, sealer: sealer}, nil
}

func (p *directMailPreparer) Enabled() bool {
	return p != nil && p.sender != nil && p.sender.Enabled() && p.sealer != nil
}

func (p *directMailPreparer) PrepareDirect(request DirectMailPreparation) (*preparedDirectMail, error) {
	user, occurrenceID, kind, key := request.Recipient, request.OccurrenceID, request.Kind, request.TemplateKey
	actionURL, at, deadline, jobType := request.ActionURL, request.At, request.Deadline, request.JobType
	if p == nil || p.sender == nil || user == nil || user.Validate() != nil || !user.IsActive() || !occurrenceID.IsValid() ||
		!key.IsValid() || at.IsZero() || !deadline.After(at) ||
		(jobType != model.JobTypeMailDeliver && jobType != model.JobTypeMailDeliverCredential) {
		return nil, errors.New("direct mail input is invalid")
	}
	return p.prepareRecipient(user.DisplayName, user.Email, user.Locale, user.ID, user.ID, "", occurrenceID,
		kind, key, actionURL, at, deadline, jobType, nil, nil)
}

func (p *directMailPreparer) PrepareInvitation(invitation *model.Invitation, actionURL string) (*preparedDirectMail, error) {
	if !p.Enabled() || invitation == nil || invitation.Validate() != nil || invitation.State != model.InvitationPending {
		return nil, errors.New("invitation mail input is invalid")
	}
	var key model.MailTemplateKey
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		key = model.MailTemplateAccessStudentClassInvitation
	case model.InvitationPurposeTeacherAcademicUnit:
		key = model.MailTemplateAccessTeacherAcademicUnitInvitation
	case model.InvitationPurposeAcademicUnitRole:
		key = model.MailTemplateAccessAcademicUnitRoleInvitation
	case model.InvitationPurposeInstitutionRole:
		key = model.MailTemplateAccessInstitutionRoleInvitation
	default:
		return nil, errors.New("invitation mail purpose is not implemented")
	}
	return p.prepareRecipient(invitation.Suggestions.DisplayName, invitation.TargetEmail, invitation.Suggestions.Locale,
		invitation.InviterUserID, "", invitation.ID, model.MailOccurrenceID(invitation.ID.String()),
		model.MailOccurrenceInvitation, key, actionURL,
		invitation.CreatedAt, invitation.ExpiresAt, model.JobTypeMailDeliverCredential, nil, nil)
}

func (p *directMailPreparer) prepareRecipient(recipientName, recipientAddress, locale string, actorUserID, targetUserID model.UserID,
	targetInvitationID model.InvitationID, occurrenceID model.MailOccurrenceID, kind model.MailOccurrenceKind,
	key model.MailTemplateKey, actionURL string, at, deadline time.Time, jobType model.JobType,
	personalAccessToken *PersonalAccessTokenMailDetails,
	examManager *ExamManagerMailDetails,
) (*preparedDirectMail, error) {
	if p == nil || p.sender == nil || p.renderer == nil || !model.IsValidEmail(recipientAddress) || !actorUserID.IsValid() || !occurrenceID.IsValid() ||
		(targetUserID.IsValid() == targetInvitationID.IsValid()) || !key.IsValid() || at.IsZero() || !deadline.After(at) ||
		(jobType != model.JobTypeMailDeliver && jobType != model.JobTypeMailDeliverCredential) {
		return nil, errors.New("direct mail recipient is invalid")
	}
	if locale == "" {
		locale = model.DefaultLocale
	}
	at = model.TimeUTC(at)
	deadline = model.TimeUTC(deadline)
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return nil, err
	}
	job, err := model.NewJob(jobID, jobType, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	if err != nil {
		return nil, err
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: kind, TemplateKey: key, ActorUserID: actorUserID, CreatedAt: at}
	if !p.sender.Enabled() {
		job, err = job.RequestCancellation(at)
		if err != nil {
			return nil, err
		}
		delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: targetUserID, TargetInvitationID: targetInvitationID,
			TemplateKey: key, TemplateDigest: digestRenderedMail("", "", ""), MaskedRecipient: maskMailAddress(recipientAddress),
			State: model.MailDeliverySuppressed, CreatedAt: at, UpdatedAt: at, MessageDate: at, Deadline: deadline,
			MessageID: stableMailMessageID(deliveryID, ""), PublicFailureCode: model.MailDeliveryDisabledCode, Revision: 1}
		if err = occurrence.Validate(); err != nil {
			return nil, err
		}
		if err = delivery.Validate(); err != nil {
			return nil, err
		}
		return &preparedDirectMail{Occurrence: occurrence, Delivery: delivery, Job: job}, nil
	}
	var rendered FrozenMailContent
	if personalAccessToken != nil && examManager != nil {
		return nil, errors.New("direct mail details are ambiguous")
	}
	if personalAccessToken != nil {
		rendered, err = p.renderer.RenderPersonalAccessTokenSecurityNotice(key, locale, model.DefaultLocale, *personalAccessToken)
	} else if examManager != nil {
		rendered, err = p.renderer.RenderExamManagerNotice(key, locale, model.DefaultLocale, *examManager)
	} else {
		rendered, err = p.renderer.Render(key, locale, model.DefaultLocale, actionURL)
	}
	if err != nil {
		return nil, err
	}
	from := p.sender.From()
	if err = validateMailAddress(from); err != nil {
		return nil, err
	}
	payload := frozenMailPayloadV1{Version: 1, RecipientName: recipientName, RecipientAddress: recipientAddress,
		FromName: from.Name, FromAddress: from.Address, Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML,
		AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return nil, errors.New("rendered mail payload is invalid")
	}
	envelope, err := p.sealer.Seal(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: deliveryID.String()}, plaintext)
	if err != nil {
		return nil, err
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: targetUserID, TargetInvitationID: targetInvitationID,
		TemplateKey: key, TemplateDigest: digestRenderedMail(rendered.Subject, rendered.Text, rendered.HTML),
		MaskedRecipient: maskMailAddress(recipientAddress), State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at,
		MessageDate: at, Deadline: deadline, MessageID: stableMailMessageID(deliveryID, from.Address),
		EncryptedPayload: encrypted, Revision: 1}
	if err = occurrence.Validate(); err != nil {
		return nil, err
	}
	if err = delivery.Validate(); err != nil {
		return nil, err
	}
	return &preparedDirectMail{Occurrence: occurrence, Delivery: delivery, Job: job}, nil
}

func newMailService(mailStore mailStore, users mailUserStore, authorization mailAuthorizer, audit mailAuditPreparer, attempts *authenticationAttemptAccounting, renderer MailTemplateRenderer, sender MailDeliverySender, metrics MailDeliveryRecorder, sealer *secretseal.Sealer, recentTTL time.Duration, now func() time.Time) (*mailService, error) {
	if mailStore == nil || users == nil || authorization == nil || audit == nil || attempts == nil || renderer == nil || sender == nil || metrics == nil || now == nil || recentTTL <= 0 {
		return nil, errors.New("mail service dependencies are invalid")
	}
	if sender.Enabled() && sealer == nil {
		return nil, errors.New("enabled mail requires secret sealing")
	}
	rekey, _ := mailStore.(mailRekeyStarter)
	keyState, _ := mailStore.(mailKeyInspector)
	rekeyAudit, _ := audit.(mutationAuditor)
	return &mailService{mail: mailStore, rekey: rekey, keyState: keyState, rekeyAudit: rekeyAudit, users: users,
		authorization: authorization, audit: audit, attempts: attempts, renderer: renderer,
		sender: sender, metrics: metrics, sealer: sealer, recentAuthenticationTTL: recentTTL, now: now}, nil
}

func (a *App) SendTestMail(ctx context.Context, invocation Invocation) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.SendTest(ctx, invocation)
}

func (a *App) GetMailDelivery(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.Get(ctx, invocation, id)
}

func (a *App) ListMailDeliveries(ctx context.Context, invocation Invocation, query ListMailDeliveriesQuery) (MailDeliveryPage, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryPage{}, NewError("mail.unavailable")
	}
	return a.mail.List(ctx, invocation, query)
}

func (a *App) GetMailMetrics(ctx context.Context, invocation Invocation) (MailMetricsSnapshot, error) {
	if a == nil || a.mail == nil {
		return MailMetricsSnapshot{}, NewError("mail.unavailable")
	}
	return a.mail.Metrics(ctx, invocation)
}

func (s *mailService) Metrics(ctx context.Context, invocation Invocation) (MailMetricsSnapshot, error) {
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailView); err != nil {
		return MailMetricsSnapshot{}, err
	}
	return s.metrics.Snapshot(), nil
}

func (a *App) CancelMailDelivery(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.Cancel(ctx, invocation, id)
}

func (a *App) RetryMailDelivery(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if a == nil || a.mail == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	return a.mail.Retry(ctx, invocation, id)
}

func (s *mailService) SendTest(ctx context.Context, invocation Invocation) (MailDeliveryView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if !principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL) {
		return MailDeliveryView{}, NewError("authentication.reauthentication_required")
	}
	institutionResource, err := s.authorization.Authorize(ctx, invocation, model.ActionMailManage)
	if err != nil {
		return MailDeliveryView{}, err
	}
	if err = s.checkTestRateLimit(ctx, principal.UserID); err != nil {
		return MailDeliveryView{}, err
	}
	if !s.sender.Enabled() || s.sealer == nil {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	user, err := s.users.Get(ctx, principal.UserID.String())
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	if user == nil || user.Validate() != nil || user.ID != principal.UserID {
		return MailDeliveryView{}, NewError("mail.unavailable")
	}
	if !user.EmailVerified {
		return MailDeliveryView{}, NewError("mail.recipient_unverified")
	}
	if !user.IsActive() {
		return MailDeliveryView{}, NewError("mail.recipient_ineligible")
	}
	rendered, err := s.renderer.Render(model.MailTemplateSystemTest, user.Locale, model.DefaultLocale, "")
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	templateDigest := digestRenderedMail(rendered.Subject, rendered.Text, rendered.HTML)
	from := s.sender.From()
	if err = validateMailAddress(from); err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	now := model.TimeUTC(s.now())
	occurrenceID, deliveryID, jobID := model.NewMailOccurrenceID(), model.NewMailDeliveryID(), model.NewJobID()
	payload := frozenMailPayloadV1{Version: 1, RecipientName: user.DisplayName, RecipientAddress: user.Email, FromName: from.Name, FromAddress: from.Address, Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML, AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	envelope, err := s.sealer.Seal(secretseal.Binding{Purpose: mailDeliverySealingPurpose, Owner: deliveryID.String()}, plaintext)
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(), now, now, model.MailMaximumAttempts)
	if err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: user.ID, TemplateKey: model.MailTemplateSystemTest, TemplateDigest: templateDigest, MaskedRecipient: maskMailAddress(user.Email), State: model.MailDeliveryQueued, CreatedAt: now, UpdatedAt: now, MessageDate: now, Deadline: now.Add(24 * time.Hour), MessageID: stableMailMessageID(deliveryID, from.Address), EncryptedPayload: encrypted, Revision: 1}
	if err = delivery.Validate(); err != nil {
		return MailDeliveryView{}, NewError("mail.unavailable").Wrap(err)
	}
	audit, err := s.audit.PrepareTest(ctx, invocation, institutionResource, deliveryID)
	if err != nil {
		return MailDeliveryView{}, err
	}
	created, err := s.mail.EnqueueTest(ctx, &store.MailTestEnqueue{Occurrence: &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceOperatorTest, TemplateKey: model.MailTemplateSystemTest, ActorUserID: user.ID, CreatedAt: now}, Delivery: delivery, Job: job, AuditEvent: audit})
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	if s.wake != nil {
		s.wake()
	}
	return mailDeliveryView(created), nil
}

func validateMailAddress(address MailAddress) error {
	if address.Address == "" || strings.ContainsAny(address.Name+address.Address, "\x00\r\n") {
		return errors.New("mail address is invalid")
	}
	parsed, err := mail.ParseAddress(address.Address)
	if err != nil || parsed.Address != address.Address {
		return errors.New("mail address is invalid")
	}
	return nil
}

func (s *mailService) Get(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailView); err != nil {
		return MailDeliveryView{}, err
	}
	if !id.IsValid() {
		return MailDeliveryView{}, NewError("request.invalid")
	}
	delivery, err := s.mail.GetDelivery(ctx, id)
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	return mailDeliveryView(delivery), nil
}

func (s *mailService) List(ctx context.Context, invocation Invocation, query ListMailDeliveriesQuery) (MailDeliveryPage, error) {
	if invocation.Principal().Validate() != nil {
		return MailDeliveryPage{}, invalidTokenAppError()
	}
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailView); err != nil {
		return MailDeliveryPage{}, err
	}
	deliveries, err := s.mail.ListDeliveries(ctx, store.MailDeliveryListOptions{
		States: query.States, TemplateKeys: query.TemplateKeys,
		CreatedAfter: query.CreatedAfter, CreatedBefore: query.CreatedBefore,
		BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID, Limit: query.Limit,
	})
	if err != nil {
		var invalid *store.ErrInvalidInput
		if errors.As(err, &invalid) {
			return MailDeliveryPage{}, NewError("mail.query.invalid").Wrap(err)
		}
		return MailDeliveryPage{}, mailAppError(err)
	}
	page := MailDeliveryPage{Items: make([]MailDeliveryView, 0, len(deliveries))}
	for _, delivery := range deliveries {
		page.Items = append(page.Items, mailDeliveryView(delivery))
	}
	return page, nil
}

func (s *mailService) Cancel(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	return s.mutate(ctx, invocation, "cancel", id, false, s.mail.CancelDelivery)
}

func (s *mailService) Retry(ctx context.Context, invocation Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	return s.mutate(ctx, invocation, "retry", id, true, s.mail.RetryDelivery)
}

func (s *mailService) mutate(ctx context.Context, invocation Invocation, operation string, id model.MailDeliveryID, retry bool, apply func(context.Context, *store.MailDeliveryMutation) (*model.MailDelivery, error)) (MailDeliveryView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if !principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL) {
		return MailDeliveryView{}, NewError("authentication.reauthentication_required")
	}
	institutionResource, err := s.authorization.Authorize(ctx, invocation, model.ActionMailManage)
	if err != nil {
		return MailDeliveryView{}, err
	}
	if !id.IsValid() {
		return MailDeliveryView{}, NewError("request.invalid")
	}
	delivery, err := s.mail.GetDelivery(ctx, id)
	if err != nil {
		return MailDeliveryView{}, mailAppError(err)
	}
	now := model.TimeUTC(s.now())
	if retry {
		relevance, relevanceErr := evaluateMailDeliveryRelevance(ctx, delivery)
		if relevanceErr != nil || relevance != mailDeliveryRelevant {
			return MailDeliveryView{}, NewError("mail.conflict").Wrap(relevanceErr)
		}
		if _, transitionErr := delivery.OperatorRetry(now); transitionErr != nil {
			return MailDeliveryView{}, NewError("mail.conflict").Wrap(transitionErr)
		}
	}
	auditID, err := s.audit.PrepareControl(ctx, invocation, institutionResource, delivery, operation)
	if err != nil {
		return MailDeliveryView{}, err
	}
	updated, err := apply(ctx, &store.MailDeliveryMutation{ID: delivery.ID, ExpectedRevision: delivery.Revision, AuditEventID: auditID, AuditAt: now.UnixMilli()})
	if err != nil {
		_ = s.audit.Fail(ctx, auditID, "mail.conflict")
		return MailDeliveryView{}, mailAppError(err)
	}
	if retry && s.wake != nil {
		s.wake()
	}
	return mailDeliveryView(updated), nil
}

func (s *mailService) checkTestRateLimit(ctx context.Context, userID model.UserID) error {
	_, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{purpose: authenticationAttemptPurposeMailTest, window: mailTestRateLimitWindow, limits: []authenticationAttemptLimit{{dimension: authenticationAttemptDimensionIdentity, maximum: mailTestRateLimitMaximum, identity: userID.String()}}})
	if err != nil {
		return NewError("mail.unavailable").Wrap(err)
	}
	if limited {
		return NewError("mail.test.rate_limited")
	}
	return nil
}

func stableMailMessageID(id model.MailDeliveryID, from string) string {
	domain := "localhost"
	if parsed, err := mail.ParseAddress(from); err == nil {
		if at := strings.LastIndexByte(parsed.Address, '@'); at >= 0 {
			domain = parsed.Address[at+1:]
		}
	}
	return "<mail." + id.String() + "@" + strings.ToLower(domain) + ">"
}

func maskMailAddress(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 1 {
		return "***"
	}
	local := []rune(address[:at])
	prefix := "***"
	if len(local) > 1 {
		prefix = string(local[0]) + strings.Repeat("*", min(3, len(local)-1))
	}
	return prefix + address[at:]
}

func mailDeliveryView(delivery *model.MailDelivery) MailDeliveryView {
	if delivery == nil {
		return MailDeliveryView{}
	}
	return MailDeliveryView{ID: delivery.ID, OccurrenceID: delivery.OccurrenceID, TargetUserID: delivery.TargetUserID, TemplateKey: delivery.TemplateKey, TemplateDigest: delivery.TemplateDigest, MaskedRecipient: delivery.MaskedRecipient, State: delivery.State, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt, MessageDate: delivery.MessageDate, Deadline: delivery.Deadline, MessageID: delivery.MessageID, AttemptCount: delivery.AttemptCount, AcceptedAt: delivery.AcceptedAt, FailedAt: delivery.FailedAt, PublicFailureCode: delivery.PublicFailureCode}
}

func digestRenderedMail(subject, text, html string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(subject))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(text))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(html))
	return hex.EncodeToString(hash.Sum(nil))
}

func mailAppError(err error) error {
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").Wrap(err)
	case store.IsConflict(err):
		return NewError("mail.conflict").Wrap(err)
	default:
		return NewError("mail.unavailable").Wrap(err)
	}
}
