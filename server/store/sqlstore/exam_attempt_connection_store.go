// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const examAttemptConnectOutcomeMaximumBytes = 2 * 1024

type examAttemptConnectOutcome struct {
	AttemptID          string    `json:"a"`
	WorkspaceID        string    `json:"w"`
	ParticipationID    string    `json:"p"`
	ConnectionID       string    `json:"c"`
	ExamID             string    `json:"e"`
	SittingID          string    `json:"s"`
	CandidateID        string    `json:"u"`
	RevisionID         string    `json:"r"`
	SessionID          string    `json:"n"`
	ClassID            string    `json:"l"`
	StartedAt          time.Time `json:"t"`
	LeaseExpiresAt     time.Time `json:"x"`
	EntryCount         int       `json:"q"`
	Generation         int64     `json:"g"`
	FirstAdmission     bool      `json:"f"`
	ConnectionOpened   bool      `json:"o"`
	RevokedSessionIDs  []string  `json:"i,omitempty"`
	RevokedTokenHashes []string  `json:"h,omitempty"`
}

func (examAttemptConnectOutcome) outcomeDocument() {}

// Connect admits the Candidate to the Attempt under command, creating the first
// Attempt and Workspace when needed. Replays recheck current eligibility and
// return the stored outcome.
func (s *SQLExamAttemptStore) Connect(ctx context.Context, input *store.ExamAttemptConnect, command *store.CommandIdempotency) (*store.ExamAttemptConnectResult, error) {
	if err := validateExamAttemptConnect(input, command); err != nil {
		return nil, err
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "connect Exam Attempt", idempotentMutation[examAttemptConnectOutcome]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examAttemptConnectOutcome, error) {
			return s.connect(ctx, tx, input)
		},
		encode: func(outcome examAttemptConnectOutcome) ([]byte, error) {
			encoded, encodeErr := encodeOutcome(outcome)
			if encodeErr == nil && len(encoded) > examAttemptConnectOutcomeMaximumBytes {
				return nil, store.NewErrInvalidInput("exam_attempt", "connect_outcome", len(encoded))
			}
			return encoded, encodeErr
		},
		decode: func(encoded []byte) (examAttemptConnectOutcome, error) {
			var outcome examAttemptConnectOutcome
			if err := decodeOutcome(encoded, &outcome); err != nil {
				return outcome, err
			}
			return outcome, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome examAttemptConnectOutcome, originalAuditID string) error {
			if err := requireDesktopCompatibilityPolicyRevision(ctx, tx, input.DesktopCompatibilityPolicyRevision); err != nil {
				return err
			}
			guard, lockErr := s.lockExamAttemptEligibility(ctx, tx, input, true)
			if lockErr != nil {
				return lockErr
			}
			if guard.ExamID != outcome.ExamID || guard.ClassID != outcome.ClassID || outcome.SittingID != input.SittingID.String() ||
				outcome.CandidateID != input.CandidateUserID.String() || outcome.SessionID != input.SessionID.String() {
				return store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
			}
			var frozen struct {
				Document []byte `db:"attempt_configuration_canonical"`
				Digest   string `db:"attempt_configuration_digest"`
			}
			if lockErr = tx.Get(ctx, &frozen, `SELECT attempt_configuration_canonical,attempt_configuration_digest
				FROM exam_attempts WHERE id=? AND exam_sitting_id=? AND candidate_user_id=? FOR SHARE`,
				outcome.AttemptID, outcome.SittingID, outcome.CandidateID); lockErr != nil {
				return translateError("exam_attempt", outcome.AttemptID, lockErr)
			}
			if _, lockErr = validateExistingAttemptConfiguration(frozen.Document, frozen.Digest, input); lockErr != nil {
				return lockErr
			}
			return completeExamAttemptConnectAudit(ctx, tx, outcome, true, originalAuditID, input.AuditEventID, input.AuditAt)
		},
	})
	if err != nil {
		return nil, err
	}
	aggregate, err := s.examAttemptConnectResult(ctx, result.Value)
	if err != nil {
		return nil, err
	}
	aggregate.Replayed = result.Replayed
	aggregate.ConnectionOpened = result.Value.ConnectionOpened && !result.Replayed
	aggregate.RevokedSessionIDs = typedIDs[model.SessionID](result.Value.RevokedSessionIDs)
	aggregate.RevokedAccessTokenDigests = append([]string(nil), result.Value.RevokedTokenHashes...)
	return aggregate, nil
}

