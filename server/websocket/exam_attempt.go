// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	examAttemptConnectAction        = "exam_attempt.connect"
	examAttemptRenewAction          = "exam_attempt.renew"
	examAttemptSecurityUpdateAction = "exam_attempt.security.update"
	examAttemptFocusLossAction      = "exam_attempt.focus_loss"
	examAttemptBrowserStartAction   = "exam_attempt.browser_activity.start"
	examAttemptBrowserAppendAction  = "exam_attempt.browser_activity.append"
)

type examAttemptApplication interface {
	ConnectExamAttempt(context.Context, app.Invocation, app.ConnectExamAttemptCommand) (app.ExamAttemptConnection, error)
	UpdateExamSecurityCoverage(context.Context, app.Invocation, app.UpdateSecurityCoverageCommand) (model.SecurityCoverageResult, error)
	RenewExamAttemptParticipation(context.Context, app.Invocation, app.RenewExamAttemptParticipationCommand) (app.ExamAttemptParticipationRenewal, error)
	EvaluateExamAttemptFocusLoss(context.Context, app.Invocation, app.EvaluateExamAttemptFocusLossCommand) (app.ExamAttemptFocusLossEvaluation, error)
	CloseExamAttemptConnection(context.Context, app.Invocation, app.CloseExamAttemptConnectionCommand) (app.ExamAttemptConnectionClosed, error)
	StartExamAttemptBrowserActivity(context.Context, app.Invocation, app.StartBrowserActivityCommand) (model.BrowserSourceStatus, error)
	AppendExamAttemptBrowserActivity(context.Context, app.Invocation, app.AppendBrowserActivityCommand) (app.BrowserActivityAcknowledgement, error)
}

type examAttemptConnectRequest struct {
	Security                         model.ConnectSecurity                `json:"security"`
	ExamSittingID                    string                               `json:"exam_sitting_id"`
	IdempotencyKey                   string                               `json:"idempotency_key"`
	ContinuityCredential             string                               `json:"continuity_credential"`
	ConfigurationManifestFingerprint string                               `json:"configuration_manifest_fingerprint"`
	InitialConfiguration             *model.AttemptConfigurationCandidate `json:"initial_configuration,omitempty"`
}

type examAttemptConnectResponse struct {
	Security                     model.AdmittedSecurity               `json:"security"`
	AttemptID                    string                               `json:"attempt_id"`
	WorkspaceID                  string                               `json:"workspace_id"`
	ParticipationID              string                               `json:"participation_id"`
	AttemptConnectionID          string                               `json:"attempt_connection_id"`
	Generation                   int64                                `json:"generation"`
	RenewalIntervalSeconds       int64                                `json:"renewal_interval_seconds"`
	StartedAt                    string                               `json:"started_at"`
	LeaseExpiresAt               string                               `json:"lease_expires_at"`
	FirstAdmission               bool                                 `json:"first_admission"`
	Replayed                     bool                                 `json:"replayed"`
	CandidateRuntimeCapabilities candidateRuntimeCapabilitiesResponse `json:"candidate_runtime_capabilities"`
	BrowserPolicy                *candidateBrowserPolicyResponse      `json:"browser_policy"`
	LiveCorrections              []candidateLiveCorrectionResponse    `json:"live_corrections"`
}

type candidateLiveCorrectionResponse struct {
	RevisionID              string                               `json:"revision_id"`
	RevisionNumber          int64                                `json:"revision_number"`
	EffectiveAt             string                               `json:"effective_at"`
	Summary                 string                               `json:"summary"`
	ChangedAreas            []model.ExamCorrectionChangedArea    `json:"changed_areas"`
	AffectedCapabilities    []model.CandidateCapability          `json:"affected_capabilities"`
	AcknowledgementRequired bool                                 `json:"acknowledgement_required"`
	AcknowledgementState    model.CorrectionAcknowledgementState `json:"acknowledgement_state"`
	AcknowledgedAt          *string                              `json:"acknowledged_at,omitempty"`
}

