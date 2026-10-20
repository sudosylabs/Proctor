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
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListAcademicUnitMembersQuery struct {
	AcademicUnitID model.AcademicUnitID
	ActiveAt       int64
}

type CreateAcademicUnitMemberCommand struct {
	AcademicUnitID            model.AcademicUnitID
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
	ID                        model.AcademicUnitMemberID
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
	Get(context.Context, model.AcademicUnitMemberID) (*model.AcademicUnitMember, error)
	ListByAcademicUnit(context.Context, model.AcademicUnitID, int64) ([]*model.AcademicUnitMember, error)
	ListPageByAcademicUnit(context.Context, store.AcademicUnitMemberPageOptions) (*store.AcademicUnitMemberPage, error)
	Create(context.Context, *store.AcademicUnitMemberCreation) (*model.AcademicUnitMember, error)
	EndWithAudit(context.Context, *store.AcademicUnitMemberEnd) (*model.AcademicUnitMember, error)
}

// membershipAuthorizer authorizes a membership mutation on its scope Resource
// and, before the scope is known, the caller's coarse authority for the
// Resource type.
type membershipAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	AuthorizePreflight(context.Context, Invocation, model.Action, model.ResourceType) error
}

type academicUnitMemberUserStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
}

type academicUnitMemberService struct {
	store         academicUnitMemberStore
	users         academicUnitMemberUserStore
	authorization membershipAuthorizer
	audit         mutationAuditor
	mail          relationshipTransitionMailPreparer
	effects       authorizationInvalidationEffects
	now           func() time.Time
	newID         func() string
}

func newAcademicUnitMemberService(persistence academicUnitMemberStore, users academicUnitMemberUserStore,
	authorization membershipAuthorizer, audit mutationAuditor, mail relationshipTransitionMailPreparer,
	effects authorizationInvalidationEffects, now func() time.Time, newID func() string,
) *academicUnitMemberService {
	return &academicUnitMemberService{store: persistence, users: users, authorization: authorization, audit: audit,
		mail: mail, effects: effects, now: now, newID: newID}
}

func (a *App) ListAcademicUnitMembers(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	return a.academicUnitMembers.List(ctx, invocation, query)
}

func (s *academicUnitMemberService) List(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	resource, err := s.authorizeUnit(ctx, invocation, query.AcademicUnitID, model.ActionAcademicUnitMembersView)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListByAcademicUnit(ctx, model.AcademicUnitID(resource.ID), query.ActiveAt)
	if err != nil {
		return nil, academicUnitMemberStoreErrors.Translate(err)
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
	resource, err := s.authorizeUnit(ctx, invocation, command.AcademicUnitID, model.ActionAcademicUnitMembersManage)
	if err != nil {
		return nil, err
	}
	unitID, err := model.ParseAcademicUnitID(resource.ID)
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	userID, err := model.ParseUserID(strings.TrimSpace(command.UserID))
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "user_id").Wrap(err)
	}
	memberID, err := model.ParseAcademicUnitMemberID(s.newID())
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_member_id").Wrap(err)
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
		recipient, err = s.users.Get(ctx, userID)
		if err != nil {
			return nil, academicUnitMemberStoreErrors.Translate(err)
		}
		notice, err = s.mail.PrepareRelationshipTransition(appmail.RelationshipTransitionPreparation{
			Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(),
			TemplateKey: model.MailTemplateAcademicUnitAssigned, ActionAt: at,
		})
		if err != nil {
			return nil, apperror.New("mail.unavailable").Wrap(err)
		}
	}
	idempotency, err := applicationidempotency.Prepare(invocation, "academic_unit_member.add.v1", command.IdempotencyKey, struct {
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
		academicUnitMemberStoreErrors.Translate,
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
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_member_id")
	}
	if err := s.authorization.AuthorizePreflight(
		ctx, invocation, model.ActionAcademicUnitMembersManage, model.ResourceAcademicUnit,
	); err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, academicUnitMemberStoreErrors.Translate(err)
	}
	resource, err := s.authorizeUnit(ctx, invocation, current.AcademicUnitID, model.ActionAcademicUnitMembersManage)
	if err != nil {
		return nil, concealMembershipAuthorizationError(err, "academic_unit_member")
	}
	if command.BatchScopeID != "" && strings.TrimSpace(command.BatchScopeID) != current.AcademicUnitID.String() {
		return nil, apperror.New("resource.not_found").WithField("resource", "academic_unit_member")
	}
	idempotency, err := applicationidempotency.Prepare(invocation, "academic_unit_member.end.v1", command.IdempotencyKey, struct {
		ID      string `json:"id"`
		ScopeID string `json:"scope_id,omitempty"`
	}{id.String(), strings.TrimSpace(command.BatchScopeID)})
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
		recipient, err = s.users.Get(ctx, current.UserID)
		if err != nil {
			return nil, academicUnitMemberStoreErrors.Translate(err)
		}
		notice, err = s.mail.PrepareRelationshipTransition(appmail.RelationshipTransitionPreparation{
			Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(),
			TemplateKey: model.MailTemplateAcademicUnitAssignmentEnded, ActionAt: at,
		})
		if err != nil {
			return nil, apperror.New("mail.unavailable").Wrap(err)
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
		academicUnitMemberStoreErrors.Translate,
	)
	if err == nil && changed {
		s.effects.InvalidateAuthorization(ctx, current.UserID.String())
	}
	return result, err
}