func validateExamAttemptConnect(input *store.ExamAttemptConnect, command *store.CommandIdempotency) error {
	if input == nil || command == nil ||
		command.Operation != store.ExamAttemptConnectOperation || !input.SittingID.IsValid() || !input.CandidateUserID.IsValid() || !input.SessionID.IsValid() ||
		!input.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(input.DPoPKeyThumbprint) ||
		command.UserID != input.CandidateUserID || !input.AttemptID.IsValid() || !input.WorkspaceID.IsValid() || !input.ParticipationID.IsValid() || !input.ConnectionID.IsValid() ||
		!model.IsValidTokenHash(input.ContinuityCredentialHash) || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() ||
		input.Security.Validate() != nil || input.DesktopBuild.NativeAgreement == nil || input.DesktopBuild.Validate() != nil || input.DesktopCompatibilityPolicyRevision < 1 || !model.IsValidSHA256Fingerprint(input.ConfigurationManifestFingerprint) ||
		input.DesktopBuild.AttemptConfigurationManifestFingerprint != input.ConfigurationManifestFingerprint {
		return store.NewErrInvalidInput("exam_attempt", "connect", nil)
	}
	if input.InitialConfiguration != nil {
		configuration := input.InitialConfiguration
		if configuration.Validate() != nil || configuration.ManifestFingerprint != input.DesktopBuild.AttemptConfigurationManifestFingerprint ||
			input.DesktopBuild.ConfigurationManifest.ValidateSelection(configuration.ApprovedCommands, configuration.ApprovedKeybindings) != nil {
			return store.NewErrInvalidInput("exam_attempt", "initial_configuration", nil)
		}
	}
	return nil
}

func (s *SQLExamAttemptStore) connect(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect) (examAttemptConnectOutcome, error) {
	var zero examAttemptConnectOutcome
	// Serialize activation with every Session issuance for this User. If the
	// Session commits first it may be used for admission; once admission commits,
	// later Session creation observes the active Attempt and fails closed.
	if err := lockUserSessions(ctx, tx, input.CandidateUserID.String()); err != nil {
		return zero, err
	}
	if err := requireDesktopCompatibilityPolicyRevision(ctx, tx, input.DesktopCompatibilityPolicyRevision); err != nil {
		return zero, err
	}
	guard, err := s.lockExamAttemptEligibility(ctx, tx, input, false)
	if err != nil {
		return zero, err
	}

	var existing struct {
		AttemptID           string    `db:"attempt_id"`
		WorkspaceID         string    `db:"workspace_id"`
		AdmissionRevision   string    `db:"admission_revision_id"`
		State               string    `db:"state"`
		CreatedAt           time.Time `db:"created_at"`
		WorkspaceEntryCount int       `db:"workspace_entry_count"`
		Configuration       []byte    `db:"attempt_configuration_canonical"`
		ConfigurationDigest string    `db:"attempt_configuration_digest"`
	}
	err = tx.Get(ctx, &existing, `SELECT a.id AS attempt_id,w.id AS workspace_id,a.admission_revision_id,a.state,a.created_at,
		a.attempt_configuration_canonical,a.attempt_configuration_digest,
		(SELECT COUNT(*) FROM exam_attempt_workspace_entries entries WHERE entries.workspace_id=w.id) AS workspace_entry_count
		FROM exam_attempts a JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
		WHERE a.exam_sitting_id=? AND a.candidate_user_id=? FOR UPDATE OF a,w`, input.SittingID.String(), input.CandidateUserID.String())
	if err == nil {
		if _, configurationErr := validateExistingAttemptConfiguration(existing.Configuration, existing.ConfigurationDigest, input); configurationErr != nil {
			return zero, configurationErr
		}
		return s.reconnect(ctx, tx, input, guard, existing.AttemptID, existing.WorkspaceID, existing.AdmissionRevision,
			existing.State, existing.CreatedAt, existing.WorkspaceEntryCount)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, fmt.Errorf("find existing Exam Attempt: %w", err)
	}
	return s.firstAdmission(ctx, tx, input, guard)
}

