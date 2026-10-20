// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sitting

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ScheduleCommand struct {
	ExamID           model.ExamID
	ExamRevisionID   model.ExamRevisionID
	ClassID          model.ClassID
	ScheduledStartAt time.Time
	ScheduledEndAt   time.Time
	IdempotencyKey   string
}

type UpdateScheduleCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	ExamRevisionID   *model.ExamRevisionID
	ClassID          *model.ClassID
	ScheduledStartAt *time.Time
	ScheduledEndAt   *time.Time
	IdempotencyKey   string
}

type CancelCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	PrivateReason    string
	IdempotencyKey   string
}

type PauseCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	PrivateReason    string
	IdempotencyKey   string
}

type ResumeCommand = PauseCommand

type EarlyCloseCommand = PauseCommand

type ExtendCommand struct {
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	ExpectedRevision int64
	ScheduledEndAt   time.Time
	PrivateReason    string
	IdempotencyKey   string
}

type ListQuery struct {
	ExamID                 model.ExamID
	ClassID                model.ClassID
	States                 []model.ExamSittingState
	OverlapStartAt         time.Time
	OverlapEndAt           time.Time
	BeforeScheduledStartAt time.Time
	BeforeSittingID        model.ExamSittingID
	Limit                  int
}

type Page struct {
	Items   []store.ExamSittingSnapshot
	HasMore bool
}

type accessStore interface {
	Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error)
}

type Authorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
	Deny(context.Context, invocation.Invocation, model.Action, model.Resource, model.AcademicUnitID) error
}

type Auditor interface {
	Begin(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
}

// SystemAuditor owns audit creation for trusted durable work. It deliberately
// accepts invocation.System rather than an Invocation so Jobs cannot impersonate a User.
type SystemAuditor interface {
	Begin(context.Context, invocation.System, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
}

// LifecycleJobFactory constructs validated durable intents. Persistence owns
// inserting them atomically with the corresponding Sitting mutation.
type LifecycleJobFactory interface {
	BoundaryJobs(model.ExamSittingID, int64, time.Time, time.Time) (*model.Job, *model.Job, error)
	DeadlineJob(model.ExamSittingID, int64, time.Time) (*model.Job, error)
	FinalizeJob(model.ExamSittingID, int64, time.Time) (*model.Job, error)
}

// ScheduleMailPreparer owns the bounded, presentation-ready fan-out intent.
// The Sitting service supplies only safe schedule facts; it cannot select
// recipients, delivery work, ciphertext, or fan-out deadlines.
type ScheduleMailPreparer interface {
	Prepare(context.Context, ScheduleMailRequest) (*store.ExamSittingMailFanout, error)
}

type ScheduleMailRequest struct {
	ActorUserID     model.UserID
	ExamID          model.ExamID
	ExamRevisionID  model.ExamRevisionID
	SittingID       model.ExamSittingID
	SittingRevision int64
	ClassID         model.ClassID
	PriorClassID    model.ClassID
	StartsAt        time.Time
	EndsAt          time.Time
	ChangeKind      store.ExamSittingMailChangeKind
}

type Effects interface {
	Scheduled(context.Context, model.ExamID, model.ExamSittingID, model.ExamSittingState, int64, time.Time) error
	ScheduleUpdated(context.Context, model.ExamID, model.ExamSittingID, model.ExamSittingState, int64, time.Time) error
	Canceled(context.Context, model.ExamID, model.ExamSittingID, model.ExamSittingState, int64, time.Time) error
	LifecycleChanged(context.Context, model.ExamID, model.ExamSittingID, model.ExamSittingState, int64,
		store.ExamSittingLifecycleTransitionCode, time.Time, time.Time) error
}

type EffectFailures interface {
	Report(context.Context, string, error)
}

type Service struct {
	persistence store.ExamSittingStore
	access      accessStore
	memberships manageraccess.Memberships
	authorizer  Authorizer
	auditor     Auditor
	systemAudit SystemAuditor
	effects     Effects
	failures    EffectFailures
	jobs        LifecycleJobFactory
	mail        ScheduleMailPreparer
	now         func() time.Time
	newID       func() model.ExamSittingID
}

func New(persistence store.ExamSittingStore, access accessStore, memberships manageraccess.Memberships, authorizer Authorizer,
	auditor Auditor, systemAudit SystemAuditor, effects Effects, failures EffectFailures, jobs LifecycleJobFactory,
	mail ScheduleMailPreparer, now func() time.Time, newID func() model.ExamSittingID,
) (*Service, error) {
	if persistence == nil || access == nil || memberships == nil || authorizer == nil || auditor == nil || systemAudit == nil || effects == nil ||
		failures == nil || jobs == nil || mail == nil || now == nil || newID == nil {
		return nil, errors.New("Exam Sitting dependencies are required")
	}
	return &Service{persistence: persistence, access: access, memberships: memberships, authorizer: authorizer,
		auditor: auditor, systemAudit: systemAudit, effects: effects, failures: failures, jobs: jobs, mail: mail, now: now, newID: newID}, nil
}

func (service *Service) Schedule(ctx context.Context, invocation invocation.Invocation, command ScheduleCommand) (store.ExamSittingSnapshot, error) {
	command.ScheduledStartAt = model.TimeUTC(command.ScheduledStartAt)
	command.ScheduledEndAt = model.TimeUTC(command.ScheduledEndAt)
	if !command.ExamID.IsValid() || !command.ExamRevisionID.IsValid() || !command.ClassID.IsValid() ||
		command.ScheduledStartAt.IsZero() || !command.ScheduledStartAt.Before(command.ScheduledEndAt) {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "schedule")
	}
	idempotency, err := prepareScheduleIdempotency(invocation, command)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "principal")
	}
	at := model.TimeUTC(service.now())
	authorization, err := service.authorizeExam(ctx, invocation, command.ExamID, at, model.ActionExamSittingCreate, model.ActionExamSittingCreateOverride)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	sittingID := service.newID()
	sitting, err := model.NewExamSitting(sittingID, command.ExamID, command.ExamRevisionID, command.ClassID,
		command.ScheduledStartAt, command.ScheduledEndAt, at)
	if err != nil {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "schedule").Wrap(err)
	}
	openJob, deadlineJob, err := service.jobs.BoundaryJobs(sittingID, sitting.Revision, sitting.ScheduledStartAt, sitting.ScheduledEndAt)
	if err != nil || openJob == nil || deadlineJob == nil {
		return store.ExamSittingSnapshot{}, jobFactoryUnavailable("construct Exam Sitting boundary Jobs", err)
	}
	mail, err := service.mail.Prepare(ctx, ScheduleMailRequest{ActorUserID: principal.UserID, ExamID: command.ExamID,
		ExamRevisionID: command.ExamRevisionID, SittingID: sittingID, SittingRevision: sitting.Revision, ClassID: command.ClassID,
		StartsAt: sitting.ScheduledStartAt, EndsAt: sitting.ScheduledEndAt, ChangeKind: store.ExamSittingMailScheduled})
	if err != nil || mail == nil {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.Join(errors.New("prepare Sitting schedule mail"), err))
	}
	auditValue := map[string]any{"exam_id": command.ExamID.String(), "exam_sitting_id": sittingID.String(),
		"exam_revision_id": command.ExamRevisionID.String(), "class_id": command.ClassID.String()}
	auditID, err := service.auditor.Begin(ctx, invocation, authorization.action, model.Resource{Type: model.ResourceExam, ID: command.ExamID.String()},
		model.RoleScopeAcademicUnit, authorization.unitID.String(), "schedule", auditValue, nil)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	result, err := service.persistence.Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: principal.UserID,
		ManagerOverride: authorization.override, AuditEventID: auditID, AuditAt: model.MillisFromTime(at), Mail: mail}, idempotency)
	if err != nil {
		return store.ExamSittingSnapshot{}, service.failAudit(ctx, auditID, err)
	}
	value, err := requireScheduledCommandResult(result, command.ExamID, sittingID)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if !result.Replayed {
		if effectErr := service.effects.Scheduled(ctx, value.Sitting.ExamID, value.Sitting.ID, value.Sitting.State, value.Sitting.Revision, at); effectErr != nil {
			service.failures.Report(ctx, "exam_sitting_scheduled", effectErr)
		}
	}
	return value, nil
}

