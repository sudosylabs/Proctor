// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	ExamSubmissionManifestSchemaVersion = 1
	ExamSubmissionManifestReadMaximum   = 200
)

const examSubmissionManifestDigestDomain = "proctor.exam_submission_manifest"

type SubmissionIntegrityState string

const (
	SubmissionIntegritySettled SubmissionIntegrityState = "settled"
	SubmissionIntegrityGapped  SubmissionIntegrityState = "gapped"
)

func (state SubmissionIntegrityState) IsValid() bool {
	return state == SubmissionIntegritySettled || state == SubmissionIntegrityGapped
}

// ExamSubmissionManifestEntry pins one acknowledged logical Workspace entry.
// File content remains in its original opaque VFS object; directories have no
// content selector. Path is retained as immutable protected Submission data,
// never as an object key, authorization selector, cursor, or audit field.
type ExamSubmissionManifestEntry struct {
	EntryID         AttemptWorkspaceEntryID
	Kind            StarterWorkspaceEntryKind
	Path            string
	ContentVersion  WorkspaceContentVersion
	MediaType       string
	SizeBytes       int64
	SHA256          string
	StorageOrigin   AttemptWorkspaceObjectStorage
	StarterObjectID StarterWorkspaceObjectID
	AttemptObjectID AttemptWorkspaceObjectID
}

func (entry ExamSubmissionManifestEntry) Validate() error {
	if !entry.EntryID.IsValid() {
		return errors.New("model: invalid Submission manifest Entry identity")
	}
	path, err := NormalizeAttemptWorkspacePath(entry.Path)
	if err != nil || path != entry.Path {
		return errors.New("model: invalid Submission manifest path")
	}
	switch entry.Kind {
	case StarterWorkspaceEntryDirectory:
		if !entry.ContentVersion.IsZero() || entry.MediaType != "" || entry.SizeBytes != 0 || entry.SHA256 != "" ||
			entry.StorageOrigin != "" || !entry.StarterObjectID.IsZero() || !entry.AttemptObjectID.IsZero() {
			return errors.New("model: Submission manifest directory cannot carry content")
		}
	case StarterWorkspaceEntryFile:
		content := AttemptWorkspaceContent{MediaType: entry.MediaType, SizeBytes: entry.SizeBytes, SHA256: entry.SHA256}
		if !entry.ContentVersion.IsValid() || content.Validate() != nil {
			return errors.New("model: invalid Submission manifest file content")
		}
		switch entry.StorageOrigin {
		case AttemptWorkspaceStorageStarter:
			if !entry.StarterObjectID.IsValid() || !entry.AttemptObjectID.IsZero() {
				return errors.New("model: invalid starter-origin Submission manifest file")
			}
		case AttemptWorkspaceStorageAttempt:
			if !entry.AttemptObjectID.IsValid() || !entry.StarterObjectID.IsZero() {
				return errors.New("model: invalid attempt-origin Submission manifest file")
			}
		default:
			return errors.New("model: invalid Submission manifest storage origin")
		}
	default:
		return errors.New("model: invalid Submission manifest Entry kind")
	}
	return nil
}

// ExamSubmissionManifest is the canonical, immutable description of an exact
// acknowledged Workspace state. Entries are byte-sorted by stable Entry ID.
// SHA256 covers a domain-separated, versioned, length-framed binary encoding;
// it is independent of JSON, SQL row order, and database rendering.
type ExamSubmissionManifest struct {
	SchemaVersion   int
	WorkspaceCursor int64
	Entries         []ExamSubmissionManifestEntry
	EntryCount      int
	TotalFileBytes  int64
	SHA256          string
}

func NewExamSubmissionManifest(workspaceCursor int64, entries []ExamSubmissionManifestEntry) (ExamSubmissionManifest, error) {
	manifest := ExamSubmissionManifest{SchemaVersion: ExamSubmissionManifestSchemaVersion,
		WorkspaceCursor: workspaceCursor, Entries: append([]ExamSubmissionManifestEntry(nil), entries...)}
	slices.SortFunc(manifest.Entries, func(left, right ExamSubmissionManifestEntry) int {
		return strings.Compare(left.EntryID.String(), right.EntryID.String())
	})
	manifest.EntryCount = len(manifest.Entries)
	for _, entry := range manifest.Entries {
		if entry.Kind == StarterWorkspaceEntryFile {
			if entry.SizeBytes > math.MaxInt64-manifest.TotalFileBytes {
				return ExamSubmissionManifest{}, errors.New("model: Submission manifest size overflows")
			}
			manifest.TotalFileBytes += entry.SizeBytes
		}
	}
	manifest.SHA256 = computeExamSubmissionManifestDigest(manifest)
	if err := manifest.Validate(); err != nil {
		return ExamSubmissionManifest{}, err
	}
	return manifest, nil
}

