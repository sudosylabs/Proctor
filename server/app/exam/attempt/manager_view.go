// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// GetManagedAttemptQuery names one Attempt by its exact Exam and Sitting for
// a managed view.
type GetManagedAttemptQuery struct {
	ExamID    model.ExamID
	SittingID model.ExamSittingID
	AttemptID model.ExamAttemptID
}

// GetExamAttempt returns a managed view of one Attempt. It authorizes Sitting
// view on the exact Sitting before reading the Attempt so a caller cannot
// distinguish existing, mismatched, or missing Attempts.
func (s *Service) GetExamAttempt(ctx context.Context, invocation invocation.Invocation, query GetManagedAttemptQuery) (_ store.ExamAttemptManagerSnapshot, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || !query.AttemptID.IsValid() {
		return store.ExamAttemptManagerSnapshot{}, apperror.Invalid("exam.attempt.invalid", "attempt")
	}
	// Authorization precedes every Attempt read so a caller without Sitting
	// view authority cannot distinguish existing, mismatched, or missing
	// Attempts; the authorizer records that decision.
	if err := s.deps.Managers.AuthorizeSittingView(ctx, invocation, query.SittingID); err != nil {
		return store.ExamAttemptManagerSnapshot{}, err
	}
	sitting, err := s.deps.Sittings.Resolve(ctx, query.SittingID)
	if err != nil {
		return store.ExamAttemptManagerSnapshot{}, storeError(storeErrors, err)
	}
	if sitting == nil || sitting.Sitting == nil || sitting.Sitting.ID != query.SittingID || !sitting.AcademicUnitID.IsValid() {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent managed Attempt Sitting projection"))
	}
	if sitting.Sitting.ExamID != query.ExamID {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.not_found")
	}
	snapshot, err := s.deps.Persistence.Get(ctx, query.ExamID, query.AttemptID)
	if err != nil {
		return store.ExamAttemptManagerSnapshot{}, storeError(storeErrors, err)
	}
	if snapshot == nil || snapshot.Attempt == nil || snapshot.Attempt.Validate() != nil ||
		snapshot.Attempt.ExamID != query.ExamID || !validActiveSuspension(snapshot.ActiveSuspension, snapshot.Attempt.ID) {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent managed Attempt projection"))
	}
	if snapshot.Attempt.SittingID != query.SittingID {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.not_found")
	}
	if snapshot.Attempt.CandidateUserID == invocation.Principal().UserID {
		resource := model.Resource{Type: model.ResourceExamSitting, ID: snapshot.Attempt.SittingID.String()}
		auditID, auditErr := s.deps.Auditor.BeginAtScope(ctx, invocation, model.ActionExamSittingView, resource,
			model.RoleScopeAcademicUnit, sitting.AcademicUnitID.String(), "view_managed_attempt", map[string]any{
				"exam_id": snapshot.Attempt.ExamID.String(), "exam_sitting_id": snapshot.Attempt.SittingID.String(),
				"exam_attempt_id": snapshot.Attempt.ID.String(),
			}, nil)
		if auditErr != nil {
			return store.ExamAttemptManagerSnapshot{}, auditErr
		}
		if auditErr = s.deps.Auditor.Fail(ctx, auditID, "exam.attempt.not_found"); auditErr != nil {
			return store.ExamAttemptManagerSnapshot{}, auditErr
		}
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.not_found")
	}
	clone := cloneManagerSnapshot(snapshot)
	if clone == nil {
		return store.ExamAttemptManagerSnapshot{}, apperror.New("exam.attempt.unavailable")
	}
	return *clone, nil
}

// ListManagedAttemptsQuery selects one page of Attempts in a Sitting,
// optionally filtered by state. BeforeCreatedAt and BeforeAttemptID are the
// keyset cursor of the last Attempt already seen.
type ListManagedAttemptsQuery struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	States          []model.ExamAttemptState
	BeforeCreatedAt time.Time
	BeforeAttemptID model.ExamAttemptID
	Limit           int
}

// ManagedAttemptPage is one page of managed Attempt snapshots; HasMore reports
// that a later page exists.
type ManagedAttemptPage struct {
	Items   []store.ExamAttemptManagerSnapshot
	HasMore bool
}

// SittingCandidateStatusesQuery selects one page of per-candidate Attempt
// statuses for a Sitting, starting after AfterCandidateUserID.
type SittingCandidateStatusesQuery struct {
	ExamID               model.ExamID
	SittingID            model.ExamSittingID
	AfterCandidateUserID model.UserID
	Limit                int
}

// SittingCandidateStatusesPage is one page of per-candidate Attempt statuses
// for a Sitting; HasMore reports that a later page exists.
type SittingCandidateStatusesPage struct {
	store.SittingCandidateStatusPage
	HasMore bool
}

