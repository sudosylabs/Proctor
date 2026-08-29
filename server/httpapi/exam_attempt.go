// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	candidateAttemptCredentialHeader    = "X-Proctor-Attempt-Credential"
	candidateAttemptConnectionHeader    = "X-Proctor-Attempt-Connection-ID"
	examAttemptManagerCursorVersion     = 1
	browserActivityCursorVersion        = 1
	candidateWorkspaceCursorVersion     = 1
	submissionManifestCursorVersion     = 1
	sittingCandidateStatusCursorVersion = 1
	sittingCandidateStatusCursorKind    = "sitting_candidate_status"
)

type browserActivityCursor struct {
	ReceivedAt      time.Time
	SourceSessionID model.BrowserSourceSessionID
	Sequence        int64
}

type browserActivityCursorWire struct {
	Version         int    `json:"version"`
	ReceivedAt      string `json:"received_at"`
	SourceSessionID string `json:"source_session_id"`
	Sequence        int64  `json:"sequence"`
}

func encodeBrowserActivityCursor(cursor browserActivityCursor) (string, error) {
	return encodeOpaqueCursor(browserActivityCursorWire{
		ReceivedAt: model.TimeUTC(cursor.ReceivedAt).Format(time.RFC3339Nano), SourceSessionID: string(cursor.SourceSessionID), Sequence: cursor.Sequence,
	}, browserActivityCursorSpec())
}

func decodeBrowserActivityCursor(raw string) (browserActivityCursor, error) {
	wire, err := decodeOpaqueCursor(raw, browserActivityCursorSpec())
	if err != nil {
		return browserActivityCursor{}, err
	}
	receivedAt, _ := time.Parse(time.RFC3339Nano, wire.ReceivedAt)
	return browserActivityCursor{ReceivedAt: model.TimeUTC(receivedAt), SourceSessionID: model.BrowserSourceSessionID(wire.SourceSessionID), Sequence: wire.Sequence}, nil
}

func browserActivityCursorSpec() opaqueCursorSpec[browserActivityCursorWire] {
	return opaqueCursorSpec[browserActivityCursorWire]{
		label: "Browser Activity", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength, currentVersion: browserActivityCursorVersion,
		members:        []string{"version", "received_at", "source_session_id", "sequence"},
		version:        func(cursor browserActivityCursorWire) int { return cursor.Version },
		setVersion:     func(cursor *browserActivityCursorWire, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == browserActivityCursorVersion },
		validate: func(cursor browserActivityCursorWire) error {
			receivedAt, err := time.Parse(time.RFC3339Nano, cursor.ReceivedAt)
			if err != nil || receivedAt.IsZero() || !model.BrowserSourceSessionID(cursor.SourceSessionID).IsValid() || cursor.Sequence < 1 {
				return errors.New("invalid Browser Activity keyset")
			}
			return nil
		},
	}
}

type candidateAttemptHeaderAccess struct {
	ConnectionID         model.AttemptConnectionID
	ContinuityCredential string
}

type submissionManifestCursor struct {
	EntryID model.AttemptWorkspaceEntryID
}

type submissionManifestCursorWire struct {
	Version int    `json:"version"`
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
		label: "Submission manifest", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength, currentVersion: submissionManifestCursorVersion,
		members:        []string{"version", "after_entry_id"},
		version:        func(cursor submissionManifestCursorWire) int { return cursor.Version },
		setVersion:     func(cursor *submissionManifestCursorWire, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == submissionManifestCursorVersion },
		validate: func(cursor submissionManifestCursorWire) error {
			if !model.AttemptWorkspaceEntryID(cursor.EntryID).IsValid() {
				return errors.New("invalid Submission manifest keyset")
			}
			return nil
		},
	}
}

func candidateAttemptAccessHeaders(request *http.Request) (candidateAttemptHeaderAccess, error) {
	if request == nil {
		return candidateAttemptHeaderAccess{}, errors.New("candidate Attempt headers are required")
	}
	credentials := request.Header.Values(candidateAttemptCredentialHeader)
	connections := request.Header.Values(candidateAttemptConnectionHeader)
	if len(credentials) != 1 || len(connections) != 1 || !model.IsValidCredentialToken(credentials[0]) {
		return candidateAttemptHeaderAccess{}, errors.New("one canonical candidate Attempt credential and Connection ID are required")
	}
	connectionID, err := model.ParseAttemptConnectionID(connections[0])
	if err != nil {
		return candidateAttemptHeaderAccess{}, errors.New("one canonical candidate Attempt credential and Connection ID are required")
	}
	return candidateAttemptHeaderAccess{ConnectionID: connectionID, ContinuityCredential: credentials[0]}, nil
}

type examAttemptManagerCursor struct {
	CreatedAt time.Time
	ID        model.ExamAttemptID
}

type examAttemptManagerCursorWire struct {
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeExamAttemptManagerCursor(cursor examAttemptManagerCursor) (string, error) {
	wire := examAttemptManagerCursorWire{
		CreatedAt: model.TimeUTC(cursor.CreatedAt).Format(time.RFC3339Nano), ID: cursor.ID.String()}
	return encodeOpaqueCursor(wire, examAttemptManagerCursorSpec())
}

func decodeExamAttemptManagerCursor(raw string) (examAttemptManagerCursor, error) {
	wire, err := decodeOpaqueCursor(raw, examAttemptManagerCursorSpec())
	if err != nil {
		return examAttemptManagerCursor{}, err
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, wire.CreatedAt)
	id, _ := model.ParseExamAttemptID(wire.ID)
	return examAttemptManagerCursor{CreatedAt: model.TimeUTC(createdAt), ID: id}, nil
}

func examAttemptManagerCursorSpec() opaqueCursorSpec[examAttemptManagerCursorWire] {
	return opaqueCursorSpec[examAttemptManagerCursorWire]{
		label: "Exam Attempt", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength, currentVersion: examAttemptManagerCursorVersion,
		members:        []string{"version", "created_at", "id"},
		version:        func(cursor examAttemptManagerCursorWire) int { return cursor.Version },
		setVersion:     func(cursor *examAttemptManagerCursorWire, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == examAttemptManagerCursorVersion },
		validate: func(cursor examAttemptManagerCursorWire) error {
			createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
			if err != nil || createdAt.IsZero() || !model.ExamAttemptID(cursor.ID).IsValid() {
				return errors.New("invalid Exam Attempt keyset")
			}
			return nil
		},
	}
}

type candidateWorkspaceCursor struct {
	ExpectedCursor int64
	ID             model.AttemptWorkspaceEntryID
}

type candidateWorkspaceCursorWire struct {
	Version        int    `json:"version"`
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
		label: "candidate Workspace", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength, currentVersion: candidateWorkspaceCursorVersion,
		members:        []string{"version", "workspace_cursor", "after_entry_id"},
		version:        func(cursor candidateWorkspaceCursorWire) int { return cursor.Version },
		setVersion:     func(cursor *candidateWorkspaceCursorWire, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == candidateWorkspaceCursorVersion },
		validate: func(cursor candidateWorkspaceCursorWire) error {
			if cursor.ExpectedCursor < 0 || cursor.ID != "" && !model.AttemptWorkspaceEntryID(cursor.ID).IsValid() {
				return errors.New("invalid candidate Workspace keyset")
			}
			return nil
		},
	}
}

