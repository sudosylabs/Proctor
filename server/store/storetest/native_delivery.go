// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type NativeDeliveryAppendProbe func(model.AttemptParticipationID, int64, int64)

func TestNativeDeliveryAppendStore(t *testing.T, ss store.Store, probes ...NativeDeliveryAppendProbe) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "native-intake-connect", "native-intake-connect"))
	require.NoError(t, err)
	access := store.NativeDeliveryAccess{Access: store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, ConnectionID: connected.Connection.ID, CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, ContinuityCredentialHash: connect.ContinuityCredentialHash}, StreamID: connected.Security.DeliveryStreamID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation}
	recovery, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, store.SecurityPreflightAccess{CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision}, connected.Attempt.ID)
	require.NoError(t, err)
	source := model.NativeSourceCoverage{}
	for _, candidate := range recovery.CurrentSources {
		if candidate.SourceID == model.NativeSourceCapture {
			source = candidate
		}
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	opened := model.NativeOccurrence{Kind: "occurrence", OccurrenceID: model.NewID(), ConditionID: "baseline.capture", DetectorID: "synthetic-baseline", DetectorVersion: 1, CapabilityID: "baseline", Mode: model.NativeClaimEnforce, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []model.NativeSourceRange{{SourceID: source.SourceID, SourceInstanceID: source.SourceInstanceID, FirstSequence: 0, LastSequence: 1}}}
	batch := func(seq int64, record model.NativeOccurrence) model.NativeSecurityBatch {
		return model.NativeSecurityBatch{StreamID: access.StreamID, BatchSequence: seq, ParticipationID: access.ParticipationID, Generation: access.Generation, SecuritySessionID: connected.Security.SecuritySessionID, PolicyDigest: connected.Security.Policy.Digest, ApplicationReleaseID: connected.Security.Policy.ApplicationReleaseID, MatrixID: connected.Security.Policy.MatrixID, Records: []model.NativeRecord{{Occurrence: &record}}}
	}
	appendBatch := func(b model.NativeSecurityBatch, key string) (*model.NativeSecurityAcknowledgement, error) {
		t.Helper()
		raw, err := b.Canonical()
		require.NoError(t, err)
		return ss.ExamAttempt().AppendNativeDelivery(ctx, &store.NativeDeliveryAppend{Access: access, Batch: b, DesktopBuild: connect.DesktopBuild, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.NativeDeliveryAppendOperation, key, model.SHA256Fingerprint(raw)))
	}
	repeated := opened
	repeated.Status = "repeated"
	repeated.RepeatCount = 2
	two := batch(2, repeated)
	pending, err := appendBatch(two, "native-two")
	require.NoError(t, err)
	require.Equal(t, int64(0), pending.HighestContiguousBatchSequence)
	require.Equal(t, int64(0), pending.SettledThroughBatchSequence)
	require.Len(t, pending.MissingBatchRanges, 1)
	require.Equal(t, model.SequenceRange{First: 1, Last: 1}, pending.MissingBatchRanges[0])
	first, err := appendBatch(batch(1, opened), "native-one")
	require.NoError(t, err)
	require.Equal(t, int64(2), first.HighestContiguousBatchSequence)
	require.Equal(t, int64(2), first.SettledThroughBatchSequence)
	replay, err := appendBatch(two, "native-two-repacked-key")
	require.NoError(t, err)
	require.Equal(t, pending.Receipt, replay.Receipt, "replay changed receipt or returned stale progress")
	require.Equal(t, int64(2), replay.HighestContiguousBatchSequence, "replay changed receipt or returned stale progress")
	changed := two
	changed.PriorAcknowledgement = 2
	_, err = appendBatch(changed, "native-two-conflict")
	require.Error(t, err, "changed original prior acknowledgement accepted as exact replay")
	recovered := repeated
	recovered.Status = "recovered"
	third, err := appendBatch(batch(3, recovered), "native-three")
	require.NoError(t, err)
	require.Equal(t, int64(3), third.HighestContiguousBatchSequence, "recovery was not retained")
	_, err = appendBatch(batch(4, recovered), "native-double-recovery")
	require.Error(t, err, "duplicate lifecycle recovery accepted")
	lost := repeated
	lost.OccurrenceID = model.NewID()
	fifth, err := appendBatch(batch(5, lost), "native-five")
	require.NoError(t, err)
	require.Equal(t, int64(3), fifth.HighestContiguousBatchSequence, "invented missing opener")
	require.Equal(t, int64(3), fifth.SettledThroughBatchSequence, "invented missing opener")
	gap := &store.NativeDeliveryGapDeclaration{Access: access, Declaration: model.DeclareDeliveryGaps{DeclarationID: model.NewID(), AllocatedThroughSequence: 5, Ranges: []model.SequenceRange{{First: 4, Last: 4}}, Reason: "spool_lost"}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	settled, err := ss.ExamAttempt().DeclareNativeDeliveryGaps(ctx, gap, examCommand(fixture.candidate.ID, store.NativeDeliveryGapsOperation, "native-gap", "native-gap"))
	require.NoError(t, err)
	require.Equal(t, int64(5), settled.SettledThroughSequence, "terminal gap did not release pending transition")
	status, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, access)
	require.NoError(t, err)
	require.Equal(t, int64(3), status.HighestContiguousBatchSequence, "gap fabricated actual receipt")
	require.Equal(t, int64(5), status.SettledThroughBatchSequence, "gap fabricated actual receipt")
	receipt, err := ss.ExamAttempt().NativeDeliveryReceipt(ctx, access, 5)
	require.NoError(t, err)
	require.Equal(t, fifth.Receipt, *receipt, "exact receipt behind terminal gap lost")
	for _, probe := range probes {
		probe(access.ParticipationID, 4, 2)
	}
	foreign := access
	foreign.Access.ConnectionID = ""
	foreign.Access.ContinuityCredentialHash = ""
	_, err = ss.ExamAttempt().NativeDeliveryStatus(ctx, foreign)
	require.Error(t, err, "live stream allowed historical header omission")
	board, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{ExamID: fixture.sitting.ExamID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID, Limit: 100})
	require.NoError(t, err)
	found := false
	for _, candidate := range board.Items {
		if candidate.Candidate.UserID == fixture.candidate.ID {
			found = true
			require.NotNil(t, candidate.NativeSecurity, "manager board lacks bounded native coverage summary")
			require.NoError(t, candidate.NativeSecurity.Validate(), "manager board lacks bounded native coverage summary")
			require.Equal(t, int64(4), candidate.NativeSecurity.RetainedConditionRecords, "manager board lacks bounded native coverage summary")
			require.True(t, candidate.NativeSecurity.LiveCoverageAvailable, "manager board lacks bounded native coverage summary")
			require.NotEmpty(t, candidate.NativeSecurity.Sources, "manager board lacks bounded native coverage summary")
		}
	}
	require.True(t, found, "candidate absent from board")
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, ContinuityCredentialHash: access.Access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "native-review-submit", "native-review-submit"))
	require.NoError(t, err)
	sub := sealed.Receipt.SubmissionID
	page, err := ss.ExamIntegrityReview().ListNativeConditions(ctx, store.NativeConditionListOptions{SubmissionID: sub, Limit: 100})
	require.NoError(t, err)
	require.Len(t, page.Items, 4)
	require.False(t, page.HasMore)
	for _, v := range page.Items {
		require.NoError(t, v.Validate())
		require.Equal(t, connected.Attempt.ID, v.AttemptID, "native review lost provenance or uncertainty")
		require.Equal(t, connected.Security.Policy.Digest, v.PolicyDigest, "native review lost provenance or uncertainty")
		require.Equal(t, access.StreamID, v.StreamID, "native review lost provenance or uncertainty")
		require.Equal(t, v.BatchSequence == 5, v.UnresolvedOpener, "native review lost provenance or uncertainty")
	}
	flags, err := ss.ExamIntegrityReview().ListFlags(ctx, store.ExamIntegrityFlagListOptions{SubmissionID: sub, Limit: 200})
	require.NoError(t, err)
	require.Empty(t, flags.Items, "native occurrence created a misconduct Flag")
	access.Access.ConnectionID = ""
	access.Access.ContinuityCredentialHash = ""
	_, err = ss.ExamAttempt().SealNativeDelivery(ctx, &store.NativeDeliveryFinalDeclaration{Access: access, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "native-review-final", FinalSequence: 6, ExpectedDeclarationRevision: 1}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.NativeDeliveryFinalOperation, "native-review-final", "native-review-final"))
	require.NoError(t, err)
	audit := func() model.AuditEventID {
		return saveIntegrityReviewAudit(t, ctx, ss, fixture, sub, model.ActionSubmissionReview).ID
	}
	draft, err := ss.ExamIntegrityReview().UpdateDraft(ctx, &store.ExamIntegrityReviewDraftMutation{SubmissionID: sub, ReviewID: model.NewSubmissionReviewID(), ActorUserID: fixture.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: audit(), AuditAt: model.GetMillis()}, examCommand(fixture.manager.ID, store.ExamIntegrityReviewDraftOperation, "native-review-draft", "native-review-draft"))
	require.NoError(t, err)
	final, err := ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: sub, ReviewID: draft.Review.ID, ExpectedReviewRevision: draft.Review.Revision, ActorUserID: fixture.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: audit(), AuditAt: model.GetMillis()}, examCommand(fixture.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "native-review-finalize", "native-review-finalize"))
	require.NoError(t, err)
	before, err := ss.ExamIntegrityReview().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, int64(4), before.NativeConditions.Records, "native finalization inventory is absent")
	require.NoError(t, before.NativeConditions.Validate(), "native finalization inventory is absent")
	late := lost
	late.Status = "recovered"
	time.Sleep(time.Second) // Allow the real shared append allowance to refill.
	_, err = appendBatch(batch(6, late), "native-review-late")
	require.NoError(t, err)
	after, err := ss.ExamIntegrityReview().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, model.SubmissionReviewDraft, after.Review.State, "late native evidence inherited finalized review")
	require.Greater(t, after.Review.Revision, final.Review.Revision, "late native evidence inherited finalized review")
	require.Equal(t, int64(5), after.NativeConditions.Records, "late native evidence inherited finalized review")
	require.NotEqual(t, before.NativeConditions.Digest, after.NativeConditions.Digest, "late native evidence inherited finalized review")
	_, err = appendBatch(batch(6, late), "native-review-late-replay")
	require.NoError(t, err)
	replayedReview, err := ss.ExamIntegrityReview().Get(ctx, sub)
	require.NoError(t, err)
	require.Equal(t, after.Review.Revision, replayedReview.Review.Revision, "native replay duplicated review evidence")
	require.Equal(t, after.NativeConditions, replayedReview.NativeConditions, "native replay duplicated review evidence")

}

