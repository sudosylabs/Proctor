// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib/ordertest"
)

func TestRecordsRequiresManagerAndExactMembershipOrScopedOverride(t *testing.T) {
	for _, tc := range []struct {
		name            string
		manager, member bool
		want            model.Action
	}{
		{"manager and current member", true, true, model.ActionExamRecordsComplete},
		{"manager without membership", true, false, model.ActionExamRecordsCompleteOverride},
		{"scoped nonmanager", false, true, model.ActionExamRecordsCompleteOverride},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, records, persistence, _ := newRecordsFixture(t)
			f.persistence.actorIsManager = tc.manager
			if tc.member {
				f.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: f.unitID}}
			}
			command := CompleteRecordsCommand{Scope: persistence.scope.Scope, ExpectedRevision: 1, IdempotencyKey: "completion-key"}
			value, err := records.CompleteExamSittingRecords(context.Background(), f.invocation, command)
			require.NoError(t, err)
			require.True(t, value.IsCurrent(), "completion failed to carry current authority: %#v, %#v", value, persistence.completion)
			require.Equal(t, tc.want, f.authorizer.action, "completion failed to carry current authority: %#v, %#v", value, persistence.completion)
			require.Equal(t, tc.want, persistence.completion.Action, "completion failed to carry current authority: %#v, %#v", value, persistence.completion)
			require.Equal(t, f.userID, persistence.completion.Principal.UserID, "completion failed to carry current authority: %#v, %#v", value, persistence.completion)
			require.Equal(t, store.ExamRecordsCompleteOperation, persistence.idempotency.Operation, "completion failed to carry current authority: %#v, %#v", value, persistence.completion)
			// Contract (guardrail 8): authorization precedes the audit, which begins
			// before the durable completion. Only a Manager's membership is consulted.
			ordertest.RequireInOrder(t, *f.order, "authorize", "audit.begin", "records.complete")
			require.Equal(t, tc.manager, slices.Contains(*f.order, "membership"), "membership consulted")
		})
	}
}

func TestRecordsRejectsSelfWaiverWithDurableDenial(t *testing.T) {
	f, records, persistence, authorizer := newRecordsFixture(t)
	persistence.scope.Scope.SubmissionID = model.NewSubmissionID()
	persistence.scope.CandidateUserID = f.userID
	_, err := records.WaiveSubmissionReview(context.Background(), f.invocation, WaiveReviewCommand{Scope: persistence.scope.Scope,
		ReasonCode: "review_not_required", PrivateReason: "Private institutional rationale", IdempotencyKey: "waiver-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "self waiver")
	require.Equal(t, "resource.not_found", fault.Code(), "self waiver")
	require.True(t, authorizer.denied, "self waiver")
	require.Nil(t, persistence.waiver, "self waiver")
	require.NotContains(t, *f.order, "records.waive", "a self waiver reached the Store")
	_, err = records.GetSubmissionReviewWaiver(context.Background(), f.invocation, persistence.scope.Scope)
	require.Error(t, err, "candidate read private waiver rationale")
}

func TestRecordsWaiverKeepsPrivateRationaleOutOfAudit(t *testing.T) {
	f, records, persistence, _ := newRecordsFixture(t)
	persistence.scope.Scope.SubmissionID = model.NewSubmissionID()
	private := "Private candidate evidence and rationale."
	value, err := records.WaiveSubmissionReview(context.Background(), f.invocation, WaiveReviewCommand{Scope: persistence.scope.Scope,
		ExpectedReviewRevision: 3, ExpectedDiscrepancyCount: 2, ReasonCode: "review_not_required", PrivateReason: private, IdempotencyKey: "waiver-key"})
	require.NoError(t, err)
	require.Equal(t, private, value.PrivateReason)
	require.Equal(t, private, persistence.waiver.PrivateReason)
	require.Equal(t, int64(2), persistence.waiver.ExpectedDiscrepancyCount)
	for _, field := range f.auditor.value {
		text, ok := field.(string)
		if ok {
			require.NotContains(t, text, private, "private rationale entered audit")
		}
	}
	require.Equal(t, "review_not_required", f.auditor.value["reason_code"])
	require.Nil(t, f.auditor.value["private_reason"])
}

func TestRecordsIdempotencyAndAuditFailClosed(t *testing.T) {
	f, records, persistence, _ := newRecordsFixture(t)
	command := CompleteRecordsCommand{Scope: persistence.scope.Scope, ExpectedRevision: 1}
	_, err := records.CompleteExamSittingRecords(context.Background(), f.invocation, command)
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "required key")
	require.Equal(t, "idempotency.key_required", fault.Code(), "required key")
	require.Empty(t, *f.order, "required key")
	command.IdempotencyKey = "completion-key"
	persistence.err = store.NewErrConflict("authorization", "authority", nil)
	_, err = records.CompleteExamSittingRecords(context.Background(), f.invocation, command)
	require.ErrorAs(t, err, &fault, "current authority failure")
	require.Equal(t, "resource.not_found", fault.Code(), "current authority failure")
	require.Equal(t, "exam.not_found", f.auditor.failedCode, "current authority failure")
}

func TestRecordsHoldReleaseRequiresStrongRecentProof(t *testing.T) {
	for _, tc := range []struct {
		name           string
		strong, recent bool
		want           string
	}{
		{"single factor", false, true, "authentication.strong_required"},
		{"stale strong proof", true, false, "authentication.reauthentication_required"},
		{"fresh strong proof", true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, records, persistence, _ := newRecordsFixture(t)
			principal := f.invocation.Principal()
			principal.AuthenticationStrength = model.AuthenticationSingleFactor
			if tc.strong {
				principal.AuthenticationStrength = model.AuthenticationMultiFactor
			}
			principal.AuthenticatedAt = fixedPublicationTime().Add(-time.Minute)
			if !tc.recent {
				principal.AuthenticatedAt = principal.AuthenticatedAt.Add(-time.Hour)
			}
			invocation := invocation.New(principal, model.RequestMetadata{})
			_, err := records.ReleaseRetentionHold(context.Background(), invocation, ReleaseRetentionHoldCommand{Scope: persistence.scope.Scope,
				HoldID: model.NewRetentionHoldID(), ExpectedRevision: 1, ReasonCode: "case_closed", PrivateReason: "Case resolved.", IdempotencyKey: "release-key"})
			if tc.want == "" {
				require.NoError(t, err, "release")
				require.NotNil(t, persistence.release, "release")
				require.Equal(t, 5*time.Minute, persistence.release.RecentAuthenticationTTL, "release")
				return
			}
			var fault *apperror.Error
			require.ErrorAs(t, err, &fault, "release")
			require.Equal(t, tc.want, fault.Code(), "release")
			require.Nil(t, persistence.release, "release")
		})
	}
}

