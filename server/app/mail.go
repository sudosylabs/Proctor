// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mailDeliverySealingPurpose = mail.DeliverySealingPurpose
	mailTestRateLimitWindow    = time.Hour
	mailTestRateLimitMaximum   = 3
)

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
	Get(context.Context, model.UserID) (*model.User, error)
}

type mailInstitutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
}

type mailAuthorizationAdapter struct {
	authorization *accessControlService
	institutions  mailInstitutionStore
}

func (a mailAuthorizationAdapter) AuthorizeInstitution(ctx context.Context, invocation invocation.Invocation, action model.Action) (model.Resource, error) {
	if a.authorization == nil || a.institutions == nil {
		return model.Resource{}, apperror.New("mail.unavailable")
	}
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return model.Resource{}, apperror.New("mail.unavailable").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err = a.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

type mailAuditPreparer interface {
	PrepareTest(context.Context, invocation.Invocation, model.Resource, model.MailDeliveryID) (*model.AuditEvent, error)
	PrepareControl(context.Context, invocation.Invocation, model.Resource, *model.MailDelivery, string) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
}

type mailService struct {
	mail                    mailStore
	rekey                   mailRekeyStarter
	keyState                mailKeyInspector
	rekeyJobs               mailRekeyJobReader
	rekeyAudit              mutationAuditor
	users                   mailUserStore
	authorization           institutionAuthorizer
	audit                   mailAuditPreparer
	attempts                *authenticationAttemptAccounting
	composer                *mail.Composer
	sender                  mail.Sender
	metrics                 MailMetricsReader
	sealer                  *secretseal.Sealer
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
	wake                    func()
}

func newDirectMailPreparer(renderer mail.Renderer, sender mail.Sender, sealer *secretseal.Sealer) (*mail.Composer, error) {
	return mail.NewComposer(renderer, sender, sealer)
}

func newMailService(mailStore mailStore, users mailUserStore, authorization institutionAuthorizer, audit mailAuditPreparer, attempts *authenticationAttemptAccounting, renderer mail.Renderer, sender mail.Sender, metrics MailMetricsReader, sealer *secretseal.Sealer, recentTTL time.Duration, now func() time.Time) (*mailService, error) {
	if mailStore == nil || users == nil || authorization == nil || audit == nil || attempts == nil || renderer == nil || sender == nil || metrics == nil || now == nil || recentTTL <= 0 {
		return nil, errors.New("mail service dependencies are invalid")
	}
	if sender.Enabled() && sealer == nil {
		return nil, errors.New("enabled mail requires secret sealing")
	}
	composer, err := newDirectMailPreparer(renderer, sender, sealer)
	if err != nil {
		return nil, err
	}
	rekey, _ := mailStore.(mailRekeyStarter)
	keyState, _ := mailStore.(mailKeyInspector)
	return &mailService{mail: mailStore, rekey: rekey, keyState: keyState, users: users,
		authorization: authorization, audit: audit, attempts: attempts, composer: composer,
		sender: sender, metrics: metrics, sealer: sealer, recentAuthenticationTTL: recentTTL, now: now}, nil
}

func (s *mailService) GetMailMetrics(ctx context.Context, invocation invocation.Invocation) (MailMetricsSnapshot, error) {
	if s == nil {
		return MailMetricsSnapshot{}, apperror.New("mail.unavailable")
	}
	if _, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionMailView); err != nil {
		return MailMetricsSnapshot{}, err
	}
	return s.metrics.Snapshot(), nil
}

func (s *mailService) SendTestMail(ctx context.Context, invocation invocation.Invocation) (MailDeliveryView, error) {
	if s == nil {
		return MailDeliveryView{}, apperror.New("mail.unavailable")
	}
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if !principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL) {
		return MailDeliveryView{}, apperror.New("authentication.reauthentication_required")
	}
	institutionResource, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionMailManage)
	if err != nil {
		return MailDeliveryView{}, err
	}
	if err = s.checkTestRateLimit(ctx, principal.UserID); err != nil {
		return MailDeliveryView{}, err
	}
	if !s.sender.Enabled() || s.sealer == nil {
		return MailDeliveryView{}, apperror.New("mail.unavailable")
	}
	user, err := s.users.Get(ctx, principal.UserID)
	if err != nil {
		return MailDeliveryView{}, mailStoreErrors.Translate(err)
	}
	if user == nil || user.Validate() != nil || user.ID != principal.UserID {
		return MailDeliveryView{}, apperror.New("mail.unavailable")
	}
	if !user.EmailVerified {
		return MailDeliveryView{}, apperror.New("mail.recipient_unverified")
	}
	if !user.IsActive() {
		return MailDeliveryView{}, apperror.New("mail.recipient_ineligible")
	}
	now := model.TimeUTC(s.now())
	prepared, err := s.composer.PrepareOperatorTest(mail.NoticePreparation{Recipient: user, At: now})
	if err != nil {
		return MailDeliveryView{}, apperror.New("mail.unavailable").Wrap(err)
	}
	audit, err := s.audit.PrepareTest(ctx, invocation, institutionResource, prepared.Delivery.ID)
	if err != nil {
		return MailDeliveryView{}, err
	}
	created, err := s.mail.EnqueueTest(ctx, &store.MailTestEnqueue{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job, AuditEvent: audit})
	if err != nil {
		return MailDeliveryView{}, mailStoreErrors.Translate(err)
	}
	if s.wake != nil {
		s.wake()
	}
	return mailDeliveryView(created), nil
}