type candidateBrowserPolicyResponse struct {
	PolicyRevisionNumber      int64                                `json:"policy_revision_number"`
	BrowserActivityDisclosure model.BrowserActivityDisclosure      `json:"browser_activity_disclosure"`
	Enabled                   bool                                 `json:"enabled"`
	StartRuleID               string                               `json:"start_rule_id,omitempty"`
	Rules                     []candidateBrowserPolicyRuleResponse `json:"rules,omitempty"`
	PolicyRevisionID          string                               `json:"policy_revision_id"`
	PolicyDigest              string                               `json:"policy_digest"`
}

type candidateBrowserPolicyRuleResponse struct {
	RuleID                   string `json:"rule_id"`
	Origin                   string `json:"origin"`
	PathPrefix               string `json:"path_prefix"`
	HostMatch                string `json:"host_match"`
	AllowRedirects           bool   `json:"allow_redirects"`
	BlockedNavigationOutcome string `json:"blocked_navigation_outcome"`
	InstitutionHTTPException bool   `json:"institution_http_exception"`
}

type candidateRuntimeCapabilitiesResponse struct {
	SchemaVersion                 int                                   `json:"schema_version"`
	ServerTime                    string                                `json:"server_time"`
	InteractionState              string                                `json:"interaction_state"`
	AttemptConfiguration          candidateAttemptConfigurationResponse `json:"attempt_configuration"`
	FocusLossCollectionEnabled    bool                                  `json:"focus_loss_collection_enabled"`
	PendingCorrectionCapabilities []model.CandidateCapability           `json:"pending_correction_capabilities"`
	WorkspaceMutationAllowed      bool                                  `json:"workspace_mutation_allowed"`
	SubmissionAllowed             bool                                  `json:"submission_allowed"`
	Browser                       candidateBrowserCapabilityResponse    `json:"browser"`
	ExamRevision                  candidateExamRevisionResponse         `json:"exam_revision"`
	Departure                     candidateDepartureResponse            `json:"departure"`
}

type candidateAttemptConfigurationResponse struct {
	Revision            string                                 `json:"attempt_configuration_revision"`
	Presentation        model.AttemptConfigurationPresentation `json:"presentation"`
	ApprovedCommands    []string                               `json:"approved_commands"`
	ApprovedKeybindings []string                               `json:"approved_keybindings"`
	Digest              string                                 `json:"digest"`
}

type candidateBrowserCapabilityResponse struct {
	State            string `json:"state"`
	PolicyRevisionID string `json:"policy_revision_id,omitempty"`
	PolicyDigest     string `json:"policy_digest,omitempty"`
}
type candidateExamRevisionResponse struct {
	AdmissionRevisionID     string `json:"admission_revision_id"`
	CurrentRevisionID       string `json:"current_revision_id"`
	AcknowledgementRequired bool   `json:"acknowledgement_required"`
}
type candidateDepartureResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type examAttemptRenewRequest struct {
	SecurityCoverage     model.SecurityCoverageRenewal `json:"security_coverage"`
	Generation           int64                         `json:"generation"`
	Sequence             int64                         `json:"sequence"`
	ContinuityCredential string                        `json:"continuity_credential"`
}

type examAttemptRenewResponse struct {
	SecurityCoverage model.SecurityCoverageResult `json:"security_coverage"`
	Generation       int64                        `json:"generation"`
	AcceptedSequence int64                        `json:"accepted_sequence"`
	DatabaseTime     string                       `json:"database_time"`
	LeaseExpiresAt   string                       `json:"lease_expires_at"`
	Duplicate        bool                         `json:"duplicate"`
}

type examAttemptFocusLossRequest struct {
	SchemaVersion        int    `json:"schema_version"`
	Generation           int64  `json:"generation"`
	Sequence             int64  `json:"sequence"`
	DurationMilliseconds int64  `json:"duration_milliseconds"`
	Source               string `json:"source,omitempty"`
	ContinuityCredential string `json:"continuity_credential"`
}

