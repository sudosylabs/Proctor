// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"strings"
	"testing"
	"time"
)

func TestNewExamAttemptCreatesActiveStableWorkIdentity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.FixedZone("source", 2*60*60))
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.State != ExamAttemptActive || attempt.Revision != 1 ||
		attempt.CreatedAt.Location() != time.UTC || attempt.CreatedAt != attempt.UpdatedAt || attempt.SubmittedAt.Valid {
		t.Fatalf("new Attempt = %#v", attempt)
	}
	if !attempt.State.AllowsCandidateConnection() || attempt.State.IsTerminal() {
		t.Fatalf("new Attempt state capabilities = %#v", attempt.State)
	}
}

func TestExamAttemptValidationClosesLifecycleStates(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}

	suspended := *attempt
	suspended.State = ExamAttemptSuspended
	suspended.UpdatedAt = at.Add(time.Minute)
	suspended.Revision++
	if err = suspended.Validate(); err != nil {
		t.Fatalf("valid Suspended Attempt: %v", err)
	}
	if suspended.State.AllowsCandidateConnection() || suspended.State.IsTerminal() {
		t.Fatalf("Suspended capabilities = %#v", suspended.State)
	}

	submitted := suspended
	submitted.State = ExamAttemptSubmitted
	submitted.SubmittedAt = OptionalTimeFrom(at.Add(2 * time.Minute))
	submitted.UpdatedAt = submitted.SubmittedAt.Time
	submitted.Revision++
	if err = submitted.Validate(); err != nil || !submitted.State.IsTerminal() {
		t.Fatalf("valid Submitted Attempt = %#v, %v", submitted, err)
	}

	invalid := submitted
	invalid.SubmittedAt = OptionalTime{}
	if err = invalid.Validate(); err == nil {
		t.Fatal("Submitted Attempt without submitted_at was accepted")
	}
	invalid = *attempt
	invalid.State = ExamAttemptState("paused")
	if err = invalid.Validate(); err == nil {
		t.Fatal("unknown Attempt state was accepted")
	}
}

func TestNewAttemptParticipationStoresOnlyHashAndUsesExactInitialLease(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.FixedZone("source", -3*60*60))
	rawCredential := NewCredentialToken()
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), NewExamAttemptID(), 1, HashToken(rawCredential), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if participation.State != AttemptParticipationActive || participation.Generation != 1 || participation.RenewalSequence != 0 ||
		participation.StartedAt.Location() != time.UTC || participation.UpdatedAt != participation.StartedAt ||
		participation.LeaseExpiresAt != participation.StartedAt.Add(AttemptParticipationInitialLease) || participation.EndedAt.Valid || participation.EndReason != "" {
		t.Fatalf("new Participation = %#v", participation)
	}
	if participation.ContinuityCredentialHash == rawCredential || participation.ContinuityCredentialHash != HashToken(rawCredential) {
		t.Fatalf("persisted credential = %q", participation.ContinuityCredentialHash)
	}
	if participation.IsExpiredAt(participation.LeaseExpiresAt.Add(-time.Nanosecond)) ||
		!participation.IsExpiredAt(participation.LeaseExpiresAt) {
		t.Fatal("Participation expiry is not exclusive at the authoritative deadline")
	}
}

func TestAttemptParticipationRejectsRawOrNonCanonicalCredentialMaterial(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	for _, credential := range []string{NewCredentialToken(), strings.Repeat("A", TokenHashLength)} {
		if _, err := NewAttemptParticipation(NewAttemptParticipationID(), NewExamAttemptID(), 1, credential, at); err == nil {
			t.Fatalf("credential material %q was accepted", credential)
		}
	}
}

func TestAttemptParticipationEndIsPermanentAndAtomic(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), NewExamAttemptID(), 1, HashToken(NewCredentialToken()), at)
	if err != nil {
		t.Fatal(err)
	}
	endedAt := at.Add(AttemptParticipationInitialLease)
	if err = participation.End(AttemptParticipationEndLeaseExpired, endedAt); err != nil {
		t.Fatal(err)
	}
	if participation.State != AttemptParticipationEnded || !participation.EndedAt.Valid ||
		participation.EndedAt.Time != endedAt || participation.EndReason != AttemptParticipationEndLeaseExpired ||
		participation.UpdatedAt != endedAt {
		t.Fatalf("ended Participation = %#v", participation)
	}
	before := *participation
	if err = participation.End(AttemptParticipationEndInterrupted, endedAt.Add(time.Second)); err == nil || *participation != before {
		t.Fatalf("second End() error=%v Participation=%#v", err, participation)
	}
}

