// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// This fixture certifies synthetic baseline adapters only inside conformance.
func syntheticNativeAgreement(t *testing.T, build model.DesktopBuildTuple) *model.DesktopNativeAgreement {
	t.Helper()
	digest := model.SHA256Fingerprint([]byte("synthetic-source-schema"))
	definitions := []model.NativeCoverageDefinition{}
	matrix := model.NativeCapabilityMatrix{RegistryDigest: model.NativeRegistryDigest, MatrixID: build.CapabilityMatrixIdentity, ReleaseID: "synthetic-release", TargetTuple: build.DesktopTarget}
	for _, item := range []struct {
		key    string
		source model.NativeSourceID
	}{{"baseline.capture", model.NativeSourceCapture}, {"baseline.display", model.NativeSourceDisplay}, {"baseline.window", model.NativeSourceWindow}} {
		definitions = append(definitions, model.NativeCoverageDefinition{CoverageKey: item.key, CapabilityID: "baseline", SourceID: item.source, SourceSchemaDigest: digest, PermittedClaims: []model.NativeCapabilityClaim{model.NativeClaimEnforce}, RequiredPermissions: []string{}, Baseline: true})
		matrix.Entries = append(matrix.Entries, model.NativeCapabilityMatrixEntry{CoverageKey: item.key, SourceSchemaDigest: digest, Claim: model.NativeClaimEnforce, ComponentID: "synthetic-component", AdapterVersion: "synthetic-adapter", RequiredPermissions: []string{}, Limitations: []string{}, Verification: "passed"})
	}
	agreement, err := model.NewDesktopNativeAgreement(model.NativeRegistryDigest, model.SHA256Fingerprint([]byte("synthetic-manifest")), model.SHA256Fingerprint([]byte("synthetic-matrix")), model.SHA256Fingerprint([]byte("synthetic-detectors")), definitions, matrix, []model.NativeDetectorDefinition{{DetectorID: "synthetic-baseline", Version: 1, CapabilityID: "baseline", ConditionIDs: []string{"baseline.capture"}, SourceIDs: []model.NativeSourceID{model.NativeSourceCapture}, AllowedModes: []model.NativeCapabilityClaim{model.NativeClaimEnforce}}})
	requireNoError(t, err)
	return agreement
}
func syntheticPreflightReport(prepared *store.SecurityPreflightPrepared, agreement *model.DesktopNativeAgreement) model.SecurityPreflightReport {
	resolved := prepared.Resolved
	report := model.SecurityPreflightReport{Challenge: prepared.Challenge.Challenge, PolicyDigest: resolved.Policy.Digest, PolicyContentDigest: resolved.PolicyContentDigest, CapabilityMatrixDigest: resolved.CapabilityMatrixDigest, SecuritySessionID: model.NewId(), SourceManifestDigest: agreement.SourceManifestDigest(), SelectedSourceCategories: resolved.Categories, Sources: []model.NativeSourceCoverage{}, Coverage: []model.NativeCoverageClaim{}, Baseline: model.NativeBaselineReport{ConstrainedWindow: "verified", SinglePhysicalDisplay: "verified", ContentProtection: "verified"}, Posture: "compliant", ReportedAt: time.Now().UTC().Truncate(time.Millisecond)}
	for _, source := range resolved.Sources {
		for _, requirement := range resolved.Requirements {
			if requirement.SourceID == source {
				report.Sources = append(report.Sources, model.NativeSourceCoverage{SourceID: source, SourceInstanceID: model.NewId(), Sequence: 0, SourceSchemaDigest: requirement.Entry.SourceSchemaDigest, AdapterVersion: requirement.Entry.AdapterVersion, Health: "healthy", Permission: "granted", Complete: true, GapCount: 0})
				break
			}
		}
	}
	for _, requirement := range resolved.Requirements {
		state := "ready"
		if requirement.Entry.Claim == model.NativeClaimUnavailable {
			state = "unavailable"
		}
		report.Coverage = append(report.Coverage, model.NativeCoverageClaim{CoverageKey: requirement.CoverageKey, SourceSchemaDigest: requirement.Entry.SourceSchemaDigest, Claim: requirement.Entry.Claim, State: state})
	}
	return report
}

type SecurityPreflightProbe struct {
	Expire    func(string)
	HasReport func(string) bool
	Exists    func(string) bool
}