func validateExistingAttemptConfiguration(document []byte, digest string, input *store.ExamAttemptConnect) (model.AttemptConfiguration, error) {
	configuration, err := model.DecodeAttemptConfiguration(document, digest)
	if err != nil {
		return model.AttemptConfiguration{}, invalidPersistedState("exam_attempt", "attempt_configuration", err)
	}
	if input.ConfigurationManifestFingerprint != configuration.ManifestFingerprint ||
		configuration.ManifestFingerprint != input.DesktopBuild.AttemptConfigurationManifestFingerprint ||
		input.DesktopBuild.ConfigurationManifest.ValidateSelection(configuration.ApprovedCommands, configuration.ApprovedKeybindings) != nil {
		return model.AttemptConfiguration{}, store.NewErrConflict("exam_attempt", "attempt_configuration_unsupported", nil)
	}
	if input.InitialConfiguration != nil {
		proposed, proposalErr := input.InitialConfiguration.CanonicalAdmission()
		retained, retainedErr := configuration.AttemptConfigurationCandidate.CanonicalAdmission()
		if proposalErr != nil || retainedErr != nil || !bytes.Equal(proposed, retained) {
			return model.AttemptConfiguration{}, store.NewErrConflict("exam_attempt", "attempt_configuration_frozen", nil)
		}
	}
	return configuration, nil
}

func (s *SQLExamAttemptStore) examAttemptConnectResult(ctx context.Context, outcome examAttemptConnectOutcome) (*store.ExamAttemptConnectResult, error) {
	attemptID, err := model.ParseExamAttemptID(outcome.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "id", err)
	}
	examID, err := model.ParseExamID(outcome.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(outcome.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	candidateID, err := model.ParseUserID(outcome.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(outcome.RevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(outcome.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(outcome.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "id", err)
	}
	connectionID, err := model.ParseAttemptConnectionID(outcome.ConnectionID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "id", err)
	}
	if _, err = model.NewExamAttempt(attemptID, examID, sittingID, candidateID, revisionID, outcome.StartedAt); err != nil {
		return nil, invalidPersistedState("exam_attempt", "outcome", err)
	}
	if _, err = model.NewExamAttemptWorkspace(workspaceID, attemptID, outcome.StartedAt); err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "outcome", err)
	}
	current, err := s.Get(ctx, examID, attemptID)
	if err != nil {
		return nil, err
	}
	attempt, workspace := current.Attempt, current.Workspace
	var participationRow struct {
		State           string         `db:"state"`
		Generation      int64          `db:"generation"`
		RenewalSequence int64          `db:"renewal_sequence"`
		StartedAt       time.Time      `db:"started_at"`
		UpdatedAt       time.Time      `db:"updated_at"`
		LeaseExpiresAt  time.Time      `db:"lease_expires_at"`
		EndedAt         sql.NullTime   `db:"ended_at"`
		EndReason       sql.NullString `db:"end_reason"`
		LeaseCurrent    bool           `db:"lease_current"`
	}
	if err = s.GetMaster().Get(ctx, &participationRow, `SELECT state,generation,renewal_sequence,started_at,updated_at,lease_expires_at,ended_at,end_reason,
		lease_expires_at>statement_timestamp() AS lease_current
		FROM exam_attempt_participations WHERE id=? AND exam_attempt_id=?`, participationID.String(), attemptID.String()); err != nil {
		return nil, translateError("attempt_participation", participationID.String(), err)
	}
	if !participationRow.LeaseCurrent {
		return nil, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	participation := &store.ExamAttemptParticipationView{ID: participationID, AttemptID: attemptID,
		State: model.AttemptParticipationState(participationRow.State), Generation: participationRow.Generation,
		RenewalSequence: participationRow.RenewalSequence, StartedAt: model.TimeUTC(participationRow.StartedAt),
		UpdatedAt: model.TimeUTC(participationRow.UpdatedAt), LeaseExpiresAt: model.TimeUTC(participationRow.LeaseExpiresAt),
		EndedAt: OptionalTimeFromNullTime(participationRow.EndedAt), EndReason: model.AttemptParticipationEndReason(participationRow.EndReason.String)}
	var connectionRow attemptConnectionRow
	if err = s.GetMaster().Get(ctx, &connectionRow, `SELECT id,exam_attempt_id,participation_id,session_id,state,opened_at,closed_at,close_reason
		FROM exam_attempt_connections WHERE id=?`, connectionID.String()); err != nil {
		return nil, translateError("attempt_connection", connectionID.String(), err)
	}
	connection, err := connectionRow.model()
	if err != nil {
		return nil, err
	}
	classID, err := model.ParseClassID(outcome.ClassID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "class_id", err)
	}
	configuration, capabilities, browserPolicy, liveCorrections, err := s.loadConnectRuntimeCapabilities(ctx, outcome)
	if err != nil {
		return nil, err
	}
	security, err := loadAdmittedSecurity(ctx, s.GetMaster(), outcome.ParticipationID)
	if err != nil {
		return nil, err
	}
	return &store.ExamAttemptConnectResult{Security: security, Attempt: attempt, Workspace: workspace, Participation: participation,
		Connection: connection, ClassID: classID, Configuration: configuration,
		RuntimeCapabilities: capabilities, BrowserPolicy: browserPolicy, LiveCorrections: liveCorrections, FirstAdmission: outcome.FirstAdmission}, nil
}

