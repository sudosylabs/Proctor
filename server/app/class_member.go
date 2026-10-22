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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type EnrollClassMemberCommand struct {
	ClassID                       model.ClassID
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
	progressionSourceAuditID      model.AuditEventID
	progressionDestinationAuditID model.AuditEventID
	onboardingImportID            model.OnboardingImportID
	onboardingImportRowNumber     int
}

type EndClassMemberCommand struct {
	ID                        model.ClassMemberID
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
	Get(context.Context, model.ClassMemberID) (*model.ClassMember, error)
	ListPageByClass(context.Context, store.ClassMemberPageOptions) (*store.ClassMemberPage, error)
	Enroll(context.Context, *store.ClassMemberEnrollment) (*store.ClassEnrollmentResult, error)
	End(context.Context, *store.ClassMemberEnd) (*model.ClassMember, error)
	ListActiveByUser(context.Context, model.UserID, int64) ([]*model.ClassMember, error)
}

type classMemberClassStore interface {
	Get(context.Context, model.ClassID) (*model.Class, error)
}

type classMemberUserStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
}

type classMemberSittingStore interface {
	ListInvalidationTargetsByClass(context.Context, model.ClassID, model.ExamSittingID, int) ([]store.ExamSittingInvalidationTarget, error)
}

type classMemberEffects interface {
	MembershipChanged(context.Context, model.UserID, []model.ClassID) error
	Report(context.Context, string, error)
}

// classMemberServiceDependencies are the collaborators of classMemberService;
// newClassMemberService validates them once and classMemberService keeps them in deps.
type classMemberServiceDependencies struct {
	Store         classMemberStore
	Classes       classMemberClassStore
	Users         classMemberUserStore
	Authorization membershipAuthorizer
	Audit         mutationAuditor
	Mail          classTransitionMailPreparer
	Effects       classMemberEffects
	Now           func() time.Time
	NewID         func() string
}

type classMemberService struct {
	deps classMemberServiceDependencies
}

func newClassMemberService(deps classMemberServiceDependencies) *classMemberService {
	return &classMemberService{deps: deps}
}

type classMemberRealtimeEffects struct {
	sittings classMemberSittingStore
	realtime *realtimeService
}

func (e classMemberRealtimeEffects) MembershipChanged(
	ctx context.Context,
	candidateID model.UserID,
	classIDs []model.ClassID,
) error {
	if e.sittings == nil || e.realtime == nil {
		return errors.New("Class Membership realtime dependencies are invalid")
	}
	candidateEvent, err := realtime.NewCandidateExamActivityChangedEvent(candidateID)
	if err != nil {
		return err
	}
	errs := []error{e.realtime.PublishRealtimeEvent(ctx, candidateEvent),
		e.realtime.InvalidateCurrentUserContext(ctx, candidateID)}
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
			targets, listErr := e.sittings.ListInvalidationTargetsByClass(ctx, classID, after, 200)
			if listErr != nil {
				errs = append(errs, listErr)
				break
			}
			for _, target := range targets {
				event, eventErr := realtime.NewManagerSittingBoardChangedEvent(target.ExamID, target.SittingID)
				if eventErr != nil {
					errs = append(errs, eventErr)
					continue
				}
				errs = append(errs, e.realtime.PublishRealtimeEvent(ctx, event))
			}
			if len(targets) < 200 {
				break
			}
			after = targets[len(targets)-1].SittingID
		}
	}
	return errors.Join(errs...)
}