func TestSecurityPreflightStore(t *testing.T, ss store.Store, probes ...SecurityPreflightProbe) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connection := &store.ExamAttemptConnect{CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID}
	prepareExamAttemptConnect(t, ctx, ss, connection)
	build := connection.DesktopBuild
	build.NativeAgreement = syntheticNativeAgreement(t, build)
	access := store.SecurityPreflightAccess{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, DesktopBuild: build, DesktopCompatibilityPolicyRevision: connection.DesktopCompatibilityPolicyRevision}
	input := &store.SecurityPreflightPrepare{Access: access, PreflightID: model.NewId(), Challenge: model.NewCredentialToken(), NativeRegistryDigest: build.NativeAgreement.RegistryDigest(), SourceManifestDigest: build.NativeAgreement.SourceManifestDigest(), ConfigurationManifestFingerprint: build.ConfigurationManifest.Fingerprint(), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	command := examCommand(fixture.candidate.ID, store.SecurityPreflightPrepareOperation, "preflight-prepare", "preflight-prepare")
	prepared, err := ss.ExamAttempt().PrepareSecurityPreflight(ctx, input, command)
	requireNoError(t, err)
	if prepared.Replayed || prepared.FrozenAttemptConfiguration != nil || prepared.Challenge.PreflightID != input.PreflightID || prepared.Challenge.ExpiresAt.Sub(prepared.Challenge.IssuedAt) != model.SecurityPreflightLifetime {
		t.Fatalf("prepared = %#v", prepared)
	}
	requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
	replay := *input
	replay.PreflightID = model.NewId()
	replay.Challenge = model.NewCredentialToken()
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	repeated, err := ss.ExamAttempt().PrepareSecurityPreflight(ctx, &replay, command)
	requireNoError(t, err)
	if !repeated.Replayed || repeated.Challenge != prepared.Challenge {
		t.Fatal("replay replaced or extended the challenge")
	}
	report := &store.SecurityPreflightReport{Access: access, PreflightID: input.PreflightID, Report: syntheticPreflightReport(prepared, build.NativeAgreement), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	// Authenticated selectors cannot be rebound even with a valid report body.
	for name, mutate := range map[string]func(*store.SecurityPreflightAccess){
		"user":         func(a *store.SecurityPreflightAccess) { a.CandidateUserID = model.NewUserID() },
		"session":      func(a *store.SecurityPreflightAccess) { a.SessionID = model.NewSessionID() },
		"registration": func(a *store.SecurityPreflightAccess) { a.DesktopRegistrationID = model.NewDesktopRegistrationID() },
		"key":          func(a *store.SecurityPreflightAccess) { a.DPoPKeyThumbprint = strings.Repeat("B", 42) + "A" },
		"build":        func(a *store.SecurityPreflightAccess) { a.DesktopBuild.DesktopBuildID = "another-build" },
	} {
		foreign := *report
		mutate(&foreign.Access)
		foreign.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		if _, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, &foreign, examCommand(fixture.candidate.ID, store.SecurityPreflightReportOperation, "foreign-"+name, "foreign-"+name)); err == nil {
			t.Fatalf("%s selector accepted a foreign preflight report", name)
		}
	}
	if len(probes) > 0 {
		// Completing a nonexistent audit must roll back the preceding report write.
		unaudited := *report
		unaudited.AuditEventID = model.NewId()
		if _, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, &unaudited, examCommand(fixture.candidate.ID, store.SecurityPreflightReportOperation, "unaudited-report", "unaudited-report")); err == nil {
			t.Fatal("accepted report without durable audit")
		}
		if probes[0].HasReport(input.PreflightID) {
			t.Fatal("failed audit left a report behind")
		}
	}
	report.Report.Baseline.ContentProtection = "unavailable"
	blocked, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, report, examCommand(fixture.candidate.ID, store.SecurityPreflightReportOperation, "blocked-report", "blocked-report"))
	requireNoError(t, err)
	if blocked.Admission != "blocked" {
		t.Fatal("missing baseline admitted")
	}
	report.Report.Baseline.ContentProtection = "verified"
	report.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	reportCommand := examCommand(fixture.candidate.ID, store.SecurityPreflightReportOperation, "eligible-report", "eligible-report")
	eligible, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, report, reportCommand)
	requireNoError(t, err)
	if eligible.Admission != "eligible" {
		t.Fatalf("eligible = %#v", eligible)
	}
	report.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	retried, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, report, reportCommand)
	requireNoError(t, err)
	if retried.ReportDigest != eligible.ReportDigest || !retried.ServerTime.Equal(eligible.ServerTime) {
		t.Fatal("retry changed report receipt or freshness")
	}
	attempts, err := ss.ExamAttempt().List(ctx, store.ExamAttemptManagerListOptions{ExamID: fixture.examID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID, Limit: 10})
	requireNoError(t, err)
	if len(attempts) != 0 {
		t.Fatal("preflight created an Attempt")
	}
	// Concurrent copies of one command have one retained receipt/freshness instant.
	type reportOutcome struct {
		result *model.SecurityPreflightResult
		err    error
	}
	outcomes := make(chan reportOutcome, 2)
	start := make(chan struct{})
	concurrentCommand := examCommand(fixture.candidate.ID, store.SecurityPreflightReportOperation, "concurrent-report", "concurrent-report")
	for range 2 {
		copy := *report
		copy.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		go func() {
			<-start
			result, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, &copy, concurrentCommand)
			outcomes <- reportOutcome{result, err}
		}()
	}
	close(start)
	var concurrentReceipt *model.SecurityPreflightResult
	for range 2 {
		outcome := <-outcomes
		requireNoError(t, outcome.err)
		if concurrentReceipt == nil {
			concurrentReceipt = outcome.result
		}
		if outcome.result.ReportDigest != eligible.ReportDigest || !outcome.result.ServerTime.Equal(concurrentReceipt.ServerTime) {
			t.Fatal("concurrent replay replaced the original receipt")
		}
	}
	// Supersession invalidates the earlier prepared command and its report.
	time.Sleep(1100 * time.Millisecond)
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err = ss.ExamAttempt().PrepareSecurityPreflight(ctx, &replay, examCommand(fixture.candidate.ID, store.SecurityPreflightPrepareOperation, "successor", "successor"))
	requireNoError(t, err)
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err = ss.ExamAttempt().PrepareSecurityPreflight(ctx, input, command)
	assertExamAttemptConflict(t, err, "preflight_superseded")
	report.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err = ss.ExamAttempt().ReportSecurityPreflight(ctx, report, reportCommand)
	assertExamAttemptConflict(t, err, "preflight_superseded")
	if len(probes) > 0 {
		probes[0].Expire(replay.PreflightID)
		replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		_, err = ss.ExamAttempt().PrepareSecurityPreflight(ctx, &replay, examCommand(fixture.candidate.ID, store.SecurityPreflightPrepareOperation, "successor", "successor"))
		assertExamAttemptConflict(t, err, "preflight_expired")
		// Expiry cleanup runs through preparation and cannot revive the old owner.
		oldID := replay.PreflightID
		replay.PreflightID = model.NewId()
		replay.Challenge = model.NewCredentialToken()
		replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		_, err = ss.ExamAttempt().PrepareSecurityPreflight(ctx, &replay, examCommand(fixture.candidate.ID, store.SecurityPreflightPrepareOperation, "after-expiry", "after-expiry"))
		requireNoError(t, err)
		if probes[0].Exists(oldID) || !probes[0].Exists(replay.PreflightID) {
			t.Fatal("expiry cleanup retained or revived the old preflight")
		}
	}
}

