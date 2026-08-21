// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

	application "github.com/sudosylabs/proctor/server/app"
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
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.stage.ExamID != fake.examID || fake.stage.SittingID != fake.sittingID || fake.stage.BaseRevisionID != fake.baseRevisionID ||
		fake.stage.Target != application.ExamSittingCorrectionResourceAddition || fake.stage.ResourceID.IsValid() ||
		fake.stage.IdempotencyKey != "stage-correction-once" || !bytes.Equal(fake.uploaded, raw) {
		t.Fatalf("command=%#v uploaded=%q", fake.stage, fake.uploaded)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"stage_id", "resource_id", "media_type", "size", "sha256", "expires_at"}
	if len(payload) != len(wantKeys) {
		t.Fatalf("response fields=%v", payload)
	}
	for _, key := range wantKeys {
		if _, exists := payload[key]; !exists {
			t.Errorf("response omitted %q", key)
		}
	}
	for _, forbidden := range []string{"file_entry_id", "file_revision_id", "rendition_id", "upload_lease_id", "object_key", "path", "url"} {
		if bytes.Contains(response.Body.Bytes(), []byte(forbidden)) {
			t.Errorf("response exposed %q: %s", forbidden, response.Body.String())
		}
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"media_type":"text/plain"`)) || !bytes.Contains(response.Body.Bytes(), []byte(strings.Repeat("b", 64))) || bytes.Contains(response.Body.Bytes(), []byte(`"media_type":"text/markdown"`)) {
		t.Fatalf("response did not use authoritative stage metadata: %s", response.Body.String())
	}
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
			if response.Code != http.StatusBadRequest || fake.stageCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, fake.stageCalls, response.Body.String())
			}
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
	if response.Code != http.StatusCreated || fake.stageCalls != 1 || fake.stage.Size != 0 || len(fake.uploaded) != 0 {
		t.Fatalf("status=%d calls=%d command=%#v body=%s", response.Code, fake.stageCalls, fake.stage, response.Body.String())
	}
}

func TestExamSittingCorrectionHTTPAppliesCompleteOrderedManifestAndKeepsReasonPrivate(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	first, second := model.NewExamResourceID(), model.NewExamResourceID()
	stageID := model.NewExamCorrectionResourceStageID()
	body := fmt.Sprintf(`{"expected_sitting_revision":7,"expected_current_revision_id":"%s","instructions_markdown":"","reason":"Fix misleading reference","resources":[{"resource_id":"%s","display_name":"New reference","description_markdown":"**Corrected**","stage_id":"%s"},{"resource_id":"%s","display_name":"Existing reference","description_markdown":""}]}`,
		fake.baseRevisionID, first, stageID, second)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/corrections", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "apply-correction-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	command := fake.apply
	if command.ExamID != fake.examID || command.SittingID != fake.sittingID || command.ExpectedSittingRevision != 7 ||
		command.ExpectedCurrentRevisionID != fake.baseRevisionID || !command.Instructions.Present || command.Instructions.Markdown != "" ||
		command.PrivateReason != "Fix misleading reference" || command.IdempotencyKey != "apply-correction-once" || len(command.Resources) != 2 ||
		command.Resources[0].ResourceID != first || command.Resources[0].StageID != stageID || command.Resources[1].ResourceID != second || command.Resources[1].StageID.IsValid() {
		t.Fatalf("command=%#v", command)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("Fix misleading reference")) || bytes.Contains(response.Body.Bytes(), []byte("Corrected")) || bytes.Contains(response.Body.Bytes(), []byte("stage_id")) {
		t.Fatalf("private input exposed: %s", response.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"exam_id", "exam_sitting_id", "previous_revision_id", "revision_id", "revision_number", "sitting_revision", "sitting_state", "effective_at"}
	if len(payload) != len(wantKeys) {
		t.Fatalf("response fields=%v", payload)
	}
}

func TestExamSittingCorrectionHTTPRejectsPaddedPrivateReasonBeforeApplication(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	body := fmt.Sprintf(`{"expected_sitting_revision":7,"expected_current_revision_id":"%s","reason":" padded ","resources":[]}`, fake.baseRevisionID)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/corrections", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "invalid-correction")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || fake.applyCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, fake.applyCalls, response.Body.String())
	}
}

func TestExamSittingCorrectionHTTPOmittedInstructionsPreservesCurrentValue(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamSittingCorrectionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examSittingCorrectionResource(fake))
	body := fmt.Sprintf(`{"expected_sitting_revision":7,"expected_current_revision_id":"%s","reason":"Resource-only correction","resources":[]}`, fake.baseRevisionID)
	request := httptest.NewRequest(http.MethodPost, examSittingCorrectionBasePath(fake)+"/corrections", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "resource-only-correction")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || fake.applyCalls != 1 || fake.apply.Instructions.Present || fake.apply.Instructions.Markdown != "" {
		t.Fatalf("status=%d calls=%d command=%#v body=%s", response.Code, fake.applyCalls, fake.apply, response.Body.String())
	}
}

func TestApplyExamSittingCorrectionRequestIsClosedDuplicateFreeAndPresenceAware(t *testing.T) {
	t.Parallel()
	valid := `{"expected_sitting_revision":4,"expected_current_revision_id":"revision","instructions_markdown":"","reason":"Correct a misleading reference","resources":[]}`
	var body applyExamSittingCorrectionRequest
	if err := json.Unmarshal([]byte(valid), &body); err != nil {
		t.Fatalf("decode valid body: %v", err)
	}
	instructions := body.InstructionsMarkdown.ValuePointer()
	if !body.InstructionsMarkdown.IsSet() || body.InstructionsMarkdown.IsNull() || instructions == nil || *instructions != "" {
		t.Fatalf("instructions = %#v", body.InstructionsMarkdown)
	}
	if body.Resources == nil {
		t.Fatal("explicit empty complete resource manifest became nil")
	}

	for name, encoded := range map[string]string{
		"unknown field":           strings.Replace(valid, `"resources":[]`, `"resources":[],"policy":{}`, 1),
		"starter workspace field": strings.Replace(valid, `"resources":[]`, `"resources":[],"starter_workspace":{}`, 1),
		"future default field":    strings.Replace(valid, `"resources":[]`, `"resources":[],"default_revision_id":"x"`, 1),
		"schedule field":          strings.Replace(valid, `"resources":[]`, `"resources":[],"scheduled_end_at":"2026-08-15T15:00:00Z"`, 1),
		"duplicate top level":     strings.Replace(valid, `"reason":`, `"reason":"first","reason":`, 1),
		"null instructions":       strings.Replace(valid, `"instructions_markdown":""`, `"instructions_markdown":null`, 1),
		"omitted resources":       strings.Replace(valid, `,"resources":[]`, ``, 1),
		"null resources":          strings.Replace(valid, `"resources":[]`, `"resources":null`, 1),
		"duplicate nested member": strings.Replace(valid, `"resources":[]`, `"resources":[{"resource_id":"one","resource_id":"two","display_name":"Reference","description_markdown":""}]`, 1),
		"unknown nested member":   strings.Replace(valid, `"resources":[]`, `"resources":[{"resource_id":"one","display_name":"Reference","description_markdown":"","path":"secret"}]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var got applyExamSittingCorrectionRequest
			if err := json.Unmarshal([]byte(encoded), &got); err == nil {
				t.Fatalf("accepted %s", encoded)
			}
		})
	}
}

