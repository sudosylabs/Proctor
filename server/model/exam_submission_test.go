// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewExamSubmissionSealsCanonicalManifestAndIntegrity(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 15, 12, 30, 0, 0, time.FixedZone("offset", 2*60*60))
	directory := ExamSubmissionManifestEntry{
		EntryID: AttemptWorkspaceEntryID(strings.Repeat("b", IDLength)),
		Kind:    StarterWorkspaceEntryDirectory,
		Path:    "cmd",
	}
	starter := ExamSubmissionManifestEntry{
		EntryID:         AttemptWorkspaceEntryID(strings.Repeat("d", IDLength)),
		Kind:            StarterWorkspaceEntryFile,
		Path:            "cmd/main.go",
		ContentVersion:  WorkspaceContentVersion(strings.Repeat("f", IDLength)),
		MediaType:       "text/x-go",
		SizeBytes:       12,
		SHA256:          strings.Repeat("a", 64),
		StorageOrigin:   AttemptWorkspaceStorageStarter,
		StarterObjectID: StarterWorkspaceObjectID(strings.Repeat("g", IDLength)),
	}
	attempt := ExamSubmissionManifestEntry{
		EntryID:         AttemptWorkspaceEntryID(strings.Repeat("c", IDLength)),
		Kind:            StarterWorkspaceEntryFile,
		Path:            "answer.txt",
		ContentVersion:  WorkspaceContentVersion(strings.Repeat("e", IDLength)),
		MediaType:       "text/plain",
		SizeBytes:       7,
		SHA256:          strings.Repeat("9", 64),
		StorageOrigin:   AttemptWorkspaceStorageAttempt,
		AttemptObjectID: AttemptWorkspaceObjectID(strings.Repeat("h", IDLength)),
	}
	input := []ExamSubmissionManifestEntry{starter, directory, attempt}
	manifest, err := NewExamSubmissionManifest(41, input)
	require.NoError(t, err)
	got := []AttemptWorkspaceEntryID{manifest.Entries[0].EntryID, manifest.Entries[1].EntryID, manifest.Entries[2].EntryID}
	require.True(t, slices.Equal(got, []AttemptWorkspaceEntryID{directory.EntryID, attempt.EntryID, starter.EntryID}), "canonical Entry order = %v", got)
	require.Equal(t, ExamSubmissionManifestSchemaVersion, manifest.SchemaVersion)
	require.Equal(t, int64(41), manifest.WorkspaceCursor)
	require.Equal(t, "d1527073b4fa44425a1309a42e4444ab22180877a59c21e88d4d05057d3f4495", manifest.SHA256)
	require.Equal(t, 3, manifest.EntryCount)
	require.Equal(t, int64(19), manifest.TotalFileBytes)
	input[0].Path = "caller-mutation.go"
	require.Equal(t, "cmd/main.go", manifest.Entries[2].Path)
	reordered, err := NewExamSubmissionManifest(41, []ExamSubmissionManifestEntry{attempt, starter, directory})
	require.NoError(t, err, "row-order-independent digest")
	require.Equal(t, manifest.SHA256, reordered.SHA256, "row-order-independent digest")

	submission, err := NewExamSubmission(ExamSubmissionSpecification{
		ID: modelSubmissionID("j"), AttemptID: ExamAttemptID(strings.Repeat("k", IDLength)),
		ExamRevisionID: NewExamRevisionID(),
		WorkspaceID:    ExamAttemptWorkspaceID(strings.Repeat("m", IDLength)), Manifest: manifest,
		FinalFocusLossSequence: 9, UnresolvedIntegrityCount: 2, Provenance: ExamSubmissionCandidateSubmitted, SubmittedAt: at,
		BrowserActivity: BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
	})
	require.NoError(t, err)
	require.Equal(t, SubmissionIntegrityGapped, submission.IntegrityState)
	require.Equal(t, int64(2), submission.UnresolvedIntegrityCount)
	require.Equal(t, manifest.SHA256, submission.ManifestDigest)
	require.Equal(t, 3, submission.ManifestEntryCount)
	require.Equal(t, int64(19), submission.ManifestTotalFileBytes)
	require.True(t, submission.SubmittedAt.Equal(at.UTC()))

	manifest.Entries[0].Path = "changed"
	require.NotEmpty(t, submission.ManifestDigest)
	require.Equal(t, int64(41), submission.WorkspaceCursor)
}