func (e classMemberRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

func (e classMemberRealtimeEffects) InvalidateAuthorization(ctx context.Context, userID string) {
	e.realtime.InvalidateAuthorization(ctx, userID)
}

func (s *classMemberService) publishMembershipChanged(
	ctx context.Context,
	candidateID model.UserID,
	classIDs ...model.ClassID,
) {
	if s.deps.Effects == nil {
		return
	}
	if err := s.deps.Effects.MembershipChanged(ctx, candidateID, classIDs); err != nil {
		s.deps.Effects.Report(ctx, "class_member.membership_changed", err)
	}
}

func (s *classMemberService) EnrollClassMember(ctx context.Context, invocation invocation.Invocation, command EnrollClassMemberCommand) (*model.ClassEnrollment, error) {
	classID := command.ClassID
	resource, err := s.authorizeClass(ctx, invocation, classID, model.ActionClassMembersManage)
	if err != nil {
		return nil, err
	}
	parsedClassID := classID
	userID, err := model.ParseUserID(strings.TrimSpace(command.UserID))
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "user_id").Wrap(err)
	}
	memberID, err := model.ParseClassMemberID(s.deps.NewID())
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "class_member_id").Wrap(err)
	}
	at := model.TimeFromMillis(model.MillisFromTime(s.deps.Now()))
	var retainedPrevious *model.ClassMember
	if command.batchRetainedOutcome && command.RequireTransfer {
		previousID, parseErr := model.ParseClassMemberID(strings.TrimSpace(command.ExpectedPreviousID))
		if parseErr != nil {
			return nil, apperror.New("request.invalid").WithField("field", "expected_previous_id").Wrap(parseErr)
		}
		retainedPrevious, err = s.deps.Store.Get(ctx, previousID)
		if err != nil {
			return nil, classMemberStoreErrors.Translate(err)
		}
		if retainedPrevious.UserID != userID {
			return nil, apperror.New("resource.not_found").WithField("resource", "class_member")
		}
		if _, err = s.authorizeClass(ctx, invocation, retainedPrevious.ClassID, model.ActionClassMembersManage); err != nil {
			return nil, concealMembershipAuthorizationError(err, "class_member")
		}
		if command.batchAuthorization != nil {
			command.batchAuthorization.ClassMemberID = retainedPrevious.ID
		}
	}
	if command.batchRetainedOutcome {
		candidate := &model.ClassMember{ClassID: parsedClassID, UserID: userID, StartsAt: model.TimeFromMillis(command.StartAt), EndsAt: model.OptionalTimeFromMillis(command.EndAt)}
		candidate.PrepareCreate(memberID, at)
		idempotency, commandErr := idempotency.Prepare(invocation, "class_member.enroll", command.IdempotencyKey, struct {
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
		result, replayErr := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
			Resource: resource, Operation: "enroll", Value: candidate.Auditable()}, func() time.Time { return at },
			func(ctx context.Context, reference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
				execute := func(ctx context.Context, sourceAuditID model.AuditEventID) (*store.ClassEnrollmentResult, error) {
					input := &store.ClassMemberEnrollment{Member: candidate, ExpectedRecipientRevision: 1, StudentProgression: command.studentProgression,
						ProgressionSourceAuditEventID: command.progressionSourceAuditID, ProgressionDestinationAuditEventID: command.progressionDestinationAuditID,
						AuditEventID: reference.ID, PreviousAuditEventID: sourceAuditID, AuditAt: reference.MutationAtMillis, Command: idempotency}
					if retainedPrevious != nil {
						input.ExpectedPreviousID = retainedPrevious.ID
					}
					value, storeErr := s.deps.Store.Enroll(ctx, input)
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
				return runAuditedMutation(ctx, s.deps.Audit, sourceAttempt, func() time.Time { return at },
					func(ctx context.Context, sourceReference mutationAttemptReference) (*store.ClassEnrollmentResult, error) {
						return execute(ctx, sourceReference.ID)
					}, classMemberStoreErrors.Translate)
			}, classMemberStoreErrors.Translate)
		if replayErr != nil {
			return nil, replayErr
		}
		if freshMutation {
			s.publishEnrollmentChanged(ctx, result)
		}
		return &model.ClassEnrollment{Membership: result.Membership, Previous: result.Previous}, nil
	}
	class, err := s.deps.Classes.Get(ctx, classID)
	if err != nil {
		return nil, classMemberStoreErrors.Translate(err)
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
		active, listErr := s.deps.Store.ListActiveByUser(ctx, userID, model.MillisFromTime(candidate.StartsAt))
		if listErr != nil {
			return nil, classMemberStoreErrors.Translate(listErr)
		}
		for _, membership := range active {
			if membership != nil && membership.AcademicPeriodID == candidate.AcademicPeriodID {
				if previous != nil {
					return nil, apperror.New("class.enrollment_conflict").WithField("resource", "class_member")
				}
				previous = membership
			}
		}
		if command.IdempotencyKey != "" {
			expectedPreviousID := strings.TrimSpace(command.ExpectedPreviousID)
			switch {
			case command.RequireTransfer && (previous == nil || (previous.ID.String() != expectedPreviousID && previous.ClassID != candidate.ClassID)):
				return nil, apperror.New("class.enrollment_conflict").WithField("resource", "class_member")
			case !command.RequireTransfer && previous != nil && previous.ClassID != candidate.ClassID:
				return nil, apperror.New("class.enrollment_conflict").WithField("resource", "class_member")
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
			return nil, apperror.New("class.enrollment_conflict").WithField("resource", "class_member")
		}
		if _, err = s.authorizeClass(ctx, invocation, previous.ClassID, model.ActionClassMembersManage); err != nil {
			return nil, concealMembershipAuthorizationError(err, "class_member")
		}
		previousClass, err = s.deps.Classes.Get(ctx, previous.ClassID)
		if err != nil {
			return nil, classMemberStoreErrors.Translate(err)
		}
	}
	idempotency, err := idempotency.Prepare(invocation, "class_member.enroll", command.IdempotencyKey, struct {
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
		recipient, err = s.deps.Users.Get(ctx, userID)
		if err != nil {
			return nil, classMemberStoreErrors.Translate(err)
		}
	}
	details := mail.ClassTransitionDetails{ClassDisplayName: class.DisplayName, StartsAt: candidate.StartsAt}
	if candidate.EndsAt.Valid {
		details.EndsAt = candidate.EndsAt.Time
	}
	key := model.MailTemplateAcademicClassEnrolled
	if previous != nil {
		key = model.MailTemplateAcademicClassTransferred
		details.PreviousClassDisplayName = previousClass.DisplayName
	}
	var prepared *store.PreparedMail
	if !command.batchRetainedOutcome {
		prepared, err = s.deps.Mail.PrepareClassTransition(mail.ClassTransitionPreparation{Recipient: recipient,
			OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: key,
			Details: details, ActionAt: at})
		if err != nil {
			return nil, apperror.New("mail.unavailable").Wrap(err)
		}
	}
	destinationAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
		Resource: resource, Operation: "enroll", Value: candidate.Auditable()}
	if previous != nil {
		destinationAttempt.Operation = "transfer_in"
	}
	freshMutation := false
	result, err := runAuditedMutation(ctx, s.deps.Audit, destinationAttempt, func() time.Time { return at },
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
				value, storeErr := s.deps.Store.Enroll(ctx, input)
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
			return runAuditedMutation(ctx, s.deps.Audit, sourceAttempt,
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
					value, storeErr := s.deps.Store.Enroll(ctx, input)
					freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
					if command.batchReplayed != nil {
						*command.batchReplayed = input.Replayed || input.NoOp
					}
					return value, storeErr
				}, classMemberStoreErrors.Translate)
		},
		classMemberStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	if freshMutation {
		s.publishEnrollmentChanged(ctx, result)
	}
	return &model.ClassEnrollment{Membership: result.Membership, Previous: result.Previous}, nil
}

