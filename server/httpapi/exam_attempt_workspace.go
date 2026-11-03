// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
)

type candidateWorkspaceCursor struct {
	ExpectedCursor int64
	ID             model.AttemptWorkspaceEntryID
}

type candidateWorkspaceCursorWire struct {
	ExpectedCursor int64  `json:"workspace_cursor"`
	ID             string `json:"after_entry_id,omitempty"`
}

func encodeCandidateWorkspaceCursor(cursor candidateWorkspaceCursor) (string, error) {
	wire := candidateWorkspaceCursorWire{
		ExpectedCursor: cursor.ExpectedCursor, ID: cursor.ID.String()}
	return encodeOpaqueCursor(wire, candidateWorkspaceCursorSpec())
}

func decodeCandidateWorkspaceCursor(raw string) (candidateWorkspaceCursor, error) {
	wire, err := decodeOpaqueCursor(raw, candidateWorkspaceCursorSpec())
	if err != nil {
		return candidateWorkspaceCursor{}, err
	}
	var id model.AttemptWorkspaceEntryID
	if wire.ID != "" {
		id, _ = model.ParseAttemptWorkspaceEntryID(wire.ID)
	}
	return candidateWorkspaceCursor{ExpectedCursor: wire.ExpectedCursor, ID: id}, nil
}

func candidateWorkspaceCursorSpec() opaqueCursorSpec[candidateWorkspaceCursorWire] {
	return opaqueCursorSpec[candidateWorkspaceCursorWire]{
		label: "candidate Workspace", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"workspace_cursor", "after_entry_id"},
		validate: func(cursor candidateWorkspaceCursorWire) error {
			if cursor.ExpectedCursor < 0 || cursor.ID != "" && !model.AttemptWorkspaceEntryID(cursor.ID).IsValid() {
				return errors.New("invalid candidate Workspace keyset")
			}
			return nil
		},
	}
}

type candidateExamWorkspaceItemResponse struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	ContentVersion string `json:"content_version,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	Size           *int64 `json:"size,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
}

type candidateExamWorkspaceListResponse struct {
	WorkspaceID     string                               `json:"workspace_id"`
	WorkspaceCursor int64                                `json:"workspace_cursor"`
	Items           []candidateExamWorkspaceItemResponse `json:"items"`
	NextCursor      string                               `json:"next_cursor,omitempty"`
	RefreshRequired bool                                 `json:"refresh_required"`
}

type candidateWorkspaceMutationAccessRequest struct {
	ParticipationID string `json:"participation_id"`
	Generation      int64  `json:"generation"`
}

type createCandidateWorkspaceDirectoryRequest struct {
	ParticipationID string `json:"participation_id"`
	Generation      int64  `json:"generation"`
	Path            string `json:"path"`
}

type candidateWorkspaceFileUploadMetadata struct {
	ParticipationID string `json:"participation_id"`
	Generation      int64  `json:"generation"`
	Path            string `json:"path"`
	MediaType       string `json:"media_type"`
	Size            *int64 `json:"size"`
	SHA256          string `json:"sha256"`
}

type replaceCandidateWorkspaceFileMetadata struct {
	ParticipationID        string `json:"participation_id"`
	Generation             int64  `json:"generation"`
	ExpectedPath           string `json:"expected_path"`
	ExpectedContentVersion string `json:"expected_content_version"`
	MediaType              string `json:"media_type"`
	Size                   *int64 `json:"size"`
	SHA256                 string `json:"sha256"`
}

type moveCandidateWorkspaceEntryRequest struct {
	ParticipationID string `json:"participation_id"`
	Generation      int64  `json:"generation"`
	ExpectedPath    string `json:"expected_path"`
	DestinationPath string `json:"destination_path"`
}

type deleteCandidateWorkspaceEntryRequest struct {
	ParticipationID         string          `json:"participation_id"`
	Generation              int64           `json:"generation"`
	ExpectedPath            string          `json:"expected_path"`
	ExpectedContentVersion  string          `json:"expected_content_version,omitempty"`
	Recursive               bool            `json:"recursive,omitempty"`
	ExpectedWorkspaceCursor Optional[int64] `json:"expected_workspace_cursor,omitempty"`
}

type candidateWorkspaceMutationResponse struct {
	WorkspaceID     string                              `json:"workspace_id"`
	WorkspaceCursor int64                               `json:"workspace_cursor"`
	Operation       string                              `json:"operation"`
	Entry           *candidateExamWorkspaceItemResponse `json:"entry,omitempty"`
}