func (s *SQLExamAttemptStore) loadConnectRuntimeCapabilities(ctx context.Context, outcome examAttemptConnectOutcome) (model.AttemptConfiguration, store.CandidateRuntimeCapabilities, *store.CandidateBrowserPolicy, []model.CandidateLiveCorrection, error) {
	var row struct {
		Configuration        []byte    `db:"attempt_configuration_canonical"`
		ConfigurationDigest  string    `db:"attempt_configuration_digest"`
		AdmissionRevisionID  string    `db:"admission_revision_id"`
		CurrentRevisionID    string    `db:"current_revision_id"`
		SittingState         string    `db:"sitting_state"`
		DatabaseNow          time.Time `db:"database_now"`
		Policy               []byte    `db:"policy_canonical"`
		BrowserPolicy        []byte    `db:"browser_policy_canonical"`
		BrowserPolicyDigest  string    `db:"browser_policy_digest"`
		PolicyRevisionNumber int64     `db:"policy_revision_number"`
		BrowserUnavailable   bool      `db:"browser_unavailable"`
		RuntimeAvailable     bool      `db:"runtime_available"`
	}
	if err := s.GetMaster().Get(ctx, &row, `SELECT a.attempt_configuration_canonical,a.attempt_configuration_digest,
		a.admission_revision_id,s.exam_revision_id AS current_revision_id,s.state AS sitting_state,
		statement_timestamp() AS database_now,r.policy_canonical,
		r.browser_policy_canonical,r.browser_policy_digest,r.number AS policy_revision_number,
 EXISTS(SELECT 1 FROM exam_attempt_security_owners bo JOIN exam_attempt_participations bp ON bp.id=bo.participation_id WHERE bo.exam_attempt_id=a.id AND bp.state='active' AND bo.browser_source_unavailable) AS browser_unavailable,
		EXISTS (SELECT 1 FROM exam_attempt_participations p JOIN exam_attempt_connections c
			ON c.participation_id=p.id AND c.exam_attempt_id=p.exam_attempt_id
			WHERE p.id=? AND c.id=? AND p.exam_attempt_id=a.id AND p.state='active' AND c.state='open'
			AND p.session_id=? AND c.session_id=?) AS runtime_available
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_revisions r ON r.id=s.exam_revision_id AND r.exam_id=s.exam_id AND r.sealed=true
		WHERE a.id=? AND a.candidate_user_id=?`, outcome.ParticipationID, outcome.ConnectionID, outcome.SessionID, outcome.SessionID,
		outcome.AttemptID, outcome.CandidateID); err != nil {
		return model.AttemptConfiguration{}, store.CandidateRuntimeCapabilities{}, nil, nil, translateError("exam_attempt", outcome.AttemptID, err)
	}
	configuration, err := model.DecodeAttemptConfiguration(row.Configuration, row.ConfigurationDigest)
	if err != nil {
		return model.AttemptConfiguration{}, store.CandidateRuntimeCapabilities{}, nil, nil, invalidPersistedState("exam_attempt", "attempt_configuration", err)
	}
	if !row.RuntimeAvailable {
		return configuration, store.CandidateRuntimeCapabilities{}, nil, []model.CandidateLiveCorrection{}, nil
	}
	attemptID, err := model.ParseExamAttemptID(outcome.AttemptID)
	if err != nil {
		return model.AttemptConfiguration{}, store.CandidateRuntimeCapabilities{}, nil, nil, invalidPersistedState("exam_attempt", "id", err)
	}
	currentRevisionID, err := model.ParseExamRevisionID(row.CurrentRevisionID)
	if err != nil {
		return model.AttemptConfiguration{}, store.CandidateRuntimeCapabilities{}, nil, nil, invalidPersistedState("exam_sitting", "exam_revision_id", err)
	}
	liveCorrections, pendingCapabilities, err := listCandidateLiveCorrections(ctx, s.GetMaster(), attemptID, currentRevisionID)
	if err != nil {
		return model.AttemptConfiguration{}, store.CandidateRuntimeCapabilities{}, nil, nil, err
	}
	retention, err := getRetentionPolicy(ctx, s.GetMaster(), "")
	if err != nil {
		return model.AttemptConfiguration{}, store.CandidateRuntimeCapabilities{}, nil, nil, err
	}
	capabilities, browserPolicy, err := candidateRuntimeCapabilities(configuration, row.AdmissionRevisionID, row.CurrentRevisionID,
		row.SittingState, row.DatabaseNow, row.Policy, row.BrowserPolicy, row.BrowserPolicyDigest, pendingCapabilities, row.PolicyRevisionNumber, retention, row.BrowserUnavailable)
	return configuration, capabilities, browserPolicy, liveCorrections, err
}