func (s *classMemberService) EndClassMember(ctx context.Context, invocation invocation.Invocation, command EndClassMemberCommand) (*model.ClassMember, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "class_member_id")
	}
	if err := s.deps.Authorization.AuthorizePreflight(
		ctx, invocation, model.ActionClassMembersManage, model.ResourceClass,
	); err != nil {
		return nil, err
	}
	if command.batchRetainedOutcome {
		idempotency, err := idempotency.Prepare(invocation, "class_member.end", command.IdempotencyKey, struct {
			ID      string `json:"id"`
			ScopeID string `json:"scope_id,omitempty"`
		}{id.String(), strings.TrimSpace(command.BatchScopeID)})
		if err != nil {
			return nil, err
		}
		bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
		bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
		bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
		at := model.TimeFromMillis(model.MillisFromTime(s.deps.Now()))
		freshMutation := false
		ended, mutationErr := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{Invocation: invocation, Action: model.ActionClassMembersManage,
			Resource: model.Resource{Type: model.ResourceClass, ID: strings.TrimSpace(command.BatchScopeID)}, Operation: "end"},
			func() time.Time { return at }, func(ctx context.Context, reference mutationAttemptReference) (*model.ClassMember, error) {
				input := &store.ClassMemberEnd{ID: id, ExpectedRevision: 1, ExpectedRecipientRevision: 1,
					EndAt: reference.MutationAtMillis, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency}
				value, storeErr := s.deps.Store.End(ctx, input)
				freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
				if command.batchReplayed != nil {
					*command.batchReplayed = input.Replayed || input.NoOp
				}
				return value, storeErr
			}, classMemberStoreErrors.Translate)
		if mutationErr != nil {
			return nil, mutationErr
		}
		if freshMutation {
			s.publishMembershipChanged(ctx, ended.UserID, ended.ClassID)
		}
		return ended, nil
	}
	current, err := s.deps.Store.Get(ctx, id)
	if err != nil {
		return nil, classMemberStoreErrors.Translate(err)
	}
	resource, err := s.authorizeClass(ctx, invocation, current.ClassID, model.ActionClassMembersManage)
	if err != nil {
		return nil, concealMembershipAuthorizationError(err, "class_member")
	}
	if command.BatchScopeID != "" && strings.TrimSpace(command.BatchScopeID) != current.ClassID.String() {
		return nil, apperror.New("resource.not_found").WithField("resource", "class_member")
	}
	class, err := s.deps.Classes.Get(ctx, current.ClassID)
	if err != nil {
		return nil, classMemberStoreErrors.Translate(err)
	}
	idempotency, err := idempotency.Prepare(invocation, "class_member.end", command.IdempotencyKey, struct {
		ID      string `json:"id"`
		ScopeID string `json:"scope_id,omitempty"`
	}{id.String(), strings.TrimSpace(command.BatchScopeID)})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	recipient, err := s.deps.Users.Get(ctx, current.UserID)
	if err != nil {
		return nil, classMemberStoreErrors.Translate(err)
	}
	at := model.TimeFromMillis(model.MillisFromTime(s.deps.Now()))
	prepared := &store.PreparedMail{}
	if !command.batchRetainedOutcome {
		prepared, err = s.deps.Mail.PrepareClassTransition(mail.ClassTransitionPreparation{Recipient: recipient,
			OccurrenceID: model.NewMailOccurrenceID(),
			TemplateKey:  model.MailTemplateAcademicClassEnrollmentEnded,
			Details:      mail.ClassTransitionDetails{ClassDisplayName: class.DisplayName, StartsAt: current.StartsAt, EndsAt: at}, ActionAt: at})
		if err != nil {
			return nil, apperror.New("mail.unavailable").Wrap(err)
		}
	}
	freshMutation := false
	ended, mutationErr := runAuditedMutation(
		ctx,
		s.deps.Audit,
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
			value, storeErr := s.deps.Store.End(ctx, input)
			freshMutation = storeErr == nil && !input.Replayed && !input.NoOp
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		classMemberStoreErrors.Translate,
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

func (s *classMemberService) authorizeClass(ctx context.Context, invocation invocation.Invocation, classID model.ClassID, action model.Action) (model.Resource, error) {
	if !classID.IsValid() {
		return model.Resource{}, apperror.New("request.invalid").WithField("field", "class_id")
	}
	resource := model.Resource{Type: model.ResourceClass, ID: classID.String()}
	if err := s.deps.Authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

var classMemberStoreErrors = apperror.AdministrationStoreTable("class_member", "class.enrollment_conflict", "class_member.invalid", map[string]apperror.Code{
	"class_member_student_affiliation_required": "class_member.student_affiliation_required",
	"class_member_end_time":                     "resource.not_found",
})

// ListClassMembersPageQuery opts into bounded membership enumeration. Each page is
// authorized independently and explicit selectors preserve its effective filter.
type ListClassMembersPageQuery struct {
	AfterUserID model.UserID
	AfterID     model.ClassMemberID
	ClassID     model.ClassID
	MembershipPageQuery
}

type ClassMemberPage struct {
	Members  []*model.ClassMember
	HasMore  bool
	ActiveAt int64
}

func (s *classMemberService) ListClassMembersPage(ctx context.Context, invocation invocation.Invocation, query ListClassMembersPageQuery) (*ClassMemberPage, error) {
	resource, err := s.authorizeClass(ctx, invocation, query.ClassID, model.ActionClassMembersView)
	if err != nil {
		return nil, err
	}
	activeAt, limit, err := resolveMembershipPage(query.MembershipPageQuery, s.deps.Now())
	if err != nil {
		return nil, err
	}
	scopeID, err := model.ParseClassID(resource.ID)
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "class_id")
	}
	if query.AfterID.IsZero() != query.AfterUserID.IsZero() || (!query.AfterID.IsZero() && (!query.AfterID.IsValid() || !query.AfterUserID.IsValid())) {
		return nil, apperror.New("request.invalid").WithField("field", "cursor")
	}
	options := store.ClassMemberPageOptions{ClassID: scopeID, ActiveAt: activeAt, Limit: limit, AfterID: query.AfterID, AfterUserID: query.AfterUserID}

	page, err := s.deps.Store.ListPageByClass(ctx, options)
	if err != nil {
		return nil, classMemberStoreErrors.Translate(err)
	}
	result := &ClassMemberPage{Members: page.Members, ActiveAt: activeAt, HasMore: page.HasMore}
	if result.Members == nil {
		result.Members = []*model.ClassMember{}
	}
	return result, nil
}