// TestNativeMaximumRecordStore covers legal wire values that exceed former
// private per-record caps while remaining within aggregate delivery admission.
func TestNativeMaximumRecordStore(t *testing.T, ss store.Store) {
	ctx := t.Context()
	fixture := newExamAttemptFixture(t, ctx, ss)
	input := &store.ExamAttemptConnect{CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, input)
	input.SittingID = fixture.sitting.ID
	schema := model.SHA256Fingerprint([]byte("maximum-source-schema"))
	definitions := []model.NativeCoverageDefinition{}
	matrix := model.NativeCapabilityMatrix{RegistryDigest: model.NativeRegistryDigest, MatrixID: input.DesktopBuild.CapabilityMatrixIdentity, ReleaseID: "synthetic-release", TargetTuple: input.DesktopBuild.DesktopTarget}
	for _, source := range model.NativeSources() {
		key := "baseline." + string(source)
		definitions = append(definitions, model.NativeCoverageDefinition{CoverageKey: key, CapabilityID: "baseline", SourceID: source, SourceSchemaDigest: schema, PermittedClaims: []model.NativeCapabilityClaim{model.NativeClaimEnforce}, RequiredPermissions: []string{}, Baseline: true})
		matrix.Entries = append(matrix.Entries, model.NativeCapabilityMatrixEntry{CoverageKey: key, SourceSchemaDigest: schema, Claim: model.NativeClaimEnforce, ComponentID: "synthetic-component", AdapterVersion: "synthetic-adapter", RequiredPermissions: []string{}, Limitations: []string{}, Verification: "passed"})
	}
	slices.SortFunc(definitions, func(a, b model.NativeCoverageDefinition) int { return strings.Compare(a.CoverageKey, b.CoverageKey) })
	slices.SortFunc(matrix.Entries, func(a, b model.NativeCapabilityMatrixEntry) int { return strings.Compare(a.CoverageKey, b.CoverageKey) })
	agreement, err := model.NewDesktopNativeAgreement(model.NativeRegistryDigest, model.SHA256Fingerprint([]byte("maximum-manifest")), model.SHA256Fingerprint([]byte("maximum-matrix")), model.SHA256Fingerprint([]byte("maximum-detectors")), definitions, matrix, []model.NativeDetectorDefinition{{DetectorID: "synthetic-baseline", Version: 1, CapabilityID: "baseline", ConditionIDs: []string{"baseline.capture"}, SourceIDs: model.NativeSources(), AllowedModes: []model.NativeCapabilityClaim{model.NativeClaimEnforce}}})
	require.NoError(t, err)
	input.DesktopBuild.NativeAgreement = agreement
	access := store.SecurityPreflightAccess{SittingID: input.SittingID, CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, DesktopBuild: input.DesktopBuild, DesktopCompatibilityPolicyRevision: input.DesktopCompatibilityPolicyRevision}
	prepare := &store.SecurityPreflightPrepare{Access: access, PreflightID: model.NewID(), Challenge: model.NewCredentialToken(), NativeRegistryDigest: agreement.RegistryDigest(), SourceManifestDigest: agreement.SourceManifestDigest(), ConfigurationManifestFingerprint: input.ConfigurationManifestFingerprint, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	prepared, err := ss.ExamAttempt().PrepareSecurityPreflight(ctx, prepare, examCommand(input.CandidateUserID, store.SecurityPreflightPrepareOperation, "maximum-prepare", "maximum-prepare"))
	require.NoError(t, err)
	report := syntheticPreflightReport(prepared, agreement)
	for i := range report.Sources {
		report.Sources[i].SourceInstanceID = strings.Repeat("i", 128)
	}
	accepted, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, &store.SecurityPreflightReport{Access: access, PreflightID: prepare.PreflightID, Report: report, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}, examCommand(input.CandidateUserID, store.SecurityPreflightReportOperation, "maximum-report", "maximum-report"))
	require.NoError(t, err)
	input.Security = model.ConnectSecurity{Kind: "preflight", PreflightID: prepare.PreflightID, ReportDigest: accepted.ReportDigest}
	connected, err := ss.ExamAttempt().Connect(ctx, input, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "maximum-connect", "maximum-connect"))
	require.NoError(t, err)
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	occurrence := model.NativeOccurrence{Kind: "occurrence", OccurrenceID: strings.Repeat("o", 128), ConditionID: "baseline.capture", DetectorID: "synthetic-baseline", DetectorVersion: 1, CapabilityID: "baseline", Mode: model.NativeClaimEnforce, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []model.NativeSourceRange{}}
	for _, source := range report.Sources {
		occurrence.SourceRanges = append(occurrence.SourceRanges, model.NativeSourceRange{SourceID: source.SourceID, SourceInstanceID: source.SourceInstanceID, FirstSequence: 0, LastSequence: 1})
	}
	raw, err := json.Marshal(occurrence)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	fields["first_observed_at"], err = json.Marshal("2026-09-09T12:00:00." + strings.Repeat("0", 240000) + "Z")
	require.NoError(t, err)
	raw, err = json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &occurrence))
	require.Greater(t, len(raw), 8192, "maximum fixture missed record bounds")
	require.Len(t, occurrence.SourceRanges, 11, "maximum fixture missed record bounds")
	batch := model.NativeSecurityBatch{StreamID: connected.Security.DeliveryStreamID, BatchSequence: 1, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SecuritySessionID: connected.Security.SecuritySessionID, PolicyDigest: connected.Security.Policy.Digest, ApplicationReleaseID: connected.Security.Policy.ApplicationReleaseID, MatrixID: connected.Security.Policy.MatrixID, Records: []model.NativeRecord{{Occurrence: &occurrence}}}
	canonical, err := batch.Canonical()
	require.NoError(t, err)
	fields["first_observed_at"], err = json.Marshal("2026-09-09T12:00:00." + strings.Repeat("0", 240000+256*1024-len(canonical)) + "Z")
	require.NoError(t, err)
	raw, err = json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &occurrence))
	canonical, err = batch.Canonical()
	require.NoError(t, err)
	require.Len(t, canonical, 256*1024, "occurrence batch did not reach exact request limit")
	deliveryAccess := store.NativeDeliveryAccess{Access: store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID, ContinuityCredentialHash: input.ContinuityCredentialHash}, StreamID: batch.StreamID, ParticipationID: batch.ParticipationID, Generation: batch.Generation}
	appendBatch := func(key string) (*model.NativeSecurityAcknowledgement, error) {
		return ss.ExamAttempt().AppendNativeDelivery(ctx, &store.NativeDeliveryAppend{Access: deliveryAccess, Batch: batch, DesktopBuild: input.DesktopBuild, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}, examCommand(input.CandidateUserID, store.NativeDeliveryAppendOperation, key, model.SHA256Fingerprint(canonical)))
	}
	result, err := appendBatch("maximum-append")
	require.NoError(t, err)
	replay, err := appendBatch("maximum-replay")
	require.NoError(t, err)
	require.Equal(t, int64(1), result.HighestContiguousBatchSequence, "large legal native record failed exact acknowledgement/replay")
	require.Equal(t, replay.Receipt, result.Receipt, "large legal native record failed exact acknowledgement/replay")
	require.Equal(t, model.SHA256Fingerprint(canonical), result.Receipt.RequestDigest, "large legal native record failed exact acknowledgement/replay")
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, ContinuityCredentialHash: deliveryAccess.Access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, ctx, ss, fixture.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "maximum-seal", "maximum-seal"))
	require.NoError(t, err)
	page, err := ss.ExamIntegrityReview().ListNativeConditions(ctx, store.NativeConditionListOptions{SubmissionID: sealed.Receipt.SubmissionID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "large occurrence was not interpreted and copied")
	copied, err := json.Marshal(page.Items[0].Occurrence)
	require.NoError(t, err)
	original, err := json.Marshal(occurrence)
	require.NoError(t, err)
	require.Equal(t, string(original), string(copied), "evidence copy changed timestamp spelling")
	reset := nativeResetWithLongTimestamp(t, model.NativeSourceReset{Kind: "source_reset", ResetID: model.NewID(), SourceID: report.Sources[0].SourceID, PreviousSourceInstanceID: report.Sources[0].SourceInstanceID, PreviousFinalSequence: 1, NewSourceInstanceID: model.NewID(), Reason: "restart", OccurredAt: at}, 240000)
	deliveryAccess.Access.ConnectionID = ""
	deliveryAccess.Access.ContinuityCredentialHash = ""
	_, err = ss.ExamAttempt().SealNativeDelivery(ctx, &store.NativeDeliveryFinalDeclaration{Access: deliveryAccess, Declaration: model.FinalDeliveryDeclaration{DeclarationID: model.NewID(), FinalSequence: 2, ExpectedDeclarationRevision: 0}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.NativeDeliveryFinalOperation, "maximum-final", "maximum-final"))
	require.NoError(t, err)
	batch.BatchSequence = 2
	batch.Records = []model.NativeRecord{{Reset: &reset}}
	canonical, err = batch.Canonical()
	require.NoError(t, err)
	reset = nativeResetWithLongTimestamp(t, reset, 240000+256*1024-len(canonical))
	canonical, err = batch.Canonical()
	require.NoError(t, err)
	require.Len(t, canonical, 256*1024, "reset batch did not reach exact request limit")
	resetAck, err := appendBatch("maximum-historical-reset")
	require.NoError(t, err)
	resetReplay, err := appendBatch("maximum-historical-reset-replay")
	require.NoError(t, err)
	require.Equal(t, resetReplay.Receipt, resetAck.Receipt, "large historical reset lost exact receipt")
	require.Equal(t, model.SHA256Fingerprint(canonical), resetAck.Receipt.RequestDigest, "large historical reset lost exact receipt")

}

// A lexical zero tail changes canonical bytes while retaining the same instant.
func nativeResetWithLongTimestamp(t *testing.T, reset model.NativeSourceReset, zeros int) model.NativeSourceReset {
	t.Helper()
	raw, err := json.Marshal(reset)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	fields["occurred_at"], err = json.Marshal(reset.OccurredAt.UTC().Format("2006-01-02T15:04:05.000") + strings.Repeat("0", zeros) + "Z")
	require.NoError(t, err)
	raw, err = json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &reset))
	return reset
}
