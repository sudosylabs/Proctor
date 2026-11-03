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
	"time"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserActivityCursor struct {
	ReceivedAt      time.Time
	SourceSessionID model.BrowserSourceSessionID
	Sequence        int64
}

type browserActivityCursorWire struct {
	ReceivedAt      string `json:"received_at"`
	SourceSessionID string `json:"source_session_id"`
	Sequence        int64  `json:"sequence"`
}

func encodeBrowserActivityCursor(cursor browserActivityCursor) (string, error) {
	return encodeOpaqueCursor(browserActivityCursorWire{
		ReceivedAt: model.FormatRFC3339(cursor.ReceivedAt), SourceSessionID: string(cursor.SourceSessionID), Sequence: cursor.Sequence,
	}, browserActivityCursorSpec())
}

func decodeBrowserActivityCursor(raw string) (browserActivityCursor, error) {
	wire, err := decodeOpaqueCursor(raw, browserActivityCursorSpec())
	if err != nil {
		return browserActivityCursor{}, err
	}
	receivedAt, _ := model.ParseRFC3339(wire.ReceivedAt)
	return browserActivityCursor{ReceivedAt: model.TimeUTC(receivedAt), SourceSessionID: model.BrowserSourceSessionID(wire.SourceSessionID), Sequence: wire.Sequence}, nil
}

func browserActivityCursorSpec() opaqueCursorSpec[browserActivityCursorWire] {
	return opaqueCursorSpec[browserActivityCursorWire]{
		label: "Browser Activity", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"received_at", "source_session_id", "sequence"},
		validate: func(cursor browserActivityCursorWire) error {
			receivedAt, err := model.ParseRFC3339(cursor.ReceivedAt)
			if err != nil || receivedAt.IsZero() || !model.BrowserSourceSessionID(cursor.SourceSessionID).IsValid() || cursor.Sequence < 1 {
				return errors.New("invalid Browser Activity keyset")
			}
			return nil
		},
	}
}

type examAttemptManagerCursor struct {
	CreatedAt time.Time
	ID        model.ExamAttemptID
}

type examAttemptManagerCursorWire struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeExamAttemptManagerCursor(cursor examAttemptManagerCursor) (string, error) {
	wire := examAttemptManagerCursorWire{
		CreatedAt: model.FormatRFC3339(cursor.CreatedAt), ID: cursor.ID.String()}
	return encodeOpaqueCursor(wire, examAttemptManagerCursorSpec())
}

func decodeExamAttemptManagerCursor(raw string) (examAttemptManagerCursor, error) {
	wire, err := decodeOpaqueCursor(raw, examAttemptManagerCursorSpec())
	if err != nil {
		return examAttemptManagerCursor{}, err
	}
	createdAt, _ := model.ParseRFC3339(wire.CreatedAt)
	id, _ := model.ParseExamAttemptID(wire.ID)
	return examAttemptManagerCursor{CreatedAt: model.TimeUTC(createdAt), ID: id}, nil
}

func examAttemptManagerCursorSpec() opaqueCursorSpec[examAttemptManagerCursorWire] {
	return opaqueCursorSpec[examAttemptManagerCursorWire]{
		label: "Exam Attempt", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"created_at", "id"},
		validate: func(cursor examAttemptManagerCursorWire) error {
			createdAt, err := model.ParseRFC3339(cursor.CreatedAt)
			if err != nil || createdAt.IsZero() || !model.ExamAttemptID(cursor.ID).IsValid() {
				return errors.New("invalid Exam Attempt keyset")
			}
			return nil
		},
	}
}

type sittingCandidateStatusCursor struct {
	Kind            string `json:"kind"`
	CandidateUserID string `json:"candidate_user_id"`
}

type sittingCandidateStatusListResponse struct {
	ServerTime      string                               `json:"server_time"`
	ExamID          string                               `json:"exam_id"`
	ExamSittingID   string                               `json:"exam_sitting_id"`
	SittingState    model.ExamSittingState               `json:"sitting_state"`
	SittingRevision int64                                `json:"sitting_revision"`
	Items           []sittingCandidateStatusItemResponse `json:"items"`
	NextCursor      string                               `json:"next_cursor,omitempty"`
}

