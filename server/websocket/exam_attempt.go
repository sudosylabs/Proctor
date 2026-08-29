// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	examAttemptFocusLossAction      = "exam_attempt.focus_loss"
	examAttemptBrowserStartAction   = "exam_attempt.browser_activity.start"
	examAttemptBrowserAppendAction  = "exam_attempt.browser_activity.append"
	examAttemptTerminalOpenAction   = "exam_attempt.terminal.open"
	examAttemptTerminalInputAction  = "exam_attempt.terminal.input"
	examAttemptTerminalResizeAction = "exam_attempt.terminal.resize"
	examAttemptTerminalCloseAction  = "exam_attempt.terminal.close"
	examAttemptTerminalOutputEvent  = "exam_attempt.terminal.output"
	examAttemptTerminalClosedEvent  = "exam_attempt.terminal.closed"
	examAttemptTerminalChunkMaximum = 32 * 1024
)

type examAttemptApplication interface {
	ConnectExamAttempt(context.Context, app.Invocation, app.ConnectExamAttemptCommand) (app.ExamAttemptConnection, error)
	RenewExamAttemptParticipation(context.Context, app.Invocation, app.RenewExamAttemptParticipationCommand) (app.ExamAttemptParticipationRenewal, error)
	EvaluateExamAttemptFocusLoss(context.Context, app.Invocation, app.EvaluateExamAttemptFocusLossCommand) (app.ExamAttemptFocusLossEvaluation, error)
	CloseExamAttemptConnection(context.Context, app.Invocation, app.CloseExamAttemptConnectionCommand) (app.ExamAttemptConnectionClosed, error)
	OpenCandidateExamTerminal(context.Context, app.Invocation, app.OpenCandidateExamTerminalCommand) (app.CandidateExamTerminal, error)
	StartExamAttemptBrowserActivity(context.Context, app.Invocation, app.StartBrowserActivityCommand) (app.BrowserActivityAcknowledgement, error)
	AppendExamAttemptBrowserActivity(context.Context, app.Invocation, app.AppendBrowserActivityCommand) (app.BrowserActivityAcknowledgement, error)
}

type examAttemptTerminalOpenRequest struct {
	Generation           int64  `json:"generation"`
	ContinuityCredential string `json:"continuity_credential"`
	Cols                 uint16 `json:"cols"`
	Rows                 uint16 `json:"rows"`
}

type examAttemptTerminalInputRequest struct {
	Data string `json:"data"`
}

type examAttemptTerminalResizeRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type examAttemptTerminalOutput struct {
	Data string `json:"data"`
}

type examAttemptTerminalClosed struct {
	Reason string `json:"reason"`
}

type examAttemptConnectRequest struct {
	ExamSittingID                          string                      `json:"exam_sitting_id"`
	IdempotencyKey                         string                      `json:"idempotency_key"`
	ContinuityCredential                   string                      `json:"continuity_credential"`
	SupportedAttemptConfigurationManifests []string                    `json:"supported_attempt_configuration_manifests"`
	InitialConfiguration                   *model.AttemptConfiguration `json:"initial_configuration,omitempty"`
}

type examAttemptConnectResponse struct {
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
	AcknowledgementRequired bool                                 `json:"acknowledgement_required"`
	AcknowledgementState    model.CorrectionAcknowledgementState `json:"acknowledgement_state"`
	AcknowledgedAt          *string                              `json:"acknowledged_at"`
}

type candidateBrowserPolicyResponse struct {
	SchemaVersion    int                                  `json:"schema_version"`
	Enabled          bool                                 `json:"enabled"`
	StartRuleID      string                               `json:"start_rule_id"`
	Rules            []candidateBrowserPolicyRuleResponse `json:"rules"`
	PolicyRevisionID string                               `json:"policy_revision_id"`
	PolicyDigest     string                               `json:"policy_digest"`
}

type candidateBrowserPolicyRuleResponse struct {
	RuleID                   string `json:"rule_id"`
	Origin                   string `json:"origin"`
	PathPrefix               string `json:"path_prefix"`
	HostMatch                string `json:"host_match"`
	AllowRedirects           bool   `json:"allow_redirects"`
	BlockedNavigationOutcome string `json:"blocked_navigation_outcome"`
}