type examAttemptFocusLossResponse struct {
	Generation          int64  `json:"generation"`
	AcceptedSequence    int64  `json:"accepted_sequence"`
	ReceivedAt          string `json:"received_at"`
	Duplicate           bool   `json:"duplicate"`
	GapDetected         bool   `json:"gap_detected"`
	PolicyDisabled      bool   `json:"policy_disabled"`
	WarningCreated      bool   `json:"warning_created"`
	SuspensionCreated   bool   `json:"suspension_created"`
	DiscrepancyRecorded bool   `json:"discrepancy_recorded"`
}

type examAttemptBrowserStartRequest struct {
	Generation           int64                        `json:"generation"`
	ContinuityCredential string                       `json:"continuity_credential"`
	ParticipationID      string                       `json:"participation_id"`
	SourceSessionID      string                       `json:"source_session_id"`
	PolicyRevisionID     string                       `json:"policy_revision_id"`
	PolicyDigest         string                       `json:"policy_digest"`
	Transition           model.BrowserStartTransition `json:"transition"`
}

type examAttemptBrowserAppendRequest struct {
	PolicyRevisionID     string            `json:"policy_revision_id"`
	PolicyDigest         string            `json:"policy_digest"`
	Generation           int64             `json:"generation"`
	ContinuityCredential string            `json:"continuity_credential"`
	ParticipationID      string            `json:"participation_id"`
	SourceSessionID      string            `json:"source_session_id"`
	Events               []json.RawMessage `json:"events"`
}

type examAttemptBrowserEventRequest struct {
	Sequence         int64           `json:"sequence"`
	Kind             string          `json:"kind"`
	PolicyRevisionID string          `json:"policy_revision_id"`
	ClientOccurredAt string          `json:"client_occurred_at"`
	Location         json.RawMessage `json:"location,omitempty"`
	MatchedRuleID    json.RawMessage `json:"matched_rule_id,omitempty"`
	BlockReason      json.RawMessage `json:"block_reason,omitempty"`
}

type examAttemptBrowserAcknowledgementResponse struct {
	SourceSessionID        string                                `json:"source_session_id"`
	Receipts               []model.BrowserEventReceipt           `json:"receipts"`
	HighestContiguous      int64                                 `json:"highest_contiguous_sequence"`
	SettledThrough         int64                                 `json:"settled_through_sequence"`
	HighestSeen            int64                                 `json:"highest_seen_sequence"`
	AllocatedThrough       int64                                 `json:"allocated_through_sequence"`
	TerminalMissingThrough int64                                 `json:"terminal_missing_through_sequence"`
	MissingRanges          []browserActivityMissingRangeResponse `json:"missing_ranges"`
	MissingRangesTruncated bool                                  `json:"missing_ranges_truncated"`
	ServerTime             string                                `json:"server_time"`
}

type browserActivityMissingRangeResponse struct {
	First int64 `json:"first"`
	Last  int64 `json:"last"`
}

func decodeExamAttemptBrowserStartRequest(document json.RawMessage) (examAttemptBrowserStartRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptBrowserStartRequest](document, "Browser Activity source start request", 7)
	if err != nil {
		return value, err
	}
	declaration := model.BrowserSourceStart{ParticipationID: model.AttemptParticipationID(value.ParticipationID), Generation: value.Generation, SourceSessionID: model.BrowserSourceSessionID(value.SourceSessionID), PolicyRevisionID: model.ExamRevisionID(value.PolicyRevisionID), PolicyDigest: value.PolicyDigest, Transition: value.Transition}
	if len(document) > 8192 || !model.IsValidCredentialToken(value.ContinuityCredential) || declaration.Validate() != nil {
		return value, errors.New("invalid Browser Activity source start")
	}
	return value, nil
}

