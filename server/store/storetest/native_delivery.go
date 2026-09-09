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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type NativeDeliveryAppendProbe func(model.AttemptParticipationID, int64, int64)

func TestNativeDeliveryAppendStore(t *testing.T, ss store.Store, probes ...NativeDeliveryAppendProbe) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "native-intake-connect", "native-intake-connect"))
	requireNoError(t, err)
	access := store.NativeDeliveryAccess{Access: store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, ConnectionID: connected.Connection.ID, CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, ContinuityCredentialHash: connect.ContinuityCredentialHash}, StreamID: connected.Security.DeliveryStreamID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation}
	recovery, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, store.SecurityPreflightAccess{CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision}, connected.Attempt.ID)
	requireNoError(t, err)
	source := model.NativeSourceCoverage{}
	for _, candidate := range recovery.CurrentSources {
		if candidate.SourceID == model.NativeSourceCapture {
			source = candidate
		}
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	opened := model.NativeOccurrence{Kind: "occurrence", OccurrenceID: model.NewId(), ConditionID: "baseline.capture", DetectorID: "synthetic-baseline", DetectorVersion: 1, CapabilityID: "baseline", Mode: model.NativeClaimEnforce, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []model.NativeSourceRange{{SourceID: source.SourceID, SourceInstanceID: source.SourceInstanceID, FirstSequence: 0, LastSequence: 1}}}
	batch := func(seq int64, record model.NativeOccurrence) model.NativeSecurityBatch {
		return model.NativeSecurityBatch{StreamID: access.StreamID, BatchSequence: seq, ParticipationID: access.ParticipationID, Generation: access.Generation, SecuritySessionID: connected.Security.SecuritySessionID, PolicyDigest: connected.Security.Policy.Digest, ApplicationReleaseID: connected.Security.Policy.ApplicationReleaseID, MatrixID: connected.Security.Policy.MatrixID, Records: []model.NativeRecord{{Occurrence: &record}}}
	}
	appendBatch := func(b model.NativeSecurityBatch, key string) (*model.NativeSecurityAcknowledgement, error) {
		t.Helper()
		raw, err := b.Canonical()
		requireNoError(t, err)
		return ss.ExamAttempt().AppendNativeDelivery(ctx, &store.NativeDeliveryAppend{Access: access, Batch: b, DesktopBuild: connect.DesktopBuild, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.NativeDeliveryAppendOperation, key, model.SHA256Fingerprint(raw)))
	}
	repeated := opened
	repeated.Status = "repeated"
	repeated.RepeatCount = 2
	two := batch(2, repeated)
	pending, err := appendBatch(two, "native-two")
	requireNoError(t, err)
	if pending.HighestContiguousBatchSequence != 0 || pending.SettledThroughBatchSequence != 0 || len(pending.MissingBatchRanges) != 1 || pending.MissingBatchRanges[0] != (model.SequenceRange{First: 1, Last: 1}) {
		t.Fatalf("pending incorrectly interpreted: %#v", pending)
	}
	first, err := appendBatch(batch(1, opened), "native-one")
	requireNoError(t, err)
	if first.HighestContiguousBatchSequence != 2 || first.SettledThroughBatchSequence != 2 {
		t.Fatalf("repair did not advance: %#v", first)
	}
	replay, err := appendBatch(two, "native-two-repacked-key")
	requireNoError(t, err)
	if replay.Receipt != pending.Receipt || replay.HighestContiguousBatchSequence != 2 {
		t.Fatal("replay changed receipt or returned stale progress")
	}
	changed := two
	changed.PriorAcknowledgement = 2
	if _, err := appendBatch(changed, "native-two-conflict"); err == nil {
		t.Fatal("changed original prior acknowledgement accepted as exact replay")
	}
	recovered := repeated
	recovered.Status = "recovered"
	third, err := appendBatch(batch(3, recovered), "native-three")
	requireNoError(t, err)
	if third.HighestContiguousBatchSequence != 3 {
		t.Fatal("recovery was not retained")
	}
	if _, err := appendBatch(batch(4, recovered), "native-double-recovery"); err == nil {
		t.Fatal("duplicate lifecycle recovery accepted")
	}
	lost := repeated
	lost.OccurrenceID = model.NewId()
	fifth, err := appendBatch(batch(5, lost), "native-five")
	requireNoError(t, err)
	if fifth.HighestContiguousBatchSequence != 3 || fifth.SettledThroughBatchSequence != 3 {
		t.Fatal("invented missing opener")
	}
	gap := &store.NativeDeliveryGapDeclaration{Access: access, Declaration: model.DeclareDeliveryGaps{DeclarationID: model.NewId(), AllocatedThroughSequence: 5, Ranges: []model.SequenceRange{{First: 4, Last: 4}}, Reason: "spool_lost"}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	settled, err := ss.ExamAttempt().DeclareNativeDeliveryGaps(ctx, gap, examCommand(fixture.candidate.ID, store.NativeDeliveryGapsOperation, "native-gap", "native-gap"))
	requireNoError(t, err)
	if settled.SettledThroughSequence != 5 {
		t.Fatal("terminal gap did not release pending transition")
	}
	status, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, access)
	requireNoError(t, err)
	if status.HighestContiguousBatchSequence != 3 || status.SettledThroughBatchSequence != 5 {
		t.Fatal("gap fabricated actual receipt")
	}
	receipt, err := ss.ExamAttempt().NativeDeliveryReceipt(ctx, access, 5)
	requireNoError(t, err)
	if *receipt != fifth.Receipt {
		t.Fatal("exact receipt behind terminal gap lost")
	}
	for _, probe := range probes {
		probe(access.ParticipationID, 4, 2)
	}
	foreign := access
	foreign.Access.ConnectionID = ""
	foreign.Access.ContinuityCredentialHash = ""
	if _, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, foreign); err == nil {
		t.Fatal("live stream allowed historical header omission")
	}
	board, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{ExamID: fixture.sitting.ExamID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID, Limit: 100})
	requireNoError(t, err)
	found := false
	for _, candidate := range board.Items {
		if candidate.Candidate.UserID == fixture.candidate.ID {
			found = true
			if candidate.NativeSecurity == nil || candidate.NativeSecurity.Validate() != nil || candidate.NativeSecurity.RetainedConditionRecords != 4 || !candidate.NativeSecurity.LiveCoverageAvailable || len(candidate.NativeSecurity.Sources) == 0 {
				t.Fatal("manager board lacks bounded native coverage summary")
			}
		}
	}
	if !found {
		t.Fatal("candidate absent from board")
	}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, ContinuityCredentialHash: access.Access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "native-review-submit", "native-review-submit"))
	requireNoError(t, err)
	sub := sealed.Receipt.SubmissionID
	page, err := ss.ExamIntegrityReview().ListNativeConditions(ctx, store.NativeConditionListOptions{SubmissionID: sub, Limit: 100})
	requireNoError(t, err)
	if len(page.Items) != 4 || page.HasMore {
		t.Fatalf("native review records=%#v", page)
	}
	for _, v := range page.Items {
		requireNoError(t, v.Validate())
		if v.AttemptID != connected.Attempt.ID || v.PolicyDigest != connected.Security.Policy.Digest || v.StreamID != access.StreamID || v.UnresolvedOpener != (v.BatchSequence == 5) {
			t.Fatal("native review lost provenance or uncertainty")
		}
	}
	flags, err := ss.ExamIntegrityReview().ListFlags(ctx, store.ExamIntegrityFlagListOptions{SubmissionID: sub, Limit: 200})
	requireNoError(t, err)
	if len(flags.Items) != 0 {
		t.Fatal("native occurrence created a misconduct Flag")
	}
	access.Access.ConnectionID = ""
	access.Access.ContinuityCredentialHash = ""
	_, err = ss.ExamAttempt().SealNativeDelivery(ctx, &store.NativeDeliveryFinalDeclaration{Access: access, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "native-review-final", FinalSequence: 6, ExpectedDeclarationRevision: 1}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.NativeDeliveryFinalOperation, "native-review-final", "native-review-final"))
	requireNoError(t, err)
	audit := func() string {
		return saveIntegrityReviewAudit(t, ctx, ss, fixture, sub, model.ActionSubmissionReview).ID.String()
	}
	draft, err := ss.ExamIntegrityReview().UpdateDraft(ctx, &store.ExamIntegrityReviewDraftMutation{SubmissionID: sub, ReviewID: model.NewSubmissionReviewID(), ActorUserID: fixture.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: audit(), AuditAt: model.GetMillis()}, examCommand(fixture.manager.ID, store.ExamIntegrityReviewDraftOperation, "native-review-draft", "native-review-draft"))
	requireNoError(t, err)
	final, err := ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: sub, ReviewID: draft.Review.ID, ExpectedReviewRevision: draft.Review.Revision, ActorUserID: fixture.manager.ID, ChangedAt: model.NowUTC(), AuditEventID: audit(), AuditAt: model.GetMillis()}, examCommand(fixture.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "native-review-finalize", "native-review-finalize"))
	requireNoError(t, err)
	before, err := ss.ExamIntegrityReview().Get(ctx, sub)
	requireNoError(t, err)
	if before.NativeConditions.Records != 4 || before.NativeConditions.Validate() != nil {
		t.Fatal("native finalization inventory is absent")
	}
	late := lost
	late.Status = "recovered"
	time.Sleep(time.Second) // Allow the real shared append allowance to refill.
	_, err = appendBatch(batch(6, late), "native-review-late")
	requireNoError(t, err)
	after, err := ss.ExamIntegrityReview().Get(ctx, sub)
	requireNoError(t, err)
	if after.Review.State != model.SubmissionReviewDraft || after.Review.Revision <= final.Review.Revision || after.NativeConditions.Records != 5 || after.NativeConditions.Digest == before.NativeConditions.Digest {
		t.Fatal("late native evidence inherited finalized review")
	}
	_, err = appendBatch(batch(6, late), "native-review-late-replay")
	requireNoError(t, err)
	replayedReview, err := ss.ExamIntegrityReview().Get(ctx, sub)
	requireNoError(t, err)
	if replayedReview.Review.Revision != after.Review.Revision || replayedReview.NativeConditions != after.NativeConditions {
		t.Fatal("native replay duplicated review evidence")
	}

}