func (service *Service) Get(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, sittingID model.ExamSittingID) (store.ExamSittingSnapshot, error) {
	if !examID.IsValid() || !sittingID.IsValid() {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "identity")
	}
	at := model.TimeUTC(service.now())
	if _, err := service.authorize(ctx, invocation, examID, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, at,
		model.ActionExamSittingView, model.ActionExamSittingViewOverride); err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	snapshot, err := service.persistence.Get(ctx, examID, sittingID)
	if err != nil {
		return store.ExamSittingSnapshot{}, storeErrors.Translate(err)
	}
	value, err := requireSnapshot(snapshot)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if value.Sitting.ExamID != examID || value.Sitting.ID != sittingID {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched snapshot"))
	}
	return value, nil
}

// AuthorizeView rechecks the current relationship and permission used by an
// exact read without returning Sitting state. Realtime subscriptions use this
// gate before accepting or retaining a resource-scoped subscription.
func (service *Service) AuthorizeView(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID) error {
	if !sittingID.IsValid() {
		return apperror.Invalid("exam.sitting.invalid", "exam_sitting_id")
	}
	if invocation.Principal().Validate() != nil {
		return apperror.Invalid("exam.sitting.invalid", "principal")
	}
	snapshot, err := service.persistence.Resolve(ctx, sittingID)
	if err != nil {
		return storeErrors.Translate(err)
	}
	value, err := requireSnapshot(snapshot)
	if err != nil {
		return err
	}
	if value.Sitting.ID != sittingID {
		return apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched snapshot"))
	}
	_, err = service.authorize(ctx, invocation, value.Sitting.ExamID,
		model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, model.TimeUTC(service.now()),
		model.ActionExamSittingView, model.ActionExamSittingViewOverride)
	return err
}

// AuthorizeBrowserActivityView requires exact current Exam Manager membership
// plus the dedicated permission on every read, including later pages.
// Browser history has no administrator or general examination override.
func (service *Service) AuthorizeBrowserActivityView(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID) (model.AcademicUnitID, error) {
	if !sittingID.IsValid() || invocation.Principal().Validate() != nil {
		return "", apperror.Invalid("exam.sitting.invalid", "exam_sitting_id")
	}
	snapshot, err := service.persistence.Resolve(ctx, sittingID)
	if err != nil {
		return "", storeErrors.Translate(err)
	}
	value, err := requireSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	if value.Sitting.ID != sittingID {
		return "", apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched snapshot"))
	}
	access, err := service.access.Access(ctx, value.Sitting.ExamID, invocation.Principal().UserID)
	if err != nil {
		return "", storeErrors.Translate(err)
	}
	if access == nil || access.Exam == nil || access.Exam.Validate() != nil || access.Exam.ID != value.Sitting.ExamID {
		return "", apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam access projection is incomplete"))
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}
	if !access.ActorIsManager {
		if err := service.authorizer.Deny(ctx, invocation, model.ActionExamAttemptBrowserActivityView, resource, access.Exam.AcademicUnitID); err != nil {
			return "", err
		}
		return "", apperror.New("exam.sitting.not_found")
	}
	if err := service.authorizer.Authorize(ctx, invocation, model.ActionExamAttemptBrowserActivityView, resource); err != nil {
		return "", err
	}
	return access.Exam.AcademicUnitID, nil
}

