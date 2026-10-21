// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func TestConnectionRuntimeRejectsMissingOrUnknownFocusLossSchemaBeforeApplication(t *testing.T) {
	t.Parallel()
	credential := model.NewCredentialToken()
	for _, test := range []struct {
		name string
		body json.RawMessage
	}{
		{name: "missing", body: json.RawMessage(`{"generation":1,"sequence":1,"duration_milliseconds":500,"continuity_credential":"` + credential + `"}`)},
		{name: "unknown", body: json.RawMessage(`{"schema_version":2,"generation":1,"sequence":1,"duration_milliseconds":500,"continuity_credential":"` + credential + `"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &inboundTestApplication{}
			runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
			runtime.attempt = &examAttemptBinding{attemptID: model.NewExamAttemptID(),
				participationID: model.NewAttemptParticipationID(), connectionID: model.NewAttemptConnectionID(), generation: 1}

			runtime.handleRequest(context.Background(), &Request{Sequence: 1, Action: examAttemptFocusLossAction, Data: test.body})

			response := nextInboundResponse(t, runtime)
			require.Equal(t, "error", response.Status)
			require.NotNil(t, response.Error)
			require.Equal(t, "websocket.request.invalid", response.Error.Code)
			application.mu.Lock()
			defer application.mu.Unlock()
			require.Empty(t, application.focusLossCalls)
		})
	}
}

func TestDecodeExamAttemptFocusLossRequestAcceptsOnlyBoundedTrustedClaim(t *testing.T) {
	t.Parallel()
	credential := model.NewCredentialToken()
	valid := `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":86400000,"source":"document_hidden","continuity_credential":"` + credential + `"}`
	claim, err := decodeExamAttemptFocusLossRequest(json.RawMessage(valid))
	require.NoError(t, err, "valid claim = %#v", claim)
	require.Equal(t, model.FocusLossSignalSchemaVersion, claim.SchemaVersion, "valid claim = %#v", claim)
	require.Equal(t, int64(2), claim.Generation, "valid claim = %#v", claim)
	require.Equal(t, int64(7), claim.Sequence, "valid claim = %#v", claim)
	require.Equal(t, int64(86_400_000), claim.DurationMilliseconds, "valid claim = %#v", claim)
	require.Equal(t, "document_hidden", claim.Source, "valid claim = %#v", claim)
	require.Equal(t, credential, claim.ContinuityCredential, "valid claim = %#v", claim)

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "missing schema version", body: `{"generation":2,"sequence":7,"duration_milliseconds":1,"continuity_credential":"` + credential + `"}`},
		{name: "unknown schema version", body: `{"schema_version":2,"generation":2,"sequence":7,"duration_milliseconds":1,"continuity_credential":"` + credential + `"}`},
		{name: "zero generation", body: `{"schema_version":1,"generation":0,"sequence":7,"duration_milliseconds":1,"continuity_credential":"` + credential + `"}`},
		{name: "zero sequence", body: `{"schema_version":1,"generation":2,"sequence":0,"duration_milliseconds":1,"continuity_credential":"` + credential + `"}`},
		{name: "zero duration", body: `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":0,"continuity_credential":"` + credential + `"}`},
		{name: "duration above 24 hours", body: `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":86400001,"continuity_credential":"` + credential + `"}`},
		{name: "fractional duration", body: `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":1.5,"continuity_credential":"` + credential + `"}`},
		{name: "unknown source", body: `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":1,"source":"clipboard","continuity_credential":"` + credential + `"}`},
		{name: "unknown member", body: `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":1,"severity":"high","continuity_credential":"` + credential + `"}`},
		{name: "duplicate sequence", body: `{"schema_version":1,"generation":2,"sequence":7,"sequence":8,"duration_milliseconds":1,"continuity_credential":"` + credential + `"}`},
		{name: "invalid credential", body: `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":1,"continuity_credential":"short"}`},
		{name: "trailing document", body: `{"schema_version":1,"generation":2,"sequence":7,"duration_milliseconds":1,"continuity_credential":"` + credential + `"} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, decodeErr := decodeExamAttemptFocusLossRequest(json.RawMessage(test.body))
			require.Error(t, decodeErr, "invalid Focus Loss claim was accepted")
		})
	}
}

func TestDecodeExamAttemptFocusLossRequestAcceptsEveryClosedSource(t *testing.T) {
	t.Parallel()
	credential := model.NewCredentialToken()
	for _, source := range []string{"", "window_blur", "document_hidden", "application_backgrounded", "fullscreen_exited"} {
		source := source
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			body := json.RawMessage(`{"schema_version":1,"generation":1,"sequence":1,"duration_milliseconds":1,"source":"` + source + `","continuity_credential":"` + credential + `"}`)
			_, err := decodeExamAttemptFocusLossRequest(body)
			require.NoError(t, err, "source %q rejected", source)
		})
	}
}

func TestExamAttemptFocusLossErrorPreservesOnlyStableCandidateSafeOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, code, wantCode, wantMessage string
	}{
		{name: "same sequence changed", code: "exam.attempt.focus_loss_conflict", wantCode: "exam.attempt.focus_loss_conflict", wantMessage: "Focus Loss signal conflicts with an accepted sequence."},
		{name: "selector concealed", code: "resource.not_found", wantCode: "resource.not_found", wantMessage: "Focus Loss signal was denied."},
		{name: "authorization concealed", code: "authorization.denied", wantCode: "resource.not_found", wantMessage: "Focus Loss signal was denied."},
		{name: "connection closed", code: "exam.attempt.connection_closed", wantCode: "exam.attempt.connection_closed", wantMessage: "Exam Attempt connection is not active."},
		{name: "connection lost", code: "exam.attempt.connection_lost", wantCode: "exam.attempt.connection_lost", wantMessage: "Secure connectivity could not be renewed. Ask a manager to re-allow access."},
		{name: "unknown application failure", code: "exam.attempt.internal_detail", wantCode: "exam.attempt.unavailable", wantMessage: "Focus Loss signal could not be accepted."},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, presentation := examAttemptFocusLossError(apperror.New(apperror.Code(test.code)))
			message := websocketErrorMessage(presentation).fallback
			require.Equal(t, test.wantCode, code)
			require.Equal(t, test.wantMessage, message)
		})
	}
}
