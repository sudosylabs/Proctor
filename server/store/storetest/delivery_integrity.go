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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestDeliveryCorruptReads exercises retained reads after the SQL harness injects
// invalid authoritative values. The callback restores each field before return.
func TestDeliveryCorruptReads(t *testing.T, ss store.Store, corrupt func(model.AttemptParticipationID, model.BrowserSourceSessionID, string) func()) {
	ctx := context.Background()
	fixture, connected, access := newBrowserActivityFixture(t, ctx, ss, "delivery-corruption")
	source := browserSourceID(19901)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	require.NoError(t, err)
	event := browserActivityEvent(1, model.BrowserActivityOpened, fixture.revisionID)
	event.ClientOccurredAt = time.Date(2026, 9, 9, 12, 0, 0, 100000000, time.UTC)
	raw, err := event.Canonical()
	require.NoError(t, err)
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &members))
	members["client_occurred_at"] = json.RawMessage(`"2026-09-09T12:00:00.100Z"`)
	raw, err = json.Marshal(members)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &event))
	canonical, err := event.Canonical()
	require.NoError(t, err)
	input := &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{event}}
	ack, err := appendBrowserActivityFixture(t, ctx, ss, input)
	require.NoError(t, err)
	require.Len(t, ack.Receipts, 1, "stored receipt normalized the Desktop timestamp")
	require.Equal(t, model.SHA256Fingerprint(canonical), ack.Receipts[0].EventDigest, "stored receipt normalized the Desktop timestamp")
	browser := store.BrowserDeliveryAccess{Access: access, SourceSessionID: source, ParticipationID: connected.Participation.ID}
	page, err := ss.ExamAttempt().BrowserDeliveryReceipts(ctx, browser, 1, 1)
	require.NoError(t, err)
	require.Len(t, page.Receipts, 1, "retained receipt changed")
	require.Equal(t, ack.Receipts[0], page.Receipts[0], "retained receipt changed")
	native := store.NativeDeliveryAccess{Access: access, StreamID: connected.Security.DeliveryStreamID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation}

	t.Run("native_progress_append", func(t *testing.T) {
		session := fixture.session
		build := model.DesktopBuildTuple{DesktopRelease: session.DesktopRelease, DesktopBuildID: session.DesktopBuildID, Platform: session.DesktopPlatform, Architecture: session.DesktopArchitecture, RealtimeProtocol: session.DesktopRealtimeProtocol, AttemptConfigurationManifestFingerprint: model.CurrentAttemptConfigurationManifestFingerprint(), DesktopSettingsRegistryFingerprint: "fnv1a64:" + strings.Repeat("b", 16), DesktopTarget: string(session.DesktopPlatform) + "-" + string(session.DesktopArchitecture), ConfigurationManifest: model.EmptyAttemptConfigurationManifest(), CapabilityMatrixIdentity: "storetest-matrix"}
		build.NativeAgreement = syntheticNativeAgreement(t, build)
		compatibility, err := ss.DesktopCompatibilityPolicy().Get(ctx)
		require.NoError(t, err)
		recoveryAccess := store.SecurityPreflightAccess{CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint, DesktopBuild: build, DesktopCompatibilityPolicyRevision: compatibility.Revision}
		recovery, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, recoveryAccess, connected.Attempt.ID)
		require.NoError(t, err)
		for _, field := range []string{"native_source_sequence", "native_source_id", "native_coverage_state"} {
			t.Run(field, func(t *testing.T) {
				restore := corrupt(connected.Participation.ID, "", field)
				defer restore()
				_, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, recoveryAccess, connected.Attempt.ID)
				assert.ErrorIs(t, err, store.ErrInvalidState)
				control := model.SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: connected.Security.Policy.Digest, SecuritySessionID: connected.Security.SecuritySessionID, StreamID: connected.Security.DeliveryStreamID, Posture: "compliant", Sources: recovery.CurrentSources, Coverage: recovery.CurrentCoverage, SourceResets: []model.NativeSourceReset{}, DeliveryWatermarks: []model.DeliveryWatermark{}}
				_, err = ss.ExamAttempt().UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: access, ParticipationID: native.ParticipationID, Generation: native.Generation, DesktopBuild: build, DesktopCompatibilityPolicyRevision: compatibility.Revision, Coverage: control})
				assert.ErrorIs(t, err, store.ErrInvalidState)
			})
		}
		var source model.NativeSourceCoverage
		for _, value := range recovery.CurrentSources {
			if value.SourceID == model.NativeSourceCapture {
				source = value
			}
		}
		at := model.NowUTC().Truncate(time.Millisecond)
		occurrence := model.NativeOccurrence{Kind: "occurrence", OccurrenceID: model.NewID(), ConditionID: "baseline.capture", DetectorID: "synthetic-baseline", DetectorVersion: 1, CapabilityID: "baseline", Mode: model.NativeClaimEnforce, Status: "opened", FirstObservedAt: at, LastConfirmedAt: at, RepeatCount: 1, Certainty: "complete", SourceRanges: []model.NativeSourceRange{{SourceID: source.SourceID, SourceInstanceID: source.SourceInstanceID, FirstSequence: 0, LastSequence: 1}}}
		appendBatch := func(sequence int64, key string) error {
			record := occurrence
			if sequence > 1 {
				record.Status = "repeated"
				record.RepeatCount = 2
			}
			batch := model.NativeSecurityBatch{StreamID: native.StreamID, BatchSequence: sequence, ParticipationID: native.ParticipationID, Generation: native.Generation, SecuritySessionID: connected.Security.SecuritySessionID, PolicyDigest: connected.Security.Policy.Digest, ApplicationReleaseID: connected.Security.Policy.ApplicationReleaseID, MatrixID: connected.Security.Policy.MatrixID, Records: []model.NativeRecord{{Occurrence: &record}}}
			raw, err := batch.Canonical()
			require.NoError(t, err)
			_, err = ss.ExamAttempt().AppendNativeDelivery(ctx, &store.NativeDeliveryAppend{Access: native, Batch: batch, DesktopBuild: build, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}, examCommand(fixture.candidate.ID, store.NativeDeliveryAppendOperation, key, model.SHA256Fingerprint(raw)))
			return err
		}
		require.NoError(t, appendBatch(2, "progress-pending"))
		func() {
			restore := corrupt(connected.Participation.ID, "", "native_progress")
			defer restore()
			require.ErrorIs(t, appendBatch(1, "progress-repair"), store.ErrInvalidState)
		}()
		require.NoError(t, appendBatch(1, "progress-repair"))
		require.NoError(t, appendBatch(4, "transition-pending"))
		declaration := model.DeclareDeliveryGaps{DeclarationID: model.NewID(), AllocatedThroughSequence: 4, Ranges: []model.SequenceRange{{First: 3, Last: 3}}, Reason: "spool_lost"}
		settle := func() error {
			_, err := ss.ExamAttempt().DeclareNativeDeliveryGaps(ctx, &store.NativeDeliveryGapDeclaration{Access: native, Declaration: declaration, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}, examCommand(fixture.candidate.ID, store.NativeDeliveryGapsOperation, "transition-settle", "transition-settle"))
			return err
		}
		func() {
			restore := corrupt(connected.Participation.ID, "", "native_pending_transition")
			defer restore()
			require.ErrorIs(t, settle(), store.ErrInvalidState)
		}()
		require.NoError(t, settle())
	})
	for _, field := range []string{"native_binding", "native_summary", "browser_receipt", "browser_closure"} {
		t.Run(field, func(t *testing.T) {
			restore := corrupt(connected.Participation.ID, source, field)
			defer restore()
			var err error
			if field == "browser_receipt" {
				_, err = ss.ExamAttempt().BrowserDeliveryReceipts(ctx, browser, 1, 1)
			} else if field == "browser_closure" {
				_, err = ss.ExamAttempt().BrowserSourceStatus(ctx, browser)
			} else {
				_, err = ss.ExamAttempt().NativeDeliveryStatus(ctx, native)
			}
			require.ErrorIs(t, err, store.ErrInvalidState)
		})
	}
	// A valid append that repairs a gap must fail internally if an already
	// retained pending record cannot be interpreted; the repair must roll back.
	input.Events = []model.BrowserActivityEvent{browserActivityEvent(3, model.BrowserActivityOpened, fixture.revisionID)}
	_, err = appendBrowserActivityFixture(t, ctx, ss, input)
	require.NoError(t, err)
	restore := corrupt(connected.Participation.ID, source, "browser_pending_record")
	input.Events = []model.BrowserActivityEvent{browserActivityEvent(2, model.BrowserActivityOpened, fixture.revisionID)}
	_, err = appendBrowserActivityFixture(t, ctx, ss, input)
	restore()
	require.ErrorIs(t, err, store.ErrInvalidState)
	_, err = appendBrowserActivityFixture(t, ctx, ss, input)
	require.NoError(t, err)

	policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/corrected", HostMatch: model.BrowserPolicyHostExact, AllowRedirects: true, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord}})
	require.NoError(t, err)
	corrected, err := ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: fixture.examID, SittingID: fixture.sitting.ID, CurrentRevisionID: fixture.revisionID, ExpectedSittingRevision: fixture.sitting.Revision, ActorUserID: fixture.manager.ID, Resources: []store.ExamCorrectionResourceManifestItem{}, BrowserPolicy: &policy, CandidateSummary: "Updated browser references", AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser}, PrivateReason: "Validate retained source boundary", AppliedAt: model.NowUTC(), AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()}, examCommand(fixture.manager.ID, "exam.sitting.correction.apply", "corrupt-predecessor-correction", "corrupt-predecessor-correction"))
	require.NoError(t, err)
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	require.NoError(t, err)
	successor := &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: browserSourceID(19902), PolicyRevisionID: corrected.Revision.ID, PolicyDigest: presentation.RuntimeCapabilities.Browser.PolicyDigest, Transition: model.BrowserStartTransition{Kind: "policy_correction", PredecessorSourceSessionID: source}}
	for _, field := range []string{"browser_predecessor_malformed", "browser_predecessor_incomplete"} {
		successor.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, browser)
		successor.AuditAt = model.NowUTC()
		restore := corrupt(connected.Participation.ID, source, field)
		_, err = ss.ExamAttempt().StartBrowserActivity(ctx, successor)
		restore()
		require.ErrorIs(t, err, store.ErrInvalidState, "%s permitted succession or misclassified corruption", field)
		sources, err := ss.ExamAttempt().BrowserSourceList(ctx, browser)
		require.NoError(t, err)
		require.Len(t, sources, 1, "invalid predecessor consumed a successor reservation")
		require.Equal(t, int64(model.BrowserCorrectionStartLimit), sources[0].RemainingCorrectionStarts, "invalid predecessor consumed a successor reservation")
	}
	successor.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, browser)
	successor.AuditAt = model.NowUTC()
	next, err := ss.ExamAttempt().StartBrowserActivity(ctx, successor)
	require.NoError(t, err)
	require.Equal(t, int64(model.BrowserCorrectionStartLimit-1), next.RemainingCorrectionStarts, "restored predecessor could not create exactly one successor")

}