// AuthorizeSubmissionView applies the current Exam Manager relationship and
// exact-unit membership decision to one immutable Submission resource. The
// caller resolves the Submission's owning Exam through the bounded ownership
// projection before invoking this gate; authorization independently resolves
// the canonical Submission resource for its scoped, durable decision audit.
func (service *Service) AuthorizeSubmissionView(ctx context.Context, invocation invocation.Invocation, examID model.ExamID,
	submissionID model.SubmissionID,
) error {
	_, err := service.authorizeSubmission(ctx, invocation, examID, submissionID,
		model.ActionSubmissionView, model.ActionSubmissionViewOverride)
	return err
}

// AuthorizeSubmissionReview applies the current Exam Manager relationship and
// exact-unit membership decision for draft decisions and finalization.
func (service *Service) AuthorizeSubmissionReview(ctx context.Context, invocation invocation.Invocation, examID model.ExamID,
	submissionID model.SubmissionID,
) (bool, error) {
	return service.authorizeSubmission(ctx, invocation, examID, submissionID,
		model.ActionSubmissionReview, model.ActionSubmissionReviewOverride)
}

// AuthorizeSubmissionRelease keeps result release as a distinct, explicit
// permission from evidence review and finalization.
func (service *Service) AuthorizeSubmissionRelease(ctx context.Context, invocation invocation.Invocation, examID model.ExamID,
	submissionID model.SubmissionID,
) (bool, error) {
	return service.authorizeSubmission(ctx, invocation, examID, submissionID,
		model.ActionSubmissionRelease, model.ActionSubmissionReleaseOverride)
}

func (service *Service) authorizeSubmission(ctx context.Context, invocation invocation.Invocation, examID model.ExamID,
	submissionID model.SubmissionID, action, overrideAction model.Action,
) (bool, error) {
	if !examID.IsValid() || !submissionID.IsValid() {
		return false, apperror.Invalid("exam.sitting.invalid", "submission_id")
	}
	if invocation.Principal().Validate() != nil {
		return false, apperror.Invalid("exam.sitting.invalid", "principal")
	}
	decision, err := service.authorize(ctx, invocation, examID,
		model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, model.TimeUTC(service.now()),
		action, overrideAction)
	if err != nil {
		return false, err
	}
	return decision.override, nil
}

// AuthorizeManage rechecks the current manager relationship for a Sitting and
// reports whether the caller used the explicit management override action.
// It returns no mutable Sitting state and performs no mutation.
func (service *Service) AuthorizeManage(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID) (bool, error) {
	if !sittingID.IsValid() {
		return false, apperror.Invalid("exam.sitting.invalid", "exam_sitting_id")
	}
	if invocation.Principal().Validate() != nil {
		return false, apperror.Invalid("exam.sitting.invalid", "principal")
	}
	snapshot, err := service.persistence.Resolve(ctx, sittingID)
	if err != nil {
		return false, storeErrors.Translate(err)
	}
	value, err := requireSnapshot(snapshot)
	if err != nil {
		return false, err
	}
	if value.Sitting.ID != sittingID {
		return false, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched snapshot"))
	}
	decision, err := service.authorize(ctx, invocation, value.Sitting.ExamID,
		model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, model.TimeUTC(service.now()),
		model.ActionExamSittingManage, model.ActionExamSittingManageOverride)
	if err != nil {
		return false, err
	}
	return decision.override, nil
}