type candidateRuntimeCapabilitiesResponse struct {
	SchemaVersion              int                                   `json:"schema_version"`
	ServerTime                 string                                `json:"server_time"`
	InteractionState           string                                `json:"interaction_state"`
	AttemptConfiguration       candidateAttemptConfigurationResponse `json:"attempt_configuration"`
	FocusLossCollectionEnabled bool                                  `json:"focus_loss_collection_enabled"`
	WorkspaceMutationAllowed   bool                                  `json:"workspace_mutation_allowed"`
	SubmissionAllowed          bool                                  `json:"submission_allowed"`
	Terminal                   candidateTerminalCapabilityResponse   `json:"terminal"`
	Browser                    candidateBrowserCapabilityResponse    `json:"browser"`
	ExamRevision               candidateExamRevisionResponse         `json:"exam_revision"`
	Departure                  candidateDepartureResponse            `json:"departure"`
}

type candidateAttemptConfigurationResponse struct {
	SchemaVersion       int                                   `json:"schema_version"`
	ManifestFingerprint string                                `json:"manifest_fingerprint"`
	Preferences         model.AttemptConfigurationPreferences `json:"preferences"`
	Digest              string                                `json:"digest"`
}

type candidateTerminalCapabilityResponse struct {
	State string `json:"state"`
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
	Generation           int64  `json:"generation"`
	Sequence             int64  `json:"sequence"`
	ContinuityCredential string `json:"continuity_credential"`
}

type examAttemptRenewResponse struct {
	Generation       int64  `json:"generation"`
	AcceptedSequence int64  `json:"accepted_sequence"`
	DatabaseTime     string `json:"database_time"`
	LeaseExpiresAt   string `json:"lease_expires_at"`
	Duplicate        bool   `json:"duplicate"`
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
	SchemaVersion        int     `json:"schema_version"`
	Generation           int64   `json:"generation"`
	ContinuityCredential string  `json:"continuity_credential"`
	ParticipationID      string  `json:"participation_id"`
	SourceSessionID      string  `json:"source_session_id"`
	PredecessorSessionID *string `json:"predecessor_session_id"`
	ResetReason          *string `json:"reset_reason"`
}

type examAttemptBrowserAppendRequest struct {
	SchemaVersion        int               `json:"schema_version"`
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
	Location         json.RawMessage `json:"location"`
	MatchedRuleID    json.RawMessage `json:"matched_rule_id"`
	BlockReason      json.RawMessage `json:"block_reason"`
}

type examAttemptBrowserAcknowledgementResponse struct {
	SourceSessionID        string                                `json:"source_session_id"`
	HighestContiguous      int64                                 `json:"highest_contiguous_sequence"`
	HighestSeen            int64                                 `json:"highest_seen_sequence"`
	MissingRanges          []browserActivityMissingRangeResponse `json:"missing_ranges"`
	MissingRangesTruncated bool                                  `json:"missing_ranges_truncated"`
	ServerTime             string                                `json:"server_time"`
}

type browserActivityMissingRangeResponse struct {
	First int64 `json:"first_sequence"`
	Last  int64 `json:"last_sequence"`
}

func decodeExamAttemptBrowserStartRequest(document json.RawMessage) (examAttemptBrowserStartRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptBrowserStartRequest](document, "Browser Activity source start request", 7)
	if err != nil {
		return value, err
	}
	var members map[string]json.RawMessage
	if err = json.Unmarshal(document, &members); err != nil || len(members) != 7 || members["predecessor_session_id"] == nil || members["reset_reason"] == nil {
		return value, errors.New("Browser Activity source start request fields are incomplete")
	}
	if value.SchemaVersion != model.BrowserActivitySchemaVersion || value.Generation < 1 || !model.IsValidCredentialToken(value.ContinuityCredential) ||
		!model.AttemptParticipationID(value.ParticipationID).IsValid() || !model.BrowserSourceSessionID(value.SourceSessionID).IsValid() ||
		(value.PredecessorSessionID == nil) != (value.ResetReason == nil) {
		return value, errors.New("Browser Activity source start request fields are invalid")
	}
	if value.PredecessorSessionID != nil && (!model.BrowserSourceSessionID(*value.PredecessorSessionID).IsValid() || !model.BrowserSourceResetReason(*value.ResetReason).IsValid()) {
		return value, errors.New("Browser Activity source replacement is invalid")
	}
	return value, nil
}

