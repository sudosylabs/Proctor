// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sitting

import (
	"errors"
	"time"

	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	idempotencyOperationSchedule       = "exam.sitting.schedule.v1"
	idempotencyOperationUpdateSchedule = "exam.sitting.schedule.update.v1"
	idempotencyOperationCancel         = "exam.sitting.cancel.v1"
	idempotencyOperationPause          = "exam.sitting.pause.v1"
	idempotencyOperationResume         = "exam.sitting.resume.v1"
	idempotencyOperationExtend         = "exam.sitting.extend.v1"
	idempotencyOperationClose          = "exam.sitting.close.v1"
)

func prepareIdempotency(call Call, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, &Fault{Code: "idempotency.key_required"}
	}
	prepared, err := applicationidempotency.Prepare(call.Principal().UserID, operation, key, semantic)
	var encodingError *applicationidempotency.SemanticEncodingError
	switch {
	case errors.Is(err, applicationidempotency.ErrInvalidPrincipal):
		return nil, &Fault{Code: "idempotency.invalid_key", Cause: err}
	case errors.As(err, &encodingError):
		return nil, &Fault{Code: "request.invalid", Cause: err}
	default:
		return prepared, err
	}
}

func prepareScheduleIdempotency(call Call, command ScheduleCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, idempotencyOperationSchedule, command.IdempotencyKey, struct {
		ExamID           model.ExamID         `json:"exam_id"`
		ExamRevisionID   model.ExamRevisionID `json:"exam_revision_id"`
		ClassID          model.ClassID        `json:"class_id"`
		ScheduledStartAt time.Time            `json:"scheduled_start_at"`
		ScheduledEndAt   time.Time            `json:"scheduled_end_at"`
	}{command.ExamID, command.ExamRevisionID, command.ClassID, command.ScheduledStartAt, command.ScheduledEndAt})
}

func prepareScheduleUpdateIdempotency(call Call, command UpdateScheduleCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, idempotencyOperationUpdateSchedule, command.IdempotencyKey, struct {
		ExamID           model.ExamID          `json:"exam_id"`
		SittingID        model.ExamSittingID   `json:"exam_sitting_id"`
		ExpectedRevision int64                 `json:"expected_revision"`
		ExamRevisionID   *model.ExamRevisionID `json:"exam_revision_id,omitempty"`
		ClassID          *model.ClassID        `json:"class_id,omitempty"`
		ScheduledStartAt *time.Time            `json:"scheduled_start_at,omitempty"`
		ScheduledEndAt   *time.Time            `json:"scheduled_end_at,omitempty"`
	}{command.ExamID, command.SittingID, command.ExpectedRevision, command.ExamRevisionID, command.ClassID, command.ScheduledStartAt, command.ScheduledEndAt})
}

func prepareTransitionIdempotency(call Call, operation string, command PauseCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, operation, command.IdempotencyKey, struct {
		ExamID           model.ExamID        `json:"exam_id"`
		SittingID        model.ExamSittingID `json:"exam_sitting_id"`
		ExpectedRevision int64               `json:"expected_revision"`
		PrivateReason    string              `json:"private_reason"`
	}{command.ExamID, command.SittingID, command.ExpectedRevision, command.PrivateReason})
}

func prepareExtensionIdempotency(call Call, command ExtendCommand) (*store.CommandIdempotency, error) {
	return prepareIdempotency(call, idempotencyOperationExtend, command.IdempotencyKey, struct {
		ExamID           model.ExamID        `json:"exam_id"`
		SittingID        model.ExamSittingID `json:"exam_sitting_id"`
		ExpectedRevision int64               `json:"expected_revision"`
		ScheduledEndAt   time.Time           `json:"scheduled_end_at"`
		PrivateReason    string              `json:"private_reason"`
	}{command.ExamID, command.SittingID, command.ExpectedRevision, command.ScheduledEndAt, command.PrivateReason})
}

func canonicalTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	canonical := model.TimeUTC(*value)
	return &canonical
}