// TestSecureConnectStore exercises real named aggregate transactions. The
// synthetic release certifies only this test's mandatory baseline adapters.
type SecureConnectProbe struct {
	AgeReport    func(string) func()
	ExpireLease  func(model.AttemptParticipationID)
	FillMetadata func(model.ExamAttemptID)
	Totals       func(model.ExamAttemptID) (int64, int64)
	Consumed     func(string) bool
}

func TestSecureConnectStore(t *testing.T, ss store.Store, probes ...SecureConnectProbe) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	input := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	testSittingID := input.SittingID
	input.SittingID = ""
	prepareExamAttemptConnect(t, ctx, ss, input)
	input.SittingID = testSittingID
	input.DesktopBuild.NativeAgreement = syntheticNativeAgreement(t, input.DesktopBuild)
	access := store.SecurityPreflightAccess{SittingID: input.SittingID, CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, DesktopBuild: input.DesktopBuild, DesktopCompatibilityPolicyRevision: input.DesktopCompatibilityPolicyRevision}
	prepare := &store.SecurityPreflightPrepare{Access: access, PreflightID: model.NewId(), Challenge: model.NewCredentialToken(), NativeRegistryDigest: access.DesktopBuild.NativeAgreement.RegistryDigest(), SourceManifestDigest: access.DesktopBuild.NativeAgreement.SourceManifestDigest(), ConfigurationManifestFingerprint: input.ConfigurationManifestFingerprint, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepared, err := ss.ExamAttempt().PrepareSecurityPreflight(ctx, prepare, examCommand(input.CandidateUserID, store.SecurityPreflightPrepareOperation, "secure-prepare", "secure-prepare"))
	requireNoError(t, err)
	report := &store.SecurityPreflightReport{Access: access, PreflightID: prepare.PreflightID, Report: syntheticPreflightReport(prepared, access.DesktopBuild.NativeAgreement), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	accepted, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, report, examCommand(input.CandidateUserID, store.SecurityPreflightReportOperation, "secure-report", "secure-report"))
	requireNoError(t, err)
	input.Security = model.ConnectSecurity{Kind: "preflight", PreflightID: prepare.PreflightID, ReportDigest: model.SHA256Fingerprint([]byte("wrong report"))}
	_, err = ss.ExamAttempt().Connect(ctx, input, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "wrong-report", "wrong-report"))
	assertExamAttemptConflict(t, err, "preflight_report_changed")
	if _, err = ss.ExamAttempt().Get(ctx, fixture.examID, input.AttemptID); !store.IsNotFound(err) {
		t.Fatalf("refusal allocated Attempt: %v", err)
	}
	if len(probes) > 0 && probes[0].AgeReport != nil {
		// Report freshness is independent of the still-valid 120-second challenge.
		restore := probes[0].AgeReport(prepare.PreflightID)
		stale := *input
		stale.Security.ReportDigest = accepted.ReportDigest
		stale.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		_, staleErr := ss.ExamAttempt().Connect(ctx, &stale, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "stale-report", "stale-report"))
		restore()
		assertExamAttemptConflict(t, staleErr, "preflight_expired")
		if probes[0].Consumed(prepare.PreflightID) {
			t.Fatal("stale report consumed preflight")
		}
		if _, err := ss.ExamAttempt().Get(ctx, fixture.examID, input.AttemptID); !store.IsNotFound(err) {
			t.Fatalf("stale report created partial Attempt: %v", err)
		}
	}
	input.Security.ReportDigest = accepted.ReportDigest
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	command := examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "secure-connect", "secure-connect")
	connected, err := ss.ExamAttempt().Connect(ctx, input, command)
	requireNoError(t, err)
	receipt := connected.Security
	requireNoError(t, receipt.Validate())
	if receipt.Policy.Scope.AttemptID != connected.Attempt.ID || receipt.PolicyContentDigest != prepared.Resolved.PolicyContentDigest || receipt.PreflightPolicyDigest != prepared.Resolved.Policy.Digest || receipt.Policy.Digest == receipt.PreflightPolicyDigest || receipt.SecuritySessionID != report.Report.SecuritySessionID {
		t.Fatalf("invalid admission binding: %#v", receipt)
	}
	requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
	recovered, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, access, connected.Attempt.ID)
	requireNoError(t, err)
	if recovered.Security.DeliveryStreamID != receipt.DeliveryStreamID || recovered.FrozenAttemptConfiguration.Digest != connected.Configuration.Digest {
		t.Fatal("recovery changed immutable admission provenance")
	}
	for _, mutate := range []func(*store.SecurityPreflightAccess){func(a *store.SecurityPreflightAccess) { a.CandidateUserID = model.NewUserID() }, func(a *store.SecurityPreflightAccess) { a.SessionID = model.NewSessionID() }, func(a *store.SecurityPreflightAccess) { a.DPoPKeyThumbprint = strings.Repeat("B", 42) + "A" }} {
		foreign := access
		mutate(&foreign)
		if _, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, foreign, connected.Attempt.ID); !store.IsNotFound(err) {
			t.Fatalf("foreign recovery exposed owner: %v", err)
		}
	}
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	replay, err := ss.ExamAttempt().Connect(ctx, input, command)
	requireNoError(t, err)
	originalBytes, _ := json.Marshal(receipt)
	replayedBytes, _ := json.Marshal(replay.Security)
	if !replay.Replayed || replay.ConnectionOpened || !bytes.Equal(originalBytes, replayedBytes) {
		t.Fatal("replay changed binding or duplicated connection effect")
	}
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err = ss.ExamAttempt().Connect(ctx, input, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "consumed-new-key", "consumed-new-key"))
	assertExamAttemptConflict(t, err, "resume_required")
	input.Security = model.ConnectSecurity{Kind: "resume", ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, PolicyDigest: receipt.Policy.Digest}
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	resumed, err := ss.ExamAttempt().Connect(ctx, input, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "secure-resume", "secure-resume"))
	requireNoError(t, err)
	resumedBytes, _ := json.Marshal(resumed.Security)
	if resumed.Participation.ID != connected.Participation.ID || !bytes.Equal(originalBytes, resumedBytes) {
		t.Fatal("resume replaced source ownership")
	}
	input.Security.Generation++
	input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	_, err = ss.ExamAttempt().Connect(ctx, input, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "wrong-generation", "wrong-generation"))
	assertExamAttemptConflict(t, err, "resume_required")
	if len(probes) > 0 {
		probe := probes[0]
		used, owners := probe.Totals(connected.Attempt.ID)
		if used != model.NativeOwnerReservationBytes || owners != 1 {
			t.Fatalf("replay double-charged metadata: %d %d", used, owners)
		}
		testProcessedSecurityControl(t, ctx, ss, input, resumed, report.Report)
		deliveryAccess := testNativeDeliveryGaps(t, ctx, ss, input, resumed, fixture)
		probe.ExpireLease(connected.Participation.ID)
		expired, err := ss.ExamAttempt().ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(), SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: saveExamAttemptSystemAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
		testClosedNativeDelivery(t, ctx, ss, deliveryAccess, fixture)
		_, err = ss.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID, SuspensionID: expired.Suspension.ID, ActorUserID: fixture.manager.ID, ExpectedAttemptRevision: expired.Attempt.Revision, PrivateReason: "secure rejoin capacity test", ChangedAt: model.NowUTC(), AuditEventID: saveExamAttemptReallowAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "secure-reallow", "secure-reallow"))
		requireNoError(t, err)
		if _, err = ss.ExamAttempt().RecoverSecurityPolicy(ctx, access, connected.Attempt.ID); !store.IsNotFound(err) {
			t.Fatalf("Ready recovered obsolete active receipt: %v", err)
		}
		prepareExamAttemptConnect(t, ctx, ss, input)
		if input.Security.Kind != "preflight" {
			t.Fatal("Ready requires a fresh preflight")
		}
		probe.FillMetadata(connected.Attempt.ID)
		input.AttemptID = model.NewExamAttemptID()
		input.ParticipationID = model.NewAttemptParticipationID()
		input.ConnectionID = model.NewAttemptConnectionID()
		input.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		_, err = ss.ExamAttempt().Connect(ctx, input, examCommand(input.CandidateUserID, store.ExamAttemptConnectOperation, "metadata-refused", "metadata-refused"))
		var capacity *model.DeliveryMetadataCapacity
		if !errors.As(err, &capacity) || capacity.RemainingReservableBytes != 0 {
			t.Fatalf("metadata refusal: %v", err)
		}
		if probe.Consumed(input.Security.PreflightID) {
			t.Fatal("capacity refusal consumed preflight")
		}
		_, owners = probe.Totals(connected.Attempt.ID)
		if owners != 1 {
			t.Fatal("capacity refusal created security owner")
		}
		snapshot, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
		requireNoError(t, err)
		if snapshot.Attempt.State != model.ExamAttemptReady {
			t.Fatal("capacity refusal activated Ready Attempt")
		}
	}

}