type ExamAttemptApplication interface {
	GetExamAttempt(context.Context, application.Invocation, application.GetExamAttemptQuery) (application.ExamAttemptManagerView, error)
	ListExamAttempts(context.Context, application.Invocation, application.ListExamAttemptsQuery) (application.ExamAttemptManagerPage, error)
	ListSittingCandidateStatuses(context.Context, application.Invocation, application.ListSittingCandidateStatusesQuery) (application.SittingCandidateStatusesPage, error)
	ListExamAttemptBrowserActivity(context.Context, application.Invocation, application.ListBrowserActivityQuery) (application.BrowserActivityPage, error)
	AcknowledgeExamAttemptCorrection(context.Context, application.Invocation, application.AcknowledgeExamCorrectionCommand) (application.ExamCorrectionAcknowledgementResult, error)
	EndExamAttemptByManager(context.Context, application.Invocation, application.EndExamAttemptByManagerCommand) (application.ExamSubmissionReceipt, error)
	GetCandidateExamPresentation(context.Context, application.Invocation, application.CandidateExamAttemptAccess) (application.CandidateExamPresentation, error)
	ListCandidateExamWorkspace(context.Context, application.Invocation, application.ListCandidateExamWorkspaceQuery) (application.CandidateExamWorkspacePage, error)
	ListCandidateExamWorkspaceJournal(context.Context, application.Invocation, application.ListCandidateExamWorkspaceJournalQuery) (application.CandidateExamWorkspaceJournalPage, error)
	CreateCandidateExamWorkspaceDirectory(context.Context, application.Invocation, application.CreateCandidateExamWorkspaceDirectoryCommand) (application.ExamAttemptWorkspaceMutationResult, error)
	CreateCandidateExamWorkspaceFile(context.Context, application.Invocation, application.CreateCandidateExamWorkspaceFileCommand) (application.ExamAttemptWorkspaceMutationResult, error)
	ReplaceCandidateExamWorkspaceFile(context.Context, application.Invocation, application.ReplaceCandidateExamWorkspaceFileCommand) (application.ExamAttemptWorkspaceMutationResult, error)
	MoveCandidateExamWorkspaceEntry(context.Context, application.Invocation, application.MoveCandidateExamWorkspaceEntryCommand) (application.ExamAttemptWorkspaceMutationResult, error)
	DeleteCandidateExamWorkspaceEntry(context.Context, application.Invocation, application.DeleteCandidateExamWorkspaceEntryCommand) (application.ExamAttemptWorkspaceMutationResult, error)
	OpenCandidateExamResource(context.Context, application.Invocation, application.OpenCandidateExamResourceQuery) (application.OpenedExamAttemptContent, error)
	OpenCandidateExamWorkspaceFile(context.Context, application.Invocation, application.OpenCandidateExamWorkspaceFileQuery) (application.OpenedExamAttemptContent, error)
	ReallowExamAttempt(context.Context, application.Invocation, application.ReallowExamAttemptCommand) (application.ExamAttemptReallowResult, error)
	SubmitExamAttempt(context.Context, application.Invocation, application.SubmitExamAttemptCommand) (application.ExamSubmissionReceipt, error)
	GetExamSubmission(context.Context, application.Invocation, application.GetExamSubmissionQuery) (application.ExamSubmissionManagerView, error)
	ListExamSubmissionManifest(context.Context, application.Invocation, application.ListExamSubmissionManifestQuery) (application.ExamSubmissionManifestPage, error)
	OpenExamSubmissionFile(context.Context, application.Invocation, application.OpenExamSubmissionFileQuery) (application.OpenedExamAttemptContent, error)
}

type sittingCandidateStatusCursor struct {
	Version         int    `json:"version"`
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

type sittingCandidateStatusItemResponse struct {
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
	State              application.SittingCandidatePresenceState `json:"state"`
	LastLeaseRenewedAt *string                                   `json:"last_lease_renewed_at"`
	LeaseExpiresAt     *string                                   `json:"lease_expires_at"`
}

type sittingCandidateSuspensionResponse struct {
	ID               string                                 `json:"id"`
	CandidateReason  model.AttemptSuspensionCandidateReason `json:"candidate_reason"`
	ReallowAvailable bool                                   `json:"reallow_available"`
}

type examAttemptHTTPModule struct{ application ExamAttemptApplication }

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
	FlagID            string `json:"flag_id"`
	Generation        int64  `json:"generation"`
	State             string `json:"state"`
	Source            string `json:"source"`
	CandidateReason   string `json:"candidate_reason"`
	StartedAt         string `json:"started_at"`
	EndedAt           string `json:"ended_at,omitempty"`
	ReallowedByUserID string `json:"reallowed_by_user_id,omitempty"`
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
	SourceSessionID string                            `json:"source_session_id"`
	Generation      int64                             `json:"generation"`
	Sequence        int64                             `json:"sequence"`
	Kind            model.BrowserActivityKind         `json:"kind"`
	PolicyRevision  string                            `json:"policy_revision_id"`
	ClientOccurred  string                            `json:"client_occurred_at"`
	Location        *browserActivityLocationResponse  `json:"location"`
	MatchedRuleID   *string                           `json:"matched_rule_id"`
	BlockReason     *model.BrowserActivityBlockReason `json:"block_reason"`
	ReceivedAt      string                            `json:"received_at"`
}

type browserActivityLocationResponse struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   string `json:"port,omitempty"`
	Path   string `json:"path"`
}

