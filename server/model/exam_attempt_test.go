// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewExamAttemptCreatesActiveStableWorkIdentity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.FixedZone("source", 2*60*60))
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	require.NoError(t, err)
	require.Equal(t, ExamAttemptActive, attempt.State)
	require.Equal(t, int64(1), attempt.Revision)
	require.Same(t, time.UTC, attempt.CreatedAt.Location())
	require.Equal(t, attempt.UpdatedAt, attempt.CreatedAt)
	require.False(t, attempt.SubmittedAt.Valid)
	require.True(t, attempt.State.AllowsCandidateConnection())
	require.False(t, attempt.State.IsTerminal())
}

func TestExamAttemptValidationClosesLifecycleStates(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	require.NoError(t, err)

	suspended := *attempt
	suspended.State = ExamAttemptSuspended
	suspended.UpdatedAt = at.Add(time.Minute)
	suspended.Revision++
	err = suspended.Validate()
	require.NoError(t, err)
	require.False(t, suspended.State.AllowsCandidateConnection())
	require.False(t, suspended.State.IsTerminal())
	ready := suspended
	ready.State = ExamAttemptReady
	ready.UpdatedAt = at.Add(90 * time.Second)
	ready.Revision++
	err = ready.Validate()
	require.NoError(t, err, "valid Ready Attempt = %#v", ready)
	require.False(t, ready.State.AllowsCandidateConnection(), "valid Ready Attempt = %#v", ready)
	require.True(t, ready.State.IsUnresolved(), "valid Ready Attempt = %#v", ready)

	submitted := ready
	submitted.State = ExamAttemptSubmitted
	submitted.SubmittedAt = OptionalTimeFrom(at.Add(2 * time.Minute))
	submitted.UpdatedAt = submitted.SubmittedAt.Time
	submitted.Revision++
	err = submitted.Validate()
	require.NoError(t, err, "valid Submitted Attempt = %#v", submitted)
	require.True(t, submitted.State.IsTerminal(), "valid Submitted Attempt = %#v", submitted)

	invalid := submitted
	invalid.SubmittedAt = OptionalTime{}
	err = invalid.Validate()
	require.Error(t, err, "Submitted Attempt without submitted_at was accepted")
	invalid = *attempt
	invalid.State = ExamAttemptState("paused")
	err = invalid.Validate()
	require.Error(t, err, "unknown Attempt state was accepted")
}

func TestNewAttemptParticipationStoresOnlyHashAndUsesExactInitialLease(t *testing.T) {
	t.Parallel()
	require.Equal(t, 5*time.Second, AttemptParticipationRenewalInterval, "Participation timing contract: renewal")
	require.Equal(t, 20*time.Second, AttemptParticipationInitialLease, "Participation timing contract: renewal")
	require.Less(t, AttemptParticipationRenewalInterval, AttemptParticipationInitialLease, "Participation timing contract: renewal")
	startedAt := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.FixedZone("source", -3*60*60))
	rawCredential := NewCredentialToken()
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), NewExamAttemptID(), NewSessionID(), 1, HashToken(rawCredential), startedAt)
	require.NoError(t, err)
	require.Equal(t, AttemptParticipationActive, participation.State)
	require.Equal(t, int64(1), participation.Generation)
	require.Equal(t, int64(0), participation.RenewalSequence)
	require.Same(t, time.UTC, participation.StartedAt.Location())
	require.Equal(t, participation.StartedAt, participation.UpdatedAt)
	require.Equal(t, participation.StartedAt.Add(AttemptParticipationInitialLease), participation.LeaseExpiresAt)
	require.False(t, participation.EndedAt.Valid)
	require.Empty(t, participation.EndReason)
	require.NotEqual(t, rawCredential, participation.ContinuityCredentialHash)
	require.Equal(t, HashToken(rawCredential), participation.ContinuityCredentialHash)
	require.False(t, participation.IsExpiredAt(participation.LeaseExpiresAt.Add(-time.Nanosecond)), "Participation expiry is not exclusive at the authoritative deadline")
	require.True(t, participation.IsExpiredAt(participation.LeaseExpiresAt), "Participation expiry is not exclusive at the authoritative deadline")
}

