// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sitting

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func assertPreparedIdempotency(t *testing.T, got *store.CommandIdempotency, userID model.UserID, operation, key, document string) {
	t.Helper()
	wantKey := sha256.Sum256([]byte(key))
	wantFingerprint := sha256.Sum256([]byte(operation + "\x00v1\x00" + document))
	require.NotNil(t, got, "prepared idempotency")
	require.Equal(t, userID, got.UserID, "prepared idempotency")
	require.Equal(t, operation, got.Operation, "prepared idempotency")
	require.Equal(t, wantKey, got.KeyDigest, "prepared idempotency")
	require.Equal(t, 1, got.FingerprintVersion, "prepared idempotency")
	require.Equal(t, wantFingerprint, got.Fingerprint, "prepared idempotency")
	require.Equal(t, 1, got.OutcomeVersion, "prepared idempotency")
	require.Equal(t, 24*time.Hour, got.Retention, "prepared idempotency")
	require.Equal(t, 2*time.Second, got.Wait, "prepared idempotency")
}

func TestIdempotencyDocumentsAndStoreBoundaryCompatibility(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	invocation := invocation.New(model.Principal{UserID: userID}, model.RequestMetadata{})
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	revisionID, classID := model.NewExamRevisionID(), model.NewClassID()
	start := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	scheduled, err := prepareScheduleIdempotency(invocation, ScheduleCommand{ExamID: examID, ExamRevisionID: revisionID,
		ClassID: classID, ScheduledStartAt: start, ScheduledEndAt: end, IdempotencyKey: "schedule-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, scheduled, userID, idempotencyOperationSchedule, "schedule-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_revision_id":%q,"class_id":%q,"scheduled_start_at":"2026-08-24T10:00:00Z","scheduled_end_at":"2026-08-24T12:00:00Z"}`, examID, revisionID, classID))

	changedEnd := end.Add(time.Hour)
	updated, err := prepareScheduleUpdateIdempotency(invocation, UpdateScheduleCommand{ExamID: examID, SittingID: sittingID,
		ExpectedRevision: 2, ScheduledEndAt: &changedEnd, IdempotencyKey: "update-key"})
	require.NoError(t, err)
	assertPreparedIdempotency(t, updated, userID, idempotencyOperationUpdateSchedule, "update-key",
		fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"expected_revision":2,"scheduled_end_at":"2026-08-24T13:00:00Z"}`, examID, sittingID))

	transition := PauseCommand{ExamID: examID, SittingID: sittingID, ExpectedRevision: 3, PrivateReason: "room unavailable", IdempotencyKey: "transition-key"}
	for _, operation := range []string{idempotencyOperationCancel, idempotencyOperationPause, idempotencyOperationResume, idempotencyOperationClose} {
		prepared, prepareErr := prepareTransitionIdempotency(invocation, operation, transition)
		require.NoError(t, prepareErr)
		assertPreparedIdempotency(t, prepared, userID, operation, "transition-key",
			fmt.Sprintf(`{"exam_id":%q,"exam_sitting_id":%q,"expected_revision":3,"private_reason":"room unavailable"}`, examID, sittingID))
	}

	extended, err := prepareExtensionIdempotency(invocation, ExtendCommand{ExamID: examID, SittingID: sittingID,
		ExpectedRevision: 4, ScheduledEndAt: changedEnd, PrivateReason: "extra time", IdempotencyKey: "extend-key"})
	require.NoError(t, err)
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
	require.Len(t, tests, 7)
	for operation, want := range tests {
		assert.Equal(t, want, operation, "operation")
	}
}

func TestPrepareIdempotencyRequiresKey(t *testing.T) {
	t.Parallel()
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	_, err := applicationidempotency.Require(invocation, "operation", "", struct{}{})
	fault, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "idempotency.key_required", fault.Code())
}