func TestExamSubmissionSettledIntegrityRequiresNoUnresolvedGaps(t *testing.T) {
	t.Parallel()

	manifest, err := NewExamSubmissionManifest(0, nil)
	require.NoError(t, err)
	submission, err := NewExamSubmission(ExamSubmissionSpecification{
		ID: NewSubmissionID(), AttemptID: NewExamAttemptID(), ExamRevisionID: NewExamRevisionID(),
		WorkspaceID: NewExamAttemptWorkspaceID(), Manifest: manifest,
		BrowserActivity: BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		Provenance:      ExamSubmissionCandidateSubmitted, SubmittedAt: time.Unix(100, 0),
	})
	require.NoError(t, err)
	require.Equal(t, SubmissionIntegritySettled, submission.IntegrityState)
	require.Equal(t, int64(0), submission.UnresolvedIntegrityCount)
	corrupt := *submission
	corrupt.ManifestTotalFileBytes = 1
	require.Error(t, corrupt.Validate(), "empty Submission header accepted nonzero manifest bytes")
}

func TestExamSubmissionIntegrityRetirementRequiresRedactedState(t *testing.T) {
	t.Parallel()
	manifest, err := NewExamSubmissionManifest(7, nil)
	require.NoError(t, err)
	submission, err := NewExamSubmission(ExamSubmissionSpecification{
		ID: NewSubmissionID(), AttemptID: NewExamAttemptID(), ExamRevisionID: NewExamRevisionID(),
		WorkspaceID: NewExamAttemptWorkspaceID(), Manifest: manifest,
		BrowserActivity: BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		Provenance:      ExamSubmissionCandidateSubmitted, SubmittedAt: time.Unix(100, 0),
	})
	require.NoError(t, err)
	submission.IntegrityState = SubmissionIntegrityRetired
	submission.IntegrityRetiredAt = OptionalTimeFrom(time.Unix(200, 0))
	require.NoError(t, submission.Validate())
	for name, corrupt := range map[string]func(*ExamSubmission){
		"missing retirement time":            func(value *ExamSubmission) { value.IntegrityRetiredAt = OptionalTime{} },
		"time before submission":             func(value *ExamSubmission) { value.IntegrityRetiredAt = OptionalTimeFrom(time.Unix(99, 0)) },
		"old focus sequence":                 func(value *ExamSubmission) { value.FinalFocusLossSequence = 9 },
		"old unresolved count":               func(value *ExamSubmission) { value.UnresolvedIntegrityCount = 3 },
		"invalid retained browser inventory": func(value *ExamSubmission) { value.BrowserActivity.InventoryRevision = 0 },
		"settled with retirement time":       func(value *ExamSubmission) { value.IntegrityState = SubmissionIntegritySettled },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *submission
			corrupt(&candidate)
			require.Error(t, candidate.Validate(), "inconsistent retirement state accepted")
		})
	}
}

func TestExamSubmissionManifestLengthFramesVariableFields(t *testing.T) {
	t.Parallel()

	base := ExamSubmissionManifestEntry{
		EntryID: modelAttemptWorkspaceEntryID("b"), Kind: StarterWorkspaceEntryFile,
		ContentVersion: WorkspaceContentVersion(strings.Repeat("c", IDLength)), SizeBytes: 1,
		SHA256: strings.Repeat("a", 64), StorageOrigin: AttemptWorkspaceStorageAttempt,
		AttemptObjectID: AttemptWorkspaceObjectID(strings.Repeat("d", IDLength)),
	}
	left, right := base, base
	left.Path, left.MediaType = "a", "bc"
	right.Path, right.MediaType = "ab", "c"
	leftManifest, err := NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{left})
	require.NoError(t, err)
	rightManifest, err := NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{right})
	require.NoError(t, err)
	require.NotEqual(t, rightManifest.SHA256, leftManifest.SHA256)
}