func decodeExamAttemptBrowserAppendRequest(document json.RawMessage) (examAttemptBrowserAppendRequest, []model.BrowserActivityEvent, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptBrowserAppendRequest](document, "Browser Activity append request", 7)
	if err != nil {
		return value, nil, err
	}
	if len(document) > model.BrowserActivityAppendMaximumBytes || !model.IsValidCredentialToken(value.ContinuityCredential) {
		return value, nil, model.ErrDeliveryInvalid
	}
	batch := model.BrowserActivityBatch{SourceSessionID: model.BrowserSourceSessionID(value.SourceSessionID), ParticipationID: model.AttemptParticipationID(value.ParticipationID), Generation: value.Generation, PolicyRevisionID: model.ExamRevisionID(value.PolicyRevisionID), PolicyDigest: value.PolicyDigest, Events: make([]model.BrowserActivityEvent, len(value.Events))}
	for i, raw := range value.Events {
		if json.Unmarshal(raw, &batch.Events[i]) != nil {
			return value, nil, model.ErrDeliveryInvalid
		}
	}
	if batch.Validate() != nil {
		return value, nil, model.ErrDeliveryInvalid
	}
	return value, batch.Events, nil
}

func browserActivityAcknowledgementWire(value app.BrowserActivityAcknowledgement) examAttemptBrowserAcknowledgementResponse {
	response := examAttemptBrowserAcknowledgementResponse{SourceSessionID: string(value.SourceSessionID), HighestContiguous: value.HighestContiguous,
		Receipts: append([]model.BrowserEventReceipt{}, value.Receipts...), SettledThrough: value.SettledThrough,
		AllocatedThrough: value.AllocatedThrough, TerminalMissingThrough: value.TerminalMissingThrough,
		HighestSeen: value.HighestSeen, MissingRangesTruncated: value.MissingRangesTruncated,
		MissingRanges: make([]browserActivityMissingRangeResponse, len(value.MissingRanges)), ServerTime: model.TimeUTC(value.ServerTime).Format(time.RFC3339Nano)}
	for index, missing := range value.MissingRanges {
		response.MissingRanges[index] = browserActivityMissingRangeResponse{First: missing.First, Last: missing.Last}
	}
	return response
}

func decodeExamAttemptConnectRequest(document json.RawMessage) (examAttemptConnectRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptConnectRequest](document, "Exam Attempt connect request", 5)
	if err != nil {
		return value, err
	}
	if value.IdempotencyKey == "" || !model.IsValidCredentialToken(value.ContinuityCredential) ||
		!model.IsValidSHA256Fingerprint(value.ConfigurationManifestFingerprint) {
		return value, errors.New("Exam Attempt connect request fields are invalid")
	}
	if _, err = model.ParseExamSittingID(value.ExamSittingID); err != nil {
		return value, errors.New("Exam Attempt Sitting identity is invalid")
	}
	return value, nil
}

func candidateRuntimeCapabilitiesWire(value app.CandidateRuntimeCapabilities) candidateRuntimeCapabilitiesResponse {
	browser := candidateBrowserCapabilityResponse{State: string(value.Browser.State)}
	if value.Browser.PolicyRevisionID.IsValid() {
		browser.PolicyRevisionID = value.Browser.PolicyRevisionID.String()
		browser.PolicyDigest = value.Browser.PolicyDigest
	}
	return candidateRuntimeCapabilitiesResponse{SchemaVersion: value.SchemaVersion,
		ServerTime: value.ServerTime.Format(time.RFC3339Nano), InteractionState: string(value.InteractionState),
		AttemptConfiguration: candidateAttemptConfigurationResponse{Revision: value.AttemptConfiguration.Revision,
			Presentation: value.AttemptConfiguration.Presentation, ApprovedCommands: append([]string{}, value.AttemptConfiguration.ApprovedCommands...),
			ApprovedKeybindings: append([]string{}, value.AttemptConfiguration.ApprovedKeybindings...), Digest: value.AttemptConfiguration.Digest},
		FocusLossCollectionEnabled:    value.FocusLossCollectionEnabled,
		PendingCorrectionCapabilities: append([]model.CandidateCapability{}, value.PendingCorrectionCapabilities...),
		WorkspaceMutationAllowed:      value.WorkspaceMutationAllowed, SubmissionAllowed: value.SubmissionAllowed,
		Browser: browser,
		ExamRevision: candidateExamRevisionResponse{AdmissionRevisionID: value.ExamRevision.AdmissionRevisionID.String(),
			CurrentRevisionID: value.ExamRevision.CurrentRevisionID.String(), AcknowledgementRequired: value.ExamRevision.AcknowledgementRequired},
		Departure: candidateDepartureResponse{Allowed: value.Departure.Allowed, Reason: value.Departure.Reason}}
}

