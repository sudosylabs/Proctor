// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestValidateExamSittingListOptions(t *testing.T) {
	examID := model.NewExamID()
	sittingID := model.NewExamSittingID()
	now := model.NowUTC()
	valid := store.ExamSittingListOptions{ExamID: examID, Limit: 201}
	require.NoError(t, validateExamSittingListOptions(valid))
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
		assert.ErrorAs(t, validateExamSittingListOptions(options), &invalid, "invalid[%d] error", index)
	}
}

func TestPrepareExamSittingCancellationBoundsPrivateReason(t *testing.T) {
	valid := &store.ExamSittingCancellation{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ActorUserID: model.NewUserID(),
		ExpectedRevision: 1, PrivateReason: "é", AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis(),
		Mail: &store.ExamSittingMailFanout{}}
	require.NoError(t, prepareExamSittingCancellation(valid))
	for _, reason := range []string{"", " padded", strings.Repeat("a", 1001), string([]byte{0xff})} {
		candidate := *valid
		candidate.PrivateReason = reason
		var invalid *store.ErrInvalidInput
		assert.ErrorAs(t, prepareExamSittingCancellation(&candidate), &invalid, "reason=%q error", reason)
	}
}

func TestExamSittingAuditExcludesPrivateContent(t *testing.T) {
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		model.NowUTC().Add(time.Hour), model.NowUTC().Add(2*time.Hour), model.NowUTC())
	require.NoError(t, err)
	encoded, err := encodeExamSittingAudit(sitting, true, model.NewID())
	require.NoError(t, err)
	require.False(t, bytes.Contains(encoded, []byte("private")), "audit unexpectedly contains private field: %s", encoded)
}

func TestValidateExamSittingLifecycleJob(t *testing.T) {
	sittingID := model.NewExamSittingID()
	availableAt := model.NowUTC().Add(time.Hour)
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	require.NoError(t, err)
	key, err := model.ExamSittingLifecycleDedupeKey(sittingID, model.ExamSittingLifecycleJobOpen, 2)
	require.NoError(t, err)
	valid, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingLifecycle, 1, command, key,
		model.JobDedupeActive, model.NowUTC(), availableAt, 8)
	require.NoError(t, err)
	err = validateExamSittingLifecycleJob(valid, sittingID, model.ExamSittingLifecycleJobOpen, 2, availableAt)
	require.NoError(t, err)
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
		err = validateExamSittingLifecycleJob(&candidate, sittingID, model.ExamSittingLifecycleJobOpen, 2, availableAt)
		assert.ErrorAs(t, err, &invalid, "mutation[%d] error", index)
	}
	finalizeKey, err := model.ExamSittingLifecycleDedupeKey(sittingID, model.ExamSittingLifecycleJobFinalize, 3)
	require.NoError(t, err)
	finalize, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingSealing, 1, command,
		finalizeKey, model.JobDedupeActive, model.NowUTC(), availableAt, 8)
	require.NoError(t, err)
	err = validateExamSittingLifecycleJob(finalize, sittingID, model.ExamSittingLifecycleJobFinalize, 3, availableAt)
	require.NoError(t, err)
	wrongType := *finalize
	wrongType.Type = model.JobTypeExamSittingLifecycle
	var invalid *store.ErrInvalidInput
	err = validateExamSittingLifecycleJob(&wrongType, sittingID, model.ExamSittingLifecycleJobFinalize, 3, availableAt)
	require.ErrorAs(t, err, &invalid)
}

func TestPrepareExamSittingManagerTransitionRequiresPhaseSpecificFinalizeJob(t *testing.T) {
	base := &store.ExamSittingManagerTransition{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), ActorUserID: model.NewUserID(),
		ExpectedRevision: 2, PrivateReason: "manager reason", ChangedAt: model.NowUTC(), AuditEventID: model.NewAuditEventID(), AuditAt: model.GetMillis()}
	require.NoError(t, prepareExamSittingManagerTransition(base, false))
	require.Error(t, prepareExamSittingManagerTransition(base, true), "early close accepted missing Finalize Job")
	withFinalize := *base
	withFinalize.FinalizeJob = &model.Job{}
	require.NoError(t, prepareExamSittingManagerTransition(&withFinalize, true))
	require.Error(t, prepareExamSittingManagerTransition(&withFinalize, false), "pause/resume accepted Finalize Job")
}

func TestStaleExamSittingFinalizeJobIsRevisionConflictCandidate(t *testing.T) {
	now := model.NowUTC()
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		now.Add(-time.Hour), now.Add(-time.Minute), now.Add(-2*time.Hour))
	require.NoError(t, err)
	err = sitting.Open(now.Add(-50 * time.Minute))
	require.NoError(t, err)
	err = sitting.Pause(now.Add(-40 * time.Minute))
	require.NoError(t, err)
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sitting.ID})
	require.NoError(t, err)
	staleKey, err := model.ExamSittingLifecycleDedupeKey(sitting.ID, model.ExamSittingLifecycleJobFinalize, sitting.Revision)
	require.NoError(t, err)
	stale, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingSealing, 1, command, staleKey,
		model.JobDedupeActive, now, sitting.ScheduledEndAt, 8)
	require.NoError(t, err)
	require.True(t, isStaleExamSittingFinalizeJob(stale, sitting), "well-formed finalizer prepared for the prior Sitting revision was not recognized as stale")
	stale.AvailableAt = stale.AvailableAt.Add(time.Second)
	require.False(t, isStaleExamSittingFinalizeJob(stale, sitting), "malformed finalizer was classified as a retryable stale revision")
}