func (service *Service) List(ctx context.Context, invocation invocation.Invocation, query ListQuery) (Page, error) {
	options, err := listOptions(query)
	if err != nil {
		return Page{}, err
	}
	at := model.TimeUTC(service.now())
	if _, err = service.authorizeExam(ctx, invocation, query.ExamID, at, model.ActionExamView, model.ActionExamViewOverride); err != nil {
		return Page{}, err
	}
	options.Limit++
	items, err := service.persistence.List(ctx, options)
	if err != nil {
		return Page{}, storeErrors.Translate(err)
	}
	for index := range items {
		if items[index].Sitting == nil || items[index].Sitting.ExamID != query.ExamID || items[index].Sitting.Validate() != nil {
			return Page{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an invalid list item"))
		}
	}
	page := Page{Items: items, HasMore: len(items) > query.Limit}
	if page.HasMore {
		page.Items = page.Items[:query.Limit]
	}
	if page.Items == nil {
		page.Items = []store.ExamSittingSnapshot{}
	}
	return page, nil
}

func (service *Service) UpdateSchedule(ctx context.Context, invocation invocation.Invocation, command UpdateScheduleCommand) (store.ExamSittingSnapshot, error) {
	command.ScheduledStartAt = canonicalTimePointer(command.ScheduledStartAt)
	command.ScheduledEndAt = canonicalTimePointer(command.ScheduledEndAt)
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || command.ExpectedRevision < 1 ||
		(command.ExamRevisionID == nil && command.ClassID == nil && command.ScheduledStartAt == nil && command.ScheduledEndAt == nil) ||
		command.ExamRevisionID != nil && !command.ExamRevisionID.IsValid() || command.ClassID != nil && !command.ClassID.IsValid() ||
		command.ScheduledStartAt != nil && command.ScheduledStartAt.IsZero() || command.ScheduledEndAt != nil && command.ScheduledEndAt.IsZero() {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "schedule")
	}
	idempotency, err := prepareScheduleUpdateIdempotency(invocation, command)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}
	at := model.TimeUTC(service.now())
	authorization, err := service.authorize(ctx, invocation, command.ExamID, resource, at,
		model.ActionExamSittingManage, model.ActionExamSittingManageOverride)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	current, err := service.persistence.Get(ctx, command.ExamID, command.SittingID)
	if err != nil {
		return store.ExamSittingSnapshot{}, storeErrors.Translate(err)
	}
	snapshot, err := requireSnapshot(current)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if snapshot.Sitting.ExamID != command.ExamID || snapshot.Sitting.ID != command.SittingID {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched snapshot"))
	}
	revisionID, classID := snapshot.Sitting.ExamRevisionID, snapshot.Sitting.ClassID
	startAt, endAt := snapshot.Sitting.ScheduledStartAt, snapshot.Sitting.ScheduledEndAt
	if command.ExamRevisionID != nil {
		revisionID = *command.ExamRevisionID
	}
	if command.ClassID != nil {
		classID = *command.ClassID
	}
	if command.ScheduledStartAt != nil {
		startAt = model.TimeUTC(*command.ScheduledStartAt)
	}
	if command.ScheduledEndAt != nil {
		endAt = model.TimeUTC(*command.ScheduledEndAt)
	}
	if !startAt.Before(endAt) {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "schedule")
	}
	noChanges := revisionID == snapshot.Sitting.ExamRevisionID && classID == snapshot.Sitting.ClassID &&
		startAt.Equal(snapshot.Sitting.ScheduledStartAt) && endAt.Equal(snapshot.Sitting.ScheduledEndAt)
	if noChanges && snapshot.Sitting.Revision == command.ExpectedRevision && snapshot.Sitting.State == model.ExamSittingScheduled && !authorization.examArchived {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.no_changes")
	}
	openJob, deadlineJob, err := service.jobs.BoundaryJobs(command.SittingID, command.ExpectedRevision+1, startAt, endAt)
	if err != nil || openJob == nil || deadlineJob == nil {
		return store.ExamSittingSnapshot{}, jobFactoryUnavailable("construct Exam Sitting boundary Jobs", err)
	}
	mail, err := service.mail.Prepare(ctx, ScheduleMailRequest{ActorUserID: invocation.Principal().UserID, ExamID: command.ExamID,
		ExamRevisionID: revisionID, SittingID: command.SittingID, SittingRevision: command.ExpectedRevision + 1,
		ClassID: classID, PriorClassID: snapshot.Sitting.ClassID, StartsAt: startAt, EndsAt: endAt,
		ChangeKind: store.ExamSittingMailRescheduled})
	if err != nil || mail == nil {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.Join(errors.New("prepare Sitting reschedule mail"), err))
	}
	auditValue := map[string]any{"exam_id": command.ExamID.String(), "exam_sitting_id": command.SittingID.String(),
		"exam_revision_id": revisionID.String(), "class_id": classID.String(),
		"expected_sitting_revision": command.ExpectedRevision}
	auditID, err := service.auditor.Begin(ctx, invocation, authorization.action, resource, model.RoleScopeAcademicUnit,
		authorization.unitID.String(), "update_schedule", auditValue, nil)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	result, err := service.persistence.UpdateSchedule(ctx, &store.ExamSittingScheduleUpdate{
		ExamID: command.ExamID, SittingID: command.SittingID, ActorUserID: invocation.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedRevision: command.ExpectedRevision,
		ExamRevisionID: revisionID, ClassID: classID, ScheduledStartAt: startAt, ScheduledEndAt: endAt,
		OpenJob: openJob, DeadlineJob: deadlineJob,
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at), Mail: mail,
	}, idempotency)
	if err != nil {
		return store.ExamSittingSnapshot{}, service.failAudit(ctx, auditID, err)
	}
	value, err := requireOwnedCommandResult(result, command.ExamID, command.SittingID)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if !result.Replayed {
		if effectErr := service.effects.ScheduleUpdated(ctx, value.Sitting.ExamID, value.Sitting.ID, value.Sitting.State, value.Sitting.Revision, at); effectErr != nil {
			service.failures.Report(ctx, "exam_sitting_schedule_updated", effectErr)
		}
	}
	return value, nil
}

func (service *Service) Cancel(ctx context.Context, invocation invocation.Invocation, command CancelCommand) (store.ExamSittingSnapshot, error) {
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || command.ExpectedRevision < 1 || !validPrivateReason(command.PrivateReason) {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "cancellation")
	}
	idempotency, err := prepareTransitionIdempotency(invocation, idempotencyOperationCancel, PauseCommand(command))
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}
	at := model.TimeUTC(service.now())
	authorization, err := service.authorize(ctx, invocation, command.ExamID, resource, at,
		model.ActionExamSittingManage, model.ActionExamSittingManageOverride)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	current, err := service.persistence.Get(ctx, command.ExamID, command.SittingID)
	if err != nil {
		return store.ExamSittingSnapshot{}, storeErrors.Translate(err)
	}
	snapshot, err := requireSnapshot(current)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	mail, err := service.mail.Prepare(ctx, ScheduleMailRequest{ActorUserID: invocation.Principal().UserID, ExamID: command.ExamID,
		ExamRevisionID: snapshot.Sitting.ExamRevisionID, SittingID: command.SittingID, SittingRevision: command.ExpectedRevision + 1,
		ClassID: snapshot.Sitting.ClassID, PriorClassID: snapshot.Sitting.ClassID, StartsAt: snapshot.Sitting.ScheduledStartAt,
		EndsAt: snapshot.Sitting.ScheduledEndAt, ChangeKind: store.ExamSittingMailCancelled})
	if err != nil || mail == nil {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.Join(errors.New("prepare Sitting cancellation mail"), err))
	}
	auditValue := map[string]any{"exam_id": command.ExamID.String(), "exam_sitting_id": command.SittingID.String(),
		"expected_sitting_revision": command.ExpectedRevision, "reason_code": string(model.ExamSittingReasonManagerCanceled)}
	auditID, err := service.auditor.Begin(ctx, invocation, authorization.action, resource, model.RoleScopeAcademicUnit,
		authorization.unitID.String(), "cancel", auditValue, nil)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	result, err := service.persistence.Cancel(ctx, &store.ExamSittingCancellation{
		ExamID: command.ExamID, SittingID: command.SittingID, ActorUserID: invocation.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedRevision: command.ExpectedRevision,
		PrivateReason: command.PrivateReason, AuditEventID: auditID, AuditAt: model.MillisFromTime(at), Mail: mail,
	}, idempotency)
	if err != nil {
		return store.ExamSittingSnapshot{}, service.failAudit(ctx, auditID, err)
	}
	value, err := requireOwnedCommandResult(result, command.ExamID, command.SittingID)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if !result.Replayed {
		if effectErr := service.effects.Canceled(ctx, value.Sitting.ExamID, value.Sitting.ID, value.Sitting.State, value.Sitting.Revision, at); effectErr != nil {
			service.failures.Report(ctx, "exam_sitting_canceled", effectErr)
		}
	}
	return value, nil
}

