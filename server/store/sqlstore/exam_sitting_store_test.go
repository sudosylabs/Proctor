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
		ExpectedRevision: 1, PrivateReason: "é", CanceledAt: model.NowUTC(), AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
		Mail: &store.ExamSittingMailFanout{}}
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

func TestValidateExamSittingLifecycleJob(t *testing.T) {
	sittingID := model.NewExamSittingID()
	availableAt := model.NowUTC().Add(time.Hour)
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := model.ExamSittingLifecycleDedupeKey(sittingID, model.ExamSittingLifecycleJobOpen, 2)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingLifecycle, 1, command, key,
		model.JobDedupeActive, model.NowUTC(), availableAt, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateExamSittingLifecycleJob(valid, sittingID, model.ExamSittingLifecycleJobOpen, 2, availableAt); err != nil {
		t.Fatalf("valid lifecycle Job: %v", err)
	}
	mutations := []func(*model.Job){
		func(job *model.Job) { job.Type = model.JobTypeCleanup },
		func(job *model.Job) { job.DedupePolicy = model.JobDedupePermanent },
		func(job *model.Job) { job.DedupeKey += "-wrong" },
		func(job *model.Job) { job.AvailableAt = job.AvailableAt.Add(time.Second) },
		func(job *model.Job) {
			job.Command = []byte(`{"exam_sitting_id":"` + model.NewExamSittingID().String() + `"}`)
		},
	}
	for index, mutate := range mutations {
		candidate := *valid
		candidate.Command = append([]byte(nil), valid.Command...)
		mutate(&candidate)
		var invalid *store.ErrInvalidInput
		if err = validateExamSittingLifecycleJob(&candidate, sittingID, model.ExamSittingLifecycleJobOpen, 2, availableAt); !errors.As(err, &invalid) {
			t.Errorf("mutation[%d] error=%v", index, err)
		}
	}
	finalizeKey, err := model.ExamSittingLifecycleDedupeKey(sittingID, model.ExamSittingLifecycleJobFinalize, 3)
	if err != nil {
		t.Fatal(err)
	}
	finalize, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingSealing, 1, command,
		finalizeKey, model.JobDedupeActive, model.NowUTC(), availableAt, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateExamSittingLifecycleJob(finalize, sittingID, model.ExamSittingLifecycleJobFinalize, 3, availableAt); err != nil {
		t.Fatalf("valid sealing Job: %v", err)
	}
	wrongType := *finalize
	wrongType.Type = model.JobTypeExamSittingLifecycle
	var invalid *store.ErrInvalidInput
	if err = validateExamSittingLifecycleJob(&wrongType, sittingID, model.ExamSittingLifecycleJobFinalize, 3, availableAt); !errors.As(err, &invalid) {
		t.Fatalf("finalize lifecycle Job error = %v", err)
	}
}

func TestPrepareExamSittingManagerTransitionRequiresPhaseSpecificFinalizeJob(t *testing.T) {
	base := &store.ExamSittingManagerTransition{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ActorUserID: model.NewUserID(),
		ExpectedRevision: 2, PrivateReason: "manager reason", ChangedAt: model.NowUTC(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	if err := prepareExamSittingManagerTransition(base, false); err != nil {
		t.Fatalf("pause/resume transition: %v", err)
	}
	if err := prepareExamSittingManagerTransition(base, true); err == nil {
		t.Fatal("early close accepted missing Finalize Job")
	}
	withFinalize := *base
	withFinalize.FinalizeJob = &model.Job{}
	if err := prepareExamSittingManagerTransition(&withFinalize, true); err != nil {
		t.Fatalf("early close shape: %v", err)
	}
	if err := prepareExamSittingManagerTransition(&withFinalize, false); err == nil {
		t.Fatal("pause/resume accepted Finalize Job")
	}
}

func TestStaleExamSittingFinalizeJobIsRevisionConflictCandidate(t *testing.T) {
	now := model.NowUTC()
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		now.Add(-time.Hour), now.Add(-time.Minute), now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = sitting.Open(now.Add(-50 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = sitting.Pause(now.Add(-40 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sitting.ID})
	if err != nil {
		t.Fatal(err)
	}
	staleKey, err := model.ExamSittingLifecycleDedupeKey(sitting.ID, model.ExamSittingLifecycleJobFinalize, sitting.Revision)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingSealing, 1, command, staleKey,
		model.JobDedupeActive, now, sitting.ScheduledEndAt, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !isStaleExamSittingFinalizeJob(stale, sitting) {
		t.Fatal("well-formed finalizer prepared for the prior Sitting revision was not recognized as stale")
	}
	stale.AvailableAt = stale.AvailableAt.Add(time.Second)
	if isStaleExamSittingFinalizeJob(stale, sitting) {
		t.Fatal("malformed finalizer was classified as a retryable stale revision")
	}
}