func TestAttemptConnectionOpensAndClosesOnce(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), NewExamAttemptID(), NewAttemptParticipationID(), NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	if connection.State != AttemptConnectionOpen || connection.OpenedAt != at || connection.ClosedAt.Valid || connection.CloseReason != "" {
		t.Fatalf("new Connection = %#v", connection)
	}
	closedAt := at.Add(time.Second)
	if err = connection.Close(AttemptConnectionCloseTransport, closedAt); err != nil {
		t.Fatal(err)
	}
	if connection.State != AttemptConnectionClosed || !connection.ClosedAt.Valid || connection.ClosedAt.Time != closedAt ||
		connection.CloseReason != AttemptConnectionCloseTransport {
		t.Fatalf("closed Connection = %#v", connection)
	}
	before := *connection
	if err = connection.Close(AttemptConnectionCloseInterrupted, closedAt.Add(time.Second)); err == nil || *connection != before {
		t.Fatalf("second Close() error=%v Connection=%#v", err, connection)
	}
}

func TestAttemptConnectionCloseReasonValidity(t *testing.T) {
	t.Parallel()
	for _, reason := range []AttemptConnectionCloseReason{
		AttemptConnectionCloseTransport, AttemptConnectionCloseInterrupted, AttemptConnectionCloseLeaseExpired,
		AttemptConnectionCloseKicked, AttemptConnectionCloseSubmitted, AttemptConnectionCloseSittingClosed,
	} {
		if !reason.IsValid() {
			t.Fatalf("known close reason %q is invalid", reason)
		}
	}
	for _, reason := range []AttemptConnectionCloseReason{"", "unknown", "TRANSPORT_CLOSED"} {
		if reason.IsValid() {
			t.Fatalf("unknown close reason %q is valid", reason)
		}
	}
}

func TestAttemptWorkspaceBootstrapUsesAttemptOwnedMetadataOverPinnedStarterBytes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	attemptID := NewExamAttemptID()
	workspace, err := NewExamAttemptWorkspace(NewExamAttemptWorkspaceID(), attemptID, at)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.AttemptID != attemptID || workspace.Cursor != 0 || workspace.CreatedAt != workspace.UpdatedAt {
		t.Fatalf("new Workspace = %#v", workspace)
	}
	object, err := NewStarterOriginAttemptWorkspaceObject(
		NewAttemptWorkspaceObjectID(), workspace.ID, NewStarterWorkspaceObjectID(), NewWorkspaceContentVersion(),
		"text/x-go", 12, strings.Repeat("a", 64), at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if object.StorageOrigin != AttemptWorkspaceStorageStarter || !object.StarterObjectID.IsValid() || object.WorkspaceID != workspace.ID {
		t.Fatalf("starter-origin object = %#v", object)
	}

	revisionID, sourceEntryID := NewExamRevisionID(), NewStarterWorkspaceEntryID()
	file, err := NewAttemptWorkspaceFile(NewAttemptWorkspaceEntryID(), workspace.ID, revisionID, sourceEntryID, "cmd/main.go", object.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := NewAttemptWorkspaceDirectory(NewAttemptWorkspaceEntryID(), workspace.ID, revisionID, NewStarterWorkspaceEntryID(), "cmd", at)
	if err != nil {
		t.Fatal(err)
	}
	if file.CurrentObjectID != object.ID || file.Kind != StarterWorkspaceEntryFile || directory.Kind != StarterWorkspaceEntryDirectory || !directory.CurrentObjectID.IsZero() {
		t.Fatalf("bootstrap file=%#v directory=%#v", file, directory)
	}

	badObject := *object
	badObject.StorageOrigin = AttemptWorkspaceStorageAttempt
	if err = badObject.Validate(); err == nil {
		t.Fatal("Attempt-origin object carrying a Starter object identity was accepted")
	}
	badFile := *file
	badFile.Path = "../escape"
	if err = badFile.Validate(); err == nil {
		t.Fatal("invalid Attempt Workspace path was accepted")
	}
	badObject = *object
	badObject.SHA256 = strings.ToUpper(badObject.SHA256)
	if err = badObject.Validate(); err == nil {
		t.Fatal("non-canonical uppercase checksum was accepted")
	}

	attemptOriginEntry := *file
	attemptOriginEntry.SourceStarterEntryID = ""
	if err = attemptOriginEntry.Validate(); err != nil {
		t.Fatalf("future attempt-origin entry without starter source: %v", err)
	}
}