// nativeSecuritySummaryResponse presents a Candidate's live native coverage
// to the Sitting manager without detector or occurrence identities.
type nativeSecuritySummaryResponse struct {
	RetainedConditionRecords int64                               `json:"retained_condition_records"`
	LiveCoverageAvailable    bool                                `json:"live_coverage_available"`
	Sources                  []nativeSourceHealthSummaryResponse `json:"sources"`
}

type nativeSourceHealthSummaryResponse struct {
	SourceID   model.NativeSourceID `json:"source_id"`
	Health     string               `json:"health"`
	Permission string               `json:"permission"`
	Complete   bool                 `json:"complete"`
}

func nativeSecuritySummaryFrom(value *model.NativeSecuritySummary) *nativeSecuritySummaryResponse {
	if value == nil {
		return nil
	}
	response := &nativeSecuritySummaryResponse{RetainedConditionRecords: value.RetainedConditionRecords, LiveCoverageAvailable: value.LiveCoverageAvailable}
	if value.Sources != nil {
		response.Sources = make([]nativeSourceHealthSummaryResponse, 0, len(value.Sources))
	}
	for _, source := range value.Sources {
		response.Sources = append(response.Sources, nativeSourceHealthSummaryResponse{SourceID: source.SourceID, Health: source.Health, Permission: source.Permission, Complete: source.Complete})
	}
	return response
}

type sittingCandidateStatusItemResponse struct {
	NativeSecurity          *nativeSecuritySummaryResponse      `json:"native_security,omitempty"`
	Candidate               sittingCandidateIdentityResponse    `json:"candidate"`
	CurrentClassMembership  bool                                `json:"current_class_membership"`
	Attempt                 *sittingCandidateAttemptResponse    `json:"attempt"`
	Presence                sittingCandidatePresenceResponse    `json:"presence"`
	Suspension              *sittingCandidateSuspensionResponse `json:"suspension"`
	IntegrityAttentionCount int64                               `json:"integrity_attention_count"`
}

type sittingCandidateIdentityResponse struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type sittingCandidateAttemptResponse struct {
	ID         string                              `json:"id"`
	State      model.ExamAttemptState              `json:"state"`
	Revision   int64                               `json:"revision"`
	CreatedAt  string                              `json:"created_at"`
	UpdatedAt  string                              `json:"updated_at"`
	Submission *sittingCandidateSubmissionResponse `json:"submission"`
}

type sittingCandidateSubmissionResponse struct {
	ID          string                         `json:"id"`
	SubmittedAt string                         `json:"submitted_at"`
	Provenance  model.ExamSubmissionProvenance `json:"provenance"`
}

type sittingCandidatePresenceResponse struct {
	State              store.SittingCandidatePresenceState `json:"state"`
	LastLeaseRenewedAt *string                             `json:"last_lease_renewed_at,omitempty"`
	LeaseExpiresAt     *string                             `json:"lease_expires_at,omitempty"`
}

type sittingCandidateSuspensionResponse struct {
	ID               string                                 `json:"id"`
	CandidateReason  model.AttemptSuspensionCandidateReason `json:"candidate_reason"`
	ReallowAvailable bool                                   `json:"reallow_available"`
}

type examAttemptManagerResponse struct {
	ID                  string                            `json:"id"`
	ExamID              string                            `json:"exam_id"`
	SittingID           string                            `json:"exam_sitting_id"`
	CandidateUserID     string                            `json:"candidate_user_id"`
	AdmissionRevisionID string                            `json:"admission_revision_id"`
	State               string                            `json:"state"`
	CreatedAt           string                            `json:"created_at"`
	UpdatedAt           string                            `json:"updated_at"`
	SubmittedAt         string                            `json:"submitted_at,omitempty"`
	Revision            int64                             `json:"revision"`
	Workspace           examAttemptWorkspaceResponse      `json:"workspace"`
	LatestParticipation *examAttemptParticipationResponse `json:"latest_participation,omitempty"`
	CurrentConnection   *examAttemptConnectionResponse    `json:"current_connection,omitempty"`
	ActiveSuspension    *examAttemptSuspensionResponse    `json:"active_suspension,omitempty"`
}

