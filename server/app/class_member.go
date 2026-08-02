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

type ListClassMembersQuery struct {
	ClassID  string
	ActiveAt int64
}

type EnrollClassMemberCommand struct {
	ClassID string
	UserID  string
	StartAt int64
	EndAt   int64
}

type EndClassMemberCommand struct {
	ID string
}

type classMemberStore interface {
	Get(context.Context, string) (*model.ClassMember, error)
	ListByClass(context.Context, string, int64) ([]*model.ClassMember, error)
	EnrollWithAudit(context.Context, *store.ClassMemberEnrollment) (*store.ClassEnrollmentResult, error)
	EndWithAudit(context.Context, *store.ClassMemberEnd) (*model.ClassMember, error)
}

type classMemberClassStore interface {
	Get(context.Context, string) (*model.Class, error)
}

type classMemberAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type classMemberService struct {
	store         classMemberStore
	classes       classMemberClassStore
	authorization classMemberAuthorizer
	audit         mutationAuditor
	now           func() time.Time
	newID         func() string
}

func newClassMemberService(persistence classMemberStore, classes classMemberClassStore, authorization classMemberAuthorizer, audit mutationAuditor, now func() time.Time, newID func() string) *classMemberService {
	return &classMemberService{store: persistence, classes: classes, authorization: authorization, audit: audit, now: now, newID: newID}
}

func (a *App) ListClassMembers(ctx context.Context, invocation Invocation, query ListClassMembersQuery) ([]*model.ClassMember, error) {
	return a.classMembers.List(ctx, invocation, query)
}

func (s *classMemberService) List(ctx context.Context, invocation Invocation, query ListClassMembersQuery) ([]*model.ClassMember, error) {
	resource, err := s.authorizeClass(ctx, invocation, strings.TrimSpace(query.ClassID), model.ActionClassMembersView)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListByClass(ctx, resource.Id, query.ActiveAt)
	if err != nil {
		return nil, classMemberError(err)
	}
	if members == nil {
		members = []*model.ClassMember{}
	}
	return members, nil
}

func (a *App) EnrollClassMember(ctx context.Context, invocation Invocation, command EnrollClassMemberCommand) (*model.ClassEnrollment, error) {
	return a.classMembers.Enroll(ctx, invocation, command)
}

func (s *classMemberService) Enroll(ctx context.Context, invocation Invocation, command EnrollClassMemberCommand) (*model.ClassEnrollment, error) {
	classID := strings.TrimSpace(command.ClassID)
	resource, err := s.authorizeClass(ctx, invocation, classID, model.ActionClassMembersManage)
	if err != nil {
		return nil, err
	}
	class, err := s.classes.Get(ctx, classID)
	if err != nil {
		return nil, classMemberError(err)
	}
	at := s.now().UnixMilli()
	candidate := &model.ClassMember{ClassId: classID, AcademicPeriodId: class.AcademicPeriodId, UserId: strings.TrimSpace(command.UserID), StartAt: command.StartAt, EndAt: command.EndAt}
	candidate.PrepareCreate(s.newID(), at)
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, NewError("class_member.invalid").WithFields(appErr.SafeFields()).Wrap(appErr)
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionClassMembersManage, resource, "enroll", candidate.Auditable(), nil)
	if err != nil {
		return nil, err
	}
	result, err := s.store.EnrollWithAudit(ctx, &store.ClassMemberEnrollment{Member: candidate, AuditEventID: auditID, AuditAt: at})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return &model.ClassEnrollment{Membership: result.Membership, Previous: result.Previous}, nil
}

func (a *App) EndClassMember(ctx context.Context, invocation Invocation, command EndClassMemberCommand) (*model.ClassMember, error) {
	return a.classMembers.End(ctx, invocation, command)
}

func (s *classMemberService) End(ctx context.Context, invocation Invocation, command EndClassMemberCommand) (*model.ClassMember, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "class_member_id")
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classMemberError(err)
	}
	resource, err := s.authorizeClass(ctx, invocation, current.ClassId, model.ActionClassMembersManage)
	if err != nil {
		return nil, err
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionClassMembersManage, resource, "end", nil, current.Auditable())
	if err != nil {
		return nil, err
	}
	at := s.now().UnixMilli()
	ended, err := s.store.EndWithAudit(ctx, &store.ClassMemberEnd{ID: id, ExpectedRevision: current.Revision, EndAt: at, AuditEventID: auditID, AuditAt: at})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return ended, nil
}

func (s *classMemberService) authorizeClass(ctx context.Context, invocation Invocation, classID string, action model.Action) (model.Resource, error) {
	if !model.IsValidId(classID) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "class_id")
	}
	resource := model.Resource{Type: model.ResourceClass, Id: classID}
	if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func (s *classMemberService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := classMemberError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
}

func classMemberError(err error) error {
	if store.IsNotFound(err) {
		return NewError("resource.not_found").WithField("resource", "class_member").Wrap(err)
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) {
		switch conflict.Constraint {
		case "class_member_student_affiliation_required":
			return NewError("class_member.student_affiliation_required").Wrap(err)
		case "class_member_end_time":
			return NewError("resource.not_found").WithField("resource", "class_member").Wrap(err)
		default:
			return NewError("class.enrollment_conflict").WithField("resource", "class_member").Wrap(err)
		}
	}
	var invalid *store.ErrInvalidInput
	var reference *store.ErrReference
	if errors.As(err, &invalid) || errors.As(err, &reference) {
		return NewError("class_member.invalid").WithField("resource", "class_member").Wrap(err)
	}
	return NewError("administration.unavailable").WithField("resource", "class_member").Wrap(err)
}
