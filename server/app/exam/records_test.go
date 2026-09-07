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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
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
			value, err := records.CompleteRecords(context.Background(), f.call, command)
			if err != nil {
				t.Fatal(err)
			}
			if !value.IsCurrent() || f.authorizer.action != tc.want || persistence.completion.Action != tc.want ||
				persistence.completion.Principal.UserID != f.userID || persistence.idempotency.Operation != store.ExamRecordsCompleteOperation {
				t.Fatalf("completion failed to carry current authority: %#v, %#v", value, persistence.completion)
			}
			want := []string{"records.resolve", "store.access", "membership", "authorize", "audit.begin", "records.complete"}
			if !tc.manager {
				want = []string{"records.resolve", "store.access", "authorize", "audit.begin", "records.complete"}
			}
			if !reflect.DeepEqual(*f.order, want) {
				t.Fatalf("order = %v, want %v", *f.order, want)
			}
		})
	}
}

func TestRecordsRejectsSelfWaiverWithDurableDenial(t *testing.T) {
	f, records, persistence, authorizer := newRecordsFixture(t)
	persistence.scope.Scope.SubmissionID = model.NewSubmissionID()
	persistence.scope.CandidateUserID = f.userID
	_, err := records.WaiveReview(context.Background(), f.call, WaiveReviewCommand{Scope: persistence.scope.Scope,
		ReasonCode: "review_not_required", PrivateReason: "Private institutional rationale", IdempotencyKey: "waiver-key"})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.not_found" || !authorizer.denied || persistence.waiver != nil {
		t.Fatalf("self waiver = %v, denied=%v, mutation=%#v", err, authorizer.denied, persistence.waiver)
	}
	if !reflect.DeepEqual(*f.order, []string{"records.resolve", "records.deny_self"}) {
		t.Fatalf("self access order = %v", *f.order)
	}
	if _, err = records.FindReviewWaiver(context.Background(), f.call, persistence.scope.Scope); err == nil {
		t.Fatal("candidate read private waiver rationale")
	}
}

func TestRecordsWaiverKeepsPrivateRationaleOutOfAudit(t *testing.T) {
	f, records, persistence, _ := newRecordsFixture(t)
	persistence.scope.Scope.SubmissionID = model.NewSubmissionID()
	private := "Private candidate evidence and rationale."
	value, err := records.WaiveReview(context.Background(), f.call, WaiveReviewCommand{Scope: persistence.scope.Scope,
		ExpectedReviewRevision: 3, ExpectedDiscrepancyCount: 2, ReasonCode: "review_not_required", PrivateReason: private, IdempotencyKey: "waiver-key"})
	if err != nil {
		t.Fatal(err)
	}
	if value.PrivateReason != private || persistence.waiver.PrivateReason != private || persistence.waiver.ExpectedDiscrepancyCount != 2 {
		t.Fatalf("waiver aggregate lost private data/inventory: %#v", persistence.waiver)
	}
	for _, field := range f.auditor.value {
		if text, ok := field.(string); ok && strings.Contains(text, private) {
			t.Fatal("private rationale entered audit")
		}
	}
	if f.auditor.value["reason_code"] != "review_not_required" || f.auditor.value["private_reason"] != nil {
		t.Fatalf("audit = %#v", f.auditor.value)
	}
}

func TestRecordsIdempotencyAndAuditFailClosed(t *testing.T) {
	f, records, persistence, _ := newRecordsFixture(t)
	command := CompleteRecordsCommand{Scope: persistence.scope.Scope, ExpectedRevision: 1}
	_, err := records.CompleteRecords(context.Background(), f.call, command)
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "idempotency.key_required" || len(*f.order) != 0 {
		t.Fatalf("required key = %v, order=%v", err, *f.order)
	}
	command.IdempotencyKey = "completion-key"
	persistence.err = store.NewErrConflict("authorization", "authority", nil)
	_, err = records.CompleteRecords(context.Background(), f.call, command)
	if !errors.As(err, &fault) || fault.Code != "exam.not_found" || f.auditor.failedCode != "exam.not_found" {
		t.Fatalf("current authority failure = %v, audit=%s", err, f.auditor.failedCode)
	}
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
			principal := f.call.Principal()
			principal.AuthenticationStrength = model.AuthenticationSingleFactor
			if tc.strong {
				principal.AuthenticationStrength = model.AuthenticationMultiFactor
			}
			principal.AuthenticatedAt = fixedPublicationTime().Add(-time.Minute)
			if !tc.recent {
				principal.AuthenticatedAt = principal.AuthenticatedAt.Add(-time.Hour)
			}
			call := NewCall(principal, model.RequestMetadata{})
			_, err := records.ReleaseHold(context.Background(), call, ReleaseRetentionHoldCommand{Scope: persistence.scope.Scope,
				HoldID: model.NewRetentionHoldID(), ExpectedRevision: 1, ReasonCode: "case_closed", PrivateReason: "Case resolved.", IdempotencyKey: "release-key"})
			if tc.want == "" {
				if err != nil || persistence.release == nil || persistence.release.RecentAuthenticationTTL != 5*time.Minute {
					t.Fatalf("release = %v, %#v", err, persistence.release)
				}
				return
			}
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != tc.want || persistence.release != nil {
				t.Fatalf("release = %v, mutation=%#v", err, persistence.release)
			}
		})
	}
}

func newRecordsFixture(t *testing.T) (authoringFixture, *Records, *recordsStoreFake, *recordsAuthorizerFake) {
	t.Helper()
	f := newAuthoringFixture(t)
	persistence := &recordsStoreFake{order: f.order, scope: store.ExamRecordsScope{Scope: model.RetentionHoldScope{ExamID: f.examID, SittingID: model.NewExamSittingID()},
		InstitutionID: model.NewInstitutionID(), AcademicUnitID: f.unitID, CandidateUserID: model.NewUserID(), AttemptID: model.NewExamAttemptID()}}
	authorizer := &recordsAuthorizerFake{authorizerFake: f.authorizer}
	records, err := NewRecords(persistence, f.persistence, f.memberships, authorizer, f.auditor, 5*time.Minute, fixedPublicationTime, model.NewRetentionHoldID)
	if err != nil {
		t.Fatal(err)
	}
	return f, records, persistence, authorizer
}

type recordsAuthorizerFake struct {
	*authorizerFake
	denied bool
}

func (f *recordsAuthorizerFake) DenySelf(_ context.Context, _ Call, _ model.Action, _ model.Resource, _ model.AcademicUnitID) error {
	*f.order = append(*f.order, "records.deny_self")
	f.denied = true
	return &Fault{Code: "exam.not_found"}
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
