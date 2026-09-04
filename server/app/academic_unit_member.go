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
	"strings"
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListAcademicUnitMembersQuery struct {
	AcademicUnitID string
	ActiveAt       int64
}

type CreateAcademicUnitMemberCommand struct {
	AcademicUnitID            string
	UserID                    string
	StartAt                   int64
	IdempotencyKey            string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type EndAcademicUnitMemberCommand struct {
	ID                        string
	IdempotencyKey            string
	BatchScopeID              string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type academicUnitMemberStore interface {
	Get(context.Context, string) (*model.AcademicUnitMember, error)
	ListByAcademicUnit(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
	Create(context.Context, *store.AcademicUnitMemberCreation) (*model.AcademicUnitMember, error)
	EndWithAudit(context.Context, *store.AcademicUnitMemberEnd) (*model.AcademicUnitMember, error)
}

type academicUnitMemberAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	AuthorizePreflight(context.Context, Invocation, model.Action, model.ResourceType) error
}

type academicUnitMemberUserStore interface {
	Get(context.Context, string) (*model.User, error)
}

type academicUnitMemberService struct {
	store         academicUnitMemberStore
	users         academicUnitMemberUserStore
	authorization academicUnitMemberAuthorizer
	audit         mutationAuditor
	mail          relationshipTransitionMailPreparer
	effects       authorizationInvalidationEffects
	now           func() time.Time
	newID         func() string
}

func newAcademicUnitMemberService(persistence academicUnitMemberStore, users academicUnitMemberUserStore,
	authorization academicUnitMemberAuthorizer, audit mutationAuditor, mail relationshipTransitionMailPreparer,
	effects authorizationInvalidationEffects, now func() time.Time, newID func() string,
) *academicUnitMemberService {
	return &academicUnitMemberService{store: persistence, users: users, authorization: authorization, audit: audit,
		mail: mail, effects: effects, now: now, newID: newID}
}

func (a *App) ListAcademicUnitMembers(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	return a.academicUnitMembers.List(ctx, invocation, query)
}

func (s *academicUnitMemberService) List(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	resource, err := s.authorizeUnit(ctx, invocation, strings.TrimSpace(query.AcademicUnitID), model.ActionAcademicUnitMembersView)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListByAcademicUnit(ctx, resource.ID, query.ActiveAt)
	if err != nil {
		return nil, academicUnitMemberError(err)
	}
	if members == nil {
		members = []*model.AcademicUnitMember{}
	}
	return members, nil
}

func (a *App) CreateAcademicUnitMember(ctx context.Context, invocation Invocation, command CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	return a.academicUnitMembers.Create(ctx, invocation, command)
}

func (s *academicUnitMemberService) Create(ctx context.Context, invocation Invocation, command CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	resource, err := s.authorizeUnit(ctx, invocation, strings.TrimSpace(command.AcademicUnitID), model.ActionAcademicUnitMembersManage)
	if err != nil {
		return nil, err
	}
	unitID, err := model.ParseAcademicUnitID(resource.ID)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	userID, err := model.ParseUserID(strings.TrimSpace(command.UserID))
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "user_id").Wrap(err)
	}
	memberID, err := model.ParseAcademicUnitMemberID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_member_id").Wrap(err)
	}
	candidate := &model.AcademicUnitMember{
		AcademicUnitID: unitID,
		UserID:         userID,
		StartsAt:       model.TimeFromMillis(command.StartAt),
	}
	at := model.TimeFromMillis(model.MillisFromTime(s.now()))
	candidate.PrepareCreate(memberID, at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_unit_member.invalid", err)
	}
	recipient := &model.User{ID: userID, Revision: 1}
	var notice *store.PreparedMail
	if !command.batchRetainedOutcome {
		recipient, err = s.users.Get(ctx, userID.String())
		if err != nil {
			return nil, academicUnitMemberError(err)
		}
		notice, err = s.mail.PrepareRelationshipTransition(appmail.RelationshipTransitionPreparation{
			Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(),
			TemplateKey: model.MailTemplateAcademicUnitAssigned, ActionAt: at,
		})
		if err != nil {
			return nil, NewError("mail.unavailable").Wrap(err)
		}
	}
	idempotency, err := newCommandIdempotency(invocation, "academic_unit_member.add.v1", command.IdempotencyKey, struct {
		AcademicUnitID string `json:"academic_unit_id"`
		UserID         string `json:"user_id"`
		StartAt        int64  `json:"start_at"`
	}{candidate.AcademicUnitID.String(), candidate.UserID.String(), command.StartAt})
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
			Action:     model.ActionAcademicUnitMembersManage,
			Resource:   resource,
			Operation:  "create_member",
			Value:      candidate.Auditable(),
		},
		func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicUnitMember, error) {
			input := &store.AcademicUnitMemberCreation{Member: candidate, ExpectedRecipientRevision: recipient.Revision,
				Notice: notice, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency}
			value, storeErr := s.store.Create(ctx, input)
			changed = storeErr == nil && !input.Replayed && !input.NoOp
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		academicUnitMemberError,
	)
	if err == nil && changed {
		s.effects.InvalidateAuthorization(ctx, userID.String())
	}
	return result, err
}