func (s *mailService) GetMailDelivery(ctx context.Context, invocation invocation.Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if s == nil {
		return MailDeliveryView{}, apperror.New("mail.unavailable")
	}
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if _, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionMailView); err != nil {
		return MailDeliveryView{}, err
	}
	if !id.IsValid() {
		return MailDeliveryView{}, apperror.New("request.invalid")
	}
	delivery, err := s.mail.GetDelivery(ctx, id)
	if err != nil {
		return MailDeliveryView{}, mailStoreErrors.Translate(err)
	}
	return mailDeliveryView(delivery), nil
}

func (s *mailService) ListMailDeliveries(ctx context.Context, invocation invocation.Invocation, query ListMailDeliveriesQuery) (MailDeliveryPage, error) {
	if s == nil {
		return MailDeliveryPage{}, apperror.New("mail.unavailable")
	}
	if invocation.Principal().Validate() != nil {
		return MailDeliveryPage{}, invalidTokenAppError()
	}
	if _, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionMailView); err != nil {
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
			return MailDeliveryPage{}, apperror.New("mail.query.invalid").Wrap(err)
		}
		return MailDeliveryPage{}, mailStoreErrors.Translate(err)
	}
	page := MailDeliveryPage{Items: make([]MailDeliveryView, 0, len(deliveries))}
	for _, delivery := range deliveries {
		page.Items = append(page.Items, mailDeliveryView(delivery))
	}
	return page, nil
}

func (s *mailService) CancelMailDelivery(ctx context.Context, invocation invocation.Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if s == nil {
		return MailDeliveryView{}, apperror.New("mail.unavailable")
	}
	return s.mutate(ctx, invocation, "cancel", id, false, s.mail.CancelDelivery)
}

func (s *mailService) RetryMailDelivery(ctx context.Context, invocation invocation.Invocation, id model.MailDeliveryID) (MailDeliveryView, error) {
	if s == nil {
		return MailDeliveryView{}, apperror.New("mail.unavailable")
	}
	return s.mutate(ctx, invocation, "retry", id, true, s.mail.RetryDelivery)
}

func (s *mailService) mutate(ctx context.Context, invocation invocation.Invocation, operation string, id model.MailDeliveryID, retry bool, apply func(context.Context, *store.MailDeliveryMutation) (*model.MailDelivery, error)) (MailDeliveryView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return MailDeliveryView{}, invalidTokenAppError()
	}
	if !principal.IsRecentlyAuthenticated(s.now(), s.recentAuthenticationTTL) {
		return MailDeliveryView{}, apperror.New("authentication.reauthentication_required")
	}
	institutionResource, err := s.authorization.AuthorizeInstitution(ctx, invocation, model.ActionMailManage)
	if err != nil {
		return MailDeliveryView{}, err
	}
	if !id.IsValid() {
		return MailDeliveryView{}, apperror.New("request.invalid")
	}
	delivery, err := s.mail.GetDelivery(ctx, id)
	if err != nil {
		return MailDeliveryView{}, mailStoreErrors.Translate(err)
	}
	now := model.TimeUTC(s.now())
	if retry {
		relevance, relevanceErr := evaluateMailDeliveryRelevance(ctx, delivery)
		if relevanceErr != nil || relevance != mailDeliveryRelevant {
			return MailDeliveryView{}, apperror.New("mail.conflict").Wrap(relevanceErr)
		}
		if _, transitionErr := delivery.OperatorRetry(now); transitionErr != nil {
			return MailDeliveryView{}, apperror.New("mail.conflict").Wrap(transitionErr)
		}
	}
	auditID, err := s.audit.PrepareControl(ctx, invocation, institutionResource, delivery, operation)
	if err != nil {
		return MailDeliveryView{}, err
	}
	updated, err := apply(ctx, &store.MailDeliveryMutation{ID: delivery.ID, ExpectedRevision: delivery.Revision, AuditEventID: auditID, AuditAt: now.UnixMilli()})
	if err != nil {
		_ = s.audit.Fail(ctx, auditID, "mail.conflict")
		return MailDeliveryView{}, mailStoreErrors.Translate(err)
	}
	if retry && s.wake != nil {
		s.wake()
	}
	return mailDeliveryView(updated), nil
}

func (s *mailService) checkTestRateLimit(ctx context.Context, userID model.UserID) error {
	_, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{purpose: authenticationAttemptPurposeMailTest, window: mailTestRateLimitWindow, limits: []authenticationAttemptLimit{{dimension: authenticationAttemptDimensionIdentity, maximum: mailTestRateLimitMaximum, identity: userID.String()}}})
	if err != nil {
		return apperror.New("mail.unavailable").Wrap(err)
	}
	if limited {
		return apperror.New("mail.test.rate_limited")
	}
	return nil
}

func mailDeliveryView(delivery *model.MailDelivery) MailDeliveryView {
	if delivery == nil {
		return MailDeliveryView{}
	}
	return MailDeliveryView{ID: delivery.ID, OccurrenceID: delivery.OccurrenceID, TargetUserID: delivery.TargetUserID, TemplateKey: delivery.TemplateKey, TemplateDigest: delivery.TemplateDigest, MaskedRecipient: delivery.MaskedRecipient, State: delivery.State, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt, MessageDate: delivery.MessageDate, Deadline: delivery.Deadline, MessageID: delivery.MessageID, AttemptCount: delivery.AttemptCount, AcceptedAt: delivery.AcceptedAt, FailedAt: delivery.FailedAt, PublicFailureCode: delivery.PublicFailureCode}
}

var mailStoreErrors = apperror.StoreTable{
	NotFound:    "resource.not_found",
	Conflict:    "mail.conflict",
	Unavailable: "mail.unavailable",
}
