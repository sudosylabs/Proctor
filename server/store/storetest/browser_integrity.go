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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestBrowserIntegrityStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/", HostMatch: model.BrowserPolicyHostExact, AllowRedirects: false, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationIntegrityEvidence}})
	requireNoError(t, err)
	f := newExamAttemptFixtureWithBrowserPolicy(t, ctx, ss, &policy)
	connected, focus := connectFocusLossFixture(t, ctx, ss, f, "browser-evidence")
	access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID, ContinuityCredentialHash: focus.ContinuityCredentialHash}
	source := browserSourceID(9001)
	status, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
	requireNoError(t, err)
	rule := "start"
	reason := model.BrowserBlockRedirectNotAllowed
	prior := int64(1)
	nav := browserActivityEvent(1, model.BrowserActivityTopNavigation, f.revisionID)
	nav.MatchedRuleID = &rule
	nav.Location = &model.BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/"}
	blocked := func(seq int64) model.BrowserActivityEvent {
		v := browserActivityEvent(seq, model.BrowserActivityBlockedNavigation, f.revisionID)
		v.MatchedRuleID = &rule
		v.BlockReason = &reason
		v.RedirectFromSequence = &prior
		v.Location = &model.BrowserLocation{Scheme: "https", Host: "elsewhere.example", Path: "/private"}
		return v
	}
	prefix := "https://elsewhere.example/"
	expanded, err := model.CanonicalizeBrowserLocation(prefix + strings.Repeat("漢", model.BrowserNavigationMaximumCharacters-len(prefix)))
	requireNoError(t, err)
	events := []model.BrowserActivityEvent{nav}
	for seq := int64(2); seq <= 102; seq++ {
		event := blocked(seq)
		if seq == 2 {
			event.Location = &expanded
		}
		events = append(events, event)
	}
	for from := 0; from < len(events); from += 64 {
		end := min(from+64, len(events))
		_, err := appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: events[from:end]})
		requireNoError(t, err)
	}
	budget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if budget.BrowserFlagGroups != 1 || budget.BrowserEvidenceRecords != 100 || budget.BrowserEvidenceBytes < 1 {
		t.Fatalf("bounded evidence budget=%#v", budget)
	}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "browser-evidence-submit", "browser-evidence-submit"))
	requireNoError(t, err)
	access.ConnectionID = ""
	access.ContinuityCredentialHash = ""
	historical := store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID, SourceSessionID: source}
	_, err = ss.ExamAttempt().SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: historical, Declaration: model.FinalDeliveryDeclaration{DeclarationID: "browser-evidence-final", FinalSequence: 103}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, historical), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.BrowserDeliveryFinalOperation, "browser-evidence-final", "browser-evidence-final"))
	requireNoError(t, err)
	reviews := ss.ExamIntegrityReview()
	sub := sealed.Receipt.SubmissionID
	flags, err := reviews.ListFlags(ctx, store.ExamIntegrityFlagListOptions{SubmissionID: sub, Limit: 200})
	requireNoError(t, err)
	if len(flags.Items) != 1 || flags.Items[0].Browser == nil || flags.Items[0].EvidenceCount != 100 || flags.Items[0].OverflowCount != 1 {
		t.Fatalf("group projection=%#v", flags)
	}
	details, err := reviews.ListEvidence(ctx, store.ExamIntegrityEvidenceListOptions{SubmissionID: sub, FlagID: flags.Items[0].Flag.ID, Limit: 100})
	requireNoError(t, err)
	if len(details.Items) != 100 || details.Items[0].Browser == nil || details.Items[0].Validate() != nil {
		t.Fatal("bounded independent evidence copies unavailable")
	}
	foundExpanded := false
	for _, detail := range details.Items {
		if detail.Browser == nil || detail.Browser.PolicyDigest != status.PolicyDigest {
			t.Fatal("evidence lost frozen policy digest")
		}
		if detail.Browser != nil && detail.Browser.Event.Location != nil && detail.Browser.Event.Location.Path == expanded.Path {
			foundExpanded = true
		}
	}
	if !foundExpanded {
		t.Fatal("large Unicode navigation was not copied into integrity evidence")
	}
	reviewID := model.NewSubmissionReviewID()
	audit := func() string {
		return saveIntegrityReviewAudit(t, ctx, ss, f, sub, model.ActionSubmissionReview).ID.String()
	}
	decision := &store.ExamIntegrityReviewDecisionMutation{SubmissionID: sub, ReviewID: reviewID, DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flags.Items[0].Flag.ID, ActorUserID: f.manager.ID, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "Review the verified browser event inventory.", ChangedAt: model.NowUTC(), AuditEventID: audit(), AuditAt: model.GetMillis()}
	decided, err := reviews.SaveDecision(ctx, decision, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "browser-decision", "browser-decision"))
	requireNoError(t, err)
	finalize := &store.ExamIntegrityReviewFinalize{SubmissionID: sub, ReviewID: reviewID, ActorUserID: f.manager.ID, ExpectedReviewRevision: decided.Review.Revision, ChangedAt: model.NowUTC(), AuditEventID: audit(), AuditAt: model.GetMillis()}
	finalized, err := reviews.Finalize(ctx, finalize, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "browser-finalize", "browser-finalize"))
	requireNoError(t, err)
	release := func(revision int64, key string) {
		prepared, err := reviews.PrepareRelease(ctx, sub, reviewID, revision)
		requireNoError(t, err)
		input := &store.ExamIntegrityReviewRelease{SubmissionID: sub, ReviewID: reviewID, ActorUserID: f.manager.ID, ExpectedReviewRevision: revision, ChangedAt: prepared.ReleaseAt, AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, sub, model.ActionSubmissionRelease).ID.String(), AuditAt: prepared.ReleaseAt.UnixMilli()}
		attachResultReleaseMail(t, f.candidate, input)
		_, err = reviews.Release(ctx, input, examCommand(f.manager.ID, store.ExamIntegrityReviewReleaseOperation, key, key))
		requireNoError(t, err)
	}
	release(finalized.Review.Revision, "browser-first-release")
	batch := &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, PolicyRevisionID: status.PolicyRevisionID, PolicyDigest: status.PolicyDigest, Events: []model.BrowserActivityEvent{blocked(103)}, AuditEventID: browserDeliveryAuditFixture(t, ctx, ss, historical), AuditAt: model.GetMillis()}
	command := examCommand(f.candidate.ID, store.BrowserDeliveryAppendOperation, "browser-evidence-late", "browser-evidence-late")
	// Two independently idempotent requests racing on one event sequence must
	// produce one copy/overflow increment and one review invalidation.
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for i := range 2 {
		copy := *batch
		copy.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, historical)
		key := command
		if i == 1 {
			key = examCommand(f.candidate.ID, store.BrowserDeliveryAppendOperation, "browser-evidence-concurrent", "browser-evidence-concurrent")
		}
		go func() {
			<-start
			_, err := ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, &copy, key)
			outcomes <- err
		}()
	}
	close(start)
	for range 2 {
		requireNoError(t, <-outcomes)
	}
	current, err := reviews.Get(ctx, sub)
	requireNoError(t, err)
	if current.Review.State != model.SubmissionReviewDraft || current.Review.Revision <= finalized.Review.Revision || len(current.Decisions) != 1 || !current.Decisions[0].InventoryStale {
		t.Fatalf("late overflow inherited finalized decision=%#v", current)
	}
	if _, err := reviews.GetReleasedStudentResult(ctx, connected.Attempt.ID, f.candidate.ID); !store.IsNotFound(err) {
		t.Fatalf("stale released result remained visible: %v", err)
	}
	revision := current.Review.Revision
	batch.AuditEventID = browserDeliveryAuditFixture(t, ctx, ss, historical)
	_, err = ss.ExamAttempt().AppendHistoricalBrowserDelivery(ctx, batch, command)
	requireNoError(t, err)
	current, err = reviews.Get(ctx, sub)
	requireNoError(t, err)
	if current.Review.Revision != revision {
		t.Fatal("exact receipt replay invalidated review again")
	}
	finalize.ExpectedReviewRevision = revision
	finalize.ChangedAt = model.NowUTC()
	finalize.AuditEventID = audit()
	if _, err := reviews.Finalize(ctx, finalize, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "browser-stale-finalize", "browser-stale-finalize")); err == nil {
		t.Fatal("stale evidence decision finalized")
	}
	decision.ExpectedReviewRevision = revision
	decision.ExpectedDecisionRevision = 1
	decision.ChangedAt = model.NowUTC()
	decision.AuditEventID = audit()
	decided, err = reviews.SaveDecision(ctx, decision, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "browser-redecision", "browser-redecision"))
	requireNoError(t, err)
	finalize.ExpectedReviewRevision = decided.Review.Revision
	finalize.ChangedAt = model.NowUTC()
	finalize.AuditEventID = audit()
	refinalized, err := reviews.Finalize(ctx, finalize, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "browser-refinalize", "browser-refinalize"))
	requireNoError(t, err)
	release(refinalized.Review.Revision, "browser-second-release")
	finalBudget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if finalBudget.BrowserEvidenceBytes != budget.BrowserEvidenceBytes || finalBudget.BrowserEvidenceRecords != 100 {
		t.Fatal("overflow or replay copied more details")
	}
	flags, err = reviews.ListFlags(ctx, store.ExamIntegrityFlagListOptions{SubmissionID: sub, Limit: 200})
	requireNoError(t, err)
	if flags.Items[0].OverflowCount != 2 {
		t.Fatal("overflow counted rejected bytes or replay")
	}
}

