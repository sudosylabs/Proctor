// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
)

type submissionManifestCursor struct {
	EntryID model.AttemptWorkspaceEntryID
}

type submissionManifestCursorWire struct {
	EntryID string `json:"after_entry_id"`
}

func encodeSubmissionManifestCursor(cursor submissionManifestCursor) (string, error) {
	wire := submissionManifestCursorWire{EntryID: cursor.EntryID.String()}
	return encodeOpaqueCursor(wire, submissionManifestCursorSpec())
}

func decodeSubmissionManifestCursor(raw string) (submissionManifestCursor, error) {
	wire, err := decodeOpaqueCursor(raw, submissionManifestCursorSpec())
	if err != nil {
		return submissionManifestCursor{}, err
	}
	entryID, _ := model.ParseAttemptWorkspaceEntryID(wire.EntryID)
	return submissionManifestCursor{EntryID: entryID}, nil
}

func submissionManifestCursorSpec() opaqueCursorSpec[submissionManifestCursorWire] {
	return opaqueCursorSpec[submissionManifestCursorWire]{
		label: "Submission manifest", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"after_entry_id"},
		validate: func(cursor submissionManifestCursorWire) error {
			if !model.AttemptWorkspaceEntryID(cursor.EntryID).IsValid() {
				return errors.New("invalid Submission manifest keyset")
			}
			return nil
		},
	}
}

type submitExamAttemptRequest struct {
	ParticipationID           string `json:"participation_id"`
	Generation                int64  `json:"generation"`
	ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
	ExpectedWorkspaceCursor   int64  `json:"expected_workspace_cursor"`
	FinalFocusLossSequence    int64  `json:"final_focus_loss_sequence"`
}

type examSubmissionReceiptResponse struct {
	BrowserActivity model.BrowserSubmissionSettlement `json:"browser_activity"`
	SubmissionID    string                            `json:"submission_id"`
	ExamAttemptID   string                            `json:"exam_attempt_id"`
	ExamRevisionID  string                            `json:"exam_revision_id"`
	State           string                            `json:"state"`
	WorkspaceCursor int64                             `json:"workspace_cursor"`
	ManifestDigest  string                            `json:"manifest_digest"`
	SubmittedAt     string                            `json:"submitted_at"`
}

type examSubmissionManagerResponse struct {
	BrowserActivity          model.BrowserSubmissionSettlement `json:"browser_activity"`
	SubmissionID             string                            `json:"submission_id"`
	ExamID                   string                            `json:"exam_id"`
	ExamSittingID            string                            `json:"exam_sitting_id"`
	ExamAttemptID            string                            `json:"exam_attempt_id"`
	ExamRevisionID           string                            `json:"exam_revision_id"`
	WorkspaceID              string                            `json:"workspace_id"`
	ManifestSchemaVersion    int                               `json:"manifest_schema_version"`
	WorkspaceCursor          int64                             `json:"workspace_cursor"`
	ManifestDigest           string                            `json:"manifest_digest"`
	ManifestEntryCount       int                               `json:"manifest_entry_count"`
	ManifestTotalFileBytes   int64                             `json:"manifest_total_file_bytes"`
	FinalFocusLossSequence   *int64                            `json:"final_focus_loss_sequence,omitempty"`
	IntegrityState           string                            `json:"integrity_state"`
	IntegrityRetiredAt       string                            `json:"integrity_retired_at,omitempty"`
	UnresolvedIntegrityCount *int64                            `json:"unresolved_integrity_count,omitempty"`
	SubmittedAt              string                            `json:"submitted_at"`
}

type examSubmissionManifestItemResponse struct {
	EntryID        string `json:"entry_id"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	ContentVersion string `json:"content_version,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	Size           *int64 `json:"size,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
}

type examSubmissionManifestResponse struct {
	SubmissionID    string                               `json:"submission_id"`
	WorkspaceCursor int64                                `json:"workspace_cursor"`
	ManifestDigest  string                               `json:"manifest_digest"`
	Items           []examSubmissionManifestItemResponse `json:"items"`
	NextCursor      string                               `json:"next_cursor,omitempty"`
}

