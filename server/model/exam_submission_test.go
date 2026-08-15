// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNewExamSubmissionSealsCanonicalManifestAndIntegrity(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 15, 12, 30, 0, 0, time.FixedZone("offset", 2*60*60))
	directory := ExamSubmissionManifestEntry{
		EntryID: AttemptWorkspaceEntryID(strings.Repeat("b", IdLength)),
		Kind:    StarterWorkspaceEntryDirectory,
		Path:    "cmd",
	}
	starter := ExamSubmissionManifestEntry{
		EntryID:         AttemptWorkspaceEntryID(strings.Repeat("d", IdLength)),
		Kind:            StarterWorkspaceEntryFile,
		Path:            "cmd/main.go",
		ContentVersion:  WorkspaceContentVersion(strings.Repeat("f", IdLength)),
		MediaType:       "text/x-go",
		SizeBytes:       12,
		SHA256:          strings.Repeat("a", 64),
		StorageOrigin:   AttemptWorkspaceStorageStarter,
		StarterObjectID: StarterWorkspaceObjectID(strings.Repeat("g", IdLength)),
	}
	attempt := ExamSubmissionManifestEntry{
		EntryID:         AttemptWorkspaceEntryID(strings.Repeat("c", IdLength)),
		Kind:            StarterWorkspaceEntryFile,
		Path:            "answer.txt",
		ContentVersion:  WorkspaceContentVersion(strings.Repeat("e", IdLength)),
		MediaType:       "text/plain",
		SizeBytes:       7,
		SHA256:          strings.Repeat("9", 64),
		StorageOrigin:   AttemptWorkspaceStorageAttempt,
		AttemptObjectID: AttemptWorkspaceObjectID(strings.Repeat("h", IdLength)),
	}
	input := []ExamSubmissionManifestEntry{starter, directory, attempt}
	manifest, err := NewExamSubmissionManifest(41, input)
	if err != nil {
		t.Fatal(err)
	}
	if got := []AttemptWorkspaceEntryID{manifest.Entries[0].EntryID, manifest.Entries[1].EntryID, manifest.Entries[2].EntryID}; !slices.Equal(got, []AttemptWorkspaceEntryID{directory.EntryID, attempt.EntryID, starter.EntryID}) {
		t.Fatalf("canonical Entry order = %v", got)
	}
	if manifest.SchemaVersion != ExamSubmissionManifestSchemaVersion || manifest.WorkspaceCursor != 41 ||
		manifest.SHA256 != "d1527073b4fa44425a1309a42e4444ab22180877a59c21e88d4d05057d3f4495" ||
		manifest.EntryCount != 3 || manifest.TotalFileBytes != 19 {
		t.Fatalf("manifest = %#v", manifest)
	}
	input[0].Path = "caller-mutation.go"
	if manifest.Entries[2].Path != "cmd/main.go" {
		t.Fatalf("manifest retained caller slice: %#v", manifest.Entries)
	}
	reordered, err := NewExamSubmissionManifest(41, []ExamSubmissionManifestEntry{attempt, starter, directory})
	if err != nil || reordered.SHA256 != manifest.SHA256 {
		t.Fatalf("row-order-independent digest = %q, %v", reordered.SHA256, err)
	}

	submission, err := NewExamSubmission(ExamSubmissionSpecification{
		ID: modelSubmissionID("j"), AttemptID: ExamAttemptID(strings.Repeat("k", IdLength)),
		WorkspaceID: ExamAttemptWorkspaceID(strings.Repeat("m", IdLength)), Manifest: manifest,
		FinalFocusLossSequence: 9, UnresolvedIntegrityCount: 2, SubmittedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if submission.IntegrityState != SubmissionIntegrityGapped || submission.UnresolvedIntegrityCount != 2 ||
		submission.ManifestDigest != manifest.SHA256 || submission.ManifestEntryCount != 3 ||
		submission.ManifestTotalFileBytes != 19 || !submission.SubmittedAt.Equal(at.UTC()) {
		t.Fatalf("submission = %#v", submission)
	}

	manifest.Entries[0].Path = "changed"
	if submission.ManifestDigest == "" || submission.WorkspaceCursor != 41 {
		t.Fatalf("submission metadata changed through caller manifest: %#v", submission)
	}
}

func TestExamSubmissionSettledIntegrityRequiresNoUnresolvedGaps(t *testing.T) {
	t.Parallel()

	manifest, err := NewExamSubmissionManifest(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := NewExamSubmission(ExamSubmissionSpecification{
		ID: NewSubmissionID(), AttemptID: NewExamAttemptID(), WorkspaceID: NewExamAttemptWorkspaceID(),
		Manifest: manifest, SubmittedAt: time.Unix(100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if submission.IntegrityState != SubmissionIntegritySettled || submission.UnresolvedIntegrityCount != 0 {
		t.Fatalf("settled submission = %#v", submission)
	}
	corrupt := *submission
	corrupt.ManifestTotalFileBytes = 1
	if err := corrupt.Validate(); err == nil {
		t.Fatal("empty Submission header accepted nonzero manifest bytes")
	}
}

func TestExamSubmissionManifestLengthFramesVariableFields(t *testing.T) {
	t.Parallel()

	base := ExamSubmissionManifestEntry{
		EntryID: modelAttemptWorkspaceEntryID("b"), Kind: StarterWorkspaceEntryFile,
		ContentVersion: WorkspaceContentVersion(strings.Repeat("c", IdLength)), SizeBytes: 1,
		SHA256: strings.Repeat("a", 64), StorageOrigin: AttemptWorkspaceStorageAttempt,
		AttemptObjectID: AttemptWorkspaceObjectID(strings.Repeat("d", IdLength)),
	}
	left, right := base, base
	left.Path, left.MediaType = "a", "bc"
	right.Path, right.MediaType = "ab", "c"
	leftManifest, err := NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{left})
	if err != nil {
		t.Fatal(err)
	}
	rightManifest, err := NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{right})
	if err != nil {
		t.Fatal(err)
	}
	if leftManifest.SHA256 == rightManifest.SHA256 {
		t.Fatalf("length framing did not distinguish variable-field boundaries: %q", leftManifest.SHA256)
	}
}

func TestExamSubmissionManifestRejectsMutationAndAmbiguousOrigin(t *testing.T) {
	t.Parallel()

	entry := ExamSubmissionManifestEntry{
		EntryID: modelAttemptWorkspaceEntryID("b"), Kind: StarterWorkspaceEntryFile, Path: "answer.txt",
		ContentVersion: WorkspaceContentVersion(strings.Repeat("c", IdLength)), MediaType: "text/plain", SizeBytes: 1,
		SHA256: strings.Repeat("a", 64), StorageOrigin: AttemptWorkspaceStorageAttempt,
		AttemptObjectID: AttemptWorkspaceObjectID(strings.Repeat("d", IdLength)),
	}
	manifest, err := NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Entries[0].Path = "changed.txt"
	if err := manifest.Validate(); err == nil {
		t.Fatal("post-seal manifest mutation retained a valid digest")
	}
	entry.StarterObjectID = NewStarterWorkspaceObjectID()
	if _, err := NewExamSubmissionManifest(1, []ExamSubmissionManifestEntry{entry}); err == nil {
		t.Fatal("manifest file accepted both starter and attempt origins")
	}
}

func TestSubmitExamAttemptAppliesCoordinatedTerminalLifecycle(t *testing.T) {
	t.Parallel()

	startedAt := time.Unix(100, 0).UTC()
	submittedAt := startedAt.Add(time.Second)
	attempt, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	participation, err := NewAttemptParticipation(NewAttemptParticipationID(), attempt.ID, 1, HashToken(NewCredentialToken()), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewAttemptConnection(NewAttemptConnectionID(), attempt.ID, participation.ID, NewSessionID(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := SubmitExamAttempt(attempt, participation, connection, submittedAt); err != nil {
		t.Fatal(err)
	}
	if attempt.State != ExamAttemptSubmitted || attempt.Revision != 2 || !attempt.SubmittedAt.Valid ||
		!attempt.SubmittedAt.Time.Equal(submittedAt) || participation.State != AttemptParticipationEnded ||
		participation.EndReason != AttemptParticipationEndSubmitted || connection.State != AttemptConnectionClosed ||
		connection.CloseReason != AttemptConnectionCloseSubmitted {
		t.Fatalf("submitted aggregate = %#v / %#v / %#v", attempt, participation, connection)
	}

	suspended, err := NewExamAttempt(NewExamAttemptID(), NewExamID(), NewExamSittingID(), NewUserID(), NewExamRevisionID(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	suspendedParticipation, err := NewAttemptParticipation(NewAttemptParticipationID(), suspended.ID, 1, HashToken(NewCredentialToken()), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	suspendedConnection, err := NewAttemptConnection(NewAttemptConnectionID(), suspended.ID, suspendedParticipation.ID, NewSessionID(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := suspended.Suspend(startedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	beforeAttempt, beforeParticipation, beforeConnection := *suspended, *suspendedParticipation, *suspendedConnection
	if err := SubmitExamAttempt(suspended, suspendedParticipation, suspendedConnection, submittedAt); err == nil {
		t.Fatal("suspended Attempt was submitted voluntarily")
	}
	if *suspended != beforeAttempt || *suspendedParticipation != beforeParticipation || *suspendedConnection != beforeConnection {
		t.Fatalf("failed Submission partially changed aggregate = %#v / %#v / %#v", suspended, suspendedParticipation, suspendedConnection)
	}
}

func modelSubmissionID(character string) SubmissionID {
	return SubmissionID(strings.Repeat(character, IdLength))
}

func modelAttemptWorkspaceEntryID(character string) AttemptWorkspaceEntryID {
	return AttemptWorkspaceEntryID(strings.Repeat(character, IdLength))
}