func newRecordsFixture(t *testing.T) (authoringFixture, *Records, *recordsStoreFake, *recordsAuthorizerFake) {
	t.Helper()
	f := newAuthoringFixture(t)
	persistence := &recordsStoreFake{order: f.order, scope: store.ExamRecordsScope{Scope: model.RetentionHoldScope{ExamID: f.examID, SittingID: model.NewExamSittingID()},
		InstitutionID: model.NewInstitutionID(), AcademicUnitID: f.unitID, CandidateUserID: model.NewUserID(), AttemptID: model.NewExamAttemptID()}}
	authorizer := &recordsAuthorizerFake{authorizerFake: f.authorizer}
	records, err := NewRecords(RecordsDependencies{
		Persistence: persistence,
		Access:      f.persistence,
		Memberships: f.memberships,
		Authorizer:  authorizer,
		Auditor:     f.auditor,
		RecentTTL:   5 * time.Minute,
		Now:         fixedPublicationTime,
		NewHoldID:   model.NewRetentionHoldID,
	})
	require.NoError(t, err)
	return f, records, persistence, authorizer
}

type recordsAuthorizerFake struct {
	*authorizerFake
	denied bool
}

func (f *recordsAuthorizerFake) RecordDenial(_ context.Context, _ invocation.Invocation, _ model.Action, _ model.Resource, _ model.AcademicUnitID) error {
	*f.order = append(*f.order, "records.deny_self")
	f.denied = true
	return nil
}

type recordsStoreFake struct {
	store.ExamRecordsStore
	order       *[]string
	scope       store.ExamRecordsScope
	completion  *store.ExamRecordsCompletion
	waiver      *store.ExamRecordsReviewWaiver
	release     *store.ExamRecordsHoldRelease
	idempotency *store.CommandIdempotency
	err         error
}

func (f *recordsStoreFake) Resolve(_ context.Context, scope model.RetentionHoldScope) (*store.ExamRecordsScope, error) {
	*f.order = append(*f.order, "records.resolve")
	out := f.scope
	out.Scope = scope
	return &out, nil
}
func (f *recordsStoreFake) CompleteRecords(_ context.Context, input *store.ExamRecordsCompletion, key *store.CommandIdempotency) (*store.ExamRecordsCompletionResult, error) {
	*f.order = append(*f.order, "records.complete")
	f.completion, f.idempotency = input, key
	if f.err != nil {
		return nil, f.err
	}
	c := model.NewExamSittingRecordsCompletion(input.Scope.SittingID)
	_, err := c.Complete(input.ExpectedRevision, input.AcknowledgedEvidenceRevision, input.Principal.UserID, fixedPublicationTime())
	return &store.ExamRecordsCompletionResult{Completion: c}, err
}
func (f *recordsStoreFake) WaiveReview(_ context.Context, input *store.ExamRecordsReviewWaiver, key *store.CommandIdempotency) (*store.ExamRecordsWaiverResult, error) {
	*f.order = append(*f.order, "records.waive")
	f.waiver, f.idempotency = input, key
	w := &model.SubmissionReviewWaiver{SubmissionID: input.Scope.SubmissionID, Revision: input.ExpectedRevision + 1, ReviewRevision: input.ExpectedReviewRevision,
		DiscrepancyCount: input.ExpectedDiscrepancyCount, ActorUserID: input.Principal.UserID, RecordedAt: fixedPublicationTime(), ReasonCode: input.ReasonCode, PrivateReason: input.PrivateReason}
	return &store.ExamRecordsWaiverResult{Waiver: w}, f.err
}
func (f *recordsStoreFake) ReleaseHold(_ context.Context, input *store.ExamRecordsHoldRelease, key *store.CommandIdempotency) (*store.ExamRecordsHoldResult, error) {
	f.release, f.idempotency = input, key
	h := &model.RetentionHold{ID: input.HoldID, Scope: input.Scope, Revision: input.ExpectedRevision, CreatedAt: fixedPublicationTime().Add(-time.Hour),
		CreatedByUserID: model.NewUserID(), ReasonCode: "integrity_review", PrivateReason: "Preserve."}
	err := h.Release(input.ExpectedRevision, input.Principal.UserID, input.ReasonCode, input.PrivateReason, fixedPublicationTime())
	return &store.ExamRecordsHoldResult{Hold: h}, err
}