// prepareConnectSecurityFixture uses the same public prepare/report/recovery
// operations as Desktop. Negative admission tests retain a structurally valid
// proof and exercise their intended eligibility guard without bypassing it.
func prepareConnectSecurityFixture(t *testing.T, ctx context.Context, ss store.Store, input *store.ExamAttemptConnect) {
	t.Helper()
	input.DesktopBuild.NativeAgreement = syntheticNativeAgreement(t, input.DesktopBuild)
	input.Security = model.ConnectSecurity{Kind: "preflight", PreflightID: model.NewId(), ReportDigest: model.SHA256Fingerprint([]byte("unavailable-preflight"))}
	if !input.SittingID.IsValid() {
		return
	}
	sitting, err := ss.ExamSitting().Resolve(ctx, input.SittingID)
	requireNoError(t, err)
	access := store.SecurityPreflightAccess{SittingID: input.SittingID, CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, DesktopBuild: input.DesktopBuild, DesktopCompatibilityPolicyRevision: input.DesktopCompatibilityPolicyRevision}
	attempts, err := ss.ExamAttempt().List(ctx, store.ExamAttemptManagerListOptions{ExamID: sitting.Sitting.ExamID, SittingID: input.SittingID, ExcludeCandidateUserID: model.NewUserID(), Limit: 100})
	requireNoError(t, err)
	var attemptID model.ExamAttemptID
	generation := int64(1)
	for _, snapshot := range attempts {
		if snapshot.Attempt.CandidateUserID != input.CandidateUserID {
			continue
		}
		attemptID = snapshot.Attempt.ID
		if snapshot.LatestParticipation != nil {
			generation = snapshot.LatestParticipation.Generation + 1
		}
		if snapshot.Attempt.State == model.ExamAttemptActive {
			recovered, recoverErr := ss.ExamAttempt().RecoverSecurityPolicy(ctx, access, attemptID)
			if recoverErr == nil {
				input.Security = model.ConnectSecurity{Kind: "resume", ParticipationID: recovered.Security.ParticipationID, Generation: recovered.Security.Generation, PolicyDigest: recovered.Security.Policy.Digest}
			}
			return
		}
		if snapshot.Attempt.State != model.ExamAttemptReady {
			return
		}
	}
	audit := func() string {
		event, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: input.CandidateUserID, Action: string(model.ActionExamSittingParticipate), Resource: model.Resource{Type: model.ResourceExamSitting, ID: input.SittingID.String()}, ScopeType: model.RoleScopeClass, ScopeID: sitting.Sitting.ClassID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
		requireNoError(t, err)
		return event.ID.String()
	}
	prepare := &store.SecurityPreflightPrepare{Access: access, AttemptID: attemptID, PreflightID: model.NewId(), Challenge: model.NewCredentialToken(), NativeRegistryDigest: access.DesktopBuild.NativeAgreement.RegistryDigest(), SourceManifestDigest: access.DesktopBuild.NativeAgreement.SourceManifestDigest(), ConfigurationManifestFingerprint: input.ConfigurationManifestFingerprint, AuditEventID: audit(), AuditAt: model.GetMillis()}
	key := fmt.Sprintf("fixture-preflight-%s-%s-%d", input.SessionID, input.SittingID, generation)
	command := examCommand(input.CandidateUserID, store.SecurityPreflightPrepareOperation, key, key)
	prepared, err := ss.ExamAttempt().PrepareSecurityPreflight(ctx, prepare, command)
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Constraint == "preflight_rate_limited" {
		time.Sleep(time.Second)
		prepare.AuditEventID = audit()
		prepared, err = ss.ExamAttempt().PrepareSecurityPreflight(ctx, prepare, command)
	}
	if err != nil {
		if store.IsNotFound(err) || errors.As(err, &conflict) {
			return
		}
		requireNoError(t, err)
	}
	report := syntheticPreflightReport(prepared, access.DesktopBuild.NativeAgreement)
	report.SecuritySessionID = prepared.Challenge.PreflightID
	report.ReportedAt = prepared.Challenge.IssuedAt
	for i := range report.Sources {
		report.Sources[i].SourceInstanceID = prepared.Challenge.PreflightID
	}
	canonical, err := report.Canonical()
	requireNoError(t, err)
	reportKey := "fixture-report-" + prepared.Challenge.PreflightID
	result, err := ss.ExamAttempt().ReportSecurityPreflight(ctx, &store.SecurityPreflightReport{Access: access, PreflightID: prepared.Challenge.PreflightID, Report: report, AuditEventID: audit(), AuditAt: model.GetMillis()}, examCommand(input.CandidateUserID, store.SecurityPreflightReportOperation, reportKey, model.SHA256Fingerprint(canonical)))
	requireNoError(t, err)
	if result.Admission != "eligible" {
		t.Fatalf("synthetic baseline preflight blocked: %#v", result)
	}
	input.Security = model.ConnectSecurity{Kind: "preflight", PreflightID: prepared.Challenge.PreflightID, ReportDigest: result.ReportDigest}
}