func (service *Service) Pause(ctx context.Context, invocation invocation.Invocation, command PauseCommand) (store.ExamSittingSnapshot, error) {
	return service.runManagerTransition(ctx, invocation, command, "pause", idempotencyOperationPause, store.ExamSittingTransitionManagerPaused, true, false,
		func(ctx context.Context, input *store.ExamSittingManagerTransition, idempotency *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
			return service.persistence.Pause(ctx, input, idempotency)
		})
}

func (service *Service) Resume(ctx context.Context, invocation invocation.Invocation, command ResumeCommand) (store.ExamSittingSnapshot, error) {
	return service.runManagerTransition(ctx, invocation, command, "resume", idempotencyOperationResume, store.ExamSittingTransitionManagerResumed, false, false,
		func(ctx context.Context, input *store.ExamSittingManagerTransition, idempotency *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
			return service.persistence.Resume(ctx, input, idempotency)
		})
}

func (service *Service) EarlyClose(ctx context.Context, invocation invocation.Invocation, command EarlyCloseCommand) (store.ExamSittingSnapshot, error) {
	return service.runManagerTransition(ctx, invocation, command, "early_close", idempotencyOperationClose, store.ExamSittingTransitionManagerClosed, true, true,
		func(ctx context.Context, input *store.ExamSittingManagerTransition, idempotency *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
			return service.persistence.EarlyClose(ctx, input, idempotency)
		})
}

type managerTransitionStoreCall func(context.Context, *store.ExamSittingManagerTransition, *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error)

func (service *Service) runManagerTransition(ctx context.Context, invocation invocation.Invocation, command PauseCommand, operation, idempotencyOperation string,
	transition store.ExamSittingLifecycleTransitionCode, allowArchived, finalize bool, run managerTransitionStoreCall,
) (store.ExamSittingSnapshot, error) {
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || command.ExpectedRevision < 1 || !validPrivateReason(command.PrivateReason) {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", operation)
	}
	idempotency, err := prepareTransitionIdempotency(invocation, idempotencyOperation, command)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}
	at := model.TimeUTC(service.now())
	authorization, err := service.authorize(ctx, invocation, command.ExamID, resource, at,
		model.ActionExamSittingManage, model.ActionExamSittingManageOverride)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if authorization.examArchived && !allowArchived {
		return store.ExamSittingSnapshot{}, apperror.New("exam.archived")
	}
	var finalizeJob *model.Job
	if finalize {
		finalizeJob, err = service.jobs.FinalizeJob(command.SittingID, command.ExpectedRevision+1, at)
		if err != nil || finalizeJob == nil {
			return store.ExamSittingSnapshot{}, jobFactoryUnavailable("construct Exam Sitting finalize Job", err)
		}
	}
	auditValue := managerLifecycleAuditValue(command.ExamID, command.SittingID, command.ExpectedRevision, transition)
	auditID, err := service.auditor.Begin(ctx, invocation, authorization.action, resource, model.RoleScopeAcademicUnit,
		authorization.unitID.String(), operation, auditValue, nil)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	input := &store.ExamSittingManagerTransition{ExamID: command.ExamID, SittingID: command.SittingID,
		ActorUserID: invocation.Principal().UserID, ManagerOverride: authorization.override, ExpectedRevision: command.ExpectedRevision,
		PrivateReason: command.PrivateReason, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at),
		FinalizeJob: finalizeJob}
	result, err := run(ctx, input, idempotency)
	if err != nil {
		return store.ExamSittingSnapshot{}, service.failAudit(ctx, auditID, err)
	}
	value, err := requireManagerLifecycleResult(result, command.ExamID, command.SittingID, transition)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	service.publishLifecycle(ctx, result, operation)
	return value, nil
}

func (service *Service) Extend(ctx context.Context, invocation invocation.Invocation, command ExtendCommand) (store.ExamSittingSnapshot, error) {
	command.ScheduledEndAt = model.TimeUTC(command.ScheduledEndAt)
	if !command.ExamID.IsValid() || !command.SittingID.IsValid() || command.ExpectedRevision < 1 || command.ScheduledEndAt.IsZero() ||
		!validPrivateReason(command.PrivateReason) {
		return store.ExamSittingSnapshot{}, apperror.Invalid("exam.sitting.invalid", "extend")
	}
	idempotency, err := prepareExtensionIdempotency(invocation, command)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}
	at := model.TimeUTC(service.now())
	authorization, err := service.authorize(ctx, invocation, command.ExamID, resource, at,
		model.ActionExamSittingManage, model.ActionExamSittingManageOverride)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if authorization.examArchived {
		return store.ExamSittingSnapshot{}, apperror.New("exam.archived")
	}
	deadline := model.TimeUTC(command.ScheduledEndAt)
	deadlineJob, err := service.jobs.DeadlineJob(command.SittingID, command.ExpectedRevision+1, deadline)
	if err != nil || deadlineJob == nil {
		return store.ExamSittingSnapshot{}, jobFactoryUnavailable("construct Exam Sitting deadline Job", err)
	}
	auditValue := managerLifecycleAuditValue(command.ExamID, command.SittingID, command.ExpectedRevision, store.ExamSittingTransitionManagerExtended)
	auditID, err := service.auditor.Begin(ctx, invocation, authorization.action, resource, model.RoleScopeAcademicUnit,
		authorization.unitID.String(), "extend", auditValue, nil)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	result, err := service.persistence.Extend(ctx, &store.ExamSittingExtension{ExamID: command.ExamID, SittingID: command.SittingID,
		ActorUserID: invocation.Principal().UserID, ManagerOverride: authorization.override, ExpectedRevision: command.ExpectedRevision,
		ScheduledEndAt: deadline, DeadlineJob: deadlineJob, PrivateReason: command.PrivateReason, ChangedAt: at,
		AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return store.ExamSittingSnapshot{}, service.failAudit(ctx, auditID, err)
	}
	value, err := requireManagerLifecycleResult(result, command.ExamID, command.SittingID, store.ExamSittingTransitionManagerExtended)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	service.publishLifecycle(ctx, result, "extend")
	return value, nil
}