func concealMembershipAuthorizationError(err error, resource string) error {
	if apperror.Is(err, "authorization.denied") {
		return apperror.New("resource.not_found").WithField("resource", resource)
	}
	return err
}

func (s *academicUnitMemberService) authorizeUnit(ctx context.Context, invocation Invocation, unitID model.AcademicUnitID, action model.Action) (model.Resource, error) {
	if !unitID.IsValid() {
		return model.Resource{}, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

var academicUnitMemberStoreErrors = apperror.AdministrationStoreTable("academic_unit_member", "academic_unit_member.conflict", "academic_unit_member.invalid", map[string]apperror.Code{
	"academic_unit_member_end_time": "resource.not_found",
})

// ListAcademicUnitMembersPageQuery opts into bounded membership enumeration. Each page is
// authorized independently and explicit selectors preserve its effective filter.
type ListAcademicUnitMembersPageQuery struct {
	AfterUserID    model.UserID
	AfterID        model.AcademicUnitMemberID
	AcademicUnitID model.AcademicUnitID
	MembershipPageQuery
}

type AcademicUnitMemberPage struct {
	Members  []*model.AcademicUnitMember
	HasMore  bool
	ActiveAt int64
}

func (a *App) ListAcademicUnitMembersPage(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersPageQuery) (*AcademicUnitMemberPage, error) {
	return a.academicUnitMembers.ListPage(ctx, invocation, query)
}

func (s *academicUnitMemberService) ListPage(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersPageQuery) (*AcademicUnitMemberPage, error) {
	resource, err := s.authorizeUnit(ctx, invocation, query.AcademicUnitID, model.ActionAcademicUnitMembersView)
	if err != nil {
		return nil, err
	}
	activeAt, limit, err := resolveMembershipPage(query.MembershipPageQuery, s.now())
	if err != nil {
		return nil, err
	}
	scopeID, err := model.ParseAcademicUnitID(resource.ID)
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "academic_unit_id")
	}
	if query.AfterID.IsZero() != query.AfterUserID.IsZero() || (!query.AfterID.IsZero() && (!query.AfterID.IsValid() || !query.AfterUserID.IsValid())) {
		return nil, apperror.New("request.invalid").WithField("field", "cursor")
	}
	options := store.AcademicUnitMemberPageOptions{AcademicUnitID: scopeID, ActiveAt: activeAt, Limit: limit, AfterID: query.AfterID, AfterUserID: query.AfterUserID}

	page, err := s.store.ListPageByAcademicUnit(ctx, options)
	if err != nil {
		return nil, academicUnitMemberStoreErrors.Translate(err)
	}
	result := &AcademicUnitMemberPage{Members: page.Members, ActiveAt: activeAt, HasMore: page.HasMore}
	if result.Members == nil {
		result.Members = []*model.AcademicUnitMember{}
	}
	return result, nil
}