// Exercise all 256 real groups across frozen policy revisions rather than
// fabricating a full counter. The next qualifying event remains count-only.
func TestBrowserIntegrityGroupCapacity(t *testing.T, ss store.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	policyFor := func(round int) model.BrowserPolicy {
		rules := make([]model.BrowserPolicyRule, 128)
		for i := range rules {
			rules[i] = model.BrowserPolicyRule{RuleID: fmt.Sprintf("rule-%03d", i), Origin: fmt.Sprintf("https://ref%d.example.edu", i), PathPrefix: fmt.Sprintf("/round-%d", round), HostMatch: model.BrowserPolicyHostExact, AllowRedirects: false, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationIntegrityEvidence}
		}
		p, err := model.NewBrowserPolicy(true, rules[0].RuleID, rules)
		requireNoError(t, err)
		return p
	}
	policy := policyFor(0)
	f := newExamAttemptFixtureWithBrowserPolicy(t, ctx, ss, &policy)
	connected, focus := connectFocusLossFixture(t, ctx, ss, f, "group-capacity")
	access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID, ContinuityCredentialHash: focus.ContinuityCredentialHash}
	renewal := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1, AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, Generation: connected.Participation.Generation, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ContinuityCredentialHash: focus.ContinuityCredentialHash}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	renew := func() {
		renewal.Sequence++
		_, err := ss.ExamAttempt().RenewParticipation(ctx, renewal)
		requireNoError(t, err)
	}
	var predecessor model.BrowserSourceSessionID
	for round := range 3 {
		renew()
		if round > 0 {
			policy = policyFor(round)
			key := fmt.Sprintf("group-correction-%d", round)
			corrected, err := ss.ExamCorrection().Apply(ctx, &store.ExamCorrectionApplication{RevisionID: model.NewExamRevisionID(), ExamID: f.examID, SittingID: f.sitting.ID, CurrentRevisionID: f.revisionID, ExpectedSittingRevision: f.sitting.Revision, ActorUserID: f.manager.ID, Resources: []store.ExamCorrectionResourceManifestItem{}, BrowserPolicy: &policy, CandidateSummary: "Updated browser references", AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser}, PrivateReason: "Verify bounded Browser groups across revisions", AppliedAt: model.NowUTC(), AuditEventID: saveExamSittingAudit(t, ctx, ss, f.manager.ID, f.examID, f.unitID).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, "exam.sitting.correction.apply.v1", key, key))
			requireNoError(t, err)
			f.revisionID = corrected.Revision.ID
			f.sitting = corrected.Sitting.Sitting
		}
		source := browserSourceID(9100 + round)
		transition := model.BrowserStartTransition{Kind: "initial"}
		if round > 0 {
			transition = model.BrowserStartTransition{Kind: "policy_correction", PredecessorSourceSessionID: predecessor}
		}
		_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Transition: transition})
		requireNoError(t, err)
		events := []model.BrowserActivityEvent{}
		count := 128
		if round == 2 {
			count = 1
		}
		for i := 0; i < count; i++ {
			rule := policy.Rules[i].RuleID
			sequence := int64(i*2 + 1)
			nav := browserActivityEvent(sequence, model.BrowserActivityTopNavigation, f.revisionID)
			nav.MatchedRuleID = &rule
			nav.Location = &model.BrowserLocation{Scheme: "https", Host: fmt.Sprintf("ref%d.example.edu", i), Path: fmt.Sprintf("/round-%d", round)}
			blocked := browserActivityEvent(sequence+1, model.BrowserActivityBlockedNavigation, f.revisionID)
			reason := model.BrowserBlockRedirectNotAllowed
			blocked.MatchedRuleID = &rule
			blocked.BlockReason = &reason
			blocked.RedirectFromSequence = &sequence
			blocked.Location = &model.BrowserLocation{Scheme: "https", Host: "outside.example", Path: "/"}
			events = append(events, nav, blocked)
		}
		for from := 0; from < len(events); from += 64 {
			renew()
			_, err = appendBrowserActivityFixture(t, ctx, ss, &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: events[from:min(from+64, len(events))]})
			requireNoError(t, err)
		}
		predecessor = source
		// Allow the real shared two-per-second bucket to replenish between rounds.
		if round < 2 {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	budget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
	requireNoError(t, err)
	if budget.BrowserFlagGroups != 256 || budget.BrowserEvidenceRecords != 256 {
		t.Fatalf("group limit=%#v", budget)
	}
	seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	attachSubmissionReceipt(t, f.candidate, seal)
	sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "group-seal", "group-seal"))
	requireNoError(t, err)
	snapshot, err := ss.ExamIntegrityReview().Get(ctx, sealed.Receipt.SubmissionID)
	requireNoError(t, err)
	if snapshot.BrowserEvidenceOverflow == nil || snapshot.BrowserEvidenceOverflow.ValidatedEventCount != 1 || snapshot.BrowserEvidenceOverflow.Reason != "group_capacity" {
		t.Fatalf("missing finite group overflow=%#v", snapshot.BrowserEvidenceOverflow)
	}
}