func candidateRuntimeCapabilities(configuration model.AttemptConfiguration, admissionRevision, currentRevision, sittingState string,
	databaseNow time.Time, policyDocument, browserPolicyDocument []byte, browserPolicyDigest string,
	pendingCapabilities []model.CandidateCapability, revisionNumber int64, retention *model.RetentionPolicy, browserUnavailable bool,
) (store.CandidateRuntimeCapabilities, *store.CandidateBrowserPolicy, error) {
	admissionRevisionID, err := model.ParseExamRevisionID(admissionRevision)
	if err != nil {
		return store.CandidateRuntimeCapabilities{}, nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	currentRevisionID, err := model.ParseExamRevisionID(currentRevision)
	if err != nil {
		return store.CandidateRuntimeCapabilities{}, nil, invalidPersistedState("exam_sitting", "exam_revision_id", err)
	}
	policy, err := model.DecodeExamPolicySet(policyDocument)
	if err != nil {
		return store.CandidateRuntimeCapabilities{}, nil, invalidPersistedState("exam_revision", "policy_canonical", err)
	}
	browserPolicy, err := model.ParseBrowserPolicyDocument(browserPolicyDocument)
	if err != nil {
		return store.CandidateRuntimeCapabilities{}, nil, invalidPersistedState("exam_revision", "browser_policy_canonical", err)
	}
	computedBrowserDigest, err := model.BrowserPolicyDigest(browserPolicy)
	if err != nil || computedBrowserDigest != browserPolicyDigest {
		return store.CandidateRuntimeCapabilities{}, nil, invalidPersistedState("exam_revision", "browser_policy_digest", errors.New("Browser Policy digest mismatch"))
	}
	interaction := store.CandidateInteractionInteractive
	if sittingState == string(model.ExamSittingPaused) {
		interaction = store.CandidateInteractionSittingPaused
	} else if sittingState != string(model.ExamSittingOpen) {
		return store.CandidateRuntimeCapabilities{}, nil, invalidPersistedState("exam_sitting", "state", errors.New("runtime projection requires Open or Paused"))
	}
	workspaceAllowed := interaction == store.CandidateInteractionInteractive && !slices.Contains(pendingCapabilities, model.CandidateCapabilityWorkspace)
	submissionAllowed := interaction == store.CandidateInteractionInteractive && !slices.Contains(pendingCapabilities, model.CandidateCapabilitySubmission)
	browserCapability := store.CandidateBrowserCapability{State: store.CandidateBrowserDisabled}
	var browserProjection *store.CandidateBrowserPolicy
	if browserPolicy.Enabled {
		state := store.CandidateBrowserAvailable
		if browserUnavailable {
			state = store.CandidateBrowserTemporarilyUnavailable
		}
		if interaction == store.CandidateInteractionSittingPaused {
			state = store.CandidateBrowserSittingPaused
		} else if slices.Contains(pendingCapabilities, model.CandidateCapabilityBrowser) {
			state = store.CandidateBrowserAcknowledgementRequired
		}
		visibleDigest := browserPolicyDigest
		browserCapability = store.CandidateBrowserCapability{State: state, PolicyRevisionID: currentRevisionID, PolicyDigest: visibleDigest}
	}
	if !slices.Contains(pendingCapabilities, model.CandidateCapabilityBrowser) {
		disclosure, err := model.NewBrowserActivityDisclosure(retention, browserPolicy.MayCreateIntegrityEvidence())
		if err != nil || revisionNumber < 1 {
			return store.CandidateRuntimeCapabilities{}, nil, invalidPersistedState("exam_revision", "browser_disclosure", errors.New("invalid candidate browser provenance"))
		}
		browserProjection = &store.CandidateBrowserPolicy{PolicyRevisionID: currentRevisionID, PolicyRevisionNumber: revisionNumber, PolicyDigest: browserPolicyDigest, Policy: browserPolicy.Clone(), BrowserActivityDisclosure: disclosure}
	}
	return store.CandidateRuntimeCapabilities{
		SchemaVersion: 1, ServerTime: model.TimeUTC(databaseNow), InteractionState: interaction,
		AttemptConfiguration: store.CandidateAttemptConfiguration{Revision: configuration.Revision,
			Presentation: configuration.Presentation, ApprovedCommands: slices.Clone(configuration.ApprovedCommands),
			ApprovedKeybindings: slices.Clone(configuration.ApprovedKeybindings), Digest: configuration.Digest},
		FocusLossCollectionEnabled: policy.FocusLoss.Enabled, WorkspaceMutationAllowed: workspaceAllowed,
		PendingCorrectionCapabilities: append([]model.CandidateCapability{}, pendingCapabilities...),
		SubmissionAllowed:             submissionAllowed,
		Browser:                       browserCapability,
		ExamRevision: store.CandidateExamRevisionCapability{AdmissionRevisionID: admissionRevisionID,
			CurrentRevisionID: currentRevisionID, AcknowledgementRequired: len(pendingCapabilities) != 0},
		Departure: store.CandidateDepartureCapability{Allowed: false, Reason: "attempt_in_progress"},
	}, browserProjection, nil
}

func completeExamAttemptConnectAudit(ctx context.Context, tx *sqlxTxWrapper, outcome examAttemptConnectOutcome,
	replayed bool, originalAuditID string, auditID model.AuditEventID, auditAt time.Time,
) error {
	data := map[string]any{"exam_id": outcome.ExamID, "exam_sitting_id": outcome.SittingID,
		"exam_attempt_id": outcome.AttemptID, "admission_revision_id": outcome.RevisionID,
		"participation_id": outcome.ParticipationID, "generation": outcome.Generation, "connection_id": outcome.ConnectionID,
		"workspace_entry_count": outcome.EntryCount, "first_admission": outcome.FirstAdmission,
		"connection_opened": outcome.ConnectionOpened && !replayed}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
		return fmt.Errorf("complete Exam Attempt connect audit: %w", err)
	}
	return nil
}

