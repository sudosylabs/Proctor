// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// RecordsAuthorizer also owns the durable decision for prohibited self-waivers.
type RecordsAuthorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
	Deny(context.Context, invocation.Invocation, model.Action, model.Resource, model.AcademicUnitID) error
}

type CompleteRecordsCommand struct {
	Scope                        model.RetentionHoldScope
	ExpectedRevision             int64
	AcknowledgedEvidenceRevision int64
	IdempotencyKey               string
}

type WaiveReviewCommand struct {
	ExpectedDeliveryInventoryRevision int64
	Scope                             model.RetentionHoldScope
	ExpectedRevision                  int64
	ExpectedReviewRevision            int64
	ExpectedDiscrepancyCount          int64
	ReasonCode                        string
	PrivateReason                     string
	IdempotencyKey                    string
}

type CreateRetentionHoldCommand struct {
	Scope          model.RetentionHoldScope
	ReasonCode     string
	PrivateReason  string
	IdempotencyKey string
}

type ReleaseRetentionHoldCommand struct {
	Scope            model.RetentionHoldScope
	HoldID           model.RetentionHoldID
	ExpectedRevision int64
	ReasonCode       string
	PrivateReason    string
	IdempotencyKey   string
}

// Records owns the separate post-delivery completion and preservation lifecycle.
// Retention scheduling and deletion are separate use cases.
type Records struct {
	persistence store.ExamRecordsStore
	access      store.ExamAuthoringStore
	memberships manageraccess.Memberships
	authorizer  RecordsAuthorizer
	auditor     Auditor
	recentTTL   time.Duration
	now         func() time.Time
	newHoldID   func() model.RetentionHoldID
}

func NewRecords(persistence store.ExamRecordsStore, access store.ExamAuthoringStore,
	memberships manageraccess.Memberships, authorizer RecordsAuthorizer, auditor Auditor,
	recentTTL time.Duration, now func() time.Time, newHoldID func() model.RetentionHoldID,
) (*Records, error) {
	if persistence == nil || access == nil || memberships == nil || authorizer == nil || auditor == nil ||
		recentTTL <= 0 || now == nil || newHoldID == nil {
		return nil, errors.New("Exam records dependencies are required")
	}
	return &Records{persistence: persistence, access: access, memberships: memberships, authorizer: authorizer,
		auditor: auditor, recentTTL: recentTTL, now: now, newHoldID: newHoldID}, nil
}

func (r *Records) authorize(ctx context.Context, invocation invocation.Invocation, scope model.RetentionHoldScope,
	ordinary, override model.Action, excludeSelf bool,
) (*store.ExamRecordsScope, model.Action, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess || scope.Validate() != nil {
		return nil, "", apperror.Invalid("exam.invalid", "scope")
	}
	resolved, err := r.persistence.Resolve(ctx, scope)
	if err != nil {
		return nil, "", examRecordsStoreErrors.Translate(err)
	}
	if resolved == nil || resolved.Scope != scope || !resolved.AcademicUnitID.IsValid() || !resolved.InstitutionID.IsValid() ||
		scope.SubmissionID.IsValid() && (!resolved.CandidateUserID.IsValid() || !resolved.AttemptID.IsValid()) {
		return nil, "", apperror.New("exam.unavailable").Wrap(errors.New("Exam records scope projection is incomplete"))
	}
	if excludeSelf && principal.UserID == resolved.CandidateUserID {
		return nil, "", r.authorizer.Deny(ctx, invocation, ordinary, scope.Resource(), resolved.AcademicUnitID)
	}
	action := ordinary
	if override != "" {
		access, accessErr := r.access.Access(ctx, scope.ExamID, principal.UserID)
		if accessErr != nil {
			return nil, "", examRecordsStoreErrors.Translate(accessErr)
		}
		if access == nil || access.Exam == nil || access.Exam.ID != scope.ExamID || access.Exam.AcademicUnitID != resolved.AcademicUnitID {
			return nil, "", apperror.New("exam.unavailable").Wrap(errors.New("Exam records access projection is incomplete"))
		}
		action, err = manageraccess.SelectAction(ctx, r.memberships, principal.UserID, access, model.TimeUTC(r.now()), ordinary, override)
		if err != nil {
			return nil, "", apperror.New("exam.unavailable").Wrap(err)
		}
	}
	if err = r.authorizer.Authorize(ctx, invocation, action, scope.Resource()); err != nil {
		return nil, "", err
	}
	return resolved, action, nil
}