func (m examAttemptHTTPModule) submit(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body submitExamAttemptRequest
	if err = decodeCandidateWorkspaceJSON(request, &body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedWorkspaceCursor < 0 || body.FinalFocusLossSequence < 0 {
		return operationResult{}, invalidRequestError(errors.New("Workspace Cursor and Focus Loss sequence must be nonnegative"))
	}
	revisionID, err := model.ParseExamRevisionID(body.ExpectedCurrentRevisionID)
	if err != nil {
		return operationResult{}, invalidRequestError(err)
	}

	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{
		ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	receipt, err := m.application.SubmitExamAttempt(request.context, request.invocation(),
		attempt.SubmitCommand{Access: mutationAccess, ExpectedCurrentRevisionID: revisionID,
			ExpectedWorkspaceCursor: body.ExpectedWorkspaceCursor, FinalFocusLossSequence: body.FinalFocusLossSequence,
			IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	response := examSubmissionReceiptResponse{BrowserActivity: receipt.BrowserActivity, SubmissionID: receipt.SubmissionID.String(),
		ExamAttemptID: receipt.AttemptID.String(), ExamRevisionID: receipt.ExamRevisionID.String(),
		State: string(receipt.State), WorkspaceCursor: receipt.WorkspaceCursor,
		ManifestDigest: receipt.ManifestDigest, SubmittedAt: model.FormatRFC3339(receipt.SubmittedAt)}
	return jsonResult(http.StatusCreated, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) getSubmission(request operationRequest) (operationResult, error) {
	query, err := managedSubmissionQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.application.GetExamSubmission(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	submission := view.Submission
	response := examSubmissionManagerResponse{BrowserActivity: submission.BrowserActivity, SubmissionID: submission.ID.String(), ExamID: view.Authorization.ExamID.String(),
		ExamSittingID: view.Authorization.SittingID.String(), ExamAttemptID: submission.AttemptID.String(),
		ExamRevisionID: submission.ExamRevisionID.String(), WorkspaceID: submission.WorkspaceID.String(),
		ManifestSchemaVersion: submission.ManifestSchemaVersion,
		WorkspaceCursor:       submission.WorkspaceCursor, ManifestDigest: submission.ManifestDigest,
		ManifestEntryCount: submission.ManifestEntryCount, ManifestTotalFileBytes: submission.ManifestTotalFileBytes,
		IntegrityState: string(submission.IntegrityState),
		SubmittedAt:    model.FormatRFC3339(submission.SubmittedAt)}
	if submission.IntegrityState == model.SubmissionIntegrityRetired {
		response.IntegrityRetiredAt = model.FormatRFC3339(submission.IntegrityRetiredAt.Time)
	} else {
		response.FinalFocusLossSequence = &submission.FinalFocusLossSequence
		response.UnresolvedIntegrityCount = &submission.UnresolvedIntegrityCount
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) listSubmissionManifest(request operationRequest) (operationResult, error) {
	ownership, err := managedSubmissionQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	query := attempt.ListSubmissionManifestQuery{GetSubmissionQuery: ownership, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > model.ExamSubmissionManifestReadMaximum {
			return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeSubmissionManifestCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError(decodeErr)
		}
		query.AfterEntryID = cursor.EntryID
	}
	page, err := m.application.ListExamSubmissionManifest(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examSubmissionManifestResponse{SubmissionID: page.SubmissionID.String(), WorkspaceCursor: page.WorkspaceCursor,
		ManifestDigest: page.ManifestDigest, Items: make([]examSubmissionManifestItemResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		mapped := examSubmissionManifestItemResponse{EntryID: item.EntryID.String(), Kind: string(item.Kind), Path: item.Path,
			ContentVersion: item.ContentVersion.String(), MediaType: item.MediaType, SHA256: item.SHA256}
		if item.Kind == model.StarterWorkspaceEntryFile {
			size := item.SizeBytes
			mapped.Size = &size
		}
		response.Items = append(response.Items, mapped)
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errors.New("Exam Attempt application returned an invalid Submission manifest page")
		}
		response.NextCursor, err = encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: page.Items[len(page.Items)-1].EntryID})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) openSubmissionFile(request operationRequest) (protocolResult, error) {
	ownership, err := managedSubmissionQuery(request)
	if err != nil {
		return protocolResult{}, err
	}
	entryID, err := request.params.RequireAttemptWorkspaceEntryID()
	if err != nil {
		return protocolResult{}, err
	}
	opened, err := m.application.OpenExamSubmissionFile(request.context, request.invocation(),
		attempt.OpenSubmissionFileQuery{GetSubmissionQuery: ownership, EntryID: entryID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.SHA256+`"`, untrustedWorkspaceContentHeaders)
}

func managedSubmissionQuery(request operationRequest) (attempt.GetSubmissionQuery, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return attempt.GetSubmissionQuery{}, err
	}
	submissionID, err := request.params.RequireSubmissionID()
	if err != nil {
		return attempt.GetSubmissionQuery{}, err
	}
	return attempt.GetSubmissionQuery{ExamID: examID, SittingID: sittingID,
		AttemptID: attemptID, SubmissionID: submissionID}, nil
}