func prepareParticipationRenewalCoverage(t *testing.T, ctx context.Context, ss store.Store, input *store.ExamAttemptParticipationRenewal) {
	t.Helper()
	connect := &store.ExamAttemptConnect{CandidateUserID: input.CandidateUserID, SessionID: input.SessionID}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	input.DesktopBuild = connect.DesktopBuild
	recovery, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, store.SecurityPreflightAccess{CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, DesktopBuild: input.DesktopBuild, DesktopCompatibilityPolicyRevision: input.DesktopCompatibilityPolicyRevision}, input.AttemptID)
	requireNoError(t, err)
	input.SecurityCoverage = model.SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: recovery.Security.Policy.Digest, SecuritySessionID: recovery.Security.SecuritySessionID, StreamID: recovery.Security.DeliveryStreamID, Posture: "compliant", Sources: recovery.CurrentSources, Coverage: recovery.CurrentCoverage, SourceResets: []model.NativeSourceReset{}, DeliveryWatermarks: []model.DeliveryWatermark{}}
}

func testProcessedSecurityControl(t *testing.T, ctx context.Context, ss store.Store, connect *store.ExamAttemptConnect, connected *store.ExamAttemptConnectResult, report model.SecurityPreflightReport) {
	t.Helper()
	access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, ConnectionID: connected.Connection.ID, CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, ContinuityCredentialHash: connect.ContinuityCredentialHash}
	control := model.SecurityCoverageRenewal{ControlSequence: 9, PolicyDigest: connected.Security.Policy.Digest, SecuritySessionID: connected.Security.SecuritySessionID, StreamID: connected.Security.DeliveryStreamID, Posture: "compliant", Sources: append([]model.NativeSourceCoverage{}, report.Sources...), Coverage: report.Coverage, SourceResets: []model.NativeSourceReset{}, DeliveryWatermarks: []model.DeliveryWatermark{}}
	update := func(body model.SecurityCoverageRenewal) model.SecurityCoverageResult {
		t.Helper()
		result, err := ss.ExamAttempt().UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision, Coverage: body})
		requireNoError(t, err)
		requireNoError(t, result.Validate())
		return result
	}
	healthy := update(control)
	if healthy.ProcessedControlSequence != 9 || !healthy.SecurityInteractionAllowed {
		t.Fatalf("healthy control: %#v", healthy)
	}
	fault := control
	fault.ControlSequence = 11
	fault.Sources = append([]model.NativeSourceCoverage{}, control.Sources...)
	fault.Sources[0].SourceInstanceID = model.NewId()
	blocked := update(fault)
	if blocked.CoverageResult != "reset_required" || blocked.ProcessedControlSequence != 11 || blocked.SecurityInteractionAllowed {
		t.Fatalf("reset fault: %#v", blocked)
	}

	workspaceAccess := store.ExamAttemptWorkspaceMutationAccess{AttemptID: access.AttemptID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash}
	_, gateErr := ss.ExamAttemptWorkspace().ResolveMutationTarget(ctx, workspaceAccess)
	assertExamAttemptConflict(t, gateErr, "posture_blocked")
	_, gateErr = ss.ExamSubmission().ResolveSealTarget(ctx, store.ExamSubmissionSealAccess{AttemptID: access.AttemptID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: connected.Security.Policy.ExamRevisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor})
	assertExamAttemptConflict(t, gateErr, "posture_blocked")
	stale := control
	stale.ControlSequence = 10
	delayed := update(stale)
	if delayed.CoverageResult != "stale_control" || delayed.ProcessedControlSequence != 11 || delayed.SecurityInteractionAllowed {
		t.Fatalf("delayed control reopened gate: %#v", delayed)
	}
	replay := update(control)
	if replay.CoverageResult != "accepted" || replay.ProcessedControlSequence != 11 || replay.SecurityInteractionAllowed {
		t.Fatalf("old receipt reopened gate: %#v", replay)
	}
	altered := fault
	altered.Posture = "degraded"
	_, err := ss.ExamAttempt().UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision, Coverage: altered})
	assertExamAttemptConflict(t, err, "control_conflict")
	renewed, err := ss.ExamAttempt().RenewParticipation(ctx, &store.ExamAttemptParticipationRenewal{AttemptID: access.AttemptID, ParticipationID: connected.Participation.ID, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash, Generation: connected.Participation.Generation, Sequence: 1, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision, SecurityCoverage: stale})
	requireNoError(t, err)
	if renewed.AcceptedSequence != 1 || renewed.SecurityCoverage.SecurityInteractionAllowed || renewed.SecurityCoverage.ProcessedControlSequence != 11 {
		t.Fatalf("stale control prevented lease renewal or reopened gate: %#v", renewed)
	}
	control.ControlSequence = 12
	if recovered := update(control); !recovered.SecurityInteractionAllowed || recovered.ProcessedControlSequence != 12 {
		t.Fatalf("greater healthy control failed recovery: %#v", recovered)
	}
	reset := model.NativeSourceReset{Kind: "source_reset", ResetID: model.NewId(), SourceID: control.Sources[0].SourceID, PreviousSourceInstanceID: control.Sources[0].SourceInstanceID, PreviousFinalSequence: control.Sources[0].Sequence, NewSourceInstanceID: fault.Sources[0].SourceInstanceID, Reason: "restart", OccurredAt: time.Now().UTC().Truncate(time.Millisecond)}
	control.ControlSequence = 13
	control.Sources = fault.Sources
	control.SourceResets = []model.NativeSourceReset{reset}
	accepted := update(control)
	if !accepted.SecurityInteractionAllowed || len(accepted.SourceResetReceipts) != 1 {
		t.Fatalf("valid reset refused: %#v", accepted)
	}
	control.ControlSequence = 14
	if replay := update(control); !replay.SecurityInteractionAllowed || len(replay.SourceResetReceipts) != 1 {
		t.Fatalf("reset identity replay refused: %#v", replay)
	}
	reset.ResetID = model.NewId()
	reset.PreviousSourceInstanceID = reset.NewSourceInstanceID
	reset.NewSourceInstanceID = model.NewId()
	control.Sources = append([]model.NativeSourceCoverage{}, control.Sources...)
	control.Sources[0].SourceInstanceID = reset.NewSourceInstanceID
	control.SourceResets = []model.NativeSourceReset{reset}
	control.ControlSequence = 15
	if conflict := update(control); conflict.CoverageResult != "reset_conflict" || conflict.SecurityInteractionAllowed {
		t.Fatalf("second automatic restart admitted: %#v", conflict)
	}
	// Eviction forgets a receipt, never the processed high-water boundary. The
	// last usable source head survives the rejected reset and is recoverable.
	control.Sources = fault.Sources
	control.SourceResets = []model.NativeSourceReset{}
	last := int64(16 + model.SecurityControlReceiptLimit)
	for seq := int64(16); seq <= last; seq++ {
		control.ControlSequence = seq
		if current := update(control); !current.SecurityInteractionAllowed || current.ProcessedControlSequence != seq {
			t.Fatal("bounded control cache lost current healthy authority")
		}
	}
	evicted := control
	evicted.ControlSequence = 9
	if result := update(evicted); result.CoverageResult != "stale_control" || result.ProcessedControlSequence != last || !result.SecurityInteractionAllowed {
		t.Fatalf("evicted control crossed durable high-water boundary: %#v", result)
	}
	recovery, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, store.SecurityPreflightAccess{CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision}, connected.Attempt.ID)
	requireNoError(t, err)
	if recovery.SecurityCoverage.ProcessedControlSequence != last || !recovery.SecurityCoverage.SecurityInteractionAllowed || len(recovery.SourceResetReceipts) != 1 {
		t.Fatal("fresh recovery lost control boundary or accepted reset receipt")
	}
}