func TestExamSubmissionManifestRejectsMutationAndAmbiguousOrigin(t *testing.T) {
	t.Parallel()

	entry := ExamSubmissionManifestEntry{
		EntryID: modelAttemptWorkspaceEntryID("b"), Kind: StarterWorkspaceEntryFile, Path: "answer.txt",
		ContentVersion: WorkspaceContentVersion(strings.Repeat("c", IDLength)), MediaType: "text/plain", SizeBytes: 1,
		SHA256: strings.Repeat("a", 64), StorageOrigin: AttemptWorkspaceStorageAttempt,
		AttemptObjectID: AttemptWorkspaceObjectID(strings.Repeat("d", IDLength)),
	}
	manifest, err := NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{entry})
	require.NoError(t, err)
	manifest.Entries[0].Path = "changed.txt"
	require.Error(t, manifest.Validate(), "post-seal manifest mutation retained a valid digest")
	entry.StarterObjectID = NewStarterWorkspaceObjectID()
	_, err = NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{entry})
	require.Error(t, err, "manifest file accepted both starter and attempt origins")
}

func TestSubmitExamAttemptKeepsLifecycleTimesMonotonic(t *testing.T) {
	t.Parallel()

	startedAt := time.Unix(100, 0).UTC()
	fixture := func(t *testing.T) (*ExamAttempt, *AttemptParticipation, *AttemptConnection) {
		t.Helper()
		attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), startedAt)
		require.NoError(t, err)
		participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 1, HashToken(NewCredentialToken()), startedAt)
		require.NoError(t, err)
		connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), startedAt.Add(time.Second))
		require.NoError(t, err)
		return attempt, participation, connection
	}

	attempt, participation, connection := fixture(t)
	err := SubmitExamAttempt(attempt, participation, connection, startedAt.Add(500*time.Millisecond))
	require.Error(t, err, "seal before the open Connection = %v, %#v", err, attempt)
	require.NotEqual(t, ExamAttemptSubmitted, attempt.State, "seal before the open Connection = %v, %#v", err, attempt)
	require.Equal(t, AttemptParticipationActive, participation.State, "seal before the open Connection = %v, %#v", err, attempt)

	// A renewal committed after the seal time was read must not be rewound.
	attempt, participation, connection = fixture(t)
	renewedAt := startedAt.Add(3 * time.Second)
	participation.UpdatedAt = renewedAt
	sealAt := startedAt.Add(2 * time.Second)
	require.NoError(t, SubmitExamAttempt(attempt, participation, connection, sealAt))
	require.True(t, participation.UpdatedAt.Equal(renewedAt), "submitted times = attempt %#v participation %#v connection %#v", attempt, participation, connection)
	require.True(t, participation.EndedAt.Time.Equal(sealAt), "submitted times = attempt %#v participation %#v connection %#v", attempt, participation, connection)
	require.True(t, attempt.SubmittedAt.Time.Equal(sealAt), "submitted times = attempt %#v participation %#v connection %#v", attempt, participation, connection)
	require.False(t, attempt.UpdatedAt.Before(sealAt), "submitted times = attempt %#v participation %#v connection %#v", attempt, participation, connection)
	require.True(t, connection.ClosedAt.Time.Equal(sealAt), "submitted times = attempt %#v participation %#v connection %#v", attempt, participation, connection)
}

func TestSubmitExamAttemptAppliesCoordinatedTerminalLifecycle(t *testing.T) {
	t.Parallel()

	startedAt := time.Unix(100, 0).UTC()
	submittedAt := startedAt.Add(time.Second)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), startedAt)
	require.NoError(t, err)
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 1, HashToken(NewCredentialToken()), startedAt)
	require.NoError(t, err)
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), startedAt)
	require.NoError(t, err)
	require.NoError(t, SubmitExamAttempt(attempt, participation, connection, submittedAt))
	require.Equal(t, ExamAttemptSubmitted, attempt.State, "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, int64(2), attempt.Revision, "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.True(t, attempt.SubmittedAt.Valid, "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.True(t, attempt.SubmittedAt.Time.Equal(submittedAt), "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptParticipationEnded, participation.State, "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptParticipationEndSubmitted, participation.EndReason, "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptConnectionClosed, connection.State, "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	require.Equal(t, AttemptConnectionCloseSubmitted, connection.CloseReason, "submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)

	suspended, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), startedAt)
	require.NoError(t, err)
	suspendedParticipation, err := NewAttemptParticipation(NewAttemptParticipationID(), suspended.ID, NewSessionID(), 1, HashToken(NewCredentialToken()), startedAt)
	require.NoError(t, err)
	suspendedConnection, err := NewAttemptConnection(NewAttemptConnectionID(), suspended.ID, suspendedParticipation.ID, NewSessionID(), startedAt)
	require.NoError(t, err)
	require.NoError(t, suspended.Suspend(startedAt.Add(time.Millisecond)))
	beforeAttempt, beforeParticipation, beforeConnection := *suspended, *suspendedParticipation, *suspendedConnection
	require.Error(t, SubmitExamAttempt(suspended, suspendedParticipation, suspendedConnection, submittedAt), "suspended Attempt was submitted voluntarily")
	require.Equal(t, beforeAttempt, *suspended, "failed Submission partially changed aggregate = %#v / %#v / %#v", suspended, suspendedParticipation, suspendedConnection)
	require.Equal(t, beforeParticipation, *suspendedParticipation, "failed Submission partially changed aggregate = %#v / %#v / %#v", suspended, suspendedParticipation, suspendedConnection)
	require.Equal(t, beforeConnection, *suspendedConnection, "failed Submission partially changed aggregate = %#v / %#v / %#v", suspended, suspendedParticipation, suspendedConnection)
}

