// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamSittingCorrectionHTTPStagesResourceContentAndReturnsOnlySafeAuthoritativeMetadata(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	raw := []byte("corrected reference")
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	metadata := `{"base_revision_id":"` + fake.baseRevisionID.String() + `","target_kind":"addition","media_type":"text/markdown","size":19,"sha256":"` + digest + `"}`
	body, contentType := examResourceMultipart(t, metadata, raw, false)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/correction-resource-stages", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "stage-correction-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d body=%s", response.Code, response.Body.String())
	require.Equal(t, fake.examID, fake.stage.ExamID, "command=%#v uploaded=%q", fake.stage, fake.uploaded)
	require.Equal(t, fake.sittingID, fake.stage.SittingID, "command=%#v uploaded=%q", fake.stage, fake.uploaded)
	require.Equal(t, fake.baseRevisionID, fake.stage.BaseRevisionID, "command=%#v uploaded=%q", fake.stage, fake.uploaded)
	require.Equal(t, app.ExamSittingCorrectionResourceAddition, fake.stage.Target, "command=%#v uploaded=%q", fake.stage, fake.uploaded)
	require.False(t, fake.stage.ResourceID.IsValid(), "command=%#v uploaded=%q", fake.stage, fake.uploaded)
	require.Equal(t, "stage-correction-once", fake.stage.IdempotencyKey, "command=%#v uploaded=%q", fake.stage, fake.uploaded)
	require.True(t, bytes.Equal(fake.uploaded, raw), "command=%#v uploaded=%q", fake.stage, fake.uploaded)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	wantKeys := []string{"stage_id", "resource_id", "media_type", "size", "sha256", "expires_at"}
	require.Len(t, payload, len(wantKeys))
	for _, key := range wantKeys {
		_, exists := payload[key]
		assert.True(t, exists, "response omitted %q", key)
	}
	for _, forbidden := range []string{"file_entry_id", "file_revision_id", "rendition_id", "upload_lease_id", "object_key", "path", "url"} {
		assert.False(t, bytes.Contains(response.Body.Bytes(), []byte(forbidden)), "response exposed %q: %s", forbidden, response.Body.String())
	}
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"media_type":"text/plain"`)))
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(strings.Repeat("b", 64))))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte(`"media_type":"text/markdown"`)))
}

func TestExamSittingCorrectionHTTPStageMetadataIsStrictAndExplicitZeroIsValid(t *testing.T) {
	t.Parallel()
	for name, metadata := range map[string]string{
		"missing size":               `{"base_revision_id":"BASE","target_kind":"addition","media_type":"text/plain","sha256":"DIGEST"}`,
		"addition names replacement": `{"base_revision_id":"BASE","target_kind":"addition","replaces_resource_id":"RESOURCE","media_type":"text/plain","size":0,"sha256":"DIGEST"}`,
		"replacement omits resource": `{"base_revision_id":"BASE","target_kind":"replacement","media_type":"text/plain","size":0,"sha256":"DIGEST"}`,
		"unknown field":              `{"base_revision_id":"BASE","target_kind":"addition","media_type":"text/plain","size":0,"sha256":"DIGEST","object_key":"private"}`,
		"duplicate field":            `{"base_revision_id":"BASE","target_kind":"addition","target_kind":"replacement","media_type":"text/plain","size":0,"sha256":"DIGEST"}`,
	} {
		name, metadata := name, metadata
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			fake := newExamSittingCorrectionHTTPFake()
			metadata = strings.ReplaceAll(metadata, "BASE", fake.baseRevisionID.String())
			metadata = strings.ReplaceAll(metadata, "RESOURCE", model.NewExamResourceID().String())
			metadata = strings.ReplaceAll(metadata, "DIGEST", fmt.Sprintf("%x", sha256.Sum256(nil)))
			httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
			body, contentType := examResourceMultipart(t, metadata, nil, false)
			request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/correction-resource-stages", body)
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("Idempotency-Key", "invalid-stage")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, "status=%d calls=%d body=%s", response.Code, fake.stageCalls, response.Body.String())
			require.Equal(t, 0, fake.stageCalls, "status=%d calls=%d body=%s", response.Code, fake.stageCalls, response.Body.String())
		})
	}

	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	fake.stageResult.Size = 0
	fake.stageResult.SHA256 = fmt.Sprintf("%x", sha256.Sum256(nil))
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	metadata := fmt.Sprintf(`{"base_revision_id":"%s","target_kind":"addition","media_type":"text/plain","size":0,"sha256":"%s"}`, fake.baseRevisionID, fake.stageResult.SHA256)
	body, contentType := examResourceMultipart(t, metadata, nil, false)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/correction-resource-stages", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "empty-stage")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d calls=%d command=%#v body=%s", response.Code, fake.stageCalls, fake.stage, response.Body.String())
	require.Equal(t, 1, fake.stageCalls, "status=%d calls=%d command=%#v body=%s", response.Code, fake.stageCalls, fake.stage, response.Body.String())
	require.Equal(t, int64(0), fake.stage.Size, "status=%d calls=%d command=%#v body=%s", response.Code, fake.stageCalls, fake.stage, response.Body.String())
	require.Empty(t, fake.uploaded, "status=%d calls=%d command=%#v body=%s", response.Code, fake.stageCalls, fake.stage, response.Body.String())
}

func TestExamSittingCorrectionHTTPAppliesCompleteOrderedManifestAndKeepsReasonPrivate(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	first, second := model.NewExamResourceID(), model.NewExamResourceID()
	stageID := model.NewExamCorrectionResourceStageID()
	body := fmt.Sprintf(`{"expected_sitting_revision":7,"expected_current_revision_id":"%s","instructions_markdown":"","candidate_summary":"A reference was corrected.","affected_capabilities":["browser","submission","workspace"],"acknowledgement_required":true,"reason":"Fix misleading reference","resources":[{"resource_id":"%s","display_name":"New reference","description_markdown":"**Corrected**","stage_id":"%s"},{"resource_id":"%s","display_name":"Existing reference","description_markdown":""}]}`,
		fake.baseRevisionID, first, stageID, second)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/corrections", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "apply-correction-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d body=%s", response.Code, response.Body.String())
	command := fake.apply
	require.Equal(t, fake.examID, command.ExamID)
	require.Equal(t, fake.sittingID, command.SittingID)
	require.Equal(t, int64(7), command.ExpectedSittingRevision)
	require.Equal(t, fake.baseRevisionID, command.ExpectedCurrentRevisionID)
	require.True(t, command.Instructions.Present)
	require.Empty(t, command.Instructions.Markdown)
	require.Equal(t, "A reference was corrected.", command.CandidateSummary)
	require.True(t, command.AcknowledgementRequired)
	require.Equal(t, "Fix misleading reference", command.PrivateReason)
	require.Equal(t, "apply-correction-once", command.IdempotencyKey)
	require.Len(t, command.Resources, 2)
	require.Equal(t, first, command.Resources[0].ResourceID)
	require.Equal(t, stageID, command.Resources[0].StageID)
	require.Equal(t, second, command.Resources[1].ResourceID)
	require.False(t, command.Resources[1].StageID.IsValid())
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("Fix misleading reference")))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("Corrected")))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("stage_id")))
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	wantKeys := []string{"exam_id", "exam_sitting_id", "previous_revision_id", "revision_id", "revision_number", "sitting_revision", "sitting_state", "effective_at", "affected_capabilities"}
	require.Len(t, payload, len(wantKeys))
	require.Equal(t, `["browser","submission","workspace"]`, string(payload["affected_capabilities"]))
}

func TestExamSittingCorrectionHTTPRejectsPaddedPrivateReasonBeforeApplication(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	body := fmt.Sprintf(`{"expected_sitting_revision":7,"expected_current_revision_id":"%s","candidate_summary":"A correction was made.","affected_capabilities":["browser","submission","workspace"],"acknowledgement_required":false,"reason":" padded ","resources":[]}`, fake.baseRevisionID)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/corrections", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "invalid-correction")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "status=%d calls=%d body=%s", response.Code, fake.applyCalls, response.Body.String())
	require.Equal(t, 0, fake.applyCalls, "status=%d calls=%d body=%s", response.Code, fake.applyCalls, response.Body.String())
}

func TestExamSittingCorrectionHTTPOmittedInstructionsPreservesCurrentValue(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	body := fmt.Sprintf(`{"expected_sitting_revision":7,"expected_current_revision_id":"%s","candidate_summary":"The resource list was corrected.","affected_capabilities":["browser","submission","workspace"],"acknowledgement_required":false,"reason":"Resource-only correction","resources":[]}`, fake.baseRevisionID)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/corrections", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "resource-only-correction")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d calls=%d command=%#v body=%s", response.Code, fake.applyCalls, fake.apply, response.Body.String())
	require.Equal(t, 1, fake.applyCalls, "status=%d calls=%d command=%#v body=%s", response.Code, fake.applyCalls, fake.apply, response.Body.String())
	require.False(t, fake.apply.Instructions.Present, "status=%d calls=%d command=%#v body=%s", response.Code, fake.applyCalls, fake.apply, response.Body.String())
	require.Empty(t, fake.apply.Instructions.Markdown, "status=%d calls=%d command=%#v body=%s", response.Code, fake.applyCalls, fake.apply, response.Body.String())
}

func TestApplyExamSittingCorrectionRequestIsClosedDuplicateFreeAndPresenceAware(t *testing.T) {
	t.Parallel()
	valid := `{"expected_sitting_revision":4,"expected_current_revision_id":"revision","instructions_markdown":"","candidate_summary":"A misleading reference was corrected.","affected_capabilities":["browser","submission","workspace"],"acknowledgement_required":true,"reason":"Correct a misleading reference","resources":[]}`
	var body applyExamSittingCorrectionRequest
	require.NoError(t, json.Unmarshal([]byte(valid), &body))
	instructions := body.InstructionsMarkdown.ValuePointer()
	require.True(t, body.InstructionsMarkdown.IsSet())
	require.False(t, body.InstructionsMarkdown.IsNull())
	require.NotNil(t, instructions)
	require.Empty(t, *instructions)
	require.NotNil(t, body.Resources, "explicit empty complete resource manifest became nil")

	for name, encoded := range map[string]string{
		"omitted capability selection": strings.Replace(valid, `"affected_capabilities":["browser","submission","workspace"],`, ``, 1),
		"null capability selection":    strings.Replace(valid, `["browser","submission","workspace"]`, `null`, 1),
		"empty capability selection":   strings.Replace(valid, `["browser","submission","workspace"]`, `[]`, 1),
		"duplicate capability":         strings.Replace(valid, `["browser","submission","workspace"]`, `["browser","browser"]`, 1),
		"unknown capability":           strings.Replace(valid, `["browser","submission","workspace"]`, `["arbitrary"]`, 1),
		"unsorted capabilities":        strings.Replace(valid, `["browser","submission","workspace"]`, `["workspace","browser"]`, 1),
		"null acknowledgement":         strings.Replace(valid, `"acknowledgement_required":true`, `"acknowledgement_required":null`, 1),
		"unknown field":                strings.Replace(valid, `"resources":[]`, `"resources":[],"policy":{}`, 1),
		"starter workspace field":      strings.Replace(valid, `"resources":[]`, `"resources":[],"starter_workspace":{}`, 1),
		"future default field":         strings.Replace(valid, `"resources":[]`, `"resources":[],"default_revision_id":"x"`, 1),
		"schedule field":               strings.Replace(valid, `"resources":[]`, `"resources":[],"scheduled_end_at":"2026-08-15T15:00:00Z"`, 1),
		"duplicate top level":          strings.Replace(valid, `"reason":`, `"reason":"first","reason":`, 1),
		"null instructions":            strings.Replace(valid, `"instructions_markdown":""`, `"instructions_markdown":null`, 1),
		"omitted resources":            strings.Replace(valid, `,"resources":[]`, ``, 1),
		"null resources":               strings.Replace(valid, `"resources":[]`, `"resources":null`, 1),
		"duplicate nested member":      strings.Replace(valid, `"resources":[]`, `"resources":[{"resource_id":"one","resource_id":"two","display_name":"Reference","description_markdown":""}]`, 1),
		"unknown nested member":        strings.Replace(valid, `"resources":[]`, `"resources":[{"resource_id":"one","display_name":"Reference","description_markdown":"","path":"secret"}]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var got applyExamSittingCorrectionRequest
			require.Error(t, json.Unmarshal([]byte(encoded), &got), "accepted %s", encoded)
		})
	}
}