func decodeExamAttemptBrowserAppendRequest(document json.RawMessage) (examAttemptBrowserAppendRequest, []model.BrowserActivityEvent, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptBrowserAppendRequest](document, "Browser Activity append request", 6)
	if err != nil {
		return value, nil, err
	}
	if len(document) > model.BrowserActivityAppendMaximumBytes || value.SchemaVersion != model.BrowserActivitySchemaVersion || value.Generation < 1 ||
		!model.IsValidCredentialToken(value.ContinuityCredential) || !model.AttemptParticipationID(value.ParticipationID).IsValid() ||
		!model.BrowserSourceSessionID(value.SourceSessionID).IsValid() || len(value.Events) < 1 || len(value.Events) > model.BrowserActivityAppendMaximumEvents {
		return value, nil, errors.New("Browser Activity append request fields are invalid")
	}
	events := make([]model.BrowserActivityEvent, len(value.Events))
	for index, eventDocument := range value.Events {
		wire, strictErr := decodeStrictExamAttemptObject[examAttemptBrowserEventRequest](eventDocument, "Browser Activity event", 7)
		if strictErr != nil {
			return value, nil, strictErr
		}
		var location *model.BrowserLocation
		if !bytes.Equal(bytes.TrimSpace(wire.Location), []byte("null")) {
			decoded, decodeErr := decodeStrictExamAttemptObject[model.BrowserLocation](wire.Location, "Browser Activity location", 4)
			if decodeErr != nil || !completeBrowserActivityLocation(wire.Location) {
				if decodeErr == nil {
					decodeErr = errors.New("Browser Activity location fields are incomplete")
				}
				return value, nil, decodeErr
			}
			location = &decoded
		}
		matchedRuleID, decodeErr := decodeNullableBrowserString(wire.MatchedRuleID)
		if decodeErr != nil {
			return value, nil, decodeErr
		}
		blockReasonString, decodeErr := decodeNullableBrowserString(wire.BlockReason)
		if decodeErr != nil {
			return value, nil, decodeErr
		}
		var blockReason *model.BrowserActivityBlockReason
		if blockReasonString != nil {
			converted := model.BrowserActivityBlockReason(*blockReasonString)
			blockReason = &converted
		}
		revisionID, parseErr := model.ParseExamRevisionID(wire.PolicyRevisionID)
		occurredAt, timeErr := time.Parse(time.RFC3339Nano, wire.ClientOccurredAt)
		if parseErr != nil || timeErr != nil {
			return value, nil, errors.New("Browser Activity event revision or occurrence time is invalid")
		}
		events[index] = model.BrowserActivityEvent{Sequence: wire.Sequence, Kind: model.BrowserActivityKind(wire.Kind),
			PolicyRevisionID: revisionID, ClientOccurredAt: model.TimeUTC(occurredAt), Location: location,
			MatchedRuleID: matchedRuleID, BlockReason: blockReason}
		if events[index].ValidateClientRecord() != nil || index > 0 && events[index].Sequence <= events[index-1].Sequence {
			return value, nil, errors.New("Browser Activity event is invalid")
		}
	}
	return value, events, nil
}

