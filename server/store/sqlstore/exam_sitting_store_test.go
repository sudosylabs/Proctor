// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestValidateExamSittingListOptions(t *testing.T) {
	examID := model.NewExamID()
	sittingID := model.NewExamSittingID()
	now := model.NowUTC()
	valid := store.ExamSittingListOptions{ExamID: examID, Limit: 201}
	if err := validateExamSittingListOptions(valid); err != nil {
		t.Fatalf("valid options: %v", err)
	}
	invalid := []store.ExamSittingListOptions{
		{ExamID: examID, Limit: 202},
		{ExamID: examID, Limit: 1, OverlapStartAt: now},
		{ExamID: examID, Limit: 1, OverlapStartAt: now, OverlapEndAt: now},
		{ExamID: examID, Limit: 1, BeforeScheduledStartAt: now},
		{ExamID: examID, Limit: 1, BeforeSittingID: sittingID},
		{ExamID: examID, Limit: 1, States: []model.ExamSittingState{model.ExamSittingScheduled, model.ExamSittingScheduled}},
		{ExamID: examID, Limit: 1, States: []model.ExamSittingState{"invented"}},
	}
	for index, options := range invalid {
		var invalid *store.ErrInvalidInput
		if err := validateExamSittingListOptions(options); !errors.As(err, &invalid) {
			t.Errorf("invalid[%d] error=%v", index, err)
		}
	}
}

func TestPrepareExamSittingCancellationBoundsPrivateReason(t *testing.T) {
	valid := &store.ExamSittingCancellation{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ActorUserID: model.NewUserID(),
		ExpectedRevision: 1, PrivateReason: "é", CanceledAt: model.NowUTC(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	if err := prepareExamSittingCancellation(valid); err != nil {
		t.Fatalf("valid cancellation: %v", err)
	}
	for _, reason := range []string{"", " padded", strings.Repeat("a", 1001), string([]byte{0xff})} {
		candidate := *valid
		candidate.PrivateReason = reason
		var invalid *store.ErrInvalidInput
		if err := prepareExamSittingCancellation(&candidate); !errors.As(err, &invalid) {
			t.Errorf("reason=%q error=%v", reason, err)
		}
	}
}

func TestExamSittingAuditExcludesPrivateContent(t *testing.T) {
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		model.NowUTC().Add(time.Hour), model.NowUTC().Add(2*time.Hour), model.NowUTC())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeExamSittingAudit(sitting, true, model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private")) {
		t.Fatalf("audit unexpectedly contains private field: %s", encoded)
	}
}