func candidateBrowserPolicyWire(value *app.CandidateBrowserPolicy) *candidateBrowserPolicyResponse {
	if value == nil {
		return nil
	}
	response := &candidateBrowserPolicyResponse{Enabled: value.Policy.Enabled, PolicyRevisionNumber: value.PolicyRevisionNumber, BrowserActivityDisclosure: value.BrowserActivityDisclosure,
		StartRuleID: value.Policy.StartRuleID, PolicyRevisionID: value.PolicyRevisionID.String(), PolicyDigest: value.PolicyDigest,
		Rules: make([]candidateBrowserPolicyRuleResponse, len(value.Policy.Rules))}
	for index, rule := range value.Policy.Rules {
		response.Rules[index] = candidateBrowserPolicyRuleResponse{RuleID: rule.RuleID, Origin: rule.Origin, PathPrefix: rule.PathPrefix,
			HostMatch: string(rule.HostMatch), AllowRedirects: rule.AllowRedirects, BlockedNavigationOutcome: string(rule.BlockedNavigationOutcome), InstitutionHTTPException: rule.InstitutionHTTPException}
	}
	return response
}

func candidateLiveCorrectionsWire(values []model.CandidateLiveCorrection) []candidateLiveCorrectionResponse {
	result := make([]candidateLiveCorrectionResponse, len(values))
	for index, value := range values {
		item := candidateLiveCorrectionResponse{RevisionID: value.RevisionID.String(), RevisionNumber: value.RevisionNumber,
			EffectiveAt: model.TimeUTC(value.EffectiveAt).Format(time.RFC3339Nano), Summary: value.Summary,
			ChangedAreas:         append([]model.ExamCorrectionChangedArea(nil), value.ChangedAreas...),
			AffectedCapabilities: append([]model.CandidateCapability{}, value.AffectedCapabilities...), AcknowledgementRequired: value.AcknowledgementRequired, AcknowledgementState: value.AcknowledgementState}
		if value.AcknowledgedAt.Valid {
			at := model.TimeUTC(value.AcknowledgedAt.Time).Format(time.RFC3339Nano)
			item.AcknowledgedAt = &at
		}
		result[index] = item
	}
	return result
}

func decodeExamAttemptRenewRequest(document json.RawMessage) (examAttemptRenewRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptRenewRequest](document, "Exam Attempt renewal request", 4)
	if err != nil {
		return value, err
	}
	if len(document) > model.SecurityControlMaxBytes || value.SecurityCoverage.Validate() != nil || value.Generation < 1 || value.Sequence < 1 || !model.IsValidCredentialToken(value.ContinuityCredential) {
		return value, errors.New("Exam Attempt renewal request fields are invalid")
	}
	return value, nil
}

func decodeExamAttemptFocusLossRequest(document json.RawMessage) (examAttemptFocusLossRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptFocusLossRequest](document, "Exam Attempt Focus Loss request", 6)
	if err != nil {
		return value, err
	}
	if value.SchemaVersion != model.FocusLossSignalSchemaVersion || value.Generation < 1 || value.Sequence < 1 ||
		value.DurationMilliseconds < 1 ||
		value.DurationMilliseconds > model.FocusLossMaximumDurationMilliseconds ||
		!model.FocusLossSource(value.Source).IsValid() || !model.IsValidCredentialToken(value.ContinuityCredential) {
		return value, errors.New("Exam Attempt Focus Loss request fields are invalid")
	}
	return value, nil
}