func completeBrowserActivityLocation(document json.RawMessage) bool {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(document, &members); err != nil {
		return false
	}
	for _, name := range []string{"scheme", "host", "path"} {
		value, exists := members[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	if port, exists := members["port"]; exists {
		trimmed := bytes.TrimSpace(port)
		if bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte(`""`)) {
			return false
		}
	}
	return true
}

func decodeNullableBrowserString(document json.RawMessage) (*string, error) {
	if len(document) == 0 {
		return nil, errors.New("required nullable Browser Activity field is absent")
	}
	if bytes.Equal(bytes.TrimSpace(document), []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(document, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func browserActivityAcknowledgementWire(value app.BrowserActivityAcknowledgement) examAttemptBrowserAcknowledgementResponse {
	response := examAttemptBrowserAcknowledgementResponse{SourceSessionID: string(value.SourceSessionID), HighestContiguous: value.HighestContiguous,
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
		len(value.SupportedAttemptConfigurationManifests) != 1 ||
		value.SupportedAttemptConfigurationManifests[0] != model.CurrentAttemptConfigurationManifestFingerprint() {
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
		AttemptConfiguration: candidateAttemptConfigurationResponse{SchemaVersion: value.AttemptConfiguration.SchemaVersion,
			ManifestFingerprint: value.AttemptConfiguration.ManifestFingerprint,
			Preferences:         value.AttemptConfiguration.Preferences, Digest: value.AttemptConfiguration.Digest},
		FocusLossCollectionEnabled: value.FocusLossCollectionEnabled,
		WorkspaceMutationAllowed:   value.WorkspaceMutationAllowed, SubmissionAllowed: value.SubmissionAllowed,
		Terminal: candidateTerminalCapabilityResponse{State: string(value.Terminal.State)}, Browser: browser,
		ExamRevision: candidateExamRevisionResponse{AdmissionRevisionID: value.ExamRevision.AdmissionRevisionID.String(),
			CurrentRevisionID: value.ExamRevision.CurrentRevisionID.String(), AcknowledgementRequired: value.ExamRevision.AcknowledgementRequired},
		Departure: candidateDepartureResponse{Allowed: value.Departure.Allowed, Reason: value.Departure.Reason}}
}

func candidateBrowserPolicyWire(value *app.CandidateBrowserPolicy) *candidateBrowserPolicyResponse {
	if value == nil {
		return nil
	}
	response := &candidateBrowserPolicyResponse{SchemaVersion: value.Policy.SchemaVersion, Enabled: true,
		StartRuleID: value.Policy.StartRuleID, PolicyRevisionID: value.PolicyRevisionID.String(), PolicyDigest: value.PolicyDigest,
		Rules: make([]candidateBrowserPolicyRuleResponse, len(value.Policy.Rules))}
	for index, rule := range value.Policy.Rules {
		response.Rules[index] = candidateBrowserPolicyRuleResponse{RuleID: rule.RuleID, Origin: rule.Origin, PathPrefix: rule.PathPrefix,
			HostMatch: string(rule.HostMatch), AllowRedirects: rule.AllowRedirects, BlockedNavigationOutcome: string(rule.BlockedNavigationOutcome)}
	}
	return response
}

func candidateLiveCorrectionsWire(values []model.CandidateLiveCorrection) []candidateLiveCorrectionResponse {
	result := make([]candidateLiveCorrectionResponse, len(values))
	for index, value := range values {
		item := candidateLiveCorrectionResponse{RevisionID: value.RevisionID.String(), RevisionNumber: value.RevisionNumber,
			EffectiveAt: model.TimeUTC(value.EffectiveAt).Format(time.RFC3339Nano), Summary: value.Summary,
			ChangedAreas:            append([]model.ExamCorrectionChangedArea(nil), value.ChangedAreas...),
			AcknowledgementRequired: value.AcknowledgementRequired, AcknowledgementState: value.AcknowledgementState}
		if value.AcknowledgedAt.Valid {
			at := model.TimeUTC(value.AcknowledgedAt.Time).Format(time.RFC3339Nano)
			item.AcknowledgedAt = &at
		}
		result[index] = item
	}
	return result
}

func decodeExamAttemptRenewRequest(document json.RawMessage) (examAttemptRenewRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptRenewRequest](document, "Exam Attempt renewal request", 3)
	if err != nil {
		return value, err
	}
	if value.Generation < 1 || value.Sequence < 1 || !model.IsValidCredentialToken(value.ContinuityCredential) {
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
