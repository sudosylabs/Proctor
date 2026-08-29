// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListClassMembersQuery struct {
	ClassID  string
	ActiveAt int64
}

type EnrollClassMemberCommand struct {
	ClassID                       string
	UserID                        string
	StartAt                       int64
	EndAt                         int64
	ExpectedPreviousID            string
	RequireTransfer               bool
	IdempotencyKey                string
	batchReplayed                 *bool
	batchAuthorization            *store.CommandAuthorization
	batchMetadata                 *store.CommandBatch
	batchRetainedOutcome          bool
	studentProgression            bool
	progressionSourceAuditID      string
	progressionDestinationAuditID string
	onboardingImportID            model.OnboardingImportID
	onboardingImportRowNumber     int
}

type EndClassMemberCommand struct {
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

type classMemberSittingStore interface {
	ListInvalidationTargetsByClass(context.Context, model.ClassID, model.ExamSittingID, int) ([]store.ExamSittingInvalidationTarget, error)
}

type classMemberEffects interface {
	MembershipChanged(context.Context, model.UserID, []model.ClassID) error
	Report(context.Context, string, error)
}

type classMemberService struct {
	store         classMemberStore
	classes       classMemberClassStore
	users         classMemberUserStore
	authorization classMemberAuthorizer
	audit         mutationAuditor
	mail          classTransitionMailPreparer
	effects       classMemberEffects
	now           func() time.Time
	newID         func() string
}

func newClassMemberService(persistence classMemberStore, classes classMemberClassStore, users classMemberUserStore,
	authorization classMemberAuthorizer, audit mutationAuditor, mail classTransitionMailPreparer,
	effects classMemberEffects, now func() time.Time, newID func() string,
) *classMemberService {
	return &classMemberService{store: persistence, classes: classes, users: users, authorization: authorization,
		audit: audit, mail: mail, effects: effects, now: now, newID: newID}
}

type classMemberRealtimeEffects struct {
	sittings classMemberSittingStore
	realtime *realtimeService
}

func (effects classMemberRealtimeEffects) MembershipChanged(
	ctx context.Context,
	candidateID model.UserID,
	classIDs []model.ClassID,
) error {
	if effects.sittings == nil || effects.realtime == nil {
		return errors.New("Class Membership realtime dependencies are invalid")
	}
	candidateEvent, err := apprealtime.NewCandidateExamActivityChangedEvent(candidateID)
	if err != nil {
		return err
	}
	errs := []error{effects.realtime.Publish(ctx, candidateEvent),
		effects.realtime.InvalidateCurrentUserContext(ctx, candidateID)}
	seen := make(map[model.ClassID]struct{}, len(classIDs))
	for _, classID := range classIDs {
		if !classID.IsValid() {
			errs = append(errs, errors.New("Class Membership invalidation requires a valid Class identity"))
			continue
		}
		if _, exists := seen[classID]; exists {
			continue
		}
		seen[classID] = struct{}{}
		var after model.ExamSittingID
		for {
			targets, listErr := effects.sittings.ListInvalidationTargetsByClass(ctx, classID, after, 200)
			if listErr != nil {
				errs = append(errs, listErr)
				break
			}
			for _, target := range targets {
				event, eventErr := apprealtime.NewManagerSittingBoardChangedEvent(target.ExamID, target.SittingID)
				if eventErr != nil {
					errs = append(errs, eventErr)
					continue
				}
				errs = append(errs, effects.realtime.Publish(ctx, event))
			}
			if len(targets) < 200 {
				break
			}
			after = targets[len(targets)-1].SittingID
		}
	}
	return errors.Join(errs...)
}

func (effects classMemberRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	effects.realtime.reportTransientFailure(ctx, operation, err)
}

func (effects classMemberRealtimeEffects) InvalidateAuthorization(ctx context.Context, userID string) {
	effects.realtime.InvalidateAuthorization(ctx, userID)
}

func (s *classMemberService) publishMembershipChanged(
	ctx context.Context,
	candidateID model.UserID,
	classIDs ...model.ClassID,
) {
	if s.effects == nil {
		return
	}
	if err := s.effects.MembershipChanged(ctx, candidateID, classIDs); err != nil {
		s.effects.Report(ctx, "class_member.membership_changed", err)
	}
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
	var retainedPrevious *model.ClassMember
	if command.batchRetainedOutcome && command.RequireTransfer {
		previousID, parseErr := model.ParseClassMemberID(strings.TrimSpace(command.ExpectedPreviousID))
		if parseErr != nil {
			return nil, NewError("request.invalid").WithField("field", "expected_previous_id").Wrap(parseErr)
		}
		retainedPrevious, err = s.store.Get(ctx, previousID.String())
		if err != nil {
			return nil, classMemberError(err)
		}
		if retainedPrevious.UserID != userID {
			return nil, NewError("resource.not_found").WithField("resource", "class_member")
		}
		if _, err = s.authorizeClass(ctx, invocation, retainedPrevious.ClassID.String(), model.ActionClassMembersManage); err != nil {
			return nil, concealMembershipAuthorizationError(err, "class_member")
		}
		if command.batchAuthorization != nil {
			command.batchAuthorization.ClassMemberID = retainedPrevious.ID
		}
	}
	if command.batchRetainedOutcome {
		candidate := &model.ClassMember{ClassID: parsedClassID, UserID: userID, StartsAt: model.TimeFromMillis(command.StartAt), EndsAt: model.OptionalTimeFromMillis(command.EndAt)}
		candidate.PrepareCreate(memberID, at)
		idempotency, commandErr := newCommandIdempotency(invocation, "class_member.enroll.v1", command.IdempotencyKey, struct {
			ClassID            string `json:"class_id"`
			UserID             string `json:"user_id"`
			StartAt            int64  `json:"start_at"`
			EndAt              int64  `json:"end_at"`
			ExpectedPreviousID string `json:"expected_previous_id,omitempty"`
			Transfer           bool   `json:"transfer"`
			Progression        bool   `json:"progression"`
		}{candidate.ClassID.String(), candidate.UserID.String(), command.StartAt, command.EndAt, strings.TrimSpace(command.ExpectedPreviousID), command.RequireTransfer, command.studentProgression})
		if commandErr != nil {
			return nil, commandErr
		}
		bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
		bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
		bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
		freshMutation := false
		result, replayErr := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
			Resource: resource, Operation: "enroll", Value: candidate.Auditable()}, func() time.Time { return at },
			func(ctx context.Context, reference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
				execute := func(ctx context.Context, sourceAuditID string) (*store.ClassEnrollmentResult, error) {
					input := &store.ClassMemberEnrollment{Member: candidate, ExpectedRecipientRevision: 1, StudentProgression: command.studentProgression,
						ProgressionSourceAuditEventID: command.progressionSourceAuditID, ProgressionDestinationAuditEventID: command.progressionDestinationAuditID,
						AuditEventID: reference.ID, PreviousAuditEventID: sourceAuditID, AuditAt: reference.MutationAtMillis, Command: idempotency}
					if retainedPrevious != nil {
						input.ExpectedPreviousID = retainedPrevious.ID
					}
					value, storeErr := s.store.EnrollWithAudit(ctx, input)
					freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
					if command.batchReplayed != nil {
						*command.batchReplayed = input.Replayed || input.NoOp
					}
					return value, storeErr
				}
				if retainedPrevious == nil {
					return execute(ctx, "")
				}
				sourceAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
					Resource:  model.Resource{Type: model.ResourceClass, ID: retainedPrevious.ClassID.String()},
					Operation: "transfer_out", Prior: retainedPrevious.Auditable()}
				return runAuditedMutation(ctx, s.audit, sourceAttempt, func() time.Time { return at },
					func(ctx context.Context, sourceReference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
						return execute(ctx, sourceReference.ID)
					}, classMemberError)
			}, classMemberError)
		if replayErr != nil {
			return nil, replayErr
		}
		if freshMutation {
			s.publishEnrollmentChanged(ctx, result)
		}
		return &model.ClassEnrollment{Membership: result.Membership, Previous: result.Previous}, nil
	}
	class, err := s.classes.Get(ctx, classID)
	if err != nil {
		return nil, classMemberError(err)
	}
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
	var previous *model.ClassMember
	possibleReplay := command.batchRetainedOutcome
	if !command.batchRetainedOutcome {
		active, listErr := s.store.ListActiveByUser(ctx, userID.String(), model.MillisFromTime(candidate.StartsAt))
		if listErr != nil {
			return nil, classMemberError(listErr)
		}
		for _, membership := range active {
			if membership != nil && membership.AcademicPeriodID == candidate.AcademicPeriodID {
				if previous != nil {
					return nil, NewError("class.enrollment_conflict").WithField("resource", "class_member")
				}
				previous = membership
			}
		}
		if command.IdempotencyKey != "" {
			expectedPreviousID := strings.TrimSpace(command.ExpectedPreviousID)
			switch {
			case command.RequireTransfer && (previous == nil || (previous.ID.String() != expectedPreviousID && previous.ClassID != candidate.ClassID)):
				return nil, NewError("class.enrollment_conflict").WithField("resource", "class_member")
			case !command.RequireTransfer && previous != nil && previous.ClassID != candidate.ClassID:
				return nil, NewError("class.enrollment_conflict").WithField("resource", "class_member")
			}
		}
		possibleReplay = command.IdempotencyKey != "" && previous != nil && previous.ClassID == candidate.ClassID
	}
	if possibleReplay {
		previous = nil
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
	idempotency, err := newCommandIdempotency(invocation, "class_member.enroll.v1", command.IdempotencyKey, struct {
		ClassID            string `json:"class_id"`
		UserID             string `json:"user_id"`
		StartAt            int64  `json:"start_at"`
		EndAt              int64  `json:"end_at"`
		ExpectedPreviousID string `json:"expected_previous_id,omitempty"`
		Transfer           bool   `json:"transfer"`
		Progression        bool   `json:"progression"`
	}{candidate.ClassID.String(), candidate.UserID.String(), command.StartAt, command.EndAt, strings.TrimSpace(command.ExpectedPreviousID), command.RequireTransfer, command.studentProgression})
	if err != nil {
		return nil, err
	}
	if previous != nil && command.batchAuthorization != nil {
		command.batchAuthorization.ClassMemberID = previous.ID
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	recipient := &model.User{ID: userID, Revision: 1}
	if !command.batchRetainedOutcome {
		recipient, err = s.users.Get(ctx, userID.String())
		if err != nil {
			return nil, classMemberError(err)
		}
	}
	details := appmail.ClassTransitionDetails{ClassDisplayName: class.DisplayName, StartsAt: candidate.StartsAt}
	if candidate.EndsAt.Valid {
		details.EndsAt = candidate.EndsAt.Time
	}
	key := model.MailTemplateAcademicClassEnrolled
	if previous != nil {
		key = model.MailTemplateAcademicClassTransferred
		details.PreviousClassDisplayName = previousClass.DisplayName
	}
	var prepared *preparedDirectMail
	if !command.batchRetainedOutcome {
		prepared, err = s.mail.PrepareClassTransition(appmail.ClassTransitionPreparation{Recipient: recipient,
			OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: key,
			Details: details, ActionAt: at})
		if err != nil {
			return nil, NewError("mail.unavailable").Wrap(err)
		}
	}
	destinationAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
		Resource: resource, Operation: "enroll", Value: candidate.Auditable()}
	if previous != nil {
		destinationAttempt.Operation = "transfer_in"
	}
	freshMutation := false
	result, err := runAuditedMutation(ctx, s.audit, destinationAttempt, func() time.Time { return at },
		func(ctx context.Context, destinationReference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
			var expectedPreviousID model.ClassMemberID
			if previous == nil {
				var notice *store.PreparedMail
				if prepared != nil {
					notice = &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job}
				}
				input := &store.ClassMemberEnrollment{
					Member: candidate, ExpectedRecipientRevision: recipient.Revision, StudentProgression: command.studentProgression,
					ProgressionSourceAuditEventID: command.progressionSourceAuditID, ProgressionDestinationAuditEventID: command.progressionDestinationAuditID,
					Notice:       notice,
					AuditEventID: destinationReference.ID, AuditAt: destinationReference.MutationAtMillis, Command: idempotency,
				}
				value, storeErr := s.store.EnrollWithAudit(ctx, input)
				freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
				if command.batchReplayed != nil {
					*command.batchReplayed = input.Replayed || input.NoOp
				}
				return value, storeErr
			}
			expectedPreviousID = previous.ID
			sourceAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
				Resource:  model.Resource{Type: model.ResourceClass, ID: previous.ClassID.String()},
				Operation: "transfer_out", Prior: previous.Auditable()}
			return runAuditedMutation(ctx, s.audit, sourceAttempt,
				func() time.Time { return model.TimeFromMillis(destinationReference.MutationAtMillis) },
				func(ctx context.Context, sourceReference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
					var notice *store.PreparedMail
					if prepared != nil {
						notice = &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job}
					}
					input := &store.ClassMemberEnrollment{
						Member: candidate, ExpectedPreviousID: expectedPreviousID, ExpectedRecipientRevision: recipient.Revision, StudentProgression: command.studentProgression,
						ProgressionSourceAuditEventID: command.progressionSourceAuditID, ProgressionDestinationAuditEventID: command.progressionDestinationAuditID,
						Notice:       notice,
						AuditEventID: destinationReference.ID, PreviousAuditEventID: sourceReference.ID,
						AuditAt: destinationReference.MutationAtMillis, Command: idempotency,
					}
					value, storeErr := s.store.EnrollWithAudit(ctx, input)
					freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
					if command.batchReplayed != nil {
						*command.batchReplayed = input.Replayed || input.NoOp
					}
					return value, storeErr
				}, classMemberError)
		},
		classMemberError,
	)
	if err != nil {
		return nil, err
	}
	if freshMutation {
		s.publishEnrollmentChanged(ctx, result)
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
	if command.batchRetainedOutcome {
		idempotency, err := newCommandIdempotency(invocation, "class_member.end.v1", command.IdempotencyKey, struct {
			ID      string `json:"id"`
			ScopeID string `json:"scope_id,omitempty"`
		}{id, strings.TrimSpace(command.BatchScopeID)})
		if err != nil {
			return nil, err
		}
		bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
		bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
		bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
		at := model.TimeFromMillis(model.MillisFromTime(s.now()))
		freshMutation := false
		ended, mutationErr := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
			Resource: model.Resource{Type: model.ResourceClass, ID: strings.TrimSpace(command.BatchScopeID)}, Operation: "end"},
			func() time.Time { return at }, func(ctx context.Context, reference mutationAttemptReference) (*model.ClassMember, error) {
				input := &store.ClassMemberEnd{ID: id, ExpectedRevision: 1, ExpectedRecipientRevision: 1,
					EndAt: reference.MutationAtMillis, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency}
				value, storeErr := s.store.EndWithAudit(ctx, input)
				freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
				if command.batchReplayed != nil {
					*command.batchReplayed = input.Replayed || input.NoOp
				}
				return value, storeErr
			}, classMemberError)
		if mutationErr != nil {
			return nil, mutationErr
		}
		if freshMutation {
			s.publishMembershipChanged(ctx, ended.UserID, ended.ClassID)
		}
		return ended, nil
	}
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, classMemberError(err)
	}
	resource, err := s.authorizeClass(ctx, invocation, current.ClassID.String(), model.ActionClassMembersManage)
	if err != nil {
		return nil, concealMembershipAuthorizationError(err, "class_member")
	}
	if command.BatchScopeID != "" && strings.TrimSpace(command.BatchScopeID) != current.ClassID.String() {
		return nil, NewError("resource.not_found").WithField("resource", "class_member")
	}
	class, err := s.classes.Get(ctx, current.ClassID.String())
	if err != nil {
		return nil, classMemberError(err)
	}
	idempotency, err := newCommandIdempotency(invocation, "class_member.end.v1", command.IdempotencyKey, struct {
		ID      string `json:"id"`
		ScopeID string `json:"scope_id,omitempty"`
	}{id, strings.TrimSpace(command.BatchScopeID)})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	recipient, err := s.users.Get(ctx, current.UserID.String())
	if err != nil {
		return nil, classMemberError(err)
	}
	at := model.TimeFromMillis(model.MillisFromTime(s.now()))
	prepared := &preparedDirectMail{}
	if !command.batchRetainedOutcome {
		prepared, err = s.mail.PrepareClassTransition(appmail.ClassTransitionPreparation{Recipient: recipient,
			OccurrenceID: model.NewMailOccurrenceID(),
			TemplateKey:  model.MailTemplateAcademicClassEnrollmentEnded,
			Details:      appmail.ClassTransitionDetails{ClassDisplayName: class.DisplayName, StartsAt: current.StartsAt, EndsAt: at}, ActionAt: at})
		if err != nil {
			return nil, NewError("mail.unavailable").Wrap(err)
		}
	}
	freshMutation := false
	ended, mutationErr := runAuditedMutation(
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
			input := &store.ClassMemberEnd{
				ID: id, ExpectedRevision: current.Revision, ExpectedRecipientRevision: recipient.Revision,
				EndAt:        reference.MutationAtMillis,
				Notice:       &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job},
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency,
			}
			value, storeErr := s.store.EndWithAudit(ctx, input)
			freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		classMemberError,
	)
	if mutationErr != nil {
		return nil, mutationErr
	}
	if freshMutation {
		s.publishMembershipChanged(ctx, ended.UserID, ended.ClassID)
	}
	return ended, nil
}

func (s *classMemberService) publishEnrollmentChanged(ctx context.Context, result *store.ClassEnrollmentResult) {
	if result == nil || result.Membership == nil {
		return
	}
	classIDs := []model.ClassID{result.Membership.ClassID}
	if result.Previous != nil {
		classIDs = append(classIDs, result.Previous.ClassID)
	}
	s.publishMembershipChanged(ctx, result.Membership.UserID, classIDs...)
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
	if mapped := idempotencyError(err); mapped != nil {
		return mapped
	}
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