func TestAttemptParticipationRejectsRawOrNonCanonicalCredentialMaterial(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	for _, credential := range []string{NewCredentialToken(), strings.Repeat("A", TokenHashLength)} {
		_, err := NewAttemptParticipation(NewAttemptParticipationID(), NewExamAttemptID(), NewSessionID(), 1, credential, at)
		require.Error(t, err, "credential material %q was accepted", credential)
	}
}

func TestAttemptParticipationEndIsPermanentAndAtomic(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), NewExamAttemptID(), NewSessionID(), 1, HashToken(NewCredentialToken()), at)
	require.NoError(t, err)
	endedAt := at.Add(AttemptParticipationInitialLease)
	err = participation.End(AttemptParticipationEndLeaseExpired, endedAt)
	require.NoError(t, err)
	require.Equal(t, AttemptParticipationEnded, participation.State)
	require.True(t, participation.EndedAt.Valid)
	require.Equal(t, endedAt, participation.EndedAt.Time)
	require.Equal(t, AttemptParticipationEndLeaseExpired, participation.EndReason)
	require.Equal(t, endedAt, participation.UpdatedAt)
	before := *participation
	err = participation.End(AttemptParticipationEndInterrupted, endedAt.Add(time.Second))
	require.Error(t, err, "second End() error=%v Participation=%#v", err, participation)
	require.Equal(t, before, *participation, "second End() error=%v Participation=%#v", err, participation)
}

func TestAttemptParticipationRenewalIsGenerationFencedAndMonotonic(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), NewExamAttemptID(), NewSessionID(), 3,
		HashToken(NewCredentialToken()), startedAt)
	require.NoError(t, err)
	databaseNow := startedAt.Add(5 * time.Second)
	duplicate, err := participation.Renew(3, 1, databaseNow)
	require.NoError(t, err, "first renewal duplicate")
	require.False(t, duplicate, "first renewal duplicate")
	require.Equal(t, int64(1), participation.RenewalSequence)
	require.Equal(t, databaseNow, participation.UpdatedAt)
	require.Equal(t, databaseNow.Add(AttemptParticipationInitialLease), participation.LeaseExpiresAt)
	before := *participation
	duplicate, err = participation.Renew(3, 1, databaseNow.Add(time.Second))
	require.NoError(t, err, "duplicate renewal duplicate=%t error=%v Participation=%#v", duplicate, err, participation)
	require.True(t, duplicate, "duplicate renewal duplicate=%t error=%v Participation=%#v", duplicate, err, participation)
	require.Equal(t, before, *participation, "duplicate renewal duplicate=%t error=%v Participation=%#v", duplicate, err, participation)
	for _, request := range []struct {
		generation int64
		sequence   int64
		at         time.Time
	}{
		{generation: 2, sequence: 2, at: databaseNow.Add(time.Second)},
		{generation: 3, sequence: 0, at: databaseNow.Add(time.Second)},
		{generation: 3, sequence: 1, at: participation.LeaseExpiresAt},
		{generation: 3, sequence: 2, at: participation.LeaseExpiresAt},
	} {
		t.Run(fmt.Sprintf("generation=%v", request.generation), func(t *testing.T) {
			_, renewErr := participation.Renew(request.generation, request.sequence, request.at)
			require.Error(t, renewErr, "invalid renewal %#v error=%v Participation=%#v", request, renewErr, participation)
			require.Equal(t, before, *participation, "invalid renewal %#v error=%v Participation=%#v", request, renewErr, participation)
		})
	}
}

func TestAttemptConnectionOpensAndClosesOnce(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), NewExamAttemptID(), NewAttemptParticipationID(), NewSessionID(), at)
	require.NoError(t, err)
	require.Equal(t, AttemptConnectionOpen, connection.State)
	require.Equal(t, at, connection.OpenedAt)
	require.False(t, connection.ClosedAt.Valid)
	require.Empty(t, connection.CloseReason)
	closedAt := at.Add(time.Second)
	err = connection.Close(AttemptConnectionCloseTransport, closedAt)
	require.NoError(t, err)
	require.Equal(t, AttemptConnectionClosed, connection.State)
	require.True(t, connection.ClosedAt.Valid)
	require.Equal(t, closedAt, connection.ClosedAt.Time)
	require.Equal(t, AttemptConnectionCloseTransport, connection.CloseReason)
	before := *connection
	err = connection.Close(AttemptConnectionCloseInterrupted, closedAt.Add(time.Second))
	require.Error(t, err, "second Close() error=%v Connection=%#v", err, connection)
	require.Equal(t, before, *connection, "second Close() error=%v Connection=%#v", err, connection)
}

