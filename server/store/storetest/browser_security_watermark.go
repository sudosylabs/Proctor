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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestBrowserSecurityWatermarks(t *testing.T, ss store.Store) {
	ctx := context.Background()
	_, connected, access := newBrowserActivityFixture(t, ctx, ss, "browser-watermarks")
	first, second := browserSourceID(8001), browserSourceID(8002)
	_, err := startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: first})
	require.NoError(t, err)
	renewal := &store.ExamAttemptParticipationRenewal{AttemptID: access.AttemptID, ParticipationID: connected.Participation.ID, ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint, ContinuityCredentialHash: access.ContinuityCredentialHash, Generation: connected.Participation.Generation, DesktopCompatibilityPolicyRevision: 1}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	control := renewal.SecurityCoverage
	control.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(first), AllocatedThroughSequence: 3}}
	update := func(body model.SecurityCoverageRenewal) (model.SecurityCoverageResult, error) {
		return ss.ExamAttempt().UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, DesktopBuild: renewal.DesktopBuild, DesktopCompatibilityPolicyRevision: 1, Coverage: body})
	}
	result, err := update(control)
	require.NoError(t, err)
	require.True(t, result.SecurityInteractionAllowed)
	require.Empty(t, result.DeliveryWatermarkRejections)
	budget := func() *model.DeliveryBudgetSnapshot {
		value, err := ss.ExamAttempt().DeliveryBudget(ctx, store.DeliveryBudgetAccess{Access: access, ParticipationID: connected.Participation.ID})
		require.NoError(t, err)
		return value
	}
	require.Equal(t, int64(3), budget().Browser.Participation.AllocatedPositions, "watermark allocation was not charged")
	status, err := ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID, SourceSessionID: first})
	require.NoError(t, err)
	require.Equal(t, int64(0), status.HighestSeen, "allocation fabricated receipt")
	require.Equal(t, int64(0), status.HighestContiguous, "allocation fabricated receipt")
	require.Equal(t, int64(3), status.AllocatedThrough, "allocation fabricated receipt")
	require.Len(t, status.MissingRanges, 1, "allocation fabricated receipt")
	bad := control
	bad.ControlSequence = 2
	bad.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(first), AllocatedThroughSequence: 4, AcknowledgedThroughSequence: 1}}
	_, err = update(bad)
	require.Error(t, err, "client fabricated contiguous receipt")
	bad.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "native", SourceID: connected.Security.DeliveryStreamID, AllocatedThroughSequence: 20}, {Family: "browser", SourceID: string(browserSourceID(8999)), AllocatedThroughSequence: 5}}
	_, err = update(bad)
	require.Error(t, err, "foreign source accepted")
	require.Equal(t, int64(0), budget().Native.Participation.AllocatedPositions, "foreign selector partially applied native watermark")
	_, err = startBrowserSourceFixture(t, ctx, ss, &store.BrowserActivitySourceStart{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, SourceSessionID: second, Transition: model.BrowserStartTransition{Kind: "runtime_reset", PredecessorSourceSessionID: first, Reason: model.BrowserSourceResetSpoolUnavailable}})
	require.NoError(t, err)
	replay, err := update(control)
	require.NoError(t, err)
	require.Equal(t, "accepted", replay.CoverageResult, "exact replay reinterpreted closed source")
	require.Empty(t, replay.DeliveryWatermarkRejections, "exact replay reinterpreted closed source")
	require.Equal(t, int64(3), budget().Browser.Participation.AllocatedPositions, "exact replay reinterpreted closed source")
	control.ControlSequence = 2
	control.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(first), AllocatedThroughSequence: 40}, {Family: "browser", SourceID: string(second), AllocatedThroughSequence: 2}}
	result, err = update(control)
	require.NoError(t, err)
	require.True(t, result.SecurityInteractionAllowed)
	require.Len(t, result.DeliveryWatermarkRejections, 1)
	require.Equal(t, string(first), result.DeliveryWatermarkRejections[0].SourceID)
	require.Equal(t, "closed_source", result.DeliveryWatermarkRejections[0].Reason)
	require.Equal(t, int64(5), budget().Browser.Participation.AllocatedPositions)
	closedControl := control
	control.ControlSequence = 3
	control.DeliveryWatermarks = []model.DeliveryWatermark{{Family: "browser", SourceID: string(second), AllocatedThroughSequence: model.BrowserParticipationPositionLimit}}
	result, err = update(control)
	require.NoError(t, err)
	b := budget()
	require.True(t, result.SecurityInteractionAllowed, "position rejection altered security or charged refused tail: %#v %#v", result, b)
	require.Len(t, result.DeliveryWatermarkRejections, 1, "position rejection altered security or charged refused tail: %#v %#v", result, b)
	require.Equal(t, "position_limit", result.DeliveryWatermarkRejections[0].Reason, "position rejection altered security or charged refused tail: %#v %#v", result, b)
	require.True(t, b.Browser.Participation.SummaryOnly, "position rejection altered security or charged refused tail: %#v %#v", result, b)
	require.Equal(t, int64(5), b.Browser.Participation.AllocatedPositions, "position rejection altered security or charged refused tail: %#v %#v", result, b)
	require.False(t, b.Native.Participation.SummaryOnly, "position rejection altered security or charged refused tail: %#v %#v", result, b)
	replay, err = update(closedControl)
	require.NoError(t, err)
	require.Equal(t, int64(3), replay.ProcessedControlSequence, "compact receipt lost immutable source identity")
	require.True(t, replay.SecurityInteractionAllowed, "compact receipt lost immutable source identity")
	require.Len(t, replay.DeliveryWatermarkRejections, 1, "compact receipt lost immutable source identity")
	require.Equal(t, string(first), replay.DeliveryWatermarkRejections[0].SourceID, "compact receipt lost immutable source identity")
	require.Equal(t, "closed_source", replay.DeliveryWatermarkRejections[0].Reason, "compact receipt lost immutable source identity")
	// A newly observed allocation still cannot move the source's frozen boundary.
	status, err = ss.ExamAttempt().BrowserSourceStatus(ctx, store.BrowserDeliveryAccess{Access: access, ParticipationID: connected.Participation.ID, SourceSessionID: first})
	require.NoError(t, err)
	require.NotNil(t, status.Closure.KnownAtClose, "late watermark moved closed boundary")
	require.Equal(t, int64(3), *status.Closure.KnownAtClose, "late watermark moved closed boundary")
}