// ListSittingCandidateStatuses returns one page of per-candidate Attempt
// statuses for a Sitting, excluding the caller. It requires Sitting view;
// whether the caller also holds Sitting management decides if re-allow is
// offered.
func (s *Service) ListSittingCandidateStatuses(ctx context.Context, invocation invocation.Invocation,
	query SittingCandidateStatusesQuery,
) (_ SittingCandidateStatusesPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil || !query.ExamID.IsValid() || !query.SittingID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		!query.AfterCandidateUserID.IsZero() && !query.AfterCandidateUserID.IsValid() {
		return SittingCandidateStatusesPage{}, apperror.Invalid("exam.attempt.invalid", "candidate_statuses")
	}
	if err := s.deps.Managers.AuthorizeSittingView(ctx, invocation, query.SittingID); err != nil {
		return SittingCandidateStatusesPage{}, err
	}
	reallowAuthorized := false
	if _, err := s.deps.Managers.AuthorizeSittingManage(ctx, invocation, query.SittingID); err == nil {
		reallowAuthorized = true
	} else {
		var fault *apperror.Error
		if !errors.As(err, &fault) || fault.Code() != "authorization.denied" {
			return SittingCandidateStatusesPage{}, err
		}
	}
	stored, err := s.deps.Persistence.ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{
		ExamID: query.ExamID, SittingID: query.SittingID, ExcludeCandidateUserID: principal.UserID,
		AfterCandidateUserID: query.AfterCandidateUserID, ReallowAuthorized: reallowAuthorized, Limit: query.Limit + 1,
	})
	if err != nil {
		return SittingCandidateStatusesPage{}, storeError(storeErrors, err)
	}
	if stored == nil || stored.ExamID != query.ExamID || stored.SittingID != query.SittingID || stored.ServerTime.IsZero() ||
		!stored.SittingState.IsValid() || stored.SittingRevision < 1 || stored.Items == nil {
		return SittingCandidateStatusesPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete Sitting candidate-status projection"))
	}
	page := SittingCandidateStatusesPage{SittingCandidateStatusPage: *stored, HasMore: len(stored.Items) > query.Limit}
	page.Items = append([]store.SittingCandidateStatusItem(nil), stored.Items[:min(len(stored.Items), query.Limit)]...)
	for i := range page.Items {
		if page.Items[i].NativeSecurity != nil {
			if page.Items[i].NativeSecurity.Validate() != nil {
				return SittingCandidateStatusesPage{}, apperror.New("exam.attempt.unavailable").Wrap(model.ErrNativeDeliveryInvalid)
			}
			value := page.Items[i].NativeSecurity.Clone()
			page.Items[i].NativeSecurity = &value
		}
	}

	return page, nil
}

// ListExamAttempts returns one page of Attempts in a Sitting for a caller
// with Sitting view, optionally filtered by state and excluding the caller's
// own Attempt.
func (s *Service) ListExamAttempts(ctx context.Context, invocation invocation.Invocation, query ListManagedAttemptsQuery) (_ ManagedAttemptPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeCreatedAt.IsZero() != query.BeforeAttemptID.IsZero()) {
		return ManagedAttemptPage{}, apperror.Invalid("exam.attempt.invalid", "attempt_list")
	}
	if err := s.deps.Managers.AuthorizeSittingView(ctx, invocation, query.SittingID); err != nil {
		return ManagedAttemptPage{}, err
	}
	rows, err := s.deps.Persistence.List(ctx, store.ExamAttemptManagerListOptions{
		ExamID: query.ExamID, SittingID: query.SittingID, ExcludeCandidateUserID: invocation.Principal().UserID,
		States:          append([]model.ExamAttemptState(nil), query.States...),
		BeforeCreatedAt: model.TimeUTC(query.BeforeCreatedAt), BeforeAttemptID: query.BeforeAttemptID, Limit: query.Limit + 1,
	})
	if err != nil {
		return ManagedAttemptPage{}, storeError(storeErrors, err)
	}
	page := ManagedAttemptPage{Items: make([]store.ExamAttemptManagerSnapshot, 0, min(len(rows), query.Limit)), HasMore: len(rows) > query.Limit}
	for index, row := range rows {
		if index == query.Limit {
			break
		}
		if row.Attempt == nil || row.Attempt.Validate() != nil || row.Attempt.ExamID != query.ExamID || row.Attempt.SittingID != query.SittingID ||
			!validActiveSuspension(row.ActiveSuspension, row.Attempt.ID) {
			return ManagedAttemptPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent managed Attempt page"))
		}
		page.Items = append(page.Items, *cloneManagerSnapshot(&row))
	}
	return page, nil
}

func cloneManagerSnapshot(snapshot *store.ExamAttemptManagerSnapshot) *store.ExamAttemptManagerSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	if snapshot.Attempt != nil {
		value := *snapshot.Attempt
		clone.Attempt = &value
	}
	if snapshot.Workspace != nil {
		value := *snapshot.Workspace
		clone.Workspace = &value
	}
	if snapshot.LatestParticipation != nil {
		value := *snapshot.LatestParticipation
		clone.LatestParticipation = &value
	}
	if snapshot.CurrentConnection != nil {
		value := *snapshot.CurrentConnection
		clone.CurrentConnection = &value
	}
	if snapshot.ActiveSuspension != nil {
		value := *snapshot.ActiveSuspension
		clone.ActiveSuspension = &value
	}
	return &clone
}