// TestNativeMaximumRecordStore covers legal wire values that exceed former
// private per-record caps while remaining within aggregate delivery admission.
func TestNativeMaximumRecordStore(t *testing.T, ss store.Store) {
	ctx := t.Context()
	fixture := newExamAttemptFixture(t, ctx, ss)
	input := &store.ExamAttemptConnect{CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
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
	requireNoError(t, err)
	input.DesktopBuild.NativeAgreement = agreement
	access := store.SecurityPreflightAccess{SittingID: input.SittingID, CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, DesktopBuild: input.DesktopBuild, DesktopCompatibilityPolicyRevision: input.DesktopCompatibilityPolicyRevision}
	prepare := &store.SecurityPreflightPrepare{Access: access, PreflightID: model.NewId(), Challenge: model.NewCredentialToken(), NativeRegistryDigest: agreement.RegistryDigest(), SourceManifestDigest: agreement.SourceManifestDigest(), ConfigurationManifestFingerprint: input.ConfigurationManifestFingerprint, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepared, err := ss.ExamAttempt().PrepareSecurityPreflight(ctx, prepare, examCommand(input.CandidateUserID, store.SecurityPreflightPrepareOperation, "maximum-prepare", "maximum-prepare"))
	requireNoError(t, err)
	report := syntheticPreflightReport(prepared, agreement)
	for i := range report.Sources {
		report.Sources[i].SourceInstanceID = strings.Repeat("i", 128)
	}
	accepted, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, &store.SecurityPreflightReport{Access: access, PreflightID: prepare.PreflightID, Report: report, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(input.CandidateUserID, store.SecurityPreflightReportOperation, "maximum-report", "maximum-report"))
	requireNoError(t, err)
	input.Security = model.ConnectSecurity{Kind: "preflight", PreflightID: prepare.PreflightID, ReportDigest: accepted.ReportDigest}
	connected, err := ss.ExamAttempt().Connect(ctx, input, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "maximum-connect", "maximum-connect"))
	requireNoError(t, err)
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	occurrence := model.NativeOccurrence{Kind: "occurrence", OccurrenceID: strings.Repeat("o", 128), ConditionID: "baseline.capture", DetectorID: "synthetic-baseline", DetectorVersion: 1, CapabilityID: "baseline", Mode: model.NativeClaimEnforce, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []model.NativeSourceRange{}}
	for _, source := range report.Sources {
		occurrence.SourceRanges = append(occurrence.SourceRanges, model.NativeSourceRange{SourceID: source.SourceID, SourceInstanceID: source.SourceInstanceID, FirstSequence: 0, LastSequence: 1})
	}
	raw, err := json.Marshal(occurrence)
	requireNoError(t, err)
	var fields map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(raw, &fields))
	fields["first_observed_at"], err = json.Marshal("2026-09-09T12:00:00." + strings.Repeat("0", 240000) + "Z")
	requireNoError(t, err)
	raw, err = json.Marshal(fields)
	requireNoError(t, err)
	requireNoError(t, json.Unmarshal(raw, &occurrence))
	if len(raw) <= 8192 || len(occurrence.SourceRanges) != 11 {
		t.Fatal("maximum fixture missed record bounds")
	}
	batch := model.NativeSecurityBatch{StreamID: connected.Security.DeliveryStreamID, BatchSequence: 1, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SecuritySessionID: connected.Security.SecuritySessionID, PolicyDigest: connected.Security.Policy.Digest, ApplicationReleaseID: connected.Security.Policy.ApplicationReleaseID, MatrixID: connected.Security.Policy.MatrixID, Records: []model.NativeRecord{{Occurrence: &occurrence}}}
	canonical, err := batch.Canonical()
	requireNoError(t, err)
	fields["first_observed_at"], err = json.Marshal("2026-09-09T12:00:00." + strings.Repeat("0", 240000+256*1024-len(canonical)) + "Z")
	requireNoError(t, err)
	raw, err = json.Marshal(fields)
	requireNoError(t, err)
	requireNoError(t, json.Unmarshal(raw, &occurrence))
	canonical, err = batch.Canonical()
	requireNoError(t, err)
	if len(canonical) != 256*1024 {
		t.Fatal("occurrence batch did not reach exact request limit")
	}
	deliveryAccess := store.NativeDeliveryAccess{Access: store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID, ContinuityCredentialHash: input.ContinuityCredentialHash}, StreamID: batch.StreamID, ParticipationID: batch.ParticipationID, Generation: batch.Generation}
	appendBatch := func(key string) (*model.NativeSecurityAcknowledgement, error) {
		return ss.ExamAttempt().AppendNativeDelivery(ctx, &store.NativeDeliveryAppend{Access: deliveryAccess, Batch: batch, DesktopBuild: input.DesktopBuild, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(input.CandidateUserID, store.NativeDeliveryAppendOperation, key, model.SHA256Fingerprint(canonical)))
	}
	result, err := appendBatch("maximum-append")
	requireNoError(t, err)
	replay, err := appendBatch("maximum-replay")
	requireNoError(t, err)
	if result.HighestContiguousBatchSequence != 1 || result.Receipt != replay.Receipt || result.Receipt.RequestDigest != model.SHA256Fingerprint(canonical) {
		t.Fatal("large legal native record failed exact acknowledgement/replay")
	}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, ContinuityCredentialHash: deliveryAccess.Access.ContinuityCredentialHash, ExpectedCurrentRevisionID: fixture.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, fixture.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(fixture.candidate.ID, store.ExamSubmissionSealOperation, "maximum-seal", "maximum-seal"))
	requireNoError(t, err)
	page, err := ss.ExamIntegrityReview().ListNativeConditions(ctx, store.NativeConditionListOptions{SubmissionID: sealed.Receipt.SubmissionID, Limit: 10})
	requireNoError(t, err)
	if len(page.Items) != 1 {
		t.Fatal("large occurrence was not interpreted and copied")
	}
	copied, err := json.Marshal(page.Items[0].Occurrence)
	requireNoError(t, err)
	original, err := json.Marshal(occurrence)
	requireNoError(t, err)
	if string(copied) != string(original) {
		t.Fatal("evidence copy changed timestamp spelling")
	}
	reset := nativeResetWithLongTimestamp(t, model.NativeSourceReset{Kind: "source_reset", ResetID: model.NewId(), SourceID: report.Sources[0].SourceID, PreviousSourceInstanceID: report.Sources[0].SourceInstanceID, PreviousFinalSequence: 1, NewSourceInstanceID: model.NewId(), Reason: "restart", OccurredAt: at}, 240000)
	deliveryAccess.Access.ConnectionID = ""
	deliveryAccess.Access.ContinuityCredentialHash = ""
	_, err = ss.ExamAttempt().SealNativeDelivery(ctx, &store.NativeDeliveryFinalDeclaration{Access: deliveryAccess, Declaration: model.FinalDeliveryDeclaration{DeclarationID: model.NewId(), FinalSequence: 2, ExpectedDeclarationRevision: 0}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.NativeDeliveryFinalOperation, "maximum-final", "maximum-final"))
	requireNoError(t, err)
	batch.BatchSequence = 2
	batch.Records = []model.NativeRecord{{Reset: &reset}}
	canonical, err = batch.Canonical()
	requireNoError(t, err)
	reset = nativeResetWithLongTimestamp(t, reset, 240000+256*1024-len(canonical))
	canonical, err = batch.Canonical()
	requireNoError(t, err)
	if len(canonical) != 256*1024 {
		t.Fatal("reset batch did not reach exact request limit")
	}
	resetAck, err := appendBatch("maximum-historical-reset")
	requireNoError(t, err)
	resetReplay, err := appendBatch("maximum-historical-reset-replay")
	requireNoError(t, err)
	if resetAck.Receipt != resetReplay.Receipt || resetAck.Receipt.RequestDigest != model.SHA256Fingerprint(canonical) {
		t.Fatal("large historical reset lost exact receipt")
	}

}

// A lexical zero tail changes canonical bytes while retaining the same instant.
func nativeResetWithLongTimestamp(t *testing.T, reset model.NativeSourceReset, zeros int) model.NativeSourceReset {
	t.Helper()
	raw, err := json.Marshal(reset)
	requireNoError(t, err)
	var fields map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(raw, &fields))
	fields["occurred_at"], err = json.Marshal(reset.OccurredAt.UTC().Format("2006-01-02T15:04:05.000") + strings.Repeat("0", zeros) + "Z")
	requireNoError(t, err)
	raw, err = json.Marshal(fields)
	requireNoError(t, err)
	requireNoError(t, json.Unmarshal(raw, &reset))
	return reset
}