type examSittingCorrectionHTTPFake struct {
	examID         model.ExamID
	sittingID      model.ExamSittingID
	baseRevisionID model.ExamRevisionID
	stage          app.StageExamSittingCorrectionResourceContentCommand
	stageCalls     int
	apply          app.ApplyExamSittingCorrectionCommand
	applyCalls     int
	uploaded       []byte
	stageResult    app.ExamSittingCorrectionResourceStage
	applyResult    app.ExamSittingCorrectionResult
}

func newExamSittingCorrectionHTTPFake() *examSittingCorrectionHTTPFake {
	examID, sittingID, baseRevisionID := model.NewExamID(), model.NewExamSittingID(), model.NewExamRevisionID()
	effectiveAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	return &examSittingCorrectionHTTPFake{
		examID: examID, sittingID: sittingID, baseRevisionID: baseRevisionID,
		stageResult: app.ExamSittingCorrectionResourceStage{
			StageID: model.NewExamCorrectionResourceStageID(), ResourceID: model.NewExamResourceID(),
			MediaType: model.ExamResourceMediaText, Size: 19,
			SHA256: strings.Repeat("b", 64), ExpiresAt: effectiveAt.Add(time.Hour),
		},
		applyResult: app.ExamSittingCorrectionResult{
			ExamID: examID, SittingID: sittingID, PreviousRevisionID: baseRevisionID,
			RevisionID: model.NewExamRevisionID(), RevisionNumber: 4,
			SittingState: model.ExamSittingOpen, SittingRevision: 8, EffectiveAt: effectiveAt,
		},
	}
}