func testNativeDeliveryGaps(t *testing.T, ctx context.Context, ss store.Store, connect *store.ExamAttemptConnect, connected *store.ExamAttemptConnectResult, fixture examAttemptFixture) store.NativeDeliveryAccess {
	t.Helper()
	access := store.NativeDeliveryAccess{Access: store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, ConnectionID: connected.Connection.ID, CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, ContinuityCredentialHash: connect.ContinuityCredentialHash}, StreamID: connected.Security.DeliveryStreamID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation}
	status, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, access)
	requireNoError(t, err)
	if status.Closure.ClosedAt != nil || status.AllocatedThroughSequence != 0 {
		t.Fatalf("initial delivery status: %#v", status)
	}
	var first *store.NativeDeliveryGapDeclaration
	for i := int64(0); i < 2; i++ {
		input := &store.NativeDeliveryGapDeclaration{Access: access, Declaration: model.DeclareDeliveryGaps{DeclarationID: model.NewId(), ExpectedDeclarationRevision: i, AllocatedThroughSequence: (i + 1) * 1024, Ranges: []model.SequenceRange{{First: i*1024 + 1, Last: (i + 1) * 1024}}, Reason: "spool_lost"}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		key := fmt.Sprintf("native-gap-%d", i)
		receipt, err := ss.ExamAttempt().DeclareNativeDeliveryGaps(ctx, input, examCommand(fixture.candidate.ID, store.NativeDeliveryGapsOperation, key, key))
		requireNoError(t, err)
		if receipt.DeclarationRevision != i+1 || receipt.SettledThroughSequence != (i+1)*1024 {
			t.Fatalf("gap receipt: %#v", receipt)
		}
		if i == 0 {
			first = input
		}
	}
	first.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	replay, err := ss.ExamAttempt().DeclareNativeDeliveryGaps(ctx, first, examCommand(fixture.candidate.ID, store.NativeDeliveryGapsOperation, "native-gap-0", "native-gap-0"))
	requireNoError(t, err)
	if replay.DeclarationRevision != 1 || replay.SettledThroughSequence != 1024 {
		t.Fatalf("old exact declaration changed receipt: %#v", replay)
	}
	status, err = ss.ExamAttempt().NativeDeliveryStatus(ctx, access)
	requireNoError(t, err)
	if status.HighestContiguousBatchSequence != 0 || status.SettledThroughBatchSequence != 2048 || status.DeclarationRevision != 2 || len(status.MissingBatchRanges) != 0 {
		t.Fatalf("terminal gap progress invented receipts: %#v", status)
	}
	if _, err := ss.ExamAttempt().NativeDeliveryReceipt(ctx, access, 1); !store.IsNotFound(err) {
		t.Fatalf("gap invented receipt: %v", err)
	}
	return access
}
func testClosedNativeDelivery(t *testing.T, ctx context.Context, ss store.Store, access store.NativeDeliveryAccess, fixture examAttemptFixture) {
	t.Helper()
	access.Access.ConnectionID = ""
	access.Access.ContinuityCredentialHash = ""
	closed, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, access)
	requireNoError(t, err)
	if closed.Closure.ClosedAt == nil || closed.Closure.KnownAtClose == nil || *closed.Closure.KnownAtClose != 2048 || !closed.Closure.UnknownTail || closed.Closure.UploadExpiresAt.Sub(*closed.Closure.ClosedAt) != 24*time.Hour {
		t.Fatalf("closure: %#v", closed)
	}
	input := &store.NativeDeliveryFinalDeclaration{Access: access, Declaration: model.FinalDeliveryDeclaration{DeclarationID: model.NewId(), ExpectedDeclarationRevision: 2, FinalSequence: 2058}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	final, err := ss.ExamAttempt().SealNativeDelivery(ctx, input, examCommand(fixture.candidate.ID, store.NativeDeliveryFinalOperation, "native-final", "native-final"))
	requireNoError(t, err)
	if final.Closure.FinalSequence == nil || *final.Closure.FinalSequence != 2058 || final.Closure.UnknownTail || final.DeclarationRevision != 3 || !final.Closure.UploadExpiresAt.Equal(*closed.Closure.UploadExpiresAt) {
		t.Fatalf("final boundary changed close time: %#v", final)
	}
	summary := model.UnretainedDeliverySummary{SummarySequence: 1, UnretainedRecordCount: 4, CountComplete: false}
	status, err := ss.ExamAttempt().UpdateNativeDeliverySummary(ctx, &store.NativeDeliverySummaryUpdate{Access: access, Summary: summary, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.NativeDeliverySummaryOperation, "native-summary", "native-summary"))
	requireNoError(t, err)
	if status.Summary == nil || status.Summary.CountComplete {
		t.Fatal("summary lost uncertainty")
	}
	for _, mutate := range []func(*store.NativeDeliveryAccess){func(a *store.NativeDeliveryAccess) { a.Access.CandidateUserID = model.NewUserID() }, func(a *store.NativeDeliveryAccess) { a.Access.DPoPKeyThumbprint = strings.Repeat("B", 42) + "A" }} {
		foreign := access
		mutate(&foreign)
		if _, err := ss.ExamAttempt().NativeDeliveryStatus(ctx, foreign); !store.IsNotFound(err) {
			t.Fatalf("foreign historical delivery: %v", err)
		}
	}
}