func managerLifecycleAuditValue(examID model.ExamID, sittingID model.ExamSittingID, expectedRevision int64,
	transition store.ExamSittingLifecycleTransitionCode,
) map[string]any {
	return map[string]any{"exam_id": examID.String(), "exam_sitting_id": sittingID.String(),
		"expected_sitting_revision": expectedRevision, "transition": string(transition)}
}

func (service *Service) AdvanceDue(ctx context.Context, system invocation.System, sittingID model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	return service.runSystemLifecycle(ctx, system, sittingID, "advance_due", true,
		func(ctx context.Context, input systemLifecycleInput) (*store.ExamSittingLifecycleResult, error) {
			return service.persistence.AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: input.sittingID,
				AuditEventID: input.auditID, AuditAt: input.auditAt, FinalizeJob: input.finalizeJob})
		})
}

func (service *Service) FinishSealing(ctx context.Context, system invocation.System, sittingID model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	return service.runSystemLifecycle(ctx, system, sittingID, "finish_sealing", false,
		func(ctx context.Context, input systemLifecycleInput) (*store.ExamSittingLifecycleResult, error) {
			return service.persistence.FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: input.sittingID,
				AuditEventID: input.auditID, AuditAt: input.auditAt})
		})
}

type NoShowPage struct {
	Items   []store.ExamSittingNoShow
	HasMore bool
}