type candidateWorkspaceJournalEntryResponse struct {
	Cursor         int64  `json:"cursor"`
	EntryID        string `json:"entry_id"`
	Kind           string `json:"kind"`
	Operation      string `json:"operation"`
	OldPath        string `json:"old_path,omitempty"`
	NewPath        string `json:"new_path,omitempty"`
	ContentVersion string `json:"content_version,omitempty"`
	ChangedAt      string `json:"changed_at"`
	Recursive      bool   `json:"recursive,omitempty"`
}

type candidateWorkspaceJournalResponse struct {
	WorkspaceID     string                                   `json:"workspace_id"`
	CurrentCursor   int64                                    `json:"current_cursor"`
	Items           []candidateWorkspaceJournalEntryResponse `json:"items"`
	NextCursor      string                                   `json:"next_cursor,omitempty"`
	RefreshRequired bool                                     `json:"refresh_required"`
}

// candidateWorkspaceJournalCursor continues the change journal after the
// last Workspace Cursor of the previous page.
type candidateWorkspaceJournalCursor struct {
	After int64 `json:"after_workspace_cursor"`
}

func candidateWorkspaceJournalCursorSpec() opaqueCursorSpec[candidateWorkspaceJournalCursor] {
	return opaqueCursorSpec[candidateWorkspaceJournalCursor]{
		label: "Workspace journal", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"after_workspace_cursor"},
		validate: func(cursor candidateWorkspaceJournalCursor) error {
			if cursor.After < 1 {
				return errors.New("invalid Workspace journal keyset")
			}
			return nil
		},
	}
}

