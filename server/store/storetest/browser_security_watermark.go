// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package storetest

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestBrowserSecurityWatermarks(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-watermarks")
	first, second := browserSourceID(8001), browserSourceID(8002)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: first})
	requireNoError(t, err)
	renewal := &store.ExamAttemptParticipationRenewal{AttemptID: access.AttemptID, ParticipationID: connected.Participation.ID, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash, Generation: connected.Participation.Generation, DesktopCompatibilityPolicyRevision: 1}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	control := renewal.SecurityCoverage
	control.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(first), AllocatedThroughSequence: 3}}
	update := func(body model.SecurityCoverageRenewal) (model.SecurityCoverageResult, error) {
		return ss.ExamAttempt().UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, DesktopBuild: renewal.DesktopBuild, DesktopCompatibilityPolicyRevision: 1, Coverage: body})
	}
	result, err := update(control)
	requireNoError(t, err)
	if !result.SecurityInteractionAllowed || len(result.DeliveryWatermarkRejections) != 0 {
		t.Fatalf("healthy watermark rejected: %#v", result)
	}
	budget := func() *model.DeliveryBudgetSnapshot {
		value, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
		requireNoError(t, err)
		return value
	}
	if budget().Browser.Participation.AllocatedPositions != 3 {
		t.Fatal("watermark allocation was not charged")
	}
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID, SourceSessionID: first})
	requireNoError(t, err)
	if status.HighestSeen != 0 || status.HighestContiguous != 0 || status.AllocatedThrough != 3 || len(status.MissingRanges) != 1 {
		t.Fatal("allocation fabricated receipt")
	}
	bad := control
	bad.ControlSequence = 2
	bad.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(first), AllocatedThroughSequence: 4, AcknowledgedThroughSequence: 1}}
	if _, err := update(bad); err == nil {
		t.Fatal("client fabricated contiguous receipt")
	}
	bad.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "native", SourceID: connected.Security.DeliveryStreamID, AllocatedThroughSequence: 20}, {Family: "browser", SourceID: string(browserSourceID(8999)), AllocatedThroughSequence: 5}}
	if _, err := update(bad); err == nil {
		t.Fatal("foreign source accepted")
	}
	if budget().Native.Participation.AllocatedPositions != 0 {
		t.Fatal("foreign selector partially applied native watermark")
	}
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: second, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: first, Reason: model.BrowserSourceResetSpoolUnavailable}})
	requireNoError(t, err)
	replay, err := update(control)
	requireNoError(t, err)
	if replay.CoverageResult != "accepted" || len(replay.DeliveryWatermarkRejections) != 0 || budget().Browser.Participation.AllocatedPositions != 3 {
		t.Fatal("exact replay reinterpreted closed source")
	}
	control.ControlSequence = 2
	control.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(first), AllocatedThroughSequence: 40}, {Family: "browser", SourceID: string(second), AllocatedThroughSequence: 2}}
	result, err = update(control)
	requireNoError(t, err)
	if !result.SecurityInteractionAllowed || len(result.DeliveryWatermarkRejections) != 1 || result.DeliveryWatermarkRejections[0].SourceID != string(first) || result.DeliveryWatermarkRejections[0].Reason != "closed_source" || budget().Browser.Participation.AllocatedPositions != 5 {
		t.Fatalf("closed source rejection affected coverage: %#v", result)
	}
	closedControl := control
	control.ControlSequence = 3
	control.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(second), AllocatedThroughSequence: model.BrowserParticipationPositionLimit}}
	result, err = update(control)
	requireNoError(t, err)
	b := budget()
	if !result.SecurityInteractionAllowed || len(result.DeliveryWatermarkRejections) != 1 || result.DeliveryWatermarkRejections[0].Reason != "position_limit" || !b.Browser.Participation.SummaryOnly || b.Browser.Participation.AllocatedPositions != 5 || b.Native.Participation.SummaryOnly {
		t.Fatalf("position rejection altered security or charged refused tail: %#v %#v", result, b)
	}
	replay, err = update(closedControl)
	requireNoError(t, err)
	if replay.ProcessedControlSequence != 3 || !replay.SecurityInteractionAllowed || len(replay.DeliveryWatermarkRejections) != 1 || replay.DeliveryWatermarkRejections[0].SourceID != string(first) || replay.DeliveryWatermarkRejections[0].Reason != "closed_source" {
		t.Fatal("compact receipt lost immutable source identity")
	}
	// A newly observed allocation still cannot move the source's frozen boundary.
	status, err = ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID, SourceSessionID: first})
	requireNoError(t, err)
	if status.Closure.KnownAtClose == nil || *status.Closure.KnownAtClose != 3 {
		t.Fatal("late watermark moved closed boundary")
	}
}