func (manifest ExamSubmissionManifest) Validate() error {
	if manifest.SchemaVersion != ExamSubmissionManifestSchemaVersion || manifest.WorkspaceCursor < 0 ||
		manifest.EntryCount != len(manifest.Entries) || manifest.EntryCount > AttemptWorkspaceMaximumEntries ||
		manifest.TotalFileBytes < 0 || manifest.TotalFileBytes > AttemptWorkspaceMaximumTotalBytes ||
		!validLowerSHA256(manifest.SHA256) {
		return errors.New("model: invalid Submission manifest metadata")
	}
	var total int64
	paths := make(map[string]struct{}, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if index > 0 && strings.Compare(manifest.Entries[index-1].EntryID.String(), entry.EntryID.String()) >= 0 {
			return errors.New("model: Submission manifest Entries are not in canonical order")
		}
		if _, exists := paths[entry.Path]; exists {
			return errors.New("model: Submission manifest contains duplicate path")
		}
		paths[entry.Path] = struct{}{}
		if entry.Kind == StarterWorkspaceEntryFile {
			if entry.SizeBytes > math.MaxInt64-total {
				return errors.New("model: Submission manifest size overflows")
			}
			total += entry.SizeBytes
		}
	}
	if total != manifest.TotalFileBytes || computeExamSubmissionManifestDigest(manifest) != manifest.SHA256 {
		return errors.New("model: invalid Submission manifest digest")
	}
	return nil
}

type ExamSubmissionSpecification struct {
	ID                       SubmissionID
	AttemptID                ExamAttemptID
	WorkspaceID              ExamAttemptWorkspaceID
	Manifest                 ExamSubmissionManifest
	FinalFocusLossSequence   int64
	UnresolvedIntegrityCount int64
	SubmittedAt              time.Time
}

// ExamSubmission is the immutable aggregate header for the single seal of an
// Attempt. Manifest entries remain separately pageable; the header retains the
// exact canonical manifest summary and terminal integrity collection state.
type ExamSubmission struct {
	ID                       SubmissionID
	AttemptID                ExamAttemptID
	WorkspaceID              ExamAttemptWorkspaceID
	ManifestSchemaVersion    int
	WorkspaceCursor          int64
	ManifestDigest           string
	ManifestEntryCount       int
	ManifestTotalFileBytes   int64
	FinalFocusLossSequence   int64
	IntegrityState           SubmissionIntegrityState
	UnresolvedIntegrityCount int64
	SubmittedAt              time.Time
}

func NewExamSubmission(spec ExamSubmissionSpecification) (*ExamSubmission, error) {
	if err := spec.Manifest.Validate(); err != nil {
		return nil, err
	}
	state := SubmissionIntegritySettled
	if spec.UnresolvedIntegrityCount > 0 {
		state = SubmissionIntegrityGapped
	}
	submission := &ExamSubmission{
		ID: spec.ID, AttemptID: spec.AttemptID, WorkspaceID: spec.WorkspaceID,
		ManifestSchemaVersion: spec.Manifest.SchemaVersion, WorkspaceCursor: spec.Manifest.WorkspaceCursor,
		ManifestDigest: spec.Manifest.SHA256, ManifestEntryCount: spec.Manifest.EntryCount,
		ManifestTotalFileBytes: spec.Manifest.TotalFileBytes, FinalFocusLossSequence: spec.FinalFocusLossSequence,
		IntegrityState: state, UnresolvedIntegrityCount: spec.UnresolvedIntegrityCount, SubmittedAt: TimeUTC(spec.SubmittedAt),
	}
	if err := submission.Validate(); err != nil {
		return nil, err
	}
	return submission, nil
}

func (submission *ExamSubmission) Validate() error {
	if submission == nil || !submission.ID.IsValid() || !submission.AttemptID.IsValid() || !submission.WorkspaceID.IsValid() ||
		submission.ManifestSchemaVersion != ExamSubmissionManifestSchemaVersion || submission.WorkspaceCursor < 0 ||
		!validLowerSHA256(submission.ManifestDigest) || submission.ManifestEntryCount < 0 ||
		submission.ManifestEntryCount > AttemptWorkspaceMaximumEntries || submission.ManifestTotalFileBytes < 0 ||
		submission.ManifestTotalFileBytes > AttemptWorkspaceMaximumTotalBytes || submission.FinalFocusLossSequence < 0 ||
		submission.UnresolvedIntegrityCount < 0 || submission.SubmittedAt.IsZero() || !submission.IntegrityState.IsValid() {
		return errors.New("model: invalid Exam Submission")
	}
	if submission.ManifestEntryCount == 0 && submission.ManifestTotalFileBytes != 0 {
		return errors.New("model: empty Exam Submission manifest has content bytes")
	}
	if (submission.IntegrityState == SubmissionIntegritySettled) != (submission.UnresolvedIntegrityCount == 0) {
		return errors.New("model: inconsistent Exam Submission integrity state")
	}
	return nil
}

