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
	ListActiveByUser(context.Context, string, int64) ([]*model.ClassMember, error)
}

type classMemberClassStore interface {
	Get(context.Context, string) (*model.Class, error)
}

type classMemberUserStore interface {
	Get(context.Context, string) (*model.User, error)
}

type classMemberAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	AuthorizePreflight(context.Context, Invocation, model.Action, model.ResourceType) error
}

type classMemberService struct {
	store         classMemberStore
	classes       classMemberClassStore
	users         classMemberUserStore
	authorization classMemberAuthorizer
	audit         mutationAuditor
	mail          classTransitionMailPreparer
	now           func() time.Time
	newID         func() string
}

func newClassMemberService(persistence classMemberStore, classes classMemberClassStore, users classMemberUserStore,
	authorization classMemberAuthorizer, audit mutationAuditor, mail classTransitionMailPreparer,
	now func() time.Time, newID func() string,
) *classMemberService {
	return &classMemberService{store: persistence, classes: classes, users: users, authorization: authorization,
		audit: audit, mail: mail, now: now, newID: newID}
}

func (a *App) ListClassMembers(ctx context.Context, invocation Invocation, query ListClassMembersQuery) ([]*model.ClassMember, error) {
	return a.classMembers.List(ctx, invocation, query)
}

func (s *classMemberService) List(ctx context.Context, invocation Invocation, query ListClassMembersQuery) ([]*model.ClassMember, error) {
	resource, err := s.authorizeClass(ctx, invocation, strings.TrimSpace(query.ClassID), model.ActionClassMembersView)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListByClass(ctx, resource.ID, query.ActiveAt)
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
	parsedClassID, err := model.ParseClassID(classID)
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "class_id").Wrap(err)
	}
	userID, err := model.ParseUserID(strings.TrimSpace(command.UserID))
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "user_id").Wrap(err)
	}
	memberID, err := model.ParseClassMemberID(s.newID())
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "class_member_id").Wrap(err)
	}
	at := model.TimeFromMillis(model.MillisFromTime(s.now()))
	candidate := &model.ClassMember{
		ClassID:          parsedClassID,
		AcademicPeriodID: class.AcademicPeriodID,
		UserID:           userID,
		StartsAt:         model.TimeFromMillis(command.StartAt),
		EndsAt:           model.OptionalTimeFromMillis(command.EndAt),
	}
	candidate.PrepareCreate(memberID, at)
	if err := candidate.Validate(); err != nil {
		return nil, domainInvalid("class_member.invalid", err)
	}
	active, err := s.store.ListActiveByUser(ctx, userID.String(), model.MillisFromTime(candidate.StartsAt))
	if err != nil {
		return nil, classMemberError(err)
	}
	var previous *model.ClassMember
	for _, membership := range active {
		if membership != nil && membership.AcademicPeriodID == candidate.AcademicPeriodID {
			if previous != nil {
				return nil, NewError("class.enrollment_conflict").WithField("resource", "class_member")
			}
			previous = membership
		}
	}
	var previousClass *model.Class
	if previous != nil {
		if previous.ClassID == candidate.ClassID {
			return nil, NewError("class.enrollment_conflict").WithField("resource", "class_member")
		}
		if _, err = s.authorizeClass(ctx, invocation, previous.ClassID.String(), model.ActionClassMembersManage); err != nil {
			return nil, concealMembershipAuthorizationError(err, "class_member")
		}
		previousClass, err = s.classes.Get(ctx, previous.ClassID.String())
		if err != nil {
			return nil, classMemberError(err)
		}
	}
	recipient, err := s.users.Get(ctx, userID.String())
	if err != nil {
		return nil, classMemberError(err)
	}
	details := ClassTransitionMailDetails{ClassDisplayName: class.DisplayName, StartsAt: candidate.StartsAt}
	if candidate.EndsAt.Valid {
		details.EndsAt = candidate.EndsAt.Time
	}
	key := model.MailTemplateAcademicClassEnrolled
	if previous != nil {
		key = model.MailTemplateAcademicClassTransferred
		details.PreviousClassDisplayName = previousClass.DisplayName
	}
	prepared, err := s.mail.PrepareClassTransition(ClassTransitionMailPreparation{Recipient: recipient,
		OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: key,
		Details: details, ActionAt: at})
	if err != nil {
		return nil, NewError("mail.unavailable").Wrap(err)
	}
	destinationAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
		Resource: resource, Operation: "enroll", Value: candidate.Auditable()}
	if previous != nil {
		destinationAttempt.Operation = "transfer_in"
	}
	result, err := runAuditedMutation(ctx, s.audit, destinationAttempt, func() time.Time { return at },
		func(ctx context.Context, destinationReference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
			var expectedPreviousID model.ClassMemberID
			if previous == nil {
				return s.store.EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
					Member: candidate, ExpectedRecipientRevision: recipient.Revision,
					Notice:       &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job},
					AuditEventID: destinationReference.ID, AuditAt: destinationReference.MutationAtMillis,
				})
			}
			expectedPreviousID = previous.ID
			sourceAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
				Resource:  model.Resource{Type: model.ResourceClass, ID: previous.ClassID.String()},
				Operation: "transfer_out", Prior: previous.Auditable()}
			return runAuditedMutation(ctx, s.audit, sourceAttempt,
				func() time.Time { return model.TimeFromMillis(destinationReference.MutationAtMillis) },
				func(ctx context.Context, sourceReference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
					return s.store.EnrollWithAudit(ctx, &store.ClassMemberEnrollment{
						Member: candidate, ExpectedPreviousID: expectedPreviousID, ExpectedRecipientRevision: recipient.Revision,
						Notice:       &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job},
						AuditEventID: destinationReference.ID, PreviousAuditEventID: sourceReference.ID,
						AuditAt: destinationReference.MutationAtMillis,
					})
				}, classMemberError)
		},
		classMemberError,
	)
	if err != nil {
		return nil, err
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
	if err := s.authorization.AuthorizePreflight(
		ctx, invocation, model.ActionClassMembersManage, model.ResourceClass,
	); err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classMemberError(err)
	}
	resource, err := s.authorizeClass(ctx, invocation, current.ClassID.String(), model.ActionClassMembersManage)
	if err != nil {
		return nil, concealMembershipAuthorizationError(err, "class_member")
	}
	class, err := s.classes.Get(ctx, current.ClassID.String())
	if err != nil {
		return nil, classMemberError(err)
	}
	recipient, err := s.users.Get(ctx, current.UserID.String())
	if err != nil {
		return nil, classMemberError(err)
	}
	at := model.TimeFromMillis(model.MillisFromTime(s.now()))
	prepared, err := s.mail.PrepareClassTransition(ClassTransitionMailPreparation{Recipient: recipient,
		OccurrenceID: model.NewMailOccurrenceID(),
		TemplateKey:  model.MailTemplateAcademicClassEnrollmentEnded,
		Details:      ClassTransitionMailDetails{ClassDisplayName: class.DisplayName, StartsAt: current.StartsAt, EndsAt: at}, ActionAt: at})
	if err != nil {
		return nil, NewError("mail.unavailable").Wrap(err)
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionClassMembersManage,
			Resource:   resource,
			Operation:  "end",
			Prior:      current.Auditable(),
		},
		func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*model.ClassMember, error) {
			return s.store.EndWithAudit(ctx, &store.ClassMemberEnd{
				ID: id, ExpectedRevision: current.Revision, ExpectedRecipientRevision: recipient.Revision,
				EndAt:        reference.MutationAtMillis,
				Notice:       &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job},
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		classMemberError,
	)
}

func (s *classMemberService) authorizeClass(ctx context.Context, invocation Invocation, classID string, action model.Action) (model.Resource, error) {
	if !model.IsValidId(classID) {
		return model.Resource{}, NewError("request.invalid").WithField("field", "class_id")
	}
	resource := model.Resource{Type: model.ResourceClass, ID: classID}
	if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
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