func (r *Records) GetCompletion(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, sittingID model.ExamSittingID) (*store.ExamRecordsCompletionSnapshot, error) {
	scope := model.RetentionHoldScope{ExamID: examID, SittingID: sittingID}
	if !sittingID.IsValid() {
		return nil, apperror.Invalid("exam.invalid", "exam_sitting_id")
	}
	if _, _, err := r.authorize(ctx, invocation, scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, false); err != nil {
		return nil, err
	}
	value, err := r.persistence.GetCompletion(ctx, examID, sittingID)
	if err != nil {
		return nil, examRecordsStoreErrors.Translate(err)
	}
	if value == nil || value.Completion.Validate() != nil || value.Completion.SittingID != sittingID ||
		value.SubmissionCount < 0 || value.PendingReviews < 0 || value.PendingReviews > value.SubmissionCount {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Exam records completion projection is incomplete"))
	}
	return value, nil
}

func (r *Records) FindReviewWaiver(ctx context.Context, invocation invocation.Invocation, scope model.RetentionHoldScope) (*model.SubmissionReviewWaiver, error) {
	if !scope.SubmissionID.IsValid() {
		return nil, apperror.Invalid("exam.invalid", "submission_id")
	}
	if _, _, err := r.authorize(ctx, invocation, scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, true); err != nil {
		return nil, err
	}
	value, found, err := r.persistence.FindReviewWaiver(ctx, scope)
	if err != nil {
		return nil, examRecordsStoreErrors.Translate(err)
	}
	if !found && value == nil {
		return nil, nil
	}
	if !found || value.Validate() != nil || value.SubmissionID != scope.SubmissionID {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Submission Review waiver projection is incomplete"))
	}
	return value, nil
}

func (r *Records) begin(ctx context.Context, invocation invocation.Invocation, scope *store.ExamRecordsScope, action model.Action, operation string,
	data map[string]any,
) (store.ExamRecordsMutation, error) {
	data["exam_id"] = scope.Scope.ExamID.String()
	if scope.Scope.SittingID.IsValid() {
		data["exam_sitting_id"] = scope.Scope.SittingID.String()
	}
	if scope.Scope.SubmissionID.IsValid() {
		data["submission_id"] = scope.Scope.SubmissionID.String()
	}
	id, err := r.auditor.Begin(ctx, invocation, action, scope.Scope.Resource(), model.RoleScopeAcademicUnit,
		scope.AcademicUnitID.String(), operation, data, nil)
	if err != nil {
		return store.ExamRecordsMutation{}, err
	}
	return store.ExamRecordsMutation{Scope: scope.Scope, Principal: invocation.Principal(), Action: action,
		AuditEventID: id, AuditAt: model.MillisFromTime(model.TimeUTC(r.now()))}, nil
}

func (r *Records) fail(ctx context.Context, mutation store.ExamRecordsMutation, err error) error {
	mapped := examRecordsStoreErrors.Translate(err)
	if auditErr := r.auditor.Fail(ctx, mutation.AuditEventID, apperror.CodeOf(mapped, "exam.unavailable")); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}