// SubmitExamAttempt applies the coordinated voluntary terminal lifecycle to a
// validated active Attempt, its exact active Participation, and its owning open
// Connection. It mutates none of them unless every ownership and transition
// check succeeds. Persistence commits this transition with the immutable
// Submission, manifest, integrity terminal, audit, and command outcome.
func SubmitExamAttempt(attempt *ExamAttempt, participation *AttemptParticipation, connection *AttemptConnection, at time.Time) error {
	if attempt == nil || participation == nil || connection == nil || attempt.Validate() != nil ||
		participation.Validate() != nil || connection.Validate() != nil || participation.AttemptID != attempt.ID ||
		connection.AttemptID != attempt.ID || connection.ParticipationID != participation.ID ||
		attempt.State != ExamAttemptActive || participation.State != AttemptParticipationActive || connection.State != AttemptConnectionOpen {
		return errors.New("model: invalid Exam Submission lifecycle aggregate")
	}
	attemptCandidate, participationCandidate, connectionCandidate := *attempt, *participation, *connection
	at = TimeUTC(at)
	attemptCandidate.State = ExamAttemptSubmitted
	attemptCandidate.SubmittedAt = OptionalTimeFrom(at)
	attemptCandidate.UpdatedAt = at
	attemptCandidate.Revision++
	if err := attemptCandidate.Validate(); err != nil {
		return err
	}
	if err := participationCandidate.End(AttemptParticipationEndSubmitted, at); err != nil {
		return err
	}
	if err := connectionCandidate.Close(AttemptConnectionCloseSubmitted, at); err != nil {
		return err
	}
	*attempt, *participation, *connection = attemptCandidate, participationCandidate, connectionCandidate
	return nil
}

// SealExamAttemptForSittingClose applies the coordinated terminal lifecycle
// when bounded Sitting finalization seals an unfinished Attempt without a
// cooperative candidate. Existing Participation and Connection fences retain
// their original causes; only still-active/open records receive the
// sitting_closed cause. No input is mutated unless the complete aggregate is
// valid and every required transition succeeds.
func SealExamAttemptForSittingClose(attempt *ExamAttempt, participation *AttemptParticipation,
	connection *AttemptConnection, at time.Time,
) error {
	if attempt == nil || participation == nil || connection == nil || attempt.Validate() != nil ||
		participation.Validate() != nil || connection.Validate() != nil || participation.AttemptID != attempt.ID ||
		connection.AttemptID != attempt.ID || connection.ParticipationID != participation.ID ||
		(attempt.State != ExamAttemptActive && attempt.State != ExamAttemptSuspended) ||
		(participation.State == AttemptParticipationEnded && connection.State != AttemptConnectionClosed) {
		return errors.New("model: invalid automatic Exam Submission lifecycle aggregate")
	}
	at = TimeUTC(at)
	if at.IsZero() || at.Before(attempt.UpdatedAt) || at.Before(participation.UpdatedAt) || at.Before(connection.OpenedAt) {
		return errors.New("model: invalid automatic Exam Submission time")
	}
	attemptCandidate, participationCandidate, connectionCandidate := *attempt, *participation, *connection
	attemptCandidate.State = ExamAttemptSubmitted
	attemptCandidate.SubmittedAt = OptionalTimeFrom(at)
	attemptCandidate.UpdatedAt = at
	attemptCandidate.Revision++
	if err := attemptCandidate.Validate(); err != nil {
		return err
	}
	if participationCandidate.State == AttemptParticipationActive {
		if err := participationCandidate.End(AttemptParticipationEndSittingClosed, at); err != nil {
			return err
		}
	}
	if connectionCandidate.State == AttemptConnectionOpen {
		if err := connectionCandidate.Close(AttemptConnectionCloseSittingClosed, at); err != nil {
			return err
		}
	}
	*attempt, *participation, *connection = attemptCandidate, participationCandidate, connectionCandidate
	return nil
}

func computeExamSubmissionManifestDigest(manifest ExamSubmissionManifest) string {
	digest := sha256.New()
	writeSubmissionDigestFrame(digest, []byte(examSubmissionManifestDigestDomain))
	writeSubmissionDigestUint64(digest, uint64(manifest.SchemaVersion))
	writeSubmissionDigestUint64(digest, uint64(manifest.WorkspaceCursor))
	writeSubmissionDigestUint64(digest, uint64(len(manifest.Entries)))
	for _, entry := range manifest.Entries {
		writeSubmissionDigestFrame(digest, []byte(entry.EntryID))
		writeSubmissionDigestFrame(digest, []byte(entry.Kind))
		writeSubmissionDigestFrame(digest, []byte(entry.Path))
		writeSubmissionDigestFrame(digest, []byte(entry.ContentVersion))
		writeSubmissionDigestFrame(digest, []byte(entry.MediaType))
		writeSubmissionDigestUint64(digest, uint64(entry.SizeBytes))
		writeSubmissionDigestFrame(digest, []byte(entry.SHA256))
		writeSubmissionDigestFrame(digest, []byte(entry.StorageOrigin))
		writeSubmissionDigestFrame(digest, []byte(entry.StarterObjectID))
		writeSubmissionDigestFrame(digest, []byte(entry.AttemptObjectID))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeSubmissionDigestFrame(target hash.Hash, value []byte) {
	writeSubmissionDigestUint64(target, uint64(len(value)))
	_, _ = target.Write(value)
}

func writeSubmissionDigestUint64(target hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = target.Write(encoded[:])
}