// CloseConnection closes the Candidate's open Attempt Connection with
// input.Reason at database time and completes the audit event. Closing an
// already-closed Connection changes nothing and reports Changed false; a
// Connection of another Candidate or Session is not found.
func (s *SQLExamAttemptStore) CloseConnection(ctx context.Context, input *store.ExamAttemptConnectionClose) (*store.ExamAttemptConnectionCloseResult, error) {
	if input == nil || !input.ConnectionID.IsValid() || !input.CandidateUserID.IsValid() || !input.SessionID.IsValid() || !input.Reason.IsValid() || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return nil, store.NewErrInvalidInput("attempt_connection", "close", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "close Attempt Connection", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptConnectionCloseResult, error) {
		row, attemptID, examID, sittingID, candidateID, err := lockAttemptConnection(ctx, tx, input.ConnectionID)
		if err != nil {
			return nil, err
		}
		if candidateID != input.CandidateUserID || row.SessionID != input.SessionID.String() {
			return nil, store.NewErrNotFound("attempt_connection", input.ConnectionID.String())
		}
		changed := row.State == string(model.AttemptConnectionOpen)
		if changed {
			var now time.Time
			if err = tx.Get(ctx, &now, `SELECT statement_timestamp()`); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state='closed',closed_at=?,close_reason=?
				WHERE id=? AND state='open'`, now, string(input.Reason), input.ConnectionID.String()); err != nil {
				return nil, fmt.Errorf("close Attempt Connection: %w", err)
			}
			row.State, row.ClosedAt, row.CloseReason = string(model.AttemptConnectionClosed), sql.NullTime{Time: now, Valid: true}, sql.NullString{String: string(input.Reason), Valid: true}
		}
		connection, err := row.model()
		if err != nil {
			return nil, err
		}
		data, err := model.EncodeAuditData(map[string]any{"exam_sitting_id": sittingID.String(), "exam_attempt_id": attemptID.String(),
			"connection_id": connection.ID.String(), "state": connection.State, "close_reason": connection.CloseReason, "changed": changed})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete Attempt Connection close audit: %w", err)
		}
		return &store.ExamAttemptConnectionCloseResult{AttemptID: attemptID, ExamID: examID, SittingID: sittingID,
			CandidateUserID: candidateID, Connection: connection, Changed: changed}, nil
	})
}

type attemptConnectionRow struct {
	ID              string         `db:"id"`
	AttemptID       string         `db:"exam_attempt_id"`
	ParticipationID string         `db:"participation_id"`
	SessionID       string         `db:"session_id"`
	State           string         `db:"state"`
	OpenedAt        time.Time      `db:"opened_at"`
	ClosedAt        sql.NullTime   `db:"closed_at"`
	CloseReason     sql.NullString `db:"close_reason"`
}

func (row attemptConnectionRow) model() (*model.AttemptConnection, error) {
	id, err := model.ParseAttemptConnectionID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "exam_attempt_id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "participation_id", err)
	}
	sessionID, err := model.ParseSessionID(row.SessionID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "session_id", err)
	}
	value := &model.AttemptConnection{ID: id, AttemptID: attemptID, ParticipationID: participationID, SessionID: sessionID,
		State: model.AttemptConnectionState(row.State), OpenedAt: model.TimeUTC(row.OpenedAt), ClosedAt: OptionalTimeFromNullTime(row.ClosedAt)}
	if row.CloseReason.Valid {
		value.CloseReason = model.AttemptConnectionCloseReason(row.CloseReason.String)
	}
	if err := value.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_connection", "value", err)
	}
	return value, nil
}

func lockAttemptConnection(ctx context.Context, tx *sqlxTxWrapper, id model.AttemptConnectionID) (attemptConnectionRow, model.ExamAttemptID, model.ExamID, model.ExamSittingID, model.UserID, error) {
	var joined struct {
		attemptConnectionRow
		ExamID      string `db:"exam_id"`
		SittingID   string `db:"exam_sitting_id"`
		CandidateID string `db:"candidate_user_id"`
	}
	err := tx.Get(ctx, &joined, `SELECT c.id,c.exam_attempt_id,c.participation_id,c.session_id,c.state,c.opened_at,c.closed_at,c.close_reason,
		a.exam_id,a.exam_sitting_id,a.candidate_user_id FROM exam_attempt_connections c JOIN exam_attempts a ON a.id=c.exam_attempt_id
		WHERE c.id=? FOR UPDATE OF c`, id.String())
	if err != nil {
		return attemptConnectionRow{}, "", "", "", "", translateError("attempt_connection", id.String(), err)
	}
	attemptID, err := model.ParseExamAttemptID(joined.AttemptID)
	if err != nil {
		return attemptConnectionRow{}, "", "", "", "", invalidPersistedState("attempt_connection", "exam_attempt_id", err)
	}
	examID, err := model.ParseExamID(joined.ExamID)
	if err != nil {
		return attemptConnectionRow{}, "", "", "", "", invalidPersistedState("attempt_connection", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(joined.SittingID)
	if err != nil {
		return attemptConnectionRow{}, "", "", "", "", invalidPersistedState("attempt_connection", "exam_sitting_id", err)
	}
	candidateID, err := model.ParseUserID(joined.CandidateID)
	if err != nil {
		return attemptConnectionRow{}, "", "", "", "", invalidPersistedState("attempt_connection", "candidate_user_id", err)
	}
	return joined.attemptConnectionRow, attemptID, examID, sittingID, candidateID, nil
}