func TestSealExamAttemptForSittingClosePreservesPriorFences(t *testing.T) {
	t.Parallel()

	startedAt := time.Unix(200, 0).UTC()
	sealedAt := startedAt.Add(2 * time.Second)
	newAggregate := func(t *testing.T) (*ExamAttempt, *AttemptParticipation, *AttemptConnection) {
		t.Helper()
		attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), startedAt)
		require.NoError(t, err)
		participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 1, HashToken(NewCredentialToken()), startedAt)
		require.NoError(t, err)
		connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), startedAt)
		require.NoError(t, err)
		return attempt, participation, connection
	}

	active, activeParticipation, activeConnection := newAggregate(t)
	require.NoError(t, SealExamAttemptForSittingClose(active, activeParticipation, activeConnection, sealedAt))
	require.Equal(t, ExamAttemptSubmitted, active.State, "active automatic seal = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.True(t, active.SubmittedAt.Valid, "active automatic seal = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.True(t, active.SubmittedAt.Time.Equal(sealedAt), "active automatic seal = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.Equal(t, AttemptParticipationEnded, activeParticipation.State, "active automatic seal = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.Equal(t, AttemptParticipationEndSittingClosed, activeParticipation.EndReason, "active automatic seal = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.Equal(t, AttemptConnectionClosed, activeConnection.State, "active automatic seal = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.Equal(t, AttemptConnectionCloseSittingClosed, activeConnection.CloseReason, "active automatic seal = %#v / %#v / %#v", active, activeParticipation, activeConnection)

	suspended, endedParticipation, closedConnection := newAggregate(t)
	policyAt := startedAt.Add(time.Second)
	require.NoError(t, suspended.Suspend(policyAt))
	require.NoError(t, endedParticipation.End(AttemptParticipationEndPolicySuspended, policyAt))
	require.NoError(t, closedConnection.Close(AttemptConnectionClosePolicySuspended, policyAt))
	require.NoError(t, SealExamAttemptForSittingClose(suspended, endedParticipation, closedConnection, sealedAt))
	require.Equal(t, ExamAttemptSubmitted, suspended.State, "suspended automatic seal = %#v / %#v / %#v", suspended, endedParticipation, closedConnection)
	require.Equal(t, int64(3), suspended.Revision, "suspended automatic seal = %#v / %#v / %#v", suspended, endedParticipation, closedConnection)
	require.Equal(t, AttemptParticipationEndPolicySuspended, endedParticipation.EndReason, "suspended automatic seal = %#v / %#v / %#v", suspended, endedParticipation, closedConnection)
	require.Equal(t, AttemptConnectionClosePolicySuspended, closedConnection.CloseReason, "suspended automatic seal = %#v / %#v / %#v", suspended, endedParticipation, closedConnection)

	ready, readyParticipation, readyConnection := newAggregate(t)
	readyFenceAt := startedAt.Add(time.Second)
	require.NoError(t, ready.Suspend(readyFenceAt))
	require.NoError(t, readyParticipation.End(AttemptParticipationEndPolicySuspended, readyFenceAt))
	require.NoError(t, readyConnection.Close(AttemptConnectionClosePolicySuspended, readyFenceAt))
	require.NoError(t, ready.Reallow(readyFenceAt.Add(time.Millisecond)))
	require.NoError(t, SealExamAttemptForSittingClose(ready, readyParticipation, readyConnection, sealedAt))
	require.Equal(t, ExamAttemptSubmitted, ready.State, "ready automatic seal = %#v / %#v / %#v", ready, readyParticipation, readyConnection)
	require.Equal(t, int64(4), ready.Revision, "ready automatic seal = %#v / %#v / %#v", ready, readyParticipation, readyConnection)
	require.Equal(t, AttemptParticipationEndPolicySuspended, readyParticipation.EndReason, "ready automatic seal = %#v / %#v / %#v", ready, readyParticipation, readyConnection)
	require.Equal(t, AttemptConnectionClosePolicySuspended, readyConnection.CloseReason, "ready automatic seal = %#v / %#v / %#v", ready, readyParticipation, readyConnection)

	invalid, invalidParticipation, invalidConnection := newAggregate(t)
	invalidConnection.AttemptID = NewExamAttemptID()
	beforeAttempt, beforeParticipation, beforeConnection := *invalid, *invalidParticipation, *invalidConnection
	require.Error(t, SealExamAttemptForSittingClose(invalid, invalidParticipation, invalidConnection, sealedAt), "automatic seal accepted a foreign Connection")
	require.Equal(t, beforeAttempt, *invalid, "failed automatic seal partially mutated aggregate = %#v / %#v / %#v", invalid, invalidParticipation, invalidConnection)
	require.Equal(t, beforeParticipation, *invalidParticipation, "failed automatic seal partially mutated aggregate = %#v / %#v / %#v", invalid, invalidParticipation, invalidConnection)
	require.Equal(t, beforeConnection, *invalidConnection, "failed automatic seal partially mutated aggregate = %#v / %#v / %#v", invalid, invalidParticipation, invalidConnection)
}

func TestEndExamAttemptByManagerSealsUnresolvedStatesWithoutJudgment(t *testing.T) {
	t.Parallel()
	startedAt := time.Unix(300, 0).UTC()
	endedAt := startedAt.Add(2 * time.Second)
	newAggregate := func(t *testing.T) (*ExamAttempt, *AttemptParticipation, *AttemptConnection) {
		t.Helper()
		attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), startedAt)
		require.NoError(t, err)
		participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, NewSessionID(), 1,
			HashToken(NewCredentialToken()), startedAt)
		require.NoError(t, err)
		connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, participation.SessionID, startedAt)
		require.NoError(t, err)
		return attempt, participation, connection
	}

	active, activeParticipation, activeConnection := newAggregate(t)
	require.NoError(t, EndExamAttemptByManager(active, activeParticipation, activeConnection, endedAt))
	require.Equal(t, ExamAttemptSubmitted, active.State, "active manager end = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.Equal(t, AttemptParticipationEndManagerEnded, activeParticipation.EndReason, "active manager end = %#v / %#v / %#v", active, activeParticipation, activeConnection)
	require.Equal(t, AttemptConnectionCloseManagerEnded, activeConnection.CloseReason, "active manager end = %#v / %#v / %#v", active, activeParticipation, activeConnection)

	suspended, suspendedParticipation, suspendedConnection := newAggregate(t)
	fencedAt := startedAt.Add(time.Second)
	require.NoError(t, suspended.Suspend(fencedAt))
	require.NoError(t, suspendedParticipation.End(AttemptParticipationEndPolicySuspended, fencedAt))
	require.NoError(t, suspendedConnection.Close(AttemptConnectionClosePolicySuspended, fencedAt))
	require.NoError(t, EndExamAttemptByManager(suspended, suspendedParticipation, suspendedConnection, endedAt))
	require.Equal(t, ExamAttemptSubmitted, suspended.State, "suspended manager end changed prior fence = %#v / %#v / %#v", suspended, suspendedParticipation, suspendedConnection)
	require.Equal(t, AttemptParticipationEndPolicySuspended, suspendedParticipation.EndReason, "suspended manager end changed prior fence = %#v / %#v / %#v", suspended, suspendedParticipation, suspendedConnection)
	require.Equal(t, AttemptConnectionClosePolicySuspended, suspendedConnection.CloseReason, "suspended manager end changed prior fence = %#v / %#v / %#v", suspended, suspendedParticipation, suspendedConnection)
}

func modelSubmissionID(character string) SubmissionID {
	return SubmissionID(strings.Repeat(character, IDLength))
}

func modelAttemptWorkspaceEntryID(character string) AttemptWorkspaceEntryID {
	return AttemptWorkspaceEntryID(strings.Repeat(character, IDLength))
}