// The probe advances only lifetime counters to an exact boundary. Public intake,
// immutable copies, overflow, sealing and Review finalization remain real.
func TestBrowserIntegrityCopyBoundaries(t *testing.T, ss store.Store, seed func(*testing.T, context.Context, model.ExamAttemptID, int64, int64)) {
	for caseIndex, mode := range []string{"record limit", "exact byte limit", "already full"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: "https://example.edu", PathPrefix: "/", HostMatch: model.BrowserPolicyHostExact, AllowRedirects: false, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationIntegrityEvidence}})
			requireNoError(t, err)
			f := newExamAttemptFixtureWithBrowserPolicy(t, ctx, ss, &policy)
			connected, focus := connectFocusLossFixture(t, ctx, ss, f, "copy-boundary")
			access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID, ContinuityCredentialHash: focus.ContinuityCredentialHash}
			source := browserSourceID(9300 + caseIndex)
			_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source})
			requireNoError(t, err)
			rule, reason, prior := "start", model.BrowserBlockRedirectNotAllowed, int64(1)
			nav := browserActivityEvent(1, model.BrowserActivityTopNavigation, f.revisionID)
			nav.MatchedRuleID = &rule
			nav.Location = &model.BrowserLocation{Scheme: "https", Host: "example.edu", Path: "/"}
			blocked := browserActivityEvent(2, model.BrowserActivityBlockedNavigation, f.revisionID)
			blocked.MatchedRuleID = &rule
			blocked.BlockReason = &reason
			blocked.RedirectFromSequence = &prior
			blocked.Location = &model.BrowserLocation{Scheme: "https", Host: "outside.example", Path: "/"}
			digest, err := model.BrowserPolicyDigest(policy)
			requireNoError(t, err)
			raw, err := json.Marshal(model.BrowserIntegrityEvidence{SourceSessionID: source, PolicyRevisionID: f.revisionID, PolicyDigest: digest, RuleID: rule, Event: blocked})
			requireNoError(t, err)
			records, bytes := int64(0), int64(0)
			if mode == "record limit" {
				records = model.BrowserEvidenceRecordLimit - 1
			}
			if mode == "exact byte limit" {
				bytes = model.BrowserEvidenceByteLimit - int64(len(raw))
			}
			if mode == "already full" {
				bytes = model.BrowserEvidenceByteLimit
			}
			seed(t, ctx, connected.Attempt.ID, records, bytes)
			last := blocked
			last.Sequence = 3
			batch := &store.BrowserActivityAppend{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: source, Events: []model.BrowserActivityEvent{nav, blocked, last}}
			_, err = appendBrowserActivityFixture(t, ctx, ss, batch)
			requireNoError(t, err)
			budget, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
			requireNoError(t, err)
			copies := int64(1)
			if mode == "already full" {
				copies = 0
			}
			if budget.BrowserEvidenceRecords != records+copies || budget.BrowserEvidenceBytes != bytes+copies*int64(len(raw)) {
				t.Fatalf("copied evidence crossed %s: %#v", mode, budget)
			}
			_, err = appendBrowserActivityFixture(t, ctx, ss, batch)
			requireNoError(t, err)
			seal := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: access.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.revisionID, ExpectedWorkspaceCursor: connected.Workspace.Cursor}, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
			attachSubmissionReceipt(t, f.candidate, seal)
			sealed, err := ss.ExamSubmission().Seal(ctx, seal, examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "copy-seal", "copy-seal"))
			requireNoError(t, err)
			sub := sealed.Receipt.SubmissionID
			flags, err := ss.ExamIntegrityReview().ListFlags(ctx, store.ExamIntegrityFlagListOptions{SubmissionID: sub, Limit: 100})
			requireNoError(t, err)
			if len(flags.Items) != 1 || int64(flags.Items[0].EvidenceCount) != copies || flags.Items[0].OverflowCount != 2-copies {
				t.Fatalf("bounded copy/overflow inventory = %#v", flags)
			}
			decision, err := ss.ExamIntegrityReview().SaveDecision(ctx, &store.ExamIntegrityReviewDecisionMutation{SubmissionID: sub, ReviewID: model.NewSubmissionReviewID(), DecisionID: model.NewIntegrityReviewDecisionID(), FlagID: flags.Items[0].Flag.ID, ActorUserID: f.manager.ID, Outcome: model.IntegrityReviewInconclusive, PrivateRationale: "Explicitly acknowledge bounded count-only evidence", ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, sub, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewDecisionOperation, "copy-decision", "copy-decision"))
			requireNoError(t, err)
			_, err = ss.ExamIntegrityReview().Finalize(ctx, &store.ExamIntegrityReviewFinalize{SubmissionID: sub, ReviewID: decision.Review.ID, ActorUserID: f.manager.ID, ExpectedReviewRevision: decision.Review.Revision, ChangedAt: model.NowUTC(), AuditEventID: saveIntegrityReviewAudit(t, ctx, ss, f, sub, model.ActionSubmissionReview).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.manager.ID, store.ExamIntegrityReviewFinalizeOperation, "copy-finalize", "copy-finalize"))
			requireNoError(t, err)
		})
	}
}