func (f *examSittingCorrectionHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := testExamHTTPPrincipal()
	return &principal, nil
}

func (f *examSittingCorrectionHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := testExamHTTPPrincipal()
	return &principal, nil
}

func (f *examSittingCorrectionHTTPFake) StageExamSittingCorrectionResourceContent(_ context.Context, _ app.Invocation, command app.StageExamSittingCorrectionResourceContentCommand) (app.ExamSittingCorrectionResourceStage, error) {
	raw, err := io.ReadAll(command.Body)
	if err != nil {
		return app.ExamSittingCorrectionResourceStage{}, apperror.New("exam.sitting.correction.content_invalid")
	}
	f.stage, f.uploaded = command, raw
	f.stageCalls++
	return f.stageResult, nil
}

func (f *examSittingCorrectionHTTPFake) ApplyExamSittingCorrection(_ context.Context, _ app.Invocation, command app.ApplyExamSittingCorrectionCommand) (app.ExamSittingCorrectionResult, error) {
	f.apply = command
	f.applyCalls++
	result := f.applyResult
	result.AffectedCapabilities = append([]model.CandidateCapability{}, command.AffectedCapabilities...)
	return result, nil
}

func examSittingCorrectionBasePath(fake *examSittingCorrectionHTTPFake) string {
	return "/api/v1/exams/" + fake.examID.String() + "/sittings/" + fake.sittingID.String()
}
