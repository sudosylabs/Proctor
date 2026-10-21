// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListAffiliationsQuery struct {
	UserID model.UserID
}

type CreateAffiliationCommand struct {
	UserID                    model.UserID
	Kind                      model.AffiliationKind
	StartAt                   int64
	EndAt                     int64
	IdempotencyKey            string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type EndAffiliationCommand struct {
	ID                        model.AffiliationID
	IdempotencyKey            string
	BatchScopeType            model.RoleScopeType
	BatchScopeID              string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type affiliationStore interface {
	Get(context.Context, model.AffiliationID) (*model.Affiliation, error)
	ListByUser(context.Context, model.UserID) ([]*model.Affiliation, error)
	Create(context.Context, *store.AffiliationCreation) (*model.Affiliation, error)
	End(context.Context, *store.AffiliationEnd) (*model.Affiliation, error)
}

type affiliationEnrollmentReader interface {
	ListByUser(context.Context, model.UserID) ([]*model.ClassMember, error)
}

type affiliationAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	AuthorizeUserRead(context.Context, Invocation, string) error
}

type affiliationEffects interface {
	Changed(context.Context, model.UserID) error
	Report(context.Context, string, error)
}

type affiliationService struct {
	store         affiliationStore
	enrollments   affiliationEnrollmentReader
	authorization affiliationAuthorizer
	audit         mutationAuditor
	effects       affiliationEffects
	now           func() time.Time
	newID         func() string
}

func newAffiliationService(persistence affiliationStore, enrollments affiliationEnrollmentReader, authorization affiliationAuthorizer,
	audit mutationAuditor, effects affiliationEffects, now func() time.Time, newID func() string,
) *affiliationService {
	return &affiliationService{store: persistence, enrollments: enrollments, authorization: authorization,
		audit: audit, effects: effects, now: now, newID: newID}
}

type affiliationRealtimeEffects struct{ realtime *realtimeService }

func (e affiliationRealtimeEffects) Changed(ctx context.Context, userID model.UserID) error {
	return e.realtime.InvalidateCurrentUserContext(ctx, userID)
}

func (e affiliationRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

func (s *affiliationService) ListAffiliations(ctx context.Context, invocation Invocation, query ListAffiliationsQuery) ([]*model.Affiliation, error) {
	userID := query.UserID
	if !userID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeUserRead(ctx, invocation, userID.String()); err != nil {
		return nil, err
	}
	affiliations, err := s.store.ListByUser(ctx, userID)
	if err != nil {
		return nil, affiliationStoreErrors.Translate(err)
	}
	if affiliations == nil {
		affiliations = []*model.Affiliation{}
	}
	return affiliations, nil
}

func (s *affiliationService) CreateAffiliation(ctx context.Context, invocation Invocation, command CreateAffiliationCommand) (*model.Affiliation, error) {
	resource, err := s.authorizeUser(ctx, invocation, command.UserID)
	if err != nil {
		return nil, err
	}
	userID, err := model.ParseUserID(resource.ID)
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "user_id").Wrap(err)
	}
	affiliationID, err := model.ParseAffiliationID(s.newID())
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "affiliation_id").Wrap(err)
	}
	candidate := &model.Affiliation{
		UserID:   userID,
		Kind:     command.Kind,
		StartsAt: model.TimeFromMillis(command.StartAt),
		EndsAt:   model.OptionalTimeFromMillis(command.EndAt),
	}
	at := s.now()
	candidate.PrepareCreate(affiliationID, at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("affiliation.invalid", err)
	}
	idempotency, err := applicationidempotency.Prepare(invocation, "affiliation.add.v1", command.IdempotencyKey, struct {
		UserID  string                `json:"user_id"`
		Kind    model.AffiliationKind `json:"kind"`
		StartAt int64                 `json:"start_at"`
		EndAt   int64                 `json:"end_at"`
	}{candidate.UserID.String(), candidate.Kind, command.StartAt, command.EndAt})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	changed := false
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionUserManage,
			Resource:   resource,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*model.Affiliation, error) {
			input := &store.AffiliationCreation{Affiliation: candidate, AuditEventID: reference.ID,
				AuditAt: reference.MutationAtMillis, Command: idempotency}
			value, storeErr := s.store.Create(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			changed = !input.Replayed && !input.NoOp
			return value, storeErr
		},
		affiliationStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	if changed {
		if effectErr := s.effects.Changed(ctx, result.UserID); effectErr != nil {
			s.effects.Report(ctx, "affiliation.changed", effectErr)
		}
	}
	return result, nil
}

func (s *affiliationService) EndAffiliation(ctx context.Context, invocation Invocation, command EndAffiliationCommand) (*model.Affiliation, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "affiliation_id")
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, affiliationStoreErrors.Translate(err)
	}
	resource, err := s.authorizeUser(ctx, invocation, current.UserID)
	if err != nil {
		return nil, err
	}
	if current.Kind == model.AffiliationStudent && !command.batchRetainedOutcome {
		enrollments, listErr := s.enrollments.ListByUser(ctx, current.UserID)
		if listErr != nil {
			return nil, affiliationStoreErrors.Translate(listErr)
		}
		for _, enrollment := range enrollments {
			// Open-ended enrollment (EndsAt absent) blocks ending a student affiliation.
			if enrollment != nil && !enrollment.EndsAt.Valid {
				return nil, apperror.New("affiliation.student_has_active_enrollment")
			}
		}
	}
	idempotency, err := applicationidempotency.Prepare(invocation, "affiliation.end.v1", command.IdempotencyKey, struct {
		ID        string              `json:"id"`
		ScopeType model.RoleScopeType `json:"scope_type,omitempty"`
		ScopeID   string              `json:"scope_id,omitempty"`
	}{id.String(), command.BatchScopeType, strings.TrimSpace(command.BatchScopeID)})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	changed := false
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionUserManage,
			Resource:   resource,
			Operation:  "end",
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Affiliation, error) {
			input := &store.AffiliationEnd{
				ID: id, ExpectedRevision: current.Revision, EndAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency,
			}
			value, storeErr := s.store.End(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			changed = !input.Replayed && !input.NoOp
			return value, storeErr
		},
		affiliationStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	if changed {
		if effectErr := s.effects.Changed(ctx, result.UserID); effectErr != nil {
			s.effects.Report(ctx, "affiliation.changed", effectErr)
		}
	}
	return result, nil
}

func (s *affiliationService) authorizeUser(ctx context.Context, invocation Invocation, userID model.UserID) (model.Resource, error) {
	if !userID.IsValid() {
		return model.Resource{}, apperror.New("request.invalid").WithField("field", "user_id")
	}
	resource := model.Resource{Type: model.ResourceUser, ID: userID.String()}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionUserManage, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

var affiliationStoreErrors = apperror.AdministrationStoreTable("affiliation", "affiliation.conflict", "affiliation.invalid", map[string]apperror.Code{
	"affiliation_student_has_active_enrollment": "affiliation.student_has_active_enrollment",
	"affiliation_end_time":                      "resource.not_found",
})