type reallowExamAttemptRequest struct {
	SuspensionID            string `json:"suspension_id"`
	ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
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

type candidateExamPresentationResponse struct {
	AttemptID                    string                               `json:"attempt_id"`
	SittingID                    string                               `json:"exam_sitting_id"`
	Title                        string                               `json:"title"`
	InstructionsMarkdown         string                               `json:"instructions_markdown"`
	CandidateRuntimeCapabilities candidateRuntimeCapabilitiesResponse `json:"candidate_runtime_capabilities"`
	BrowserPolicy                *candidateBrowserPolicyResponse      `json:"browser_policy"`
	LiveCorrections              []candidateLiveCorrectionResponse    `json:"live_corrections"`
	Resources                    []candidateExamResourceResponse      `json:"resources"`
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

type candidateExamResourceResponse struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"display_name"`
	DescriptionMarkdown string `json:"description_markdown"`
	Position            int    `json:"position"`
	MediaType           string `json:"media_type"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256"`
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
	ParticipationID        string `json:"participation_id"`
	Generation             int64  `json:"generation"`
	ExpectedPath           string `json:"expected_path"`
	ExpectedContentVersion string `json:"expected_content_version,omitempty"`
}

type candidateWorkspaceMutationResponse struct {
	WorkspaceID     string                              `json:"workspace_id"`
	WorkspaceCursor int64                               `json:"workspace_cursor"`
	Operation       string                              `json:"operation"`
	Entry           *candidateExamWorkspaceItemResponse `json:"entry,omitempty"`
}

type submitExamAttemptRequest struct {
	ParticipationID           string                           `json:"participation_id"`
	Generation                int64                            `json:"generation"`
	ExpectedCurrentRevisionID string                           `json:"expected_current_revision_id"`
	ExpectedWorkspaceCursor   int64                            `json:"expected_workspace_cursor"`
	FinalFocusLossSequence    int64                            `json:"final_focus_loss_sequence"`
	BrowserActivity           browserActivitySubmissionRequest `json:"browser_activity"`
}

type browserActivitySubmissionRequest struct {
	State           string `json:"state"`
	SourceSessionID string `json:"source_session_id,omitempty"`
	FinalSequence   *int64 `json:"final_sequence,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

func (body *browserActivitySubmissionRequest) UnmarshalJSON(encoded []byte) error {
	type wire browserActivitySubmissionRequest
	var decoded wire
	if err := rejectDuplicateTopLevelJSONMembers(encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return err
	}
	hasSource := members["source_session_id"] != nil
	hasFinal := members["final_sequence"] != nil
	hasReason := members["reason"] != nil
	if hasFinal && decoded.FinalSequence == nil {
		return errors.New("browser_activity.final_sequence must be an integer when present")
	}
	switch model.BrowserActivitySubmissionState(decoded.State) {
	case model.BrowserActivitySubmissionNotApplicable:
		if len(members) != 1 || hasSource || hasFinal || hasReason {
			return errors.New("not_applicable browser_activity must contain only state")
		}
	case model.BrowserActivitySubmissionComplete:
		if len(members) != 3 || !hasSource || !hasFinal || hasReason {
			return errors.New("complete browser_activity requires source_session_id and final_sequence")
		}
	case model.BrowserActivitySubmissionGapped:
		if !hasSource || !hasReason || len(members) < 3 || len(members) > 4 {
			return errors.New("gapped browser_activity requires source_session_id and reason")
		}
	default:
		return errors.New("invalid browser_activity state")
	}
	*body = browserActivitySubmissionRequest(decoded)
	return nil
}

func (body browserActivitySubmissionRequest) model() (model.BrowserActivitySubmission, error) {
	value := model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionState(body.State),
		SourceSessionID: model.BrowserSourceSessionID(body.SourceSessionID), FinalSequence: body.FinalSequence,
		GapReason: model.BrowserActivitySubmissionGapReason(body.Reason)}
	if err := value.ValidateClient(); err != nil {
		return model.BrowserActivitySubmission{}, err
	}
	return value, nil
}

type acknowledgeExamCorrectionRequest struct {
	ParticipationID           string `json:"participation_id"`
	Generation                int64  `json:"generation"`
	ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
}

type acknowledgeExamCorrectionResponse struct {
	RevisionID           string `json:"revision_id"`
	AcknowledgementState string `json:"acknowledgement_state"`
	AcknowledgedAt       string `json:"acknowledged_at"`
}

type endExamAttemptByManagerRequest struct {
	ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
	Reason                  string `json:"reason"`
}

type examSubmissionReceiptResponse struct {
	SubmissionID    string `json:"submission_id"`
	ExamAttemptID   string `json:"exam_attempt_id"`
	ExamRevisionID  string `json:"exam_revision_id"`
	State           string `json:"state"`
	WorkspaceCursor int64  `json:"workspace_cursor"`
	ManifestDigest  string `json:"manifest_digest"`
	SubmittedAt     string `json:"submitted_at"`
}

type examSubmissionManagerResponse struct {
	SubmissionID             string `json:"submission_id"`
	ExamID                   string `json:"exam_id"`
	ExamSittingID            string `json:"exam_sitting_id"`
	ExamAttemptID            string `json:"exam_attempt_id"`
	ExamRevisionID           string `json:"exam_revision_id"`
	WorkspaceID              string `json:"workspace_id"`
	ManifestSchemaVersion    int    `json:"manifest_schema_version"`
	WorkspaceCursor          int64  `json:"workspace_cursor"`
	ManifestDigest           string `json:"manifest_digest"`
	ManifestEntryCount       int    `json:"manifest_entry_count"`
	ManifestTotalFileBytes   int64  `json:"manifest_total_file_bytes"`
	FinalFocusLossSequence   int64  `json:"final_focus_loss_sequence"`
	IntegrityState           string `json:"integrity_state"`
	UnresolvedIntegrityCount int64  `json:"unresolved_integrity_count"`
	SubmittedAt              string `json:"submitted_at"`
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

type candidateWorkspaceJournalEntryResponse struct {
	Cursor         int64  `json:"cursor"`
	EntryID        string `json:"entry_id"`
	Kind           string `json:"kind"`
	Operation      string `json:"operation"`
	OldPath        string `json:"old_path,omitempty"`
	NewPath        string `json:"new_path,omitempty"`
	ContentVersion string `json:"content_version,omitempty"`
	ChangedAt      string `json:"changed_at"`
}

type candidateWorkspaceJournalResponse struct {
	WorkspaceID     string                                   `json:"workspace_id"`
	CurrentCursor   int64                                    `json:"current_cursor"`
	Entries         []candidateWorkspaceJournalEntryResponse `json:"entries"`
	HasMore         bool                                     `json:"has_more"`
	RefreshRequired bool                                     `json:"refresh_required"`
}

func examAttemptResource(application ExamAttemptApplication) resource {
	module := examAttemptHTTPModule{application: application}
	managerCollection := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("attempts"))
	managerMember := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("attempts"), canonicalID("exam_attempt_id"))
	managerBrowserActivity := appendRoutePath(managerMember, literal("browser-activity"))
	managerEnd := appendRoutePath(managerMember, literal("end"))
	candidateStatuses := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("candidate-statuses"))
	candidate := apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"))
	presentation := appendRoutePath(candidate, literal("presentation"))
	workspace := appendRoutePath(candidate, literal("workspace"))
	workspaceChanges := appendRoutePath(workspace, literal("changes"))
	workspaceDirectories := appendRoutePath(workspace, literal("directories"))
	workspaceFiles := appendRoutePath(workspace, literal("files"))
	workspaceEntry := appendRoutePath(workspace, literal("entries"), canonicalID("attempt_workspace_entry_id"))
	resourceContent := appendRoutePath(candidate, literal("resources"), canonicalID("exam_resource_id"), literal("content"))
	workspaceContent := appendRoutePath(candidate, literal("workspace"), literal("files"), canonicalID("attempt_workspace_entry_id"), literal("content"))
	candidateSubmissions := appendRoutePath(candidate, literal("submissions"))
	correctionAcknowledgement := appendRoutePath(candidate, literal("corrections"), canonicalID("exam_revision_id"), literal("acknowledgement"))
	managerSubmission := appendRoutePath(managerMember, literal("submissions"), canonicalID("submission_id"))
	managerSubmissionManifest := appendRoutePath(managerSubmission, literal("manifest"))
	managerSubmissionContent := appendRoutePath(managerSubmission, literal("files"), canonicalID("attempt_workspace_entry_id"), literal("content"))
	reallow := appendRoutePath(managerMember, literal("reallow"))
	managerErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable")
	reallowErrors := academicMutationErrorCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.revision_conflict", "exam.attempt.suspension_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.conflict", "exam.attempt.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	managerEndErrors := academicMutationErrorCodes("request.invalid", "resource.not_found",
		"exam.attempt.invalid", "exam.attempt.revision_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.management_authority_conflict", "exam.attempt.conflict",
		"exam.attempt.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict",
		"idempotency.in_progress")
	candidateErrors := personalAccessTokenSessionCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.sitting_unavailable", "exam.attempt.state_conflict", "exam.attempt.unavailable")
	directoryMutationErrors := candidateWorkspaceMutationErrors("exam.attempt.workspace.path_conflict",
		"exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.entry_limit")
	createFileMutationErrors := candidateWorkspaceMutationErrors("exam.attempt.workspace.path_conflict",
		"exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.entry_limit", "exam.attempt.workspace.size_limit",
		"exam.attempt.workspace.object_conflict")
	moveMutationErrors := candidateWorkspaceMutationErrors("exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict")
	replaceMutationErrors := candidateWorkspaceMutationErrors("exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict",
		"exam.attempt.workspace.content_conflict", "exam.attempt.workspace.size_limit", "exam.attempt.workspace.object_conflict")
	deleteMutationErrors := candidateWorkspaceMutationErrors("exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict",
		"exam.attempt.workspace.content_conflict", "exam.attempt.workspace.directory_not_empty")
	submissionErrors := candidateWorkspaceMutationErrors("exam.attempt.workspace.cursor_conflict", "exam.attempt.focus_loss_conflict",
		"exam.attempt.revision_conflict", "exam.attempt.correction_conflict", "exam.attempt.browser_activity_conflict",
		"exam.attempt.connection_lost")
	correctionAcknowledgementErrors := personalAccessTokenSessionMutationCodes("audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.revision_conflict", "exam.attempt.correction_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.connection_closed", "exam.attempt.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	return newResource("exam-attempts",
		principalRoute(http.MethodGet, managerCollection, managerErrors, module.listManaged),
		principalRoute(http.MethodGet, candidateStatuses, managerErrors, module.listCandidateStatuses),
		principalRoute(http.MethodGet, managerMember, managerErrors, module.getManaged),
		principalRoute(http.MethodGet, managerBrowserActivity, managerErrors, module.listBrowserActivity),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, managerEnd, managerEndErrors, module.endByManager),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, reallow, reallowErrors, module.reallow),
		sessionRoute(http.MethodGet, presentation, candidateErrors, module.presentation),
		sessionRoute(http.MethodGet, workspace, candidateErrors, module.workspace),
		sessionRoute(http.MethodGet, workspaceChanges, candidateErrors, module.workspaceChanges),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPost, workspaceDirectories, directoryMutationErrors, module.createWorkspaceDirectory),
		idempotentProtocolRoute(IdempotencyRequired, model.AttemptWorkspaceMaximumRequestBytes, "candidate-exam-workspace-file-upload",
			RouteProtocolStreamingUpload, AuthSessionRequired, http.MethodPost, workspaceFiles, createFileMutationErrors, module.createWorkspaceFile),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPatch, workspaceEntry, moveMutationErrors, module.moveWorkspaceEntry),
		idempotentProtocolRoute(IdempotencyRequired, model.AttemptWorkspaceMaximumRequestBytes, "candidate-exam-workspace-file-replacement",
			RouteProtocolStreamingUpload, AuthSessionRequired, http.MethodPut, workspaceContent, replaceMutationErrors, module.replaceWorkspaceFile),
		idempotentSessionRoute(IdempotencyRequired, http.MethodDelete, workspaceEntry, deleteMutationErrors, module.deleteWorkspaceEntry),
		protocolRoute("candidate-exam-resource-content", RouteProtocolBinaryDownload, AuthSessionRequired, http.MethodGet, resourceContent, candidateErrors, module.openResource),
		protocolRoute("candidate-exam-workspace-content", RouteProtocolBinaryDownload, AuthSessionRequired, http.MethodGet, workspaceContent, candidateErrors, module.openWorkspaceFile),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPost, candidateSubmissions, submissionErrors, module.submit),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPut, correctionAcknowledgement, correctionAcknowledgementErrors, module.acknowledgeCorrection),
		principalRoute(http.MethodGet, managerSubmission, managerErrors, module.getSubmission),
		principalRoute(http.MethodGet, managerSubmissionManifest, managerErrors, module.listSubmissionManifest),
		protocolRoute("exam-submission-file-content", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet,
			managerSubmissionContent, managerErrors, module.openSubmissionFile),
	)
}

func (module examAttemptHTTPModule) endByManager(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body endExamAttemptByManagerRequest
	if err = decodeCandidateWorkspaceJSON(request, &body, "endExamAttemptByManager"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedAttemptRevision < 1 {
		return operationResult{}, invalidRequestError("expected_attempt_revision", errors.New("must be positive"))
	}
	receipt, err := module.application.EndExamAttemptByManager(request.context, request.invocation(),
		application.EndExamAttemptByManagerCommand{ExamID: examID, SittingID: sittingID, AttemptID: attemptID,
			ExpectedAttemptRevision: body.ExpectedAttemptRevision, PrivateReason: body.Reason,
			IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	response := examSubmissionReceiptResponse{SubmissionID: receipt.SubmissionID.String(),
		ExamAttemptID: receipt.AttemptID.String(), ExamRevisionID: receipt.ExamRevisionID.String(),
		State: string(receipt.State), WorkspaceCursor: receipt.WorkspaceCursor,
		ManifestDigest: receipt.ManifestDigest, SubmittedAt: model.TimeUTC(receipt.SubmittedAt).Format(time.RFC3339Nano)}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) acknowledgeCorrection(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	rawRevisionID, err := request.params.RequireExamRevisionId()
	if err != nil {
		return operationResult{}, err
	}
	correctionRevisionID, err := model.ParseExamRevisionID(rawRevisionID)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_revision_id", err)
	}
	var body acknowledgeExamCorrectionRequest
	if err = decodeCandidateWorkspaceJSON(request, &body, "acknowledgeExamCorrection"); err != nil {
		return operationResult{}, err
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{
		ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	currentRevisionID, err := model.ParseExamRevisionID(body.ExpectedCurrentRevisionID)
	if err != nil {
		return operationResult{}, invalidRequestError("expected_current_revision_id", err)
	}
	result, err := module.application.AcknowledgeExamAttemptCorrection(request.context, request.invocation(), application.AcknowledgeExamCorrectionCommand{
		Access: mutationAccess, CorrectionRevisionID: correctionRevisionID, ExpectedCurrentRevisionID: currentRevisionID,
		IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if !result.AcknowledgedAt.Valid {
		return operationResult{}, application.NewError("exam.attempt.unavailable")
	}
	return jsonResult(http.StatusOK, acknowledgeExamCorrectionResponse{RevisionID: result.CorrectionRevisionID.String(),
		AcknowledgementState: string(model.CorrectionAcknowledgementAcknowledged),
		AcknowledgedAt:       model.TimeUTC(result.AcknowledgedAt.Time).Format(time.RFC3339Nano)}).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) listCandidateStatuses(request operationRequest) (operationResult, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListSittingCandidateStatusesQuery{ExamID: examID, SittingID: sittingID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeOpaqueCursor(raw, sittingCandidateStatusCursorSpec())
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.AfterCandidateUserID = model.UserID(cursor.CandidateUserID)
	}
	page, err := module.application.ListSittingCandidateStatuses(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := sittingCandidateStatusListResponse{ServerTime: model.TimeUTC(page.ServerTime).Format(time.RFC3339Nano),
		ExamID: page.ExamID.String(), ExamSittingID: page.SittingID.String(), SittingState: page.SittingState,
		SittingRevision: page.SittingRevision, Items: make([]sittingCandidateStatusItemResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		mapped := sittingCandidateStatusItemResponse{Candidate: sittingCandidateIdentityResponse{UserID: item.Candidate.UserID.String(),
			Username: item.Candidate.Username, DisplayName: item.Candidate.DisplayName},
			CurrentClassMembership:  item.CurrentClassMembership,
			Presence:                sittingCandidatePresenceResponse{State: item.Presence.State},
			IntegrityAttentionCount: item.IntegrityAttentionCount}
		if item.Presence.LastLeaseRenewedAt.Valid {
			value := model.TimeUTC(item.Presence.LastLeaseRenewedAt.Time).Format(time.RFC3339Nano)
			mapped.Presence.LastLeaseRenewedAt = &value
		}
		if item.Presence.LeaseExpiresAt.Valid {
			value := model.TimeUTC(item.Presence.LeaseExpiresAt.Time).Format(time.RFC3339Nano)
			mapped.Presence.LeaseExpiresAt = &value
		}
		if item.Attempt != nil {
			mapped.Attempt = &sittingCandidateAttemptResponse{ID: item.Attempt.ID.String(), State: item.Attempt.State,
				Revision: item.Attempt.Revision, CreatedAt: model.TimeUTC(item.Attempt.CreatedAt).Format(time.RFC3339Nano),
				UpdatedAt: model.TimeUTC(item.Attempt.UpdatedAt).Format(time.RFC3339Nano)}
			if item.Attempt.Submission != nil {
				mapped.Attempt.Submission = &sittingCandidateSubmissionResponse{ID: item.Attempt.Submission.ID.String(),
					SubmittedAt: model.TimeUTC(item.Attempt.Submission.SubmittedAt).Format(time.RFC3339Nano),
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
			return operationResult{}, application.NewError("exam.attempt.unavailable")
		}
		response.NextCursor, err = encodeOpaqueCursor(sittingCandidateStatusCursor{
			Kind: sittingCandidateStatusCursorKind, CandidateUserID: page.Items[len(page.Items)-1].Candidate.UserID.String(),
		}, sittingCandidateStatusCursorSpec())
		if err != nil {
			return operationResult{}, application.NewError("exam.attempt.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func sittingCandidateStatusCursorSpec() opaqueCursorSpec[sittingCandidateStatusCursor] {
	return opaqueCursorSpec[sittingCandidateStatusCursor]{label: "Sitting candidate status",
		maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength, currentVersion: sittingCandidateStatusCursorVersion,
		members:        []string{"version", "kind", "candidate_user_id"},
		version:        func(cursor sittingCandidateStatusCursor) int { return cursor.Version },
		setVersion:     func(cursor *sittingCandidateStatusCursor, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == sittingCandidateStatusCursorVersion },
		validate: func(cursor sittingCandidateStatusCursor) error {
			if cursor.Kind != sittingCandidateStatusCursorKind || !model.UserID(cursor.CandidateUserID).IsValid() {
				return errors.New("invalid Sitting candidate-status keyset")
			}
			return nil
		}}
}

func candidateWorkspaceMutationErrors(specific ...string) []string {
	common := []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.sitting_unavailable", "exam.attempt.state_conflict", "exam.attempt.connection_closed",
		"exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.invalid_key",
		"idempotency.conflict", "idempotency.in_progress"}
	return personalAccessTokenSessionMutationCodes(append(common, specific...)...)
}

func idempotentSessionRoute(requirement IdempotencyRequirement, method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	definition := sessionRoute(method, path, errorCodes, operation)
	definition.idempotency = requirement
	return definition
}

func (module examAttemptHTTPModule) reallow(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body reallowExamAttemptRequest
	if err = request.decodeJSON(&body, "reallowExamAttempt"); err != nil {
		return operationResult{}, err
	}
	suspensionID, err := model.ParseAttemptSuspensionID(body.SuspensionID)
	if err != nil {
		return operationResult{}, invalidRequestError("suspension_id", err)
	}
	result, err := module.application.ReallowExamAttempt(request.context, request.invocation(), application.ReallowExamAttemptCommand{
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

func (module examAttemptHTTPModule) getManaged(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.application.GetExamAttempt(request.context, request.invocation(), application.GetExamAttemptQuery{
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

func (module examAttemptHTTPModule) listManaged(request operationRequest) (operationResult, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamAttemptsQuery{ExamID: examID, SittingID: sittingID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	seenStates := make(map[model.ExamAttemptState]struct{})
	for _, raw := range values["state"] {
		state := model.ExamAttemptState(raw)
		switch state {
		case model.ExamAttemptReady, model.ExamAttemptActive, model.ExamAttemptSuspended, model.ExamAttemptSubmitted:
		default:
			return operationResult{}, invalidRequestError("state", errors.New("is not supported"))
		}
		if _, duplicate := seenStates[state]; duplicate {
			return operationResult{}, invalidRequestError("state", errors.New("must not contain duplicates"))
		}
		seenStates[state] = struct{}{}
		query.States = append(query.States, state)
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeExamAttemptManagerCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.BeforeCreatedAt, query.BeforeAttemptID = cursor.CreatedAt, cursor.ID
	}
	page, err := module.application.ListExamAttempts(request.context, request.invocation(), query)
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
			return operationResult{}, application.NewError("exam.attempt.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) listBrowserActivity(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListBrowserActivityQuery{ExamID: examID, SittingID: sittingID, AttemptID: attemptID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeBrowserActivityCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.AfterReceivedAt, query.AfterSourceID, query.AfterSequence = cursor.ReceivedAt, cursor.SourceSessionID, cursor.Sequence
	}
	page, err := module.application.ListExamAttemptBrowserActivity(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := browserActivityListResponse{Items: make([]browserActivityItemResponse, 0, len(page.Items))}
	for _, record := range page.Items {
		event := record.Event
		item := browserActivityItemResponse{SourceSessionID: string(record.SourceSessionID), Generation: record.Generation,
			Sequence: event.Sequence, Kind: event.Kind, PolicyRevision: event.PolicyRevisionID.String(),
			ClientOccurred: model.TimeUTC(event.ClientOccurredAt).Format(time.RFC3339Nano), MatchedRuleID: event.MatchedRuleID,
			BlockReason: event.BlockReason, ReceivedAt: model.TimeUTC(event.ReceivedAt).Format(time.RFC3339Nano)}
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
			return operationResult{}, application.NewError("exam.attempt.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) presentation(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.application.GetCandidateExamPresentation(request.context, request.invocation(), access)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateExamPresentationResponse{AttemptID: view.AttemptID.String(), SittingID: view.SittingID.String(), Title: view.Title,
		InstructionsMarkdown:         view.InstructionsMarkdown,
		CandidateRuntimeCapabilities: candidateRuntimeCapabilitiesFromStore(view.RuntimeCapabilities),
		BrowserPolicy:                candidateBrowserPolicyFromStore(view.BrowserPolicy),
		LiveCorrections:              candidateLiveCorrectionsFromModel(view.LiveCorrections),
		Resources:                    make([]candidateExamResourceResponse, 0, len(view.Resources))}
	for _, resource := range view.Resources {
		response.Resources = append(response.Resources, candidateExamResourceResponse{ID: resource.ResourceID.String(), DisplayName: resource.DisplayName,
			DescriptionMarkdown: resource.DescriptionMarkdown, Position: resource.Position, MediaType: string(resource.MediaType), Size: resource.SizeBytes, SHA256: resource.SHA256})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func candidateLiveCorrectionsFromModel(values []model.CandidateLiveCorrection) []candidateLiveCorrectionResponse {
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

func candidateBrowserPolicyFromStore(value *application.CandidateBrowserPolicy) *candidateBrowserPolicyResponse {
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

func candidateRuntimeCapabilitiesFromStore(value application.CandidateRuntimeCapabilities) candidateRuntimeCapabilitiesResponse {
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

func (module examAttemptHTTPModule) workspace(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListCandidateExamWorkspaceQuery{Access: access, ExpectedCursor: -1, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeCandidateWorkspaceCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.ExpectedCursor, query.AfterEntryID = cursor.ExpectedCursor, cursor.ID
	}
	page, err := module.application.ListCandidateExamWorkspace(request.context, request.invocation(), query)
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
			return operationResult{}, application.NewError("exam.attempt.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) workspaceChanges(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListCandidateExamWorkspaceJournalQuery{Access: access, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("after_cursor"); raw != "" {
		query.AfterCursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || query.AfterCursor < 0 {
			return operationResult{}, invalidRequestError("after_cursor", errors.New("must be nonnegative"))
		}
	}
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > model.AttemptWorkspaceJournalReadMaximum {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	page, err := module.application.ListCandidateExamWorkspaceJournal(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateWorkspaceJournalResponse{WorkspaceID: page.WorkspaceID.String(), CurrentCursor: page.CurrentCursor,
		Entries: make([]candidateWorkspaceJournalEntryResponse, 0, len(page.Entries)), HasMore: page.HasMore,
		RefreshRequired: page.RefreshRequired}
	for _, entry := range page.Entries {
		response.Entries = append(response.Entries, candidateWorkspaceJournalEntryResponse{Cursor: entry.Cursor,
			EntryID: entry.EntryID.String(), Kind: string(entry.EntryKind), Operation: string(entry.Operation), OldPath: entry.OldPath,
			NewPath: entry.NewPath, ContentVersion: entry.ContentVersion.String(),
			ChangedAt: model.TimeUTC(entry.ChangedAt).Format(time.RFC3339Nano)})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) createWorkspaceDirectory(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body createCandidateWorkspaceDirectoryRequest
	if err = decodeCandidateWorkspaceJSON(request, &body, "createCandidateExamWorkspaceDirectory"); err != nil {
		return operationResult{}, err
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	result, err := module.application.CreateCandidateExamWorkspaceDirectory(request.context, request.invocation(),
		application.CreateCandidateExamWorkspaceDirectoryCommand{Access: mutationAccess, Path: body.Path,
			IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) submit(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body submitExamAttemptRequest
	if err = decodeCandidateWorkspaceJSON(request, &body, "submitExamAttempt"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedWorkspaceCursor < 0 || body.FinalFocusLossSequence < 0 {
		return operationResult{}, invalidRequestError("submission_sequence", errors.New("Workspace Cursor and Focus Loss sequence must be nonnegative"))
	}
	revisionID, err := model.ParseExamRevisionID(body.ExpectedCurrentRevisionID)
	if err != nil {
		return operationResult{}, invalidRequestError("expected_current_revision_id", err)
	}
	browserActivity, err := body.BrowserActivity.model()
	if err != nil {
		return operationResult{}, invalidRequestError("browser_activity", err)
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{
		ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	receipt, err := module.application.SubmitExamAttempt(request.context, request.invocation(),
		application.SubmitExamAttemptCommand{Access: mutationAccess, ExpectedCurrentRevisionID: revisionID,
			ExpectedWorkspaceCursor: body.ExpectedWorkspaceCursor, FinalFocusLossSequence: body.FinalFocusLossSequence,
			BrowserActivity: browserActivity, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	response := examSubmissionReceiptResponse{SubmissionID: receipt.SubmissionID.String(),
		ExamAttemptID: receipt.AttemptID.String(), ExamRevisionID: receipt.ExamRevisionID.String(),
		State: string(receipt.State), WorkspaceCursor: receipt.WorkspaceCursor,
		ManifestDigest: receipt.ManifestDigest, SubmittedAt: model.TimeUTC(receipt.SubmittedAt).Format(time.RFC3339Nano)}
	return jsonResult(http.StatusCreated, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) getSubmission(request operationRequest) (operationResult, error) {
	query, err := managedSubmissionQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.application.GetExamSubmission(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	submission := view.Submission
	response := examSubmissionManagerResponse{SubmissionID: submission.ID.String(), ExamID: view.Authorization.ExamID.String(),
		ExamSittingID: view.Authorization.SittingID.String(), ExamAttemptID: submission.AttemptID.String(),
		ExamRevisionID: submission.ExamRevisionID.String(), WorkspaceID: submission.WorkspaceID.String(),
		ManifestSchemaVersion: submission.ManifestSchemaVersion,
		WorkspaceCursor:       submission.WorkspaceCursor, ManifestDigest: submission.ManifestDigest,
		ManifestEntryCount: submission.ManifestEntryCount, ManifestTotalFileBytes: submission.ManifestTotalFileBytes,
		FinalFocusLossSequence: submission.FinalFocusLossSequence, IntegrityState: string(submission.IntegrityState),
		UnresolvedIntegrityCount: submission.UnresolvedIntegrityCount,
		SubmittedAt:              model.TimeUTC(submission.SubmittedAt).Format(time.RFC3339Nano)}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) listSubmissionManifest(request operationRequest) (operationResult, error) {
	ownership, err := managedSubmissionQuery(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamSubmissionManifestQuery{GetSubmissionQuery: ownership, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > model.ExamSubmissionManifestReadMaximum {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeSubmissionManifestCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.AfterEntryID = cursor.EntryID
	}
	page, err := module.application.ListExamSubmissionManifest(request.context, request.invocation(), query)
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
			return operationResult{}, application.NewError("exam.attempt.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) openSubmissionFile(request operationRequest) (protocolResult, error) {
	ownership, err := managedSubmissionQuery(request)
	if err != nil {
		return protocolResult{}, err
	}
	raw, err := request.params.RequireAttemptWorkspaceEntryId()
	if err != nil {
		return protocolResult{}, err
	}
	entryID, err := model.ParseAttemptWorkspaceEntryID(raw)
	if err != nil {
		return protocolResult{}, invalidRequestError("attempt_workspace_entry_id", err)
	}
	opened, err := module.application.OpenExamSubmissionFile(request.context, request.invocation(),
		application.OpenExamSubmissionFileQuery{GetSubmissionQuery: ownership, EntryID: entryID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.SHA256+`"`)
}

func managedSubmissionQuery(request operationRequest) (application.GetExamSubmissionQuery, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return application.GetExamSubmissionQuery{}, err
	}
	raw, err := request.params.RequireSubmissionId()
	if err != nil {
		return application.GetExamSubmissionQuery{}, err
	}
	submissionID, err := model.ParseSubmissionID(raw)
	if err != nil {
		return application.GetExamSubmissionQuery{}, invalidRequestError("submission_id", err)
	}
	return application.GetExamSubmissionQuery{ExamID: examID, SittingID: sittingID,
		AttemptID: attemptID, SubmissionID: submissionID}, nil
}

