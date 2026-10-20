// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sitting

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/app/apperror"
)

func TestPublicFailuresConcealDenialAndAbsence(t *testing.T) {
	t.Parallel()
	want := apperror.Concealment{Unavailable: "exam.sitting.unavailable", Concealed: []apperror.Code{"exam.sitting.not_found"}}
	if !reflect.DeepEqual(publicFailures, want) {
		t.Fatalf("publicFailures = %#v, want %#v", publicFailures, want)
	}
	for _, code := range append([]apperror.Code{"authorization.denied"}, want.Concealed...) {
		cause := apperror.New(code)
		if err := publicFailures.Conceal(cause); !apperror.Is(err, "resource.not_found") || !errors.Is(err, cause) {
			t.Fatalf("Conceal(%s) = %v, want wrapped resource.not_found", code, err)
		}
	}
	safe := apperror.Invalid("request.invalid", "field")
	if err := publicFailures.Conceal(safe); !apperror.Is(err, "request.invalid") || !errors.Is(err, safe) {
		t.Fatalf("Conceal(request.invalid) = %v, want unchanged", err)
	}
	dependency := errors.New("dependency failed")
	if err := publicFailures.Conceal(dependency); !apperror.Is(err, want.Unavailable) || !errors.Is(err, dependency) {
		t.Fatalf("Conceal(dependency) = %v, want wrapped %s", err, want.Unavailable)
	}
}

type operationalRecorderFake struct {
	subsystem, event string
	err              error
}

func (fake *operationalRecorderFake) RecordOperational(subsystem, event string, err error) {
	fake.subsystem, fake.event, fake.err = subsystem, event, err
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
	if !apperror.Is(err, "resource.not_found") {
		t.Fatalf("PauseExamSitting error = %v, want concealed resource.not_found", err)
	}
	if recorder.subsystem != "exam_sitting" || recorder.event != "pause" || !apperror.Is(recorder.err, "resource.not_found") {
		t.Fatalf("recorded %s/%s %v, want exam_sitting/pause with the concealed failure", recorder.subsystem, recorder.event, recorder.err)
	}
}
