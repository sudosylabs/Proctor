// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sitting

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertStoreBoundaryCommand(t *testing.T, got, want *store.CommandIdempotency) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Store idempotency = %#v; want %#v", got, want)
	}
}

func assertPreparedIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	if got == nil || got.UserID != userID || got.Operation != operation || got.KeyDigest != wantKey ||
		got.FingerprintVersion != 1 || got.Fingerprint != wantFingerprint || got.OutcomeVersion != 1 ||
		got.Retention != 24*time.Hour || got.Wait != 2*time.Second {
		t.Fatalf("prepared idempotency = %#v; want user=%s operation=%q key=%x fingerprint=%x versions=1/1 retention=24h wait=2s",
			got, userID, operation, wantKey, wantFingerprint)
	}
}

func TestIdempotencyDocumentsAndStoreBoundaryCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	call := NewCall(model.Principal{UserID: userID}, model.RequestMetadata{})
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	revisionID, classID := model.NewExamRevisionID(), model.NewClassID()
	start := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	scheduled, err := prepareScheduleIdempotency(call, ScheduleCommand{ExamID: examID, ExamRevisionID: revisionID,
		ClassID: classID, ScheduledStartAt: start, ScheduledEndAt: end, IdempotencyKey: "schedule-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, scheduled, userID, idempotencyOperationSchedule, "schedule-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_revision_id":%q,"class_id":%q,"scheduled_start_at":"2026-08-24T10:00:00Z","scheduled_end_at":"2026-08-24T12:00:00Z"}`, examID, revisionID, classID))

	changedEnd := end.Add(time.Hour)
	updated, err := prepareScheduleUpdateIdempotency(call, UpdateScheduleCommand{ExamID: examID, SittingID: sittingID,
		ExpectedRevision: 2, ScheduledEndAt: &changedEnd, IdempotencyKey: "update-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, updated, userID, idempotencyOperationUpdateSchedule, "update-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"expected_revision":2,"scheduled_end_at":"2026-08-24T13:00:00Z"}`, examID, sittingID))

	transition := PauseCommand{ExamID: examID, SittingID: sittingID, ExpectedRevision: 3, PrivateReason: "room unavailable", IdempotencyKey: "transition-key"}
	for _, operation := range []string{idempotencyOperationCancel, idempotencyOperationPause, idempotencyOperationResume, idempotencyOperationClose} {
		prepared, prepareErr := prepareTransitionIdempotency(call, operation, transition)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		assertPreparedIdempotency(t, prepared, userID, operation, "transition-key",
			fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"expected_revision":3,"private_reason":"room unavailable"}`, examID, sittingID))
	}

	extended, err := prepareExtensionIdempotency(call, ExtendCommand{ExamID: examID, SittingID: sittingID,
		ExpectedRevision: 4, ScheduledEndAt: changedEnd, PrivateReason: "extra time", IdempotencyKey: "extend-key"})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedIdempotency(t, extended, userID, idempotencyOperationExtend, "extend-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"expected_revision":4,"scheduled_end_at":"2026-08-24T13:00:00Z","private_reason":"extra time"}`, examID, sittingID))
}

func TestIdempotencyOperationCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		idempotencyOperationSchedule:       "exam.sitting.schedule.v1",
		idempotencyOperationUpdateSchedule: "exam.sitting.schedule.update.v1",
		idempotencyOperationCancel:         "exam.sitting.cancel.v1",
		idempotencyOperationPause:          "exam.sitting.pause.v1",
		idempotencyOperationResume:         "exam.sitting.resume.v1",
		idempotencyOperationExtend:         "exam.sitting.extend.v1",
		idempotencyOperationClose:          "exam.sitting.close.v1",
	}
	if len(tests) != 7 {
		t.Fatalf("operation set collapsed: %#v", tests)
	}
	for operation, want := range tests {
		if operation != want {
			t.Errorf("operation = %q; want %q", operation, want)
		}
	}
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	call := NewCall(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := prepareIdempotency(call, "operation", "", struct{}{})
	if fault, ok := err.(*Fault); !ok || fault.Code != "idempotency.key_required" {
		t.Fatalf("error = %v", err)
	}
}