func decodeStrictExamAttemptObject[T any](document json.RawMessage, label string, expectedMembers int) (T, error) {
	var value T
	if !utf8.Valid(document) {
		return value, errors.New(label + " must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return value, errors.New(label + " must be an object")
	}
	seen := make(map[string]struct{}, expectedMembers)
	for decoder.More() {
		member, tokenErr := decoder.Token()
		if tokenErr != nil {
			return value, tokenErr
		}
		name, ok := member.(string)
		if !ok {
			return value, errors.New(label + " member is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return value, errors.New(label + " contains a duplicate member")
		}
		seen[name] = struct{}{}
		var raw json.RawMessage
		if err = decoder.Decode(&raw); err != nil {
			return value, err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return value, err
	}
	strict := json.NewDecoder(bytes.NewReader(document))
	strict.DisallowUnknownFields()
	if err = strict.Decode(&value); err != nil {
		return value, err
	}
	var trailing any
	if err = strict.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return value, errors.New(label + " contains trailing JSON")
		}
		return value, err
	}
	return value, nil
}

func examAttemptConnectError(err error) (string, websocketErrorPresentation) {
	code := "exam.attempt.unavailable"
	presentation := websocketErrorAttemptConnectionFailed
	if failure, ok := app.As(err); ok {
		code = failure.Code()
		if code == "resource.not_found" || code == "authorization.denied" {
			presentation = websocketErrorAttemptConnectionDenied
		}
	}
	return code, presentation
}

func examAttemptRenewError(err error) (string, websocketErrorPresentation) {
	code := "exam.attempt.unavailable"
	presentation := websocketErrorAttemptRenewalFailed
	if failure, ok := app.As(err); ok {
		code = failure.Code()
		switch code {
		case "resource.not_found", "authorization.denied":
			presentation = websocketErrorAttemptRenewalDenied
		case "exam.attempt.connection_lost":
			presentation = websocketErrorAttemptConnectionLost
		}
	}
	return code, presentation
}

func examAttemptFocusLossError(err error) (string, websocketErrorPresentation) {
	failure, ok := app.As(err)
	if !ok {
		return "exam.attempt.unavailable", websocketErrorFocusLossFailed
	}
	switch failure.Code() {
	case "authorization.denied", "resource.not_found":
		return "resource.not_found", websocketErrorFocusLossDenied
	case "authentication.invalid_token":
		return "authentication.invalid_token", websocketErrorFocusLossDenied
	case "exam.attempt.connection_closed":
		return "exam.attempt.connection_closed", websocketErrorAttemptConnectionInactive
	case "exam.attempt.connection_lost":
		return "exam.attempt.connection_lost", websocketErrorAttemptConnectionLost
	case "exam.attempt.focus_loss_conflict":
		return "exam.attempt.focus_loss_conflict", websocketErrorFocusLossConflict
	case "exam.attempt.sitting_unavailable", "exam.attempt.state_conflict":
		return failure.Code(), websocketErrorFocusLossFailed
	default:
		return "exam.attempt.unavailable", websocketErrorFocusLossFailed
	}
}

type examAttemptSecurityUpdateRequest struct {
	Generation           int64                         `json:"generation"`
	ContinuityCredential string                        `json:"continuity_credential"`
	SecurityCoverage     model.SecurityCoverageRenewal `json:"security_coverage"`
}

func decodeExamAttemptSecurityUpdateRequest(raw json.RawMessage) (examAttemptSecurityUpdateRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptSecurityUpdateRequest](raw, "security coverage update", 3)
	if err != nil {
		return value, err
	}
	if len(raw) > model.SecurityControlMaxBytes || value.Generation < 1 || !model.IsValidCredentialToken(value.ContinuityCredential) || value.SecurityCoverage.Validate() != nil {
		return value, errors.New("invalid security coverage update")
	}
	return value, nil
}
