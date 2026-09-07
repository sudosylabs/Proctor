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

	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// RecordsAuthorizer also owns the durable decision for prohibited self-waivers.
type RecordsAuthorizer interface {
	Authorize(context.Context, Call, model.Action, model.Resource) error
	DenySelf(context.Context, Call, model.Action, model.Resource, model.AcademicUnitID) error
}

type CompleteRecordsCommand struct {
	Scope                        model.RetentionHoldScope
	ExpectedRevision             int64
	AcknowledgedEvidenceRevision int64
	IdempotencyKey               string
}

type WaiveReviewCommand struct {
	Scope                    model.RetentionHoldScope
	ExpectedRevision         int64
	ExpectedReviewRevision   int64
	ExpectedDiscrepancyCount int64
	ReasonCode               string
	PrivateReason            string
	IdempotencyKey           string
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

func (r *Records) authorize(ctx context.Context, call Call, scope model.RetentionHoldScope,
	ordinary, override model.Action, excludeSelf bool,
) (*store.ExamRecordsScope, model.Action, error) {
	principal := call.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess || scope.Validate() != nil {
		return nil, "", invalid("scope")
	}
	resolved, err := r.persistence.Resolve(ctx, scope)
	if err != nil {
		return nil, "", recordsStoreError(err)
	}
	if resolved == nil || resolved.Scope != scope || !resolved.AcademicUnitID.IsValid() || !resolved.InstitutionID.IsValid() ||
		scope.SubmissionID.IsValid() && (!resolved.CandidateUserID.IsValid() || !resolved.AttemptID.IsValid()) {
		return nil, "", unavailable(errors.New("Exam records scope projection is incomplete"))
	}
	if excludeSelf && principal.UserID == resolved.CandidateUserID {
		return nil, "", r.authorizer.DenySelf(ctx, call, ordinary, scope.Resource(), resolved.AcademicUnitID)
	}
	action := ordinary
	if override != "" {
		access, accessErr := r.access.Access(ctx, scope.ExamID, principal.UserID)
		if accessErr != nil {
			return nil, "", recordsStoreError(accessErr)
		}
		if access == nil || access.Exam == nil || access.Exam.ID != scope.ExamID || access.Exam.AcademicUnitID != resolved.AcademicUnitID {
			return nil, "", unavailable(errors.New("Exam records access projection is incomplete"))
		}
		action, err = manageraccess.SelectAction(ctx, r.memberships, principal.UserID, access, model.TimeUTC(r.now()), ordinary, override)
		if err != nil {
			return nil, "", unavailable(err)
		}
	}
	if err = r.authorizer.Authorize(ctx, call, action, scope.Resource()); err != nil {
		return nil, "", err
	}
	return resolved, action, nil
}

func (r *Records) GetCompletion(ctx context.Context, call Call, examID model.ExamID, sittingID model.ExamSittingID) (*store.ExamRecordsCompletionSnapshot, error) {
	scope := model.RetentionHoldScope{ExamID: examID, SittingID: sittingID}
	if !sittingID.IsValid() {
		return nil, invalid("exam_sitting_id")
	}
	if _, _, err := r.authorize(ctx, call, scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, false); err != nil {
		return nil, err
	}
	value, err := r.persistence.GetCompletion(ctx, examID, sittingID)
	if err != nil {
		return nil, recordsStoreError(err)
	}
	if value == nil || value.Completion.Validate() != nil || value.Completion.SittingID != sittingID ||
		value.SubmissionCount < 0 || value.PendingReviews < 0 || value.PendingReviews > value.SubmissionCount {
		return nil, unavailable(errors.New("Exam records completion projection is incomplete"))
	}
	return value, nil
}

func (r *Records) FindReviewWaiver(ctx context.Context, call Call, scope model.RetentionHoldScope) (*model.SubmissionReviewWaiver, error) {
	if !scope.SubmissionID.IsValid() {
		return nil, invalid("submission_id")
	}
	if _, _, err := r.authorize(ctx, call, scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, true); err != nil {
		return nil, err
	}
	value, found, err := r.persistence.FindReviewWaiver(ctx, scope)
	if err != nil {
		return nil, recordsStoreError(err)
	}
	if !found && value == nil {
		return nil, nil
	}
	if !found || value.Validate() != nil || value.SubmissionID != scope.SubmissionID {
		return nil, unavailable(errors.New("Submission Review waiver projection is incomplete"))
	}
	return value, nil
}

func (r *Records) begin(ctx context.Context, call Call, scope *store.ExamRecordsScope, action model.Action, operation string,
	data map[string]any,
) (store.ExamRecordsMutation, error) {
	data["exam_id"] = scope.Scope.ExamID.String()
	if scope.Scope.SittingID.IsValid() {
		data["exam_sitting_id"] = scope.Scope.SittingID.String()
	}
	if scope.Scope.SubmissionID.IsValid() {
		data["submission_id"] = scope.Scope.SubmissionID.String()
	}
	id, err := r.auditor.Begin(ctx, call, action, scope.Scope.Resource(), model.RoleScopeAcademicUnit,
		scope.AcademicUnitID.String(), operation, data, nil)
	if err != nil {
		return store.ExamRecordsMutation{}, err
	}
	return store.ExamRecordsMutation{Scope: scope.Scope, Principal: call.Principal(), Action: action,
		AuditEventID: id, AuditAt: model.MillisFromTime(model.TimeUTC(r.now()))}, nil
}

func (r *Records) fail(ctx context.Context, mutation store.ExamRecordsMutation, err error) error {
	mapped := recordsStoreError(err)
	var fault *Fault
	if !errors.As(mapped, &fault) {
		fault = &Fault{Code: "exam.unavailable", Cause: mapped}
	}
	if auditErr := r.auditor.Fail(ctx, mutation.AuditEventID, fault.Code); auditErr != nil {
		return auditErr
	}
	return mapped
}

func (r *Records) CompleteRecords(ctx context.Context, call Call, command CompleteRecordsCommand) (*model.ExamSittingRecordsCompletion, error) {
	if !command.Scope.SittingID.IsValid() || !command.Scope.SubmissionID.IsZero() || command.ExpectedRevision < 1 || command.AcknowledgedEvidenceRevision < 0 {
		return nil, invalid("records_completion")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := prepareIdempotency(call, store.ExamRecordsCompleteOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, call, command.Scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, false)
	if err != nil {
		return nil, err
	}
	mutation, err := r.begin(ctx, call, scope, action, "complete_records", map[string]any{"expected_revision": command.ExpectedRevision,
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
		return nil, unavailable(errors.New("Exam records completion result is incomplete"))
	}
	return result.Completion, nil
}

func (r *Records) WaiveReview(ctx context.Context, call Call, command WaiveReviewCommand) (*model.SubmissionReviewWaiver, error) {
	if !command.Scope.SubmissionID.IsValid() || command.ExpectedRevision < 0 || command.ExpectedReviewRevision < 0 ||
		command.ExpectedDiscrepancyCount < 0 || model.ValidateRecordsReason(command.ReasonCode, command.PrivateReason) != nil {
		return nil, invalid("review_waiver")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := prepareIdempotency(call, store.ExamRecordsWaiveReviewOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, call, command.Scope, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride, true)
	if err != nil {
		return nil, err
	}
	mutation, err := r.begin(ctx, call, scope, action, "waive_review", map[string]any{"expected_revision": command.ExpectedRevision, "reason_code": command.ReasonCode})
	if err != nil {
		return nil, err
	}
	result, err := r.persistence.WaiveReview(ctx, &store.ExamRecordsReviewWaiver{ExamRecordsMutation: mutation,
		ExpectedRevision: command.ExpectedRevision, ExpectedReviewRevision: command.ExpectedReviewRevision,
		ExpectedDiscrepancyCount: command.ExpectedDiscrepancyCount, ReasonCode: command.ReasonCode, PrivateReason: command.PrivateReason}, idempotency)
	if err != nil {
		return nil, r.fail(ctx, mutation, err)
	}
	if result == nil || result.Waiver.Validate() != nil || result.Waiver.SubmissionID != command.Scope.SubmissionID {
		return nil, unavailable(errors.New("Submission Review waiver result is incomplete"))
	}
	return result.Waiver, nil
}

func (r *Records) ListHolds(ctx context.Context, call Call, query store.ExamRecordsHoldListOptions) (*store.ExamRecordsHoldPage, error) {
	if query.Limit < 1 || query.Limit > 200 || !query.After.IsZero() && !query.After.IsValid() {
		return nil, invalid("hold_page")
	}
	if _, _, err := r.authorize(ctx, call, query.Scope, model.ActionExamRecordsHold, model.ActionExamRecordsHoldOverride, false); err != nil {
		return nil, err
	}
	page, err := r.persistence.ListHolds(ctx, query)
	if err != nil {
		return nil, recordsStoreError(err)
	}
	if page == nil || len(page.Holds) > query.Limit {
		return nil, unavailable(errors.New("Retention hold page is incomplete"))
	}
	for _, hold := range page.Holds {
		if hold.Validate() != nil || hold.Scope.ExamID != query.Scope.ExamID {
			return nil, unavailable(errors.New("Retention hold projection is invalid"))
		}
	}
	return page, nil
}

func (r *Records) CreateHold(ctx context.Context, call Call, command CreateRetentionHoldCommand) (*model.RetentionHold, error) {
	if model.ValidateRecordsReason(command.ReasonCode, command.PrivateReason) != nil {
		return nil, invalid("retention_hold")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := prepareIdempotency(call, store.ExamRecordsCreateHoldOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, call, command.Scope, model.ActionExamRecordsHold, model.ActionExamRecordsHoldOverride, false)
	if err != nil {
		return nil, err
	}
	holdID := r.newHoldID()
	if !holdID.IsValid() {
		return nil, unavailable(errors.New("Retention hold identity is invalid"))
	}
	mutation, err := r.begin(ctx, call, scope, action, "create_hold", map[string]any{"retention_hold_id": holdID.String(), "reason_code": command.ReasonCode})
	if err != nil {
		return nil, err
	}
	result, err := r.persistence.CreateHold(ctx, &store.ExamRecordsHoldCreation{ExamRecordsMutation: mutation,
		HoldID: holdID, ReasonCode: command.ReasonCode, PrivateReason: command.PrivateReason}, idempotency)
	if err != nil {
		return nil, r.fail(ctx, mutation, err)
	}
	if result == nil || result.Hold.Validate() != nil || result.Hold.Scope != command.Scope {
		return nil, unavailable(errors.New("Retention hold creation result is incomplete"))
	}
	return result.Hold, nil
}

func (r *Records) ReleaseHold(ctx context.Context, call Call, command ReleaseRetentionHoldCommand) (*model.RetentionHold, error) {
	if !command.HoldID.IsValid() || command.ExpectedRevision < 1 || model.ValidateRecordsReason(command.ReasonCode, command.PrivateReason) != nil {
		return nil, invalid("retention_hold")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := prepareIdempotency(call, store.ExamRecordsReleaseHoldOperation, key, command)
	if err != nil {
		return nil, err
	}
	scope, action, err := r.authorize(ctx, call, command.Scope, model.ActionRetentionHoldRelease, "", false)
	if err != nil {
		return nil, err
	}
	if !call.Principal().HasStrongAuthentication() {
		return nil, &Fault{Code: "authentication.strong_required"}
	}
	if !call.Principal().IsRecentlyAuthenticated(model.TimeUTC(r.now()), r.recentTTL) {
		return nil, &Fault{Code: "authentication.reauthentication_required"}
	}
	mutation, err := r.begin(ctx, call, scope, action, "release_hold", map[string]any{"retention_hold_id": command.HoldID.String(),
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
		return nil, unavailable(errors.New("Retention hold release result is incomplete"))
	}
	return result.Hold, nil
}

func recordsStoreError(err error) error {
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Resource == "authorization" {
		return &Fault{Code: "exam.not_found", Cause: err}
	}
	return mapStoreError(err)
}