type examSittingCorrectionHTTPFake struct {
	examID         model.ExamID
	sittingID      model.ExamSittingID
	baseRevisionID model.ExamRevisionID
	stage          application.StageExamSittingCorrectionResourceContentCommand
	stageCalls     int
	apply          application.ApplyExamSittingCorrectionCommand
	applyCalls     int
	uploaded       []byte
	stageResult    application.ExamSittingCorrectionResourceStage
	applyResult    application.ExamSittingCorrectionResult
}

func newExamSittingCorrectionHTTPFake() *examSittingCorrectionHTTPFake {
	examID, sittingID, baseRevisionID := model.NewExamID(), model.NewExamSittingID(), model.NewExamRevisionID()
	effectiveAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	return &examSittingCorrectionHTTPFake{
		examID: examID, sittingID: sittingID, baseRevisionID: baseRevisionID,
		stageResult: application.ExamSittingCorrectionResourceStage{
			StageID: model.NewExamCorrectionResourceStageID(), ResourceID: model.NewExamResourceID(),
			MediaType: model.ExamResourceMediaText, Size: 19,
			SHA256: strings.Repeat("b", 64), ExpiresAt: effectiveAt.Add(time.Hour),
		},
		applyResult: application.ExamSittingCorrectionResult{
			ExamID: examID, SittingID: sittingID, PreviousRevisionID: baseRevisionID,
			RevisionID: model.NewExamRevisionID(), RevisionNumber: 4,
			SittingState: model.ExamSittingOpen, SittingRevision: 8, EffectiveAt: effectiveAt,
		},
	}
}

func (fake *examSittingCorrectionHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := testExamHTTPPrincipal()
	return &principal, nil
}

func (fake *examSittingCorrectionHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := testExamHTTPPrincipal()
	return &principal, nil
}

func (fake *examSittingCorrectionHTTPFake) StageExamSittingCorrectionResourceContent(_ context.Context, _ application.Invocation, command application.StageExamSittingCorrectionResourceContentCommand) (application.ExamSittingCorrectionResourceStage, error) {
	raw, err := io.ReadAll(command.Body)
	if err != nil {
		return application.ExamSittingCorrectionResourceStage{}, application.NewError("exam.sitting.correction.invalid_content")
	}
	fake.stage, fake.uploaded = command, raw
	fake.stageCalls++
	return fake.stageResult, nil
}

func (fake *examSittingCorrectionHTTPFake) ApplyExamSittingCorrection(_ context.Context, _ application.Invocation, command application.ApplyExamSittingCorrectionCommand) (application.ExamSittingCorrectionResult, error) {
	fake.apply = command
	fake.applyCalls++
	return fake.applyResult, nil
}

func examSittingCorrectionBasePath(fake *examSittingCorrectionHTTPFake) string {
	return "/api/v1/exams/" + fake.examID.String() + "/sittings/" + fake.sittingID.String()
}