func (r *Records) CompleteRecords(ctx context.Context, invocation invocation.Invocation, command CompleteRecordsCommand) (*model.ExamSittingRecordsCompletion, error) {
	if !command.Scope.SittingID.IsValid() || !command.Scope.SubmissionID.IsZero() || command.ExpectedRevision < 1 || command.AcknowledgedEvidenceRevision < 0 {
		return nil, apperror.Invalid("exam.invalid", "records_completion")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := applicationidempotency.Require(invocation, store.ExamRecordsCompleteOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, invocation, command.Scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, false)
	if err != nil {
		return nil, err
	}
	mutation, err := r.begin(ctx, invocation, scope, action, "complete_records", map[string]any{"expected_revision": command.ExpectedRevision,
		"acknowledged_evidence_revision": command.AcknowledgedEvidenceRevision})
	if err != nil {
		return nil, err
	}
	result, err := r.persistence.CompleteRecords(ctx, &store.ExamRecordsCompletion{ExamRecordsMutation: mutation,
		ExpectedRevision: command.ExpectedRevision, AcknowledgedEvidenceRevision: command.AcknowledgedEvidenceRevision}, idempotency)
	if err != nil {
		return nil, r.fail(ctx, mutation, err)
	}
	if result == nil || result.Completion.Validate() != nil || result.Completion.SittingID != command.Scope.SittingID {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Exam records completion result is incomplete"))
	}
	return result.Completion, nil
}

func (r *Records) WaiveReview(ctx context.Context, invocation invocation.Invocation, command WaiveReviewCommand) (*model.SubmissionReviewWaiver, error) {
	if !command.Scope.SubmissionID.IsValid() || command.ExpectedRevision < 0 || command.ExpectedReviewRevision < 0 ||
		command.ExpectedDeliveryInventoryRevision < 0 || command.ExpectedDiscrepancyCount < 0 || model.ValidateRecordsReason(command.ReasonCode, command.PrivateReason) != nil {
		return nil, apperror.Invalid("exam.invalid", "review_waiver")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := applicationidempotency.Require(invocation, store.ExamRecordsWaiveReviewOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, invocation, command.Scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, true)
	if err != nil {
		return nil, err
	}
	mutation, err := r.begin(ctx, invocation, scope, action, "waive_review", map[string]any{"expected_revision": command.ExpectedRevision, "reason_code": command.ReasonCode})
	if err != nil {
		return nil, err
	}
	result, err := r.persistence.WaiveReview(ctx, &store.ExamRecordsReviewWaiver{ExamRecordsMutation: mutation,
		ExpectedRevision: command.ExpectedRevision, ExpectedReviewRevision: command.ExpectedReviewRevision,
		ExpectedDeliveryInventoryRevision: command.ExpectedDeliveryInventoryRevision, ExpectedDiscrepancyCount: command.ExpectedDiscrepancyCount, ReasonCode: command.ReasonCode, PrivateReason: command.PrivateReason}, idempotency)
	if err != nil {
		return nil, r.fail(ctx, mutation, err)
	}
	if result == nil || result.Waiver.Validate() != nil || result.Waiver.SubmissionID != command.Scope.SubmissionID {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Submission Review waiver result is incomplete"))
	}
	return result.Waiver, nil
}

func (r *Records) ListHolds(ctx context.Context, invocation invocation.Invocation, query store.ExamRecordsHoldListOptions) (*store.ExamRecordsHoldPage, error) {
	if query.Limit < 1 || query.Limit > 200 || !query.After.IsZero() && !query.After.IsValid() {
		return nil, apperror.Invalid("exam.invalid", "hold_page")
	}
	if _, _, err := r.authorize(ctx, invocation, query.Scope, model.ActionExamRecordsHold, model.ActionExamRecordsHoldOverride, false); err != nil {
		return nil, err
	}
	page, err := r.persistence.ListHolds(ctx, query)
	if err != nil {
		return nil, examRecordsStoreErrors.Translate(err)
	}
	if page == nil || len(page.Holds) > query.Limit {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Retention hold page is incomplete"))
	}
	for _, hold := range page.Holds {
		if hold.Validate() != nil || hold.Scope.ExamID != query.Scope.ExamID {
			return nil, apperror.New("exam.unavailable").Wrap(errors.New("Retention hold projection is invalid"))
		}
	}
	return page, nil
}

func (r *Records) CreateHold(ctx context.Context, invocation invocation.Invocation, command CreateRetentionHoldCommand) (*model.RetentionHold, error) {
	if model.ValidateRecordsReason(command.ReasonCode, command.PrivateReason) != nil {
		return nil, apperror.Invalid("exam.invalid", "retention_hold")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := applicationidempotency.Require(invocation, store.ExamRecordsCreateHoldOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, invocation, command.Scope, model.ActionExamRecordsHold, model.ActionExamRecordsHoldOverride, false)
	if err != nil {
		return nil, err
	}
	holdID := r.newHoldID()
	if !holdID.IsValid() {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Retention hold identity is invalid"))
	}
	mutation, err := r.begin(ctx, invocation, scope, action, "create_hold", map[string]any{"retention_hold_id": holdID.String(), "reason_code": command.ReasonCode})
	if err != nil {
		return nil, err
	}
	result, err := r.persistence.CreateHold(ctx, &store.ExamRecordsHoldCreation{ExamRecordsMutation: mutation,
		HoldID: holdID, ReasonCode: command.ReasonCode, PrivateReason: command.PrivateReason}, idempotency)
	if err != nil {
		return nil, r.fail(ctx, mutation, err)
	}
	if result == nil || result.Hold.Validate() != nil || result.Hold.Scope != command.Scope {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Retention hold creation result is incomplete"))
	}
	return result.Hold, nil
}

func (r *Records) ReleaseHold(ctx context.Context, invocation invocation.Invocation, command ReleaseRetentionHoldCommand) (*model.RetentionHold, error) {
	if !command.HoldID.IsValid() || command.ExpectedRevision < 1 || model.ValidateRecordsReason(command.ReasonCode, command.PrivateReason) != nil {
		return nil, apperror.Invalid("exam.invalid", "retention_hold")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := applicationidempotency.Require(invocation, store.ExamRecordsReleaseHoldOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, invocation, command.Scope, model.ActionRetentionHoldRelease, "", false)
	if err != nil {
		return nil, err
	}
	if !invocation.Principal().HasStrongAuthentication() {
		return nil, apperror.New("authentication.strong_required")
	}
	if !invocation.Principal().IsRecentlyAuthenticated(model.TimeUTC(r.now()), r.recentTTL) {
		return nil, apperror.New("authentication.reauthentication_required")
	}
	mutation, err := r.begin(ctx, invocation, scope, action, "release_hold", map[string]any{"retention_hold_id": command.HoldID.String(),
		"expected_revision": command.ExpectedRevision, "reason_code": command.ReasonCode})
	if err != nil {
		return nil, err
	}
	result, err := r.persistence.ReleaseHold(ctx, &store.ExamRecordsHoldRelease{ExamRecordsMutation: mutation,
		HoldID: command.HoldID, ExpectedRevision: command.ExpectedRevision, ReasonCode: command.ReasonCode,
		PrivateReason: command.PrivateReason, RecentAuthenticationTTL: r.recentTTL}, idempotency)
	if err != nil {
		return nil, r.fail(ctx, mutation, err)
	}
	if result == nil || result.Hold.Validate() != nil || result.Hold.ID != command.HoldID || result.Hold.Scope != command.Scope || !result.Hold.ReleasedAt.Valid {
		return nil, apperror.New("exam.unavailable").Wrap(errors.New("Retention hold release result is incomplete"))
	}
	return result.Hold, nil
}

// examRecordsStoreErrors conceals an authorization refusal discovered by the
// Store as an absent Exam.
var examRecordsStoreErrors = examStoreErrors.With(
	apperror.StoreRule{Kind: apperror.StoreConflict, Entity: "authorization", Code: "exam.not_found"})