type examAttemptWorkspaceResponse struct {
	ID        string `json:"id"`
	Cursor    int64  `json:"cursor"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type examAttemptParticipationResponse struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	Generation      int64  `json:"generation"`
	RenewalSequence int64  `json:"renewal_sequence"`
	StartedAt       string `json:"started_at"`
	UpdatedAt       string `json:"updated_at"`
	LeaseExpiresAt  string `json:"lease_expires_at"`
	EndedAt         string `json:"ended_at,omitempty"`
	EndReason       string `json:"end_reason,omitempty"`
}

type examAttemptConnectionResponse struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	OpenedAt    string `json:"opened_at"`
	ClosedAt    string `json:"closed_at,omitempty"`
	CloseReason string `json:"close_reason,omitempty"`
}

type examAttemptSuspensionResponse struct {
	ID                string `json:"id"`
	ParticipationID   string `json:"participation_id"`
	FlagID            string `json:"integrity_flag_id,omitempty"`
	Generation        int64  `json:"generation"`
	State             string `json:"state"`
	Source            string `json:"source"`
	CandidateReason   string `json:"candidate_reason"`
	StartedAt         string `json:"started_at"`
	EndedAt           string `json:"ended_at,omitempty"`
	ReallowedByUserID string `json:"reallowed_by_user_id,omitempty"`
	SuspendedByUserID string `json:"suspended_by_user_id,omitempty"`
}

type examAttemptManagerListResponse struct {
	Items      []examAttemptManagerResponse `json:"items"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

type browserActivityListResponse struct {
	Items      []browserActivityItemResponse `json:"items"`
	NextCursor string                        `json:"next_cursor,omitempty"`
}

type browserActivityItemResponse struct {
	RedirectFromSequence *int64                            `json:"redirect_from_sequence"`
	SourceSessionID      string                            `json:"source_session_id"`
	Generation           int64                             `json:"generation"`
	Sequence             int64                             `json:"sequence"`
	Kind                 model.BrowserActivityKind         `json:"kind"`
	PolicyRevision       string                            `json:"policy_revision_id"`
	ClientOccurred       string                            `json:"client_occurred_at"`
	Location             *browserActivityLocationResponse  `json:"location"`
	MatchedRuleID        *string                           `json:"matched_rule_id"`
	BlockReason          *model.BrowserActivityBlockReason `json:"block_reason"`
	ReceivedAt           string                            `json:"received_at"`
}

type browserActivityLocationResponse struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   string `json:"port,omitempty"`
	Path   string `json:"path"`
}

type reallowExamAttemptRequest struct {
	SuspensionID            string `json:"suspension_id"`
	ExpectedAttemptRevision int64  `json:"expected_revision"`
	Reason                  string `json:"reason"`
}

type examAttemptReallowResponse struct {
	ExamAttemptID     string `json:"exam_attempt_id"`
	ExamSittingID     string `json:"exam_sitting_id"`
	State             string `json:"state"`
	AttemptRevision   int64  `json:"attempt_revision"`
	SuspensionID      string `json:"suspension_id"`
	SuspensionState   string `json:"suspension_state"`
	CandidateReason   string `json:"candidate_reason"`
	ReallowedByUserID string `json:"reallowed_by_user_id"`
}

type suspendExamAttemptRequest struct {
	ExpectedAttemptRevision int64  `json:"expected_revision"`
	Reason                  string `json:"reason"`
}

type examAttemptSuspendResponse struct {
	ExamAttemptID     string `json:"exam_attempt_id"`
	ExamSittingID     string `json:"exam_sitting_id"`
	State             string `json:"state"`
	AttemptRevision   int64  `json:"attempt_revision"`
	SuspensionID      string `json:"suspension_id"`
	SuspensionState   string `json:"suspension_state"`
	Source            string `json:"source"`
	CandidateReason   string `json:"candidate_reason"`
	SuspendedByUserID string `json:"suspended_by_user_id"`
	ConnectionClosed  bool   `json:"connection_closed"`
}

type endExamAttemptByManagerRequest struct {
	ExpectedAttemptRevision int64  `json:"expected_revision"`
	Reason                  string `json:"reason"`
}