func (m examAttemptHTTPModule) workspace(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	query := attempt.WorkspaceQuery{Access: access, ExpectedCursor: -1, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeCandidateWorkspaceCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError(decodeErr)
		}
		query.ExpectedCursor, query.AfterEntryID = cursor.ExpectedCursor, cursor.ID
	}
	page, err := m.application.ListCandidateExamWorkspace(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateExamWorkspaceListResponse{WorkspaceID: page.WorkspaceID.String(), WorkspaceCursor: page.Cursor,
		Items: make([]candidateExamWorkspaceItemResponse, 0, len(page.Items)), RefreshRequired: page.RefreshRequired}
	for _, item := range page.Items {
		mapped := candidateExamWorkspaceItemResponse{ID: item.EntryID.String(), Kind: string(item.Kind), Path: item.Path,
			ContentVersion: item.ContentVersion.String(), MediaType: item.MediaType, SHA256: item.SHA256}
		if item.Kind == model.StarterWorkspaceEntryFile {
			size := item.SizeBytes
			mapped.Size = &size
		}
		response.Items = append(response.Items, mapped)
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errors.New("Exam Attempt application returned an invalid Workspace page")
		}
		last := page.Items[len(page.Items)-1]
		response.NextCursor, err = encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: page.Cursor, ID: last.EntryID})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) workspaceChanges(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	paging, err := request.pageRequest()
	if err != nil {
		return operationResult{}, err
	}
	if paging.Limit > model.AttemptWorkspaceJournalReadMaximum {
		return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
	}
	query := attempt.WorkspaceJournalQuery{Access: access, Limit: paging.Limit}
	values := request.request.URL.Query()
	cursor, continued, err := decodePageCursor(paging, candidateWorkspaceJournalCursorSpec())
	if err != nil {
		return operationResult{}, err
	}
	switch raw := values.Get("workspace_cursor"); {
	case continued && values.Has("workspace_cursor"):
		return operationResult{}, invalidRequestError(errors.New("workspace_cursor starts a listing that cursor continues"))
	case continued:
		query.AfterCursor = cursor.After
	case raw != "":
		query.AfterCursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || query.AfterCursor < 0 {
			return operationResult{}, invalidRequestError(errors.New("must be nonnegative"))
		}
	}
	page, err := m.application.ListCandidateExamWorkspaceJournal(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateWorkspaceJournalResponse{WorkspaceID: page.WorkspaceID.String(), CurrentCursor: page.CurrentCursor,
		Items: make([]candidateWorkspaceJournalEntryResponse, 0, len(page.Entries)), RefreshRequired: page.RefreshRequired}
	if page.HasMore {
		if len(page.Entries) == 0 {
			return operationResult{}, unpresentableResult(errors.New("Workspace journal page made no progress"))
		}
		next := candidateWorkspaceJournalCursor{After: page.Entries[len(page.Entries)-1].Cursor}
		if response.NextCursor, err = encodeOpaqueCursor(next, candidateWorkspaceJournalCursorSpec()); err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	for _, entry := range page.Entries {
		response.Items = append(response.Items, candidateWorkspaceJournalEntryResponse{Cursor: entry.Cursor,
			EntryID: entry.EntryID.String(), Kind: string(entry.EntryKind), Operation: string(entry.Operation), OldPath: entry.OldPath,
			NewPath: entry.NewPath, ContentVersion: entry.ContentVersion.String(),
			ChangedAt: model.FormatRFC3339(entry.ChangedAt), Recursive: entry.Recursive})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) createWorkspaceDirectory(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body createCandidateWorkspaceDirectoryRequest
	if err = decodeCandidateWorkspaceJSON(request, &body); err != nil {
		return operationResult{}, err
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	result, err := m.application.CreateCandidateExamWorkspaceDirectory(request.context, request.invocation(),
		attempt.CreateWorkspaceDirectoryCommand{Access: mutationAccess, Path: body.Path,
			IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) createWorkspaceFile(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata candidateWorkspaceFileUploadMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil || metadata.Size == nil {
		return protocolResult{}, invalidRequestError(errors.New("valid metadata and size are required"))
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: metadata.ParticipationID, Generation: metadata.Generation})
	if err != nil {
		return protocolResult{}, err
	}
	result, err := m.application.CreateCandidateExamWorkspaceFile(request.context, request.invocation(),
		attempt.CreateWorkspaceFileCommand{Access: mutationAccess, Path: metadata.Path, MediaType: metadata.MediaType,
			ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusCreated, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) moveWorkspaceEntry(request operationRequest) (operationResult, error) {
	access, entryID, err := candidateWorkspaceEntryAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body moveCandidateWorkspaceEntryRequest
	if err = decodeCandidateWorkspaceJSON(request, &body); err != nil {
		return operationResult{}, err
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	result, err := m.application.MoveCandidateExamWorkspaceEntry(request.context, request.invocation(),
		attempt.MoveWorkspaceEntryCommand{Access: mutationAccess, EntryID: entryID,
			ExpectedPath: body.ExpectedPath, DestinationPath: body.DestinationPath, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) replaceWorkspaceFile(request operationRequest) (protocolResult, error) {
	access, entryID, err := candidateWorkspaceEntryAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata replaceCandidateWorkspaceFileMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil || metadata.Size == nil {
		return protocolResult{}, invalidRequestError(errors.New("valid metadata and size are required"))
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: metadata.ParticipationID, Generation: metadata.Generation})
	if err != nil {
		return protocolResult{}, err
	}
	version, err := model.ParseWorkspaceContentVersion(metadata.ExpectedContentVersion)
	if err != nil {
		return protocolResult{}, invalidRequestError(err)
	}
	result, err := m.application.ReplaceCandidateExamWorkspaceFile(request.context, request.invocation(),
		attempt.ReplaceWorkspaceFileCommand{Access: mutationAccess, EntryID: entryID,
			ExpectedPath: metadata.ExpectedPath, ExpectedContentVersion: version, MediaType: metadata.MediaType,
			ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusOK, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) deleteWorkspaceEntry(request operationRequest) (operationResult, error) {
	access, entryID, err := candidateWorkspaceEntryAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body deleteCandidateWorkspaceEntryRequest
	if err = decodeCandidateWorkspaceJSON(request, &body); err != nil {
		return operationResult{}, err
	}
	cursor := body.ExpectedWorkspaceCursor.ValuePointer()
	if body.ExpectedWorkspaceCursor.IsNull() || (body.Recursive && (cursor == nil || *cursor < 0 || body.ExpectedContentVersion != "")) ||
		(!body.Recursive && cursor != nil) {
		return operationResult{}, invalidRequestError(errors.New("recursive directory deletion requires expected_workspace_cursor and no content version"))
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	var version model.WorkspaceContentVersion
	if body.ExpectedContentVersion != "" {
		version, err = model.ParseWorkspaceContentVersion(body.ExpectedContentVersion)
		if err != nil {
			return operationResult{}, invalidRequestError(err)
		}
	}
	result, err := m.application.DeleteCandidateExamWorkspaceEntry(request.context, request.invocation(),
		attempt.DeleteWorkspaceEntryCommand{Access: mutationAccess, EntryID: entryID,
			ExpectedPath: body.ExpectedPath, ExpectedContentVersion: version, Recursive: body.Recursive,
			ExpectedWorkspaceCursor: cursor, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func candidateWorkspaceMutationAccess(access attempt.CandidateAccess,
	wire candidateWorkspaceMutationAccessRequest,
) (attempt.WorkspaceMutationAccess, error) {
	participationID, err := model.ParseAttemptParticipationID(wire.ParticipationID)
	if err != nil || wire.Generation < 1 {
		return attempt.WorkspaceMutationAccess{}, invalidRequestError(errors.New("valid participation_id and generation are required"))
	}
	return attempt.WorkspaceMutationAccess{CandidateAccess: access, ParticipationID: participationID,
		Generation: wire.Generation}, nil
}

func decodeCandidateWorkspaceJSON(request operationRequest, target any) error {
	if request.request == nil || request.request.Body == nil {
		return invalidRequestError(errors.New("request body is required"))
	}
	if err := requireJSONMediaType(request.request); err != nil {
		return invalidRequestError(err)
	}
	raw, err := io.ReadAll(io.LimitReader(request.request.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || rejectDuplicateTopLevelJSONMembers(raw) != nil {
		return invalidRequestError(errors.New("invalid strict JSON body"))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return invalidRequestError(err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invalidRequestError(errors.New("request body must contain one JSON value"))
	}
	return nil
}

func candidateWorkspaceEntryAccess(request operationRequest) (attempt.CandidateAccess, model.AttemptWorkspaceEntryID, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return attempt.CandidateAccess{}, "", err
	}
	entryID, err := request.params.RequireAttemptWorkspaceEntryID()
	if err != nil {
		return attempt.CandidateAccess{}, "", err
	}
	return access, entryID, nil
}

func candidateWorkspaceMutationResult(result attempt.WorkspaceMutationResult) candidateWorkspaceMutationResponse {
	response := candidateWorkspaceMutationResponse{WorkspaceID: result.WorkspaceID.String(), WorkspaceCursor: result.Change.Cursor,
		Operation: string(result.Change.Operation)}
	if result.Entry != nil {
		entry := candidateExamWorkspaceItemResponse{ID: result.Entry.EntryID.String(), Kind: string(result.Entry.Kind),
			Path: result.Entry.Path, ContentVersion: result.Entry.ContentVersion.String(), MediaType: result.Entry.MediaType,
			SHA256: result.Entry.SHA256}
		if result.Entry.Kind == model.StarterWorkspaceEntryFile {
			size := result.Entry.SizeBytes
			entry.Size = &size
		}
		response.Entry = &entry
	}
	return response
}

func (m examAttemptHTTPModule) openWorkspaceFile(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	entryID, err := request.params.RequireAttemptWorkspaceEntryID()
	if err != nil {
		return protocolResult{}, err
	}
	opened, err := m.application.OpenCandidateExamWorkspaceFile(request.context, request.invocation(), attempt.OpenWorkspaceFileQuery{Access: access, EntryID: entryID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.ContentVersion.String()+`"`, untrustedWorkspaceContentHeaders)
}

// untrustedWorkspaceContentHeaders keeps Workspace bytes, whose media type an
// author chose, from rendering as an application page. A media type that a
// browser renders as an active document (HTML, XML, or SVG) is served as plain
// text, and the sandbox policy gives any rendering an opaque origin with no
// scripts or form submission. The content stays inline: the protected clients
// read it with fetch and take its authored media type from the metadata.
func untrustedWorkspaceContentHeaders(headers http.Header) {
	headers.Set("Content-Type", passiveWorkspaceMediaType(headers.Get("Content-Type")))
	headers.Set("Content-Security-Policy", "sandbox")
}

// passiveWorkspaceMediaType returns mediaType unless a browser would render it
// as an active document, in which case it returns plain text.
func passiveWorkspaceMediaType(mediaType string) string {
	parsed, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return "application/octet-stream"
	}
	switch {
	case parsed == "text/html", parsed == "text/xml", parsed == "text/xsl",
		parsed == "application/xml", strings.HasSuffix(parsed, "+xml"):
		return "text/plain; charset=utf-8"
	default:
		return mediaType
	}
}