// Fill a valid single-event request to its exact wire ceiling. Evidence adds
// frozen provenance to that event and must use its own larger retained bound.
func maximumBrowserEvidenceEvent(t *testing.T, event model.BrowserActivityEvent, source model.BrowserSourceSessionID, part model.AttemptParticipationID, generation int64, digest string) model.BrowserActivityEvent {
	t.Helper()
	raw, err := json.Marshal(event)
	requireNoError(t, err)
	var fields map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(raw, &fields))
	prefix := event.ClientOccurredAt.UTC().Format("2006-01-02T15:04:05.000")
	fields["client_occurred_at"], err = json.Marshal(prefix + "Z")
	requireNoError(t, err)
	raw, err = json.Marshal(fields)
	requireNoError(t, err)
	requireNoError(t, json.Unmarshal(raw, &event))
	batch := model.BrowserActivityBatch{SourceSessionID: source, ParticipationID: part, Generation: generation, PolicyRevisionID: event.PolicyRevisionID, PolicyDigest: digest, Events: []model.BrowserActivityEvent{event}}
	encoded, err := json.Marshal(batch)
	requireNoError(t, err)
	fields["client_occurred_at"], err = json.Marshal(prefix + strings.Repeat("0", model.BrowserActivityAppendMaximumBytes-len(encoded)) + "Z")
	requireNoError(t, err)
	raw, err = json.Marshal(fields)
	requireNoError(t, err)
	requireNoError(t, json.Unmarshal(raw, &event))
	batch.Events[0] = event
	encoded, err = json.Marshal(batch)
	requireNoError(t, err)
	if len(encoded) != model.BrowserActivityAppendMaximumBytes || batch.Validate() != nil {
		t.Fatal("maximum Browser fixture not at valid request limit")
	}
	return event
}
