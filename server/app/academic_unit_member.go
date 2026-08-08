// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListAcademicUnitMembersQuery struct {
	AcademicUnitID string
	ActiveAt       int64
}

type CreateAcademicUnitMemberCommand struct {
	AcademicUnitID string
	UserID         string
	StartAt        int64
}

type EndAcademicUnitMemberCommand struct {
	ID string
}

type academicUnitMemberStore interface {
	Get(context.Context, string) (*model.AcademicUnitMember, error)
	ListByAcademicUnit(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
	Create(context.Context, *store.AcademicUnitMemberCreation) (*model.AcademicUnitMember, error)
	EndWithAudit(context.Context, *store.AcademicUnitMemberEnd) (*model.AcademicUnitMember, error)
}

type academicUnitMemberAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type academicUnitMemberService struct {
	store         academicUnitMemberStore
	authorization academicUnitMemberAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newAcademicUnitMemberService(persistence academicUnitMemberStore, authorization academicUnitMemberAuthorizer, audit mutationAuditor, now func() time.Time, newID func() string) *academicUnitMemberService {
	return &academicUnitMemberService{store: persistence, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) ListAcademicUnitMembers(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	return a.academicUnitMembers.List(ctx, invocation, query)
}

func (s *academicUnitMemberService) List(ctx context.Context, invocation Invocation, query ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	resource, err := s.authorizeUnit(ctx, invocation, strings.TrimSpace(query.AcademicUnitID))
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListByAcademicUnit(ctx, resource.Id, query.ActiveAt)
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
	resource, err := s.authorizeUnit(ctx, invocation, strings.TrimSpace(command.AcademicUnitID))
	if err != nil {
		return nil, err
	}
	unitID, err := model.ParseAcademicUnitID(resource.Id)
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
	at := s.now()
	candidate.PrepareCreate(memberID, at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("academic_unit_member.invalid", err)
	}
	auditAt := model.MillisFromTime(at)
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "create_member", candidate.Auditable(), nil)
	if err != nil {
		return nil, err
	}
	saved, err := s.store.Create(ctx, &store.AcademicUnitMemberCreation{Member: candidate, AuditEventID: auditID, AuditAt: auditAt})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return saved, nil
}

func (a *App) EndAcademicUnitMember(ctx context.Context, invocation Invocation, command EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	return a.academicUnitMembers.End(ctx, invocation, command)
}

func (s *academicUnitMemberService) End(ctx context.Context, invocation Invocation, command EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "academic_unit_member_id")
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, academicUnitMemberError(err)
	}
	resource, err := s.authorizeUnit(ctx, invocation, current.AcademicUnitID.String())
	if err != nil {
		return nil, err
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionAcademicUnitManage, resource, "end_member", nil, current.Auditable())
	if err != nil {
		return nil, err
	}
	at := s.now()
	auditAt := model.MillisFromTime(at)
	ended, err := s.store.EndWithAudit(ctx, &store.AcademicUnitMemberEnd{ID: id, ExpectedRevision: current.Revision, EndAt: auditAt, AuditEventID: auditID, AuditAt: auditAt})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return ended, nil
}

func (s *academicUnitMemberService) authorizeUnit(ctx context.Context, invocation Invocation, unitID string) (model.Resource, error) {
	if !model.IsValidId(unitID) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "academic_unit_id")
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, Id: unitID}
	if err := s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitManage, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func (s *academicUnitMemberService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := academicUnitMemberError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
}

func academicUnitMemberError(err error) error {
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