func (service *Service) ListNoShows(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, sittingID model.ExamSittingID,
	after model.UserID, limit int,
) (NoShowPage, error) {
	if !examID.IsValid() || !sittingID.IsValid() || (!after.IsZero() && !after.IsValid()) || limit < 1 || limit > 200 {
		return NoShowPage{}, apperror.Invalid("exam.sitting.invalid", "no_show_list")
	}
	snapshot, err := service.Get(ctx, invocation, examID, sittingID)
	if err != nil {
		return NoShowPage{}, err
	}
	if snapshot.Sitting == nil || (snapshot.Sitting.State != model.ExamSittingClosing && snapshot.Sitting.State != model.ExamSittingClosed) {
		return NoShowPage{}, apperror.New("exam.sitting.state_conflict")
	}
	items, err := service.persistence.ListNoShows(ctx, store.ExamSittingNoShowListOptions{
		SittingID: sittingID, AfterCandidateUserID: after, Limit: limit + 1,
	})
	if err != nil {
		return NoShowPage{}, storeErrors.Translate(err)
	}
	if len(items) > limit+1 {
		return NoShowPage{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an oversized no-show page"))
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	previous := after
	for _, item := range items {
		if !item.CandidateUserID.IsValid() || (!previous.IsZero() && item.CandidateUserID.String() <= previous.String()) {
			return NoShowPage{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an invalid no-show page"))
		}
		previous = item.CandidateUserID
	}
	if items == nil {
		items = []store.ExamSittingNoShow{}
	}
	return NoShowPage{Items: items, HasMore: hasMore}, nil
}

type systemLifecycleInput struct {
	sittingID   model.ExamSittingID
	auditID     model.AuditEventID
	auditAt     int64
	finalizeJob *model.Job
}

type systemLifecycleStoreCall func(context.Context, systemLifecycleInput) (*store.ExamSittingLifecycleResult, error)

func (service *Service) runSystemLifecycle(ctx context.Context, system invocation.System, sittingID model.ExamSittingID, operation string,
	prepareFinalize bool, run systemLifecycleStoreCall,
) (store.ExamSittingLifecycleResult, error) {
	if !system.Valid() || !sittingID.IsValid() {
		return store.ExamSittingLifecycleResult{}, apperror.Invalid("exam.sitting.invalid", "system_call")
	}
	current, err := service.persistence.Resolve(ctx, sittingID)
	if err != nil {
		return store.ExamSittingLifecycleResult{}, storeErrors.Translate(err)
	}
	snapshot, err := requireSnapshot(current)
	if err != nil || snapshot.Sitting.ID != sittingID || !snapshot.AcademicUnitID.IsValid() {
		return store.ExamSittingLifecycleResult{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched system snapshot"))
	}
	var finalizeJob *model.Job
	if prepareFinalize {
		resultingRevision, availableAt := snapshot.Sitting.Revision+1, snapshot.Sitting.ScheduledEndAt
		if snapshot.Sitting.State == model.ExamSittingClosing && snapshot.Sitting.ClosingAt.Valid {
			resultingRevision, availableAt = snapshot.Sitting.Revision, snapshot.Sitting.ClosingAt.Time
		}
		finalizeJob, err = service.jobs.FinalizeJob(sittingID, resultingRevision, availableAt)
		if err != nil || finalizeJob == nil {
			return store.ExamSittingLifecycleResult{}, jobFactoryUnavailable("construct Exam Sitting finalize Job", err)
		}
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}
	auditValue := map[string]any{"exam_id": snapshot.Sitting.ExamID.String(), "exam_sitting_id": sittingID.String(),
		"expected_sitting_revision": snapshot.Sitting.Revision, "job_id": system.JobID.String(), "job_attempt_id": system.AttemptID.String()}
	auditID, err := service.systemAudit.Begin(ctx, system, model.ActionExamSittingManage, resource,
		model.RoleScopeAcademicUnit, snapshot.AcademicUnitID.String(), operation, auditValue)
	if err != nil {
		return store.ExamSittingLifecycleResult{}, err
	}
	at := model.TimeUTC(service.now())
	result, err := run(ctx, systemLifecycleInput{sittingID: sittingID, auditID: auditID, auditAt: model.MillisFromTime(at), finalizeJob: finalizeJob})
	if err != nil {
		return store.ExamSittingLifecycleResult{}, service.failSystemAudit(ctx, auditID, err)
	}
	value, err := requireSystemLifecycleResult(result, snapshot.Sitting.ExamID, sittingID)
	if err != nil {
		return store.ExamSittingLifecycleResult{}, err
	}
	service.publishLifecycle(ctx, result, operation)
	return value, nil
}

func (service *Service) ListLifecycleDue(ctx context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	invalidCursor := options.AfterDueAt.IsZero() != options.AfterSittingID.IsZero() ||
		!options.AfterSittingID.IsZero() && !options.AfterSittingID.IsValid()
	if invalidCursor || options.Limit < 1 || options.Limit > 200 {
		return nil, apperror.Invalid("exam.sitting.invalid", "lifecycle_due")
	}
	options.AfterDueAt = model.TimeUTC(options.AfterDueAt)
	items, err := service.persistence.ListLifecycleDue(ctx, options)
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	for _, item := range items {
		if item.Value == nil || item.Value.Sitting == nil || item.Value.Sitting.Validate() != nil || item.DueAt.IsZero() || item.DueAt.Location() != time.UTC {
			return nil, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an invalid lifecycle due item"))
		}
	}
	if items == nil {
		items = []store.ExamSittingLifecycleDue{}
	}
	return items, nil
}

func requireManagerLifecycleResult(result *store.ExamSittingLifecycleResult, examID model.ExamID, sittingID model.ExamSittingID,
	transition store.ExamSittingLifecycleTransitionCode,
) (store.ExamSittingSnapshot, error) {
	if result == nil || result.Value == nil || !result.Changed || result.Transition != transition {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an invalid manager lifecycle result"))
	}
	value, err := requireSnapshot(result.Value)
	if err != nil || value.Sitting.ExamID != examID || value.Sitting.ID != sittingID {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched manager lifecycle result"))
	}
	return value, nil
}

func requireSystemLifecycleResult(result *store.ExamSittingLifecycleResult, examID model.ExamID, sittingID model.ExamSittingID) (store.ExamSittingLifecycleResult, error) {
	if result == nil || result.Value == nil || result.Changed != (result.Transition != "") || result.Replayed ||
		result.Changed && !validLifecycleTransition(result.Transition) {
		return store.ExamSittingLifecycleResult{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an invalid system lifecycle result"))
	}
	value, err := requireSnapshot(result.Value)
	if err != nil || value.Sitting.ExamID != examID || value.Sitting.ID != sittingID {
		return store.ExamSittingLifecycleResult{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched system lifecycle result"))
	}
	return *result, nil
}

func validLifecycleTransition(value store.ExamSittingLifecycleTransitionCode) bool {
	switch value {
	case store.ExamSittingTransitionOpened, store.ExamSittingTransitionManagerPaused, store.ExamSittingTransitionManagerResumed,
		store.ExamSittingTransitionManagerExtended, store.ExamSittingTransitionManagerClosed,
		store.ExamSittingTransitionAcademicStructureInvalid, store.ExamSittingTransitionScheduleElapsed,
		store.ExamSittingTransitionScheduledEndReached, store.ExamSittingTransitionClosedNoAttempts,
		store.ExamSittingTransitionSealingCompleted:
		return true
	default:
		return false
	}
}

func (service *Service) publishLifecycle(ctx context.Context, result *store.ExamSittingLifecycleResult, operation string) {
	if result == nil || !result.Changed || result.Replayed || result.Value == nil || result.Value.Sitting == nil {
		return
	}
	sitting := result.Value.Sitting
	if err := service.effects.LifecycleChanged(ctx, sitting.ExamID, sitting.ID, sitting.State, sitting.Revision,
		result.Transition, sitting.ScheduledEndAt, sitting.UpdatedAt); err != nil {
		service.failures.Report(ctx, "exam_sitting_"+operation, err)
	}
}

func (service *Service) failSystemAudit(ctx context.Context, auditID model.AuditEventID, err error) error {
	mapped := storeErrors.Translate(err)
	code := apperror.CodeOf(mapped, "exam.sitting.unavailable")
	if auditErr := service.systemAudit.Fail(ctx, auditID, code); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}

func jobFactoryUnavailable(message string, cause error) error {
	if cause == nil {
		cause = errors.New(message)
	} else {
		cause = errors.Join(errors.New(message), cause)
	}
	return apperror.New("exam.sitting.unavailable").Wrap(cause)
}

func validPrivateReason(reason string) bool {
	return reason != "" && utf8.ValidString(reason) && strings.TrimSpace(reason) == reason &&
		utf8.RuneCountInString(reason) <= 1000 && len(reason) <= 4000
}

func listOptions(query ListQuery) (store.ExamSittingListOptions, error) {
	invalidIdentity := !query.ExamID.IsValid() || !query.ClassID.IsZero() && !query.ClassID.IsValid()
	invalidCursor := query.BeforeScheduledStartAt.IsZero() != query.BeforeSittingID.IsZero() ||
		!query.BeforeSittingID.IsZero() && !query.BeforeSittingID.IsValid()
	invalidOverlap := query.OverlapStartAt.IsZero() != query.OverlapEndAt.IsZero() ||
		!query.OverlapStartAt.IsZero() && !query.OverlapStartAt.Before(query.OverlapEndAt)
	if invalidIdentity || invalidCursor || invalidOverlap || query.Limit < 1 || query.Limit > 200 || len(query.States) > 6 {
		return store.ExamSittingListOptions{}, apperror.Invalid("exam.sitting.invalid", "list_query")
	}
	states := append([]model.ExamSittingState(nil), query.States...)
	seen := make(map[model.ExamSittingState]struct{}, len(states))
	for _, state := range states {
		if !state.IsValid() {
			return store.ExamSittingListOptions{}, apperror.Invalid("exam.sitting.invalid", "state")
		}
		if _, duplicate := seen[state]; duplicate {
			return store.ExamSittingListOptions{}, apperror.Invalid("exam.sitting.invalid", "state")
		}
		seen[state] = struct{}{}
	}
	return store.ExamSittingListOptions{ExamID: query.ExamID, ClassID: query.ClassID, States: states,
		OverlapStartAt: model.TimeUTC(query.OverlapStartAt), OverlapEndAt: model.TimeUTC(query.OverlapEndAt),
		BeforeScheduledStartAt: model.TimeUTC(query.BeforeScheduledStartAt), BeforeSittingID: query.BeforeSittingID,
		Limit: query.Limit}, nil
}

type authorizationDecision struct {
	action       model.Action
	unitID       model.AcademicUnitID
	override     bool
	examArchived bool
}

func (service *Service) authorizeExam(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, at time.Time, ordinaryAction, overrideAction model.Action) (authorizationDecision, error) {
	return service.authorize(ctx, invocation, examID, model.Resource{Type: model.ResourceExam, ID: examID.String()}, at, ordinaryAction, overrideAction)
}

func (service *Service) authorize(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, resource model.Resource, at time.Time, ordinaryAction, overrideAction model.Action) (authorizationDecision, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return authorizationDecision{}, apperror.Invalid("exam.sitting.invalid", "exam_id")
	}
	access, err := service.access.Access(ctx, examID, principal.UserID)
	if err != nil {
		return authorizationDecision{}, storeErrors.Translate(err)
	}
	if access == nil || access.Exam == nil || access.Exam.Validate() != nil || access.Exam.ID != examID {
		return authorizationDecision{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam access projection is incomplete"))
	}
	action, err := manageraccess.SelectAction(ctx, service.memberships, principal.UserID, access, at, ordinaryAction, overrideAction)
	if err != nil {
		return authorizationDecision{}, apperror.New("exam.sitting.unavailable").Wrap(err)
	}
	if err = service.authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return authorizationDecision{}, err
	}
	return authorizationDecision{action: action, unitID: access.Exam.AcademicUnitID, override: action == overrideAction, examArchived: access.Exam.IsArchived()}, nil
}

func requireSnapshot(snapshot *store.ExamSittingSnapshot) (store.ExamSittingSnapshot, error) {
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.Validate() != nil {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an incomplete snapshot"))
	}
	return *snapshot, nil
}

func requireCommandResult(result *store.ExamSittingCommandResult) (store.ExamSittingSnapshot, error) {
	if result == nil || result.Value == nil {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned an incomplete result"))
	}
	return requireSnapshot(result.Value)
}

func requireOwnedCommandResult(result *store.ExamSittingCommandResult, examID model.ExamID, sittingID model.ExamSittingID) (store.ExamSittingSnapshot, error) {
	value, err := requireCommandResult(result)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if value.Sitting.ExamID != examID || value.Sitting.ID != sittingID {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched result"))
	}
	return value, nil
}

func requireScheduledCommandResult(result *store.ExamSittingCommandResult, examID model.ExamID, generatedID model.ExamSittingID) (store.ExamSittingSnapshot, error) {
	value, err := requireCommandResult(result)
	if err != nil {
		return store.ExamSittingSnapshot{}, err
	}
	if value.Sitting.ExamID != examID || (!result.Replayed && value.Sitting.ID != generatedID) {
		return store.ExamSittingSnapshot{}, apperror.New("exam.sitting.unavailable").Wrap(errors.New("Exam Sitting Store returned a mismatched result"))
	}
	return value, nil
}

func (service *Service) failAudit(ctx context.Context, auditID model.AuditEventID, err error) error {
	mapped := storeErrors.Translate(err)
	code := apperror.CodeOf(mapped, "exam.sitting.unavailable")
	if auditErr := service.auditor.Fail(ctx, auditID, code); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}

var storeErrors = apperror.StoreTable{
	Constraints: map[string]apperror.Code{
		"exam_sitting_revision":                "exam.sitting.revision_conflict",
		"exam_sitting_no_changes":              "exam.sitting.no_changes",
		"exam_sitting_state":                   "exam.sitting.state_conflict",
		"exam_sitting_extension":               "exam.sitting.extension_not_later",
		"exam_sitting_extension_not_later":     "exam.sitting.extension_not_later",
		"exam_sitting_deadline_reached":        "exam.sitting.deadline_reached",
		"exam_sitting_class_lineage":           "exam.sitting.class_ineligible",
		"exam_sitting_period_containment":      "exam.sitting.schedule_outside_period",
		"exam_sitting_schedule_outside_period": "exam.sitting.schedule_outside_period",
		"exam_sitting_not_future":              "exam.sitting.schedule_not_future",
		"exam_sitting_revision_lineage":        "exam.sitting.not_found",
		"exam_archived":                        "exam.archived",
	},
	NotFound:     "exam.sitting.not_found",
	Conflict:     "exam.sitting.conflict",
	Invalid:      "exam.sitting.invalid",
	InvalidField: "value",
	Unavailable:  "exam.sitting.unavailable",
}