func TestAttemptConnectionCloseReasonValidity(t *testing.T) {
	t.Parallel()
	for _, reason := range []AttemptConnectionCloseReason{
		AttemptConnectionCloseTransport, AttemptConnectionCloseInterrupted, AttemptConnectionCloseLeaseExpired,
		AttemptConnectionCloseKicked, AttemptConnectionCloseSubmitted, AttemptConnectionCloseManagerEnded,
		AttemptConnectionCloseSittingClosed,
	} {
		require.True(t, reason.IsValid(), "known close reason %q is invalid", reason)
	}
	for _, reason := range []AttemptConnectionCloseReason{"", "unknown", "TRANSPORT_CLOSED"} {
		require.False(t, reason.IsValid(), "unknown close reason %q is valid", reason)
	}
}

func TestConnectionLossIntegrityEpisodeIsNeutralAndGenerationScoped(t *testing.T) {
	t.Parallel()
	attemptID := NewExamAttemptID()
	participationID := NewAttemptParticipationID()
	flagID := NewIntegrityFlagID()
	leaseExpiredAt := time.Date(2026, time.August, 17, 9, 0, 20, 0, time.UTC)
	recordedAt := leaseExpiredAt.Add(2 * time.Second)

	flag, err := NewIntegrityFlag(flagID, attemptID, 4, IntegrityPolicyConnectionLoss, recordedAt)
	require.NoError(t, err)
	evidence, err := NewConnectionLossEvidence(NewIntegrityEvidenceID(), attemptID, participationID, flag.ID, 4, leaseExpiredAt, recordedAt)
	require.NoError(t, err)
	suspension, err := NewPolicyAttemptSuspension(NewAttemptSuspensionID(), attemptID, participationID, flag.ID, 4,
		AttemptSuspensionCandidateReasonSecureContinuityLost, recordedAt)
	require.NoError(t, err)
	require.Equal(t, IntegrityFlagOpen, flag.State, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, IntegrityPolicyConnectionLoss, evidence.Kind, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, leaseExpiredAt, evidence.ObservedAt, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, recordedAt, evidence.RecordedAt, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, AttemptSuspensionActive, suspension.State, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, AttemptSuspensionSourcePolicy, suspension.Source, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, AttemptSuspensionCandidateReasonSecureContinuityLost, suspension.CandidateReason, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Empty(t, suspension.PrivateReason, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, flag.ID, suspension.FlagID, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
	require.Equal(t, int64(4), suspension.Generation, "connection-loss episode = %#v / %#v / %#v", flag, evidence, suspension)
}

func TestAttemptSuspensionReallowBoundsPrivateReasonAndClosesOnlyOnce(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	suspension, err := NewPolicyAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(),
		NewIntegrityFlagID(), 2, AttemptSuspensionCandidateReasonSecureContinuityLost, startedAt)
	require.NoError(t, err)
	actorID := NewUserID()
	err = suspension.Reallow(actorID, "manager verified continuity", startedAt.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, AttemptSuspensionClosed, suspension.State)
	require.Equal(t, "manager verified continuity", suspension.PrivateReason)
	require.Equal(t, actorID, suspension.ReallowedByUserID)
	require.True(t, suspension.EndedAt.Valid)
	before := *suspension
	err = suspension.Reallow(actorID, "again", startedAt.Add(2*time.Minute))
	require.Error(t, err, "second re-allow error=%v Suspension=%#v", err, suspension)
	require.Equal(t, before, *suspension, "second re-allow error=%v Suspension=%#v", err, suspension)
	for _, reason := range []string{"", " not trimmed ", "\xff", strings.Repeat("é", AttemptSuspensionPrivateReasonMaximumRunes+1)} {
		candidate, createErr := NewPolicyAttemptSuspension(NewAttemptSuspensionID(), NewExamAttemptID(), NewAttemptParticipationID(),
			NewIntegrityFlagID(), 2, AttemptSuspensionCandidateReasonSecureContinuityLost, startedAt)
		require.NoError(t, createErr)
		require.Error(t, candidate.Reallow(actorID, reason, startedAt.Add(time.Minute)), "private reason %q was accepted", reason)
	}
}

func TestExamAttemptSuspendAndReallowAreRevisionFencedStateChanges(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	require.NoError(t, err)
	err = attempt.Suspend(at.Add(time.Second))
	require.NoError(t, err, "Suspend() Attempt=%#v error", attempt)
	require.Equal(t, ExamAttemptSuspended, attempt.State, "Suspend() Attempt=%#v error", attempt)
	require.Equal(t, int64(2), attempt.Revision, "Suspend() Attempt=%#v error", attempt)
	err = attempt.Reallow(at.Add(2 * time.Second))
	require.NoError(t, err, "Reallow() Attempt=%#v error", attempt)
	require.Equal(t, ExamAttemptReady, attempt.State, "Reallow() Attempt=%#v error", attempt)
	require.Equal(t, int64(3), attempt.Revision, "Reallow() Attempt=%#v error", attempt)
	err = attempt.Activate(at.Add(3 * time.Second))
	require.NoError(t, err, "Activate() Attempt=%#v error", attempt)
	require.Equal(t, ExamAttemptActive, attempt.State, "Activate() Attempt=%#v error", attempt)
	require.Equal(t, int64(4), attempt.Revision, "Activate() Attempt=%#v error", attempt)
}

func TestAttemptWorkspaceBootstrapUsesAttemptOwnedMetadataOverPinnedStarterBytes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	attemptID := NewExamAttemptID()
	workspace, err := NewExamAttemptWorkspace(NewExamAttemptWorkspaceID(), attemptID, at)
	require.NoError(t, err)
	require.Equal(t, attemptID, workspace.AttemptID)
	require.Equal(t, int64(0), workspace.Cursor)
	require.Equal(t, workspace.UpdatedAt, workspace.CreatedAt)
	object, err := NewStarterOriginAttemptWorkspaceObject(
		NewAttemptWorkspaceObjectID(), workspace.ID, NewStarterWorkspaceObjectID(), NewWorkspaceContentVersion(),
		"text/x-go", 12, strings.Repeat("a", 64), at,
	)
	require.NoError(t, err)
	require.Equal(t, AttemptWorkspaceStorageStarter, object.StorageOrigin)
	require.True(t, object.StarterObjectID.IsValid())
	require.Equal(t, workspace.ID, object.WorkspaceID)

	revisionID, sourceEntryID := NewExamRevisionID(), NewStarterWorkspaceEntryID()
	file, err := NewAttemptWorkspaceFile(NewAttemptWorkspaceEntryID(), workspace.ID, revisionID, sourceEntryID, "cmd/main.go", object.ID, at)
	require.NoError(t, err)
	directory, err := NewAttemptWorkspaceDirectory(NewAttemptWorkspaceEntryID(), workspace.ID, revisionID, NewStarterWorkspaceEntryID(), "cmd", at)
	require.NoError(t, err)
	require.Equal(t, object.ID, file.CurrentObjectID, "bootstrap file=%#v directory=%#v", file, directory)
	require.Equal(t, StarterWorkspaceEntryFile, file.Kind, "bootstrap file=%#v directory=%#v", file, directory)
	require.Equal(t, StarterWorkspaceEntryDirectory, directory.Kind, "bootstrap file=%#v directory=%#v", file, directory)
	require.True(t, directory.CurrentObjectID.IsZero(), "bootstrap file=%#v directory=%#v", file, directory)

	badObject := *object
	badObject.StorageOrigin = AttemptWorkspaceStorageAttempt
	err = badObject.Validate()
	require.Error(t, err, "Attempt-origin object carrying a Starter object identity was accepted")
	badFile := *file
	badFile.Path = "../escape"
	err = badFile.Validate()
	require.Error(t, err, "invalid Attempt Workspace path was accepted")
	badObject = *object
	badObject.SHA256 = strings.ToUpper(badObject.SHA256)
	err = badObject.Validate()
	require.Error(t, err, "non-canonical uppercase checksum was accepted")

	attemptOriginEntry := *file
	attemptOriginEntry.SourceStarterEntryID = ""
	err = attemptOriginEntry.Validate()
	require.NoError(t, err)
}