func (module examAttemptHTTPModule) createWorkspaceFile(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata candidateWorkspaceFileUploadMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil || metadata.Size == nil {
		return protocolResult{}, invalidRequestError("multipart", errors.New("valid metadata and size are required"))
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: metadata.ParticipationID, Generation: metadata.Generation})
	if err != nil {
		return protocolResult{}, err
	}
	result, err := module.application.CreateCandidateExamWorkspaceFile(request.context, request.invocation(),
		application.CreateCandidateExamWorkspaceFileCommand{Access: mutationAccess, Path: metadata.Path, MediaType: metadata.MediaType,
			ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusCreated, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) moveWorkspaceEntry(request operationRequest) (operationResult, error) {
	access, entryID, err := candidateWorkspaceEntryAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body moveCandidateWorkspaceEntryRequest
	if err = decodeCandidateWorkspaceJSON(request, &body, "moveCandidateExamWorkspaceEntry"); err != nil {
		return operationResult{}, err
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	result, err := module.application.MoveCandidateExamWorkspaceEntry(request.context, request.invocation(),
		application.MoveCandidateExamWorkspaceEntryCommand{Access: mutationAccess, EntryID: entryID,
			ExpectedPath: body.ExpectedPath, DestinationPath: body.DestinationPath, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) replaceWorkspaceFile(request operationRequest) (protocolResult, error) {
	access, entryID, err := candidateWorkspaceEntryAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata replaceCandidateWorkspaceFileMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil || metadata.Size == nil {
		return protocolResult{}, invalidRequestError("multipart", errors.New("valid metadata and size are required"))
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: metadata.ParticipationID, Generation: metadata.Generation})
	if err != nil {
		return protocolResult{}, err
	}
	version, err := model.ParseWorkspaceContentVersion(metadata.ExpectedContentVersion)
	if err != nil {
		return protocolResult{}, invalidRequestError("expected_content_version", err)
	}
	result, err := module.application.ReplaceCandidateExamWorkspaceFile(request.context, request.invocation(),
		application.ReplaceCandidateExamWorkspaceFileCommand{Access: mutationAccess, EntryID: entryID,
			ExpectedPath: metadata.ExpectedPath, ExpectedContentVersion: version, MediaType: metadata.MediaType,
			ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusOK, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) deleteWorkspaceEntry(request operationRequest) (operationResult, error) {
	access, entryID, err := candidateWorkspaceEntryAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	var body deleteCandidateWorkspaceEntryRequest
	if err = decodeCandidateWorkspaceJSON(request, &body, "deleteCandidateExamWorkspaceEntry"); err != nil {
		return operationResult{}, err
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	var version model.WorkspaceContentVersion
	if body.ExpectedContentVersion != "" {
		version, err = model.ParseWorkspaceContentVersion(body.ExpectedContentVersion)
		if err != nil {
			return operationResult{}, invalidRequestError("expected_content_version", err)
		}
	}
	result, err := module.application.DeleteCandidateExamWorkspaceEntry(request.context, request.invocation(),
		application.DeleteCandidateExamWorkspaceEntryCommand{Access: mutationAccess, EntryID: entryID,
			ExpectedPath: body.ExpectedPath, ExpectedContentVersion: version, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, candidateWorkspaceMutationResult(result)).withHeaders(noStoreHeaders()), nil
}

func candidateWorkspaceMutationAccess(access application.CandidateExamAttemptAccess,
	wire candidateWorkspaceMutationAccessRequest,
) (application.ExamAttemptWorkspaceMutationAccess, error) {
	participationID, err := model.ParseAttemptParticipationID(wire.ParticipationID)
	if err != nil || wire.Generation < 1 {
		return application.ExamAttemptWorkspaceMutationAccess{}, invalidRequestError("participation", errors.New("valid participation_id and generation are required"))
	}
	return application.ExamAttemptWorkspaceMutationAccess{CandidateAccess: access, ParticipationID: participationID,
		Generation: wire.Generation}, nil
}

func decodeCandidateWorkspaceJSON(request operationRequest, target any, where string) error {
	if request.request == nil || request.request.Body == nil {
		return invalidRequestError(where, errors.New("request body is required"))
	}
	raw, err := io.ReadAll(io.LimitReader(request.request.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || rejectDuplicateTopLevelJSONMembers(raw) != nil {
		return invalidRequestError(where, errors.New("invalid strict JSON body"))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return invalidRequestError(where, err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return invalidRequestError(where, errors.New("request body must contain one JSON value"))
	}
	return nil
}

func candidateWorkspaceEntryAccess(request operationRequest) (application.CandidateExamAttemptAccess, model.AttemptWorkspaceEntryID, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return application.CandidateExamAttemptAccess{}, "", err
	}
	raw, err := request.params.RequireAttemptWorkspaceEntryId()
	if err != nil {
		return application.CandidateExamAttemptAccess{}, "", err
	}
	entryID, err := model.ParseAttemptWorkspaceEntryID(raw)
	if err != nil {
		return application.CandidateExamAttemptAccess{}, "", invalidRequestError("attempt_workspace_entry_id", err)
	}
	return access, entryID, nil
}

func candidateWorkspaceMutationResult(result application.ExamAttemptWorkspaceMutationResult) candidateWorkspaceMutationResponse {
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

func (module examAttemptHTTPModule) openResource(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	raw, err := request.params.RequireExamResourceId()
	if err != nil {
		return protocolResult{}, err
	}
	resourceID, err := model.ParseExamResourceID(raw)
	if err != nil {
		return protocolResult{}, invalidRequestError("exam_resource_id", err)
	}
	opened, err := module.application.OpenCandidateExamResource(request.context, request.invocation(), application.OpenCandidateExamResourceQuery{Access: access, ResourceID: resourceID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.SHA256+`"`)
}

func (module examAttemptHTTPModule) openWorkspaceFile(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	raw, err := request.params.RequireAttemptWorkspaceEntryId()
	if err != nil {
		return protocolResult{}, err
	}
	entryID, err := model.ParseAttemptWorkspaceEntryID(raw)
	if err != nil {
		return protocolResult{}, invalidRequestError("attempt_workspace_entry_id", err)
	}
	opened, err := module.application.OpenCandidateExamWorkspaceFile(request.context, request.invocation(), application.OpenCandidateExamWorkspaceFileQuery{Access: access, EntryID: entryID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.ContentVersion.String()+`"`)
}

func candidateAttemptContentResult(request operationRequest, opened application.OpenedExamAttemptContent, etag string) (protocolResult, error) {
	if opened.Body == nil || opened.SizeBytes < 0 || strings.TrimSpace(opened.MediaType) == "" || etag == `""` {
		return protocolResult{}, errors.New("Exam Attempt application returned incomplete content")
	}
	headers := privateNoStoreHeaders()
	headers.Set("Content-Type", opened.MediaType)
	headers.Set("ETag", etag)
	if etagMatches(request.request.Header.Get("If-None-Match"), etag) {
		_ = opened.Body.Close()
		return notModifiedProtocolResult(opened.SizeBytes).withHeaders(headers), nil
	}
	return binaryDownloadProtocolResult(opened.Body, opened.SizeBytes).withHeaders(headers), nil
}

func candidateAccess(request operationRequest) (application.CandidateExamAttemptAccess, error) {
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return application.CandidateExamAttemptAccess{}, err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return application.CandidateExamAttemptAccess{}, invalidRequestError("exam_attempt_id", err)
	}
	headers, err := candidateAttemptAccessHeaders(request.request)
	if err != nil {
		return application.CandidateExamAttemptAccess{}, invalidRequestError("candidate_attempt_headers", err)
	}
	return application.CandidateExamAttemptAccess{AttemptID: attemptID, ConnectionID: headers.ConnectionID,
		ContinuityCredential: headers.ContinuityCredential}, nil
}

func managedExamAttemptScope(request operationRequest) (model.ExamID, model.ExamSittingID, error) {
	rawExam, err := request.params.RequireExamId()
	if err != nil {
		return "", "", err
	}
	rawSitting, err := request.params.RequireExamSittingId()
	if err != nil {
		return "", "", err
	}
	examID, err := model.ParseExamID(rawExam)
	if err != nil {
		return "", "", invalidRequestError("exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(rawSitting)
	if err != nil {
		return "", "", invalidRequestError("exam_sitting_id", err)
	}
	return examID, sittingID, nil
}

func managedExamAttemptIDs(request operationRequest) (model.ExamID, model.ExamSittingID, model.ExamAttemptID, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return "", "", "", err
	}
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return "", "", "", err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return "", "", "", invalidRequestError("exam_attempt_id", err)
	}
	return examID, sittingID, attemptID, nil
}

func examAttemptManagerResponseFromView(view application.ExamAttemptManagerView) (examAttemptManagerResponse, error) {
	if view.Attempt == nil || view.Workspace == nil {
		return examAttemptManagerResponse{}, errors.New("Exam Attempt application returned an incomplete manager projection")
	}
	attempt, workspace := view.Attempt, view.Workspace
	response := examAttemptManagerResponse{ID: attempt.ID.String(), ExamID: attempt.ExamID.String(), SittingID: attempt.SittingID.String(),
		CandidateUserID: attempt.CandidateUserID.String(), AdmissionRevisionID: attempt.AdmissionRevisionID.String(), State: string(attempt.State),
		CreatedAt: model.TimeUTC(attempt.CreatedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(attempt.UpdatedAt).Format(time.RFC3339Nano),
		SubmittedAt: attempt.SubmittedAt.FormatRFC3339(), Revision: attempt.Revision,
		Workspace: examAttemptWorkspaceResponse{ID: workspace.ID.String(), Cursor: workspace.Cursor,
			CreatedAt: model.TimeUTC(workspace.CreatedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(workspace.UpdatedAt).Format(time.RFC3339Nano)}}
	if participation := view.LatestParticipation; participation != nil {
		response.LatestParticipation = &examAttemptParticipationResponse{ID: participation.ID.String(), State: string(participation.State),
			Generation: participation.Generation, RenewalSequence: participation.RenewalSequence,
			StartedAt: model.TimeUTC(participation.StartedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(participation.UpdatedAt).Format(time.RFC3339Nano),
			LeaseExpiresAt: model.TimeUTC(participation.LeaseExpiresAt).Format(time.RFC3339Nano), EndedAt: participation.EndedAt.FormatRFC3339(), EndReason: string(participation.EndReason)}
	}
	if connection := view.CurrentConnection; connection != nil {
		response.CurrentConnection = &examAttemptConnectionResponse{ID: connection.ID.String(), State: string(connection.State),
			OpenedAt: model.TimeUTC(connection.OpenedAt).Format(time.RFC3339Nano), ClosedAt: connection.ClosedAt.FormatRFC3339(), CloseReason: string(connection.CloseReason)}
	}
	if suspension := view.ActiveSuspension; suspension != nil {
		response.ActiveSuspension = &examAttemptSuspensionResponse{ID: suspension.ID.String(),
			ParticipationID: suspension.ParticipationID.String(), FlagID: suspension.FlagID.String(), Generation: suspension.Generation,
			State: string(suspension.State), Source: string(suspension.Source), CandidateReason: string(suspension.CandidateReason),
			StartedAt: model.TimeUTC(suspension.StartedAt).Format(time.RFC3339Nano), EndedAt: suspension.EndedAt.FormatRFC3339(),
			ReallowedByUserID: suspension.ReallowedByUserID.String()}
	}
	return response, nil
}

func privateNoStoreHeaders() http.Header { return http.Header{"Cache-Control": {"private, no-store"}} }
