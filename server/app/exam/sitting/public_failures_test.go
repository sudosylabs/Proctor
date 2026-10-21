// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sitting

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
)

func TestPublicFailuresConcealDenialAndAbsence(t *testing.T) {
	t.Parallel()
	want := apperror.Concealment{Unavailable: "exam.sitting.unavailable", Concealed: []apperror.Code{"exam.sitting.not_found"}}
	require.Equal(t, want, publicFailures, "publicFailures")
	for _, code := range append([]apperror.Code{"authorization.denied"}, want.Concealed...) {
		cause := apperror.New(code)
		err := publicFailures.Conceal(cause)
		require.True(t, apperror.Is(err, "resource.not_found"), "Conceal(%s)", code)
		require.ErrorIs(t, err, cause, "Conceal(%s)", code)
	}
	safe := apperror.Invalid("request.invalid", "field")
	err := publicFailures.Conceal(safe)
	require.True(t, apperror.Is(err, "request.invalid"), "Conceal(request.invalid)")
	require.ErrorIs(t, err, safe, "Conceal(request.invalid)")
	dependency := errors.New("dependency failed")
	err = publicFailures.Conceal(dependency)
	require.True(t, apperror.Is(err, want.Unavailable), "Conceal(dependency) = %v, want wrapped %s", err, want.Unavailable)
	require.ErrorIs(t, err, dependency, "Conceal(dependency) = %v, want wrapped %s", err, want.Unavailable)
}

type operationalRecorderFake struct {
	subsystem, event string
	err              error
}

func (f *operationalRecorderFake) RecordOperational(subsystem, event string, err error) {
	f.subsystem, f.event, f.err = subsystem, event, err
}

func TestObservedUseCaseRecordsItsConcealedOutcome(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	recorder := &operationalRecorderFake{}
	f.service.deps.Operational = recorder
	f.authorizer.err = apperror.New("authorization.denied")
	_, err := f.service.PauseExamSitting(t.Context(), f.invocation, PauseCommand{
		ExamID: f.examID, SittingID: f.sittingID, ExpectedRevision: 1,
		PrivateReason: "Candidate requested clarification", IdempotencyKey: "test-key",
	})
	require.True(t, apperror.Is(err, "resource.not_found"), "PauseExamSitting error = %v, want concealed resource.not_found", err)
	require.Equal(t, "exam_sitting", recorder.subsystem, "recorded %s/%s %v, want exam_sitting/pause with the concealed failure", recorder.subsystem, recorder.event, recorder.err)
	require.Equal(t, "pause", recorder.event, "recorded %s/%s %v, want exam_sitting/pause with the concealed failure", recorder.subsystem, recorder.event, recorder.err)
	require.True(t, apperror.Is(recorder.err, "resource.not_found"), "recorded %s/%s %v, want exam_sitting/pause with the concealed failure", recorder.subsystem, recorder.event, recorder.err)
}