func (a *App) EndAcademicUnitMember(ctx context.Context, invocation Invocation, command EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	return a.academicUnitMembers.End(ctx, invocation, command)
}

func (s *academicUnitMemberService) End(ctx context.Context, invocation Invocation, command EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_member_id")
	}
	if err := s.authorization.AuthorizePreflight(
		ctx, invocation, model.ActionAcademicUnitMembersManage, model.ResourceAcademicUnit,
	); err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, academicUnitMemberError(err)
	}
	resource, err := s.authorizeUnit(ctx, invocation, current.AcademicUnitID.String(), model.ActionAcademicUnitMembersManage)
	if err != nil {
		return nil, concealMembershipAuthorizationError(err, "academic_unit_member")
	}
	if command.BatchScopeID != "" && strings.TrimSpace(command.BatchScopeID) != current.AcademicUnitID.String() {
		return nil, NewError("resource.not_found").WithField("resource", "academic_unit_member")
	}
	idempotency, err := newCommandIdempotency(invocation, "academic_unit_member.end.v1", command.IdempotencyKey, struct {
		ID      string `json:"id"`
		ScopeID string `json:"scope_id,omitempty"`
	}{id, strings.TrimSpace(command.BatchScopeID)})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	recipient := &model.User{ID: current.UserID, Revision: 1}
	var notice *store.PreparedMail
	at := model.TimeFromMillis(model.MillisFromTime(s.now()))
	if !command.batchRetainedOutcome {
		recipient, err = s.users.Get(ctx, current.UserID.String())
		if err != nil {
			return nil, academicUnitMemberError(err)
		}
		notice, err = s.mail.PrepareRelationshipTransition(appmail.RelationshipTransitionPreparation{
			Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(),
			TemplateKey: model.MailTemplateAcademicUnitAssignmentEnded, ActionAt: at,
		})
		if err != nil {
			return nil, NewError("mail.unavailable").Wrap(err)
		}
	}
	changed := false
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicUnitMembersManage,
			Resource:   resource,
			Operation:  "end_member",
			Prior:      current.Auditable(),
		},
		func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*model.AcademicUnitMember, error) {
			input := &store.AcademicUnitMemberEnd{
				ID: id, ExpectedRevision: current.Revision, EndAt: reference.MutationAtMillis,
				ExpectedRecipientRevision: recipient.Revision, Notice: notice,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency,
			}
			value, storeErr := s.store.EndWithAudit(ctx, input)
			changed = storeErr == nil && !input.Replayed && !input.NoOp
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		academicUnitMemberError,
	)
	if err == nil && changed {
		s.effects.InvalidateAuthorization(ctx, current.UserID.String())
	}
	return result, err
}

func concealMembershipAuthorizationError(err error, resource string) error {
	if Is(err, "authorization.denied") {
		return NewError("resource.not_found").WithField("resource", resource)
	}
	return err
}

func (s *academicUnitMemberService) authorizeUnit(ctx context.Context, invocation Invocation, unitID string, action model.Action) (model.Resource, error) {
	if !model.IsValidId(unitID) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID}
	if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func academicUnitMemberError(err error) error {
	if mapped := idempotencyError(err); mapped != nil {
		return mapped
	}
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "academic_unit_member").Wrap(err)
	case store.IsConflict(err):
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "academic_unit_member_end_time" {
			return NewError("resource.not_found").WithField("resource", "academic_unit_member").Wrap(err)
		}
		return NewError("academic_unit_member.conflict").WithField("resource", "academic_unit_member").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("academic_unit_member.invalid").WithField("resource", "academic_unit_member").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "academic_unit_member").Wrap(err)
	}
}