func (m examAttemptHTTPModule) endByManager(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body endExamAttemptByManagerRequest
	if err = decodeCandidateWorkspaceJSON(request, &body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedAttemptRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	receipt, err := m.application.EndExamAttemptByManager(request.context, request.invocation(),
		attempt.ManagerEndCommand{ExamID: examID, SittingID: sittingID, AttemptID: attemptID,
			ExpectedAttemptRevision: body.ExpectedAttemptRevision, PrivateReason: body.Reason,
			IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	response := examSubmissionReceiptResponse{BrowserActivity: receipt.BrowserActivity, SubmissionID: receipt.SubmissionID.String(),
		ExamAttemptID: receipt.AttemptID.String(), ExamRevisionID: receipt.ExamRevisionID.String(),
		State: string(receipt.State), WorkspaceCursor: receipt.WorkspaceCursor,
		ManifestDigest: receipt.ManifestDigest, SubmittedAt: model.FormatRFC3339(receipt.SubmittedAt)}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) listCandidateStatuses(request operationRequest) (operationResult, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return operationResult{}, err
	}
	query := attempt.SittingCandidateStatusesQuery{ExamID: examID, SittingID: sittingID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeOpaqueCursor(raw, sittingCandidateStatusCursorSpec())
		if decodeErr != nil {
			return operationResult{}, invalidRequestError(decodeErr)
		}
		query.AfterCandidateUserID = model.UserID(cursor.CandidateUserID)
	}
	page, err := m.application.ListSittingCandidateStatuses(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := sittingCandidateStatusListResponse{ServerTime: model.FormatRFC3339(page.ServerTime),
		ExamID: page.ExamID.String(), ExamSittingID: page.SittingID.String(), SittingState: page.SittingState,
		SittingRevision: page.SittingRevision, Items: make([]sittingCandidateStatusItemResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		mapped := sittingCandidateStatusItemResponse{Candidate: sittingCandidateIdentityResponse{UserID: item.Candidate.UserID.String(),
			Username: item.Candidate.Username, DisplayName: item.Candidate.DisplayName},
			CurrentClassMembership: item.CurrentClassMembership,
			Presence:               sittingCandidatePresenceResponse{State: item.Presence.State},
			NativeSecurity:         nativeSecuritySummaryFrom(item.NativeSecurity), IntegrityAttentionCount: item.IntegrityAttentionCount}
		if item.Presence.LastLeaseRenewedAt.Valid {
			value := model.FormatRFC3339(item.Presence.LastLeaseRenewedAt.Time)
			mapped.Presence.LastLeaseRenewedAt = &value
		}
		if item.Presence.LeaseExpiresAt.Valid {
			value := model.FormatRFC3339(item.Presence.LeaseExpiresAt.Time)
			mapped.Presence.LeaseExpiresAt = &value
		}
		if item.Attempt != nil {
			mapped.Attempt = &sittingCandidateAttemptResponse{ID: item.Attempt.ID.String(), State: item.Attempt.State,
				Revision: item.Attempt.Revision, CreatedAt: model.FormatRFC3339(item.Attempt.CreatedAt),
				UpdatedAt: model.FormatRFC3339(item.Attempt.UpdatedAt)}
			if item.Attempt.Submission != nil {
				mapped.Attempt.Submission = &sittingCandidateSubmissionResponse{ID: item.Attempt.Submission.ID.String(),
					SubmittedAt: model.FormatRFC3339(item.Attempt.Submission.SubmittedAt),
					Provenance:  item.Attempt.Submission.Provenance}
			}
		}
		if item.Suspension != nil {
			mapped.Suspension = &sittingCandidateSuspensionResponse{ID: item.Suspension.ID.String(),
				CandidateReason: item.Suspension.CandidateReason, ReallowAvailable: item.Suspension.ReallowAvailable}
		}
		response.Items = append(response.Items, mapped)
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errUnpresentableResult
		}
		response.NextCursor, err = encodeOpaqueCursor(sittingCandidateStatusCursor{
			Kind: sittingCandidateStatusCursorKind, CandidateUserID: page.Items[len(page.Items)-1].Candidate.UserID.String(),
		}, sittingCandidateStatusCursorSpec())
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func sittingCandidateStatusCursorSpec() opaqueCursorSpec[sittingCandidateStatusCursor] {
	return opaqueCursorSpec[sittingCandidateStatusCursor]{label: "Sitting candidate status",
		maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members:              []string{"kind", "candidate_user_id"},
		validate: func(cursor sittingCandidateStatusCursor) error {
			if cursor.Kind != sittingCandidateStatusCursorKind || !model.UserID(cursor.CandidateUserID).IsValid() {
				return errors.New("invalid Sitting candidate-status keyset")
			}
			return nil
		}}
}

func (m examAttemptHTTPModule) reallow(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body reallowExamAttemptRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	suspensionID, err := model.ParseAttemptSuspensionID(body.SuspensionID)
	if err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	result, err := m.application.ReallowExamAttempt(request.context, request.invocation(), attempt.ReallowCommand{
		ExamID: examID, SittingID: sittingID, AttemptID: attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: body.ExpectedAttemptRevision, PrivateReason: body.Reason, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	response := examAttemptReallowResponse{ExamAttemptID: result.Attempt.ID.String(), ExamSittingID: result.SittingID.String(),
		State: string(result.Attempt.State), AttemptRevision: result.Attempt.Revision, SuspensionID: result.Suspension.ID.String(),
		SuspensionState: string(result.Suspension.State), CandidateReason: string(result.Suspension.CandidateReason),
		ReallowedByUserID: result.Suspension.ReallowedByUserID.String()}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) suspend(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body suspendExamAttemptRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedAttemptRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("expected_revision must be positive"))
	}
	result, err := m.application.SuspendExamAttempt(request.context, request.invocation(), attempt.ManualSuspensionCommand{
		ExamID: examID, SittingID: sittingID, AttemptID: attemptID,
		ExpectedAttemptRevision: body.ExpectedAttemptRevision, PrivateReason: body.Reason, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	response := examAttemptSuspendResponse{ExamAttemptID: result.Attempt.ID.String(), ExamSittingID: result.SittingID.String(),
		State: string(result.Attempt.State), AttemptRevision: result.Attempt.Revision, SuspensionID: result.Suspension.ID.String(),
		SuspensionState: string(result.Suspension.State), Source: string(result.Suspension.Source),
		CandidateReason: string(result.Suspension.CandidateReason), SuspendedByUserID: result.Suspension.SuspendedByUserID.String(),
		ConnectionClosed: result.ConnectionClosed}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) getManaged(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.application.GetExamAttempt(request.context, request.invocation(), attempt.GetManagedAttemptQuery{
		ExamID: examID, SittingID: sittingID, AttemptID: attemptID,
	})
	if err != nil {
		return operationResult{}, err
	}
	response, err := examAttemptManagerResponseFromView(view)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) listManaged(request operationRequest) (operationResult, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return operationResult{}, err
	}
	query := attempt.ListManagedAttemptsQuery{ExamID: examID, SittingID: sittingID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
		}
	}
	seenStates := make(map[model.ExamAttemptState]struct{})
	for _, raw := range values["state"] {
		state := model.ExamAttemptState(raw)
		switch state {
		case model.ExamAttemptReady, model.ExamAttemptActive, model.ExamAttemptSuspended, model.ExamAttemptSubmitted:
		default:
			return operationResult{}, invalidRequestError(errors.New("is not supported"))
		}
		if _, duplicate := seenStates[state]; duplicate {
			return operationResult{}, invalidRequestError(errors.New("must not contain duplicates"))
		}
		seenStates[state] = struct{}{}
		query.States = append(query.States, state)
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeExamAttemptManagerCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError(decodeErr)
		}
		query.BeforeCreatedAt, query.BeforeAttemptID = cursor.CreatedAt, cursor.ID
	}
	page, err := m.application.ListExamAttempts(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examAttemptManagerListResponse{Items: make([]examAttemptManagerResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		mapped, mapErr := examAttemptManagerResponseFromView(item)
		if mapErr != nil {
			return operationResult{}, mapErr
		}
		response.Items = append(response.Items, mapped)
	}
	if page.HasMore {
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].Attempt == nil {
			return operationResult{}, errors.New("Exam Attempt application returned an invalid page")
		}
		last := page.Items[len(page.Items)-1].Attempt
		response.NextCursor, err = encodeExamAttemptManagerCursor(examAttemptManagerCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) listBrowserActivity(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	query := attempt.BrowserActivityPageQuery{ExamID: examID, SittingID: sittingID, AttemptID: attemptID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeBrowserActivityCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError(decodeErr)
		}
		query.AfterReceivedAt, query.AfterSourceID, query.AfterSequence = cursor.ReceivedAt, cursor.SourceSessionID, cursor.Sequence
	}
	page, err := m.application.ListExamAttemptBrowserActivity(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := browserActivityListResponse{Items: make([]browserActivityItemResponse, 0, len(page.Items))}
	for _, record := range page.Items {
		event := record.Event
		item := browserActivityItemResponse{RedirectFromSequence: event.RedirectFromSequence, SourceSessionID: string(record.SourceSessionID), Generation: record.Generation,
			Sequence: event.Sequence, Kind: event.Kind, PolicyRevision: event.PolicyRevisionID.String(),
			ClientOccurred: model.FormatRFC3339(event.ClientOccurredAt), MatchedRuleID: event.MatchedRuleID,
			BlockReason: event.BlockReason, ReceivedAt: model.FormatRFC3339(event.ReceivedAt)}
		if event.Location != nil {
			item.Location = &browserActivityLocationResponse{Scheme: event.Location.Scheme, Host: event.Location.Host, Port: event.Location.Port, Path: event.Location.Path}
		}
		response.Items = append(response.Items, item)
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errors.New("Browser Activity application returned an invalid page")
		}
		last := page.Items[len(page.Items)-1]
		response.NextCursor, err = encodeBrowserActivityCursor(browserActivityCursor{ReceivedAt: last.Event.ReceivedAt,
			SourceSessionID: last.SourceSessionID, Sequence: last.Event.Sequence})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func managedExamAttemptScope(request operationRequest) (model.ExamID, model.ExamSittingID, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return "", "", err
	}
	sittingID, err := request.params.RequireExamSittingID()
	if err != nil {
		return "", "", err
	}
	return examID, sittingID, nil
}

func managedExamAttemptIDs(request operationRequest) (model.ExamID, model.ExamSittingID, model.ExamAttemptID, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return "", "", "", err
	}
	attemptID, err := request.params.RequireExamAttemptID()
	if err != nil {
		return "", "", "", err
	}
	return examID, sittingID, attemptID, nil
}

func examAttemptManagerResponseFromView(view store.ExamAttemptManagerSnapshot) (examAttemptManagerResponse, error) {
	if view.Attempt == nil || view.Workspace == nil {
		return examAttemptManagerResponse{}, errors.New("Exam Attempt application returned an incomplete manager projection")
	}
	attempt, workspace := view.Attempt, view.Workspace
	response := examAttemptManagerResponse{ID: attempt.ID.String(), ExamID: attempt.ExamID.String(), SittingID: attempt.SittingID.String(),
		CandidateUserID: attempt.CandidateUserID.String(), AdmissionRevisionID: attempt.AdmissionRevisionID.String(), State: string(attempt.State),
		CreatedAt: model.FormatRFC3339(attempt.CreatedAt), UpdatedAt: model.FormatRFC3339(attempt.UpdatedAt),
		SubmittedAt: attempt.SubmittedAt.FormatRFC3339(), Revision: attempt.Revision,
		Workspace: examAttemptWorkspaceResponse{ID: workspace.ID.String(), Cursor: workspace.Cursor,
			CreatedAt: model.FormatRFC3339(workspace.CreatedAt), UpdatedAt: model.FormatRFC3339(workspace.UpdatedAt)}}
	if participation := view.LatestParticipation; participation != nil {
		response.LatestParticipation = &examAttemptParticipationResponse{ID: participation.ID.String(), State: string(participation.State),
			Generation: participation.Generation, RenewalSequence: participation.RenewalSequence,
			StartedAt: model.FormatRFC3339(participation.StartedAt), UpdatedAt: model.FormatRFC3339(participation.UpdatedAt),
			LeaseExpiresAt: model.FormatRFC3339(participation.LeaseExpiresAt), EndedAt: participation.EndedAt.FormatRFC3339(), EndReason: string(participation.EndReason)}
	}
	if connection := view.CurrentConnection; connection != nil {
		response.CurrentConnection = &examAttemptConnectionResponse{ID: connection.ID.String(), State: string(connection.State),
			OpenedAt: model.FormatRFC3339(connection.OpenedAt), ClosedAt: connection.ClosedAt.FormatRFC3339(), CloseReason: string(connection.CloseReason)}
	}
	if suspension := view.ActiveSuspension; suspension != nil {
		response.ActiveSuspension = &examAttemptSuspensionResponse{ID: suspension.ID.String(),
			ParticipationID: suspension.ParticipationID.String(), FlagID: suspension.FlagID.String(), Generation: suspension.Generation,
			State: string(suspension.State), Source: string(suspension.Source), CandidateReason: string(suspension.CandidateReason),
			StartedAt: model.FormatRFC3339(suspension.StartedAt), EndedAt: suspension.EndedAt.FormatRFC3339(),
			ReallowedByUserID: suspension.ReallowedByUserID.String(), SuspendedByUserID: suspension.SuspendedByUserID.String()}
	}
	return response, nil
}
