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
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamAttemptSQLProbe struct {
	SetParticipationLeaseExpired func(*testing.T, context.Context, model.AttemptParticipationID)
	FenceRenewalPastDeadline     func(*testing.T, context.Context, model.AttemptParticipationID, func() error) error
	UnresolvedFocusLossMissing   func(*testing.T, context.Context, model.ExamAttemptID, int64) int64
	AgeFocusLossPending          func(*testing.T, context.Context, model.ExamAttemptID, int64, int64, time.Duration)
	FocusLossPersistence         func(*testing.T, context.Context, model.ExamAttemptID, int64) FocusLossPersistenceProbe
	ConcurrentExamAttempt        store.ExamAttemptStore
	AssertFocusLossSchema        func(*testing.T, context.Context)
	// LeadDatabaseClock moves one Session's node-clock instants after the
	// database clock, as a creating node whose clock leads PostgreSQL does.
	LeadDatabaseClock func(*testing.T, context.Context, model.SessionID)
}

type DesktopCompatibilityPolicySerializationProbe func(
	*testing.T,
	context.Context,
	func() error,
) error

// FocusLossPersistenceProbe exposes bounded persistence totals that cannot be
// observed through the candidate seam. It is used only by SQL conformance to
// prove the evidence cap, overflow summary, bucket consumption, and diagnostic
// bound without making those private details part of the production Store.
type FocusLossPersistenceProbe struct {
	Flags, Evidence, Pending int
	OverflowCount            int64
	DiagnosticCount          int64
}

func prepareExamAttemptConnect(t *testing.T, ctx context.Context, ss store.Store, input *store.ExamAttemptConnect) {
	t.Helper()
	require.NotNil(t, input, "Exam Attempt connect input is required")
	session, err := ss.Session().Get(ctx, input.SessionID)
	require.NoError(t, err)
	settings, err := ss.UserSettings().Get(ctx, input.CandidateUserID)
	require.NoError(t, err)
	manifest := model.CurrentAttemptConfigurationManifestFingerprint()
	configuration := model.AttemptConfigurationCandidate{ManifestFingerprint: manifest, RegistryFingerprint: "fnv1a64:" + strings.Repeat("b", 16), UserSettingsRevision: settings.Revision, DesktopBuild: session.DesktopBuildID, DesktopTarget: string(session.DesktopPlatform) + "-" + string(session.DesktopArchitecture), Presentation: model.AttemptConfigurationPresentation{ColorTheme: "dark", ZoomPercent: 100, EditorFontSizePX: 14, EditorLineHeightPX: 22, ScreenReaderMode: "auto", AnnouncementMode: "auto", CursorStyle: "line", CursorBlinking: "blink"}, ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}
	err = configuration.Validate()
	require.NoError(t, err)
	input.ConfigurationManifestFingerprint = manifest
	input.InitialConfiguration = &configuration
	input.DesktopBuild = model.DesktopBuildTuple{DesktopRelease: session.DesktopRelease, DesktopBuildID: session.DesktopBuildID,
		Platform: session.DesktopPlatform, Architecture: session.DesktopArchitecture, RealtimeProtocol: session.DesktopRealtimeProtocol,
		AttemptConfigurationManifestFingerprint: manifest,
		DesktopSettingsRegistryFingerprint:      configuration.RegistryFingerprint,
		DesktopTarget:                           configuration.DesktopTarget, ConfigurationManifest: model.EmptyAttemptConfigurationManifest(),
		CapabilityMatrixIdentity: "storetest-matrix"}
	policy, err := ss.DesktopCompatibilityPolicy().Get(ctx)
	require.NoError(t, err)
	input.DesktopCompatibilityPolicyRevision = policy.Revision
	prepareConnectSecurityFixture(t, ctx, ss, input)
}

func TestExamAttemptStore(t *testing.T, ss store.Store, probes ...ExamAttemptSQLProbe) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	alternateSession := saveRegisteredDesktopSession(t, ctx, ss, fixture.candidate.ID, fixture.institutionID)
	if len(probes) > 0 && probes[0].LeadDatabaseClock != nil {
		probes[0].LeadDatabaseClock(t, ctx, alternateSession.ID)
	}
	ineligible := saveUser(t, ctx, ss)
	ineligibleSession := saveRegisteredDesktopSession(t, ctx, ss, ineligible.ID, fixture.institutionID)
	ineligibleFixture := fixture
	ineligibleFixture.candidate, ineligibleFixture.session = ineligible, ineligibleSession
	ineligibleInput := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: ineligible.ID,
		SessionID: ineligibleSession.ID, DesktopRegistrationID: ineligibleSession.DesktopRegistrationID,
		DPoPKeyThumbprint: ineligibleSession.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, ineligibleFixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, ineligibleInput)
	_, err := ss.ExamAttempt().Connect(ctx, ineligibleInput,
		examCommand(ineligible.ID, store.ExamAttemptConnectOperation, "attempt-ineligible", "attempt-ineligible"))
	require.True(t, store.IsNotFound(err), "Connect(without current membership) error")
	_, err = ss.ExamAttempt().Get(ctx, fixture.examID, ineligibleInput.AttemptID)
	require.True(t, store.IsNotFound(err), "Get(ineligible proposed Attempt) error")
	configurationCandidate, configurationSession, configurationFixture := fixture.candidate, fixture.session, fixture
	configurationInput := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: configurationCandidate.ID,
		SessionID: configurationSession.ID, DesktopRegistrationID: configurationSession.DesktopRegistrationID,
		DPoPKeyThumbprint: configurationSession.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, configurationFixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, configurationInput)
	configurationInput.InitialConfiguration = nil
	_, err = ss.ExamAttempt().Connect(ctx, configurationInput,
		examCommand(configurationCandidate.ID, store.ExamAttemptConnectOperation, "attempt-config-missing", "attempt-config-missing"))
	requireConflictConstraint(t, err, "attempt_configuration_required")
	_, err = ss.ExamAttempt().Get(ctx, fixture.examID, configurationInput.AttemptID)
	require.True(t, store.IsNotFound(err), "missing configuration created partial Attempt")
	prepareExamAttemptConnect(t, ctx, ss, configurationInput)
	stale := configurationInput.InitialConfiguration.Clone()
	stale.UserSettingsRevision = model.NewUserSettingsRevision()
	require.NoError(t, stale.Validate())
	configurationInput.InitialConfiguration = &stale
	configurationInput.AttemptID = model.NewExamAttemptID()
	configurationInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, configurationFixture).ID
	_, err = ss.ExamAttempt().Connect(ctx, configurationInput,
		examCommand(configurationCandidate.ID, store.ExamAttemptConnectOperation, "attempt-config-stale", "attempt-config-stale"))
	requireConflictConstraint(t, err, "attempt_configuration_stale")
	_, err = ss.ExamAttempt().Get(ctx, fixture.examID, configurationInput.AttemptID)
	require.True(t, store.IsNotFound(err), "stale configuration created partial Attempt")
	credentialHash := model.HashToken(model.NewCredentialToken())
	input := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash}
	input.AuditEventID, input.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	prepareExamAttemptConnect(t, ctx, ss, input)
	stalePolicy := *input
	stalePolicy.DesktopCompatibilityPolicyRevision++
	stalePolicy.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	_, err = ss.ExamAttempt().Connect(ctx, &stalePolicy,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-stale-desktop-policy", "attempt-stale-desktop-policy"))
	requireConflictConstraint(t, err, "desktop_compatibility_policy_revision")
	command := examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-connect", "attempt-connect")

	connected, err := ss.ExamAttempt().Connect(ctx, input, command)
	require.NoError(t, err)
	require.False(t, connected.Replayed)
	require.True(t, connected.FirstAdmission)
	require.True(t, connected.ConnectionOpened)
	require.Equal(t, input.AttemptID, connected.Attempt.ID)
	require.Equal(t, input.WorkspaceID, connected.Workspace.ID)
	require.Equal(t, input.ParticipationID, connected.Participation.ID)
	require.Equal(t, input.ConnectionID, connected.Connection.ID)
	require.Equal(t, fixture.class.ID, connected.ClassID)
	require.Equal(t, int64(1), connected.Participation.Generation)
	require.Equal(t, int64(0), connected.Participation.RenewalSequence)
	require.Equal(t, model.AttemptParticipationInitialLease, connected.Participation.LeaseExpiresAt.Sub(connected.Participation.StartedAt))
	requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
	require.NoError(t, connected.Configuration.Validate(), "first admission did not freeze the complete proposed configuration")
	require.NotEmpty(t, connected.Configuration.Revision, "first admission did not freeze the complete proposed configuration")
	require.Equal(t, input.InitialConfiguration.UserSettingsRevision, connected.Configuration.UserSettingsRevision, "first admission did not freeze the complete proposed configuration")
	require.Equal(t, input.InitialConfiguration.DesktopBuild, connected.Configuration.DesktopBuild, "first admission did not freeze the complete proposed configuration")
	frozenDocument, err := connected.Configuration.CanonicalAdmission()
	require.NoError(t, err)
	settings, err := ss.UserSettings().Get(ctx, fixture.candidate.ID)
	require.NoError(t, err)
	nextSettingsRevision := model.NewUserSettingsRevision()
	changedSource := "{\"workbench.colorTheme\":\"hcLight\"}"
	_, err = ss.UserSettings().Replace(ctx, &store.UserSettingsReplacement{
		UserID: fixture.candidate.ID, Source: changedSource, FormatVersion: 1,
		ExpectedRevision: settings.Revision, NextRevision: nextSettingsRevision, UpdatedAt: settings.UpdatedAt.Add(time.Second),
		AuditEvent: userSettingsAudit(fixture.candidate.ID, fixture.institutionID, settings.Revision, nextSettingsRevision, len(changedSource)),
	}, userSettingsCommand(fixture.candidate.ID, "after-attempt-freeze", changedSource))
	require.NoError(t, err)
	connectedAudit, err := ss.Audit().Get(ctx, input.AuditEventID)
	require.NoError(t, err)
	require.False(t, bytes.Contains(connectedAudit.Result, []byte(credentialHash)), "Connect audit exposed the continuity credential hash")
	replayInput := *input
	replayInput.AuditEventID, replayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	replayed, err := ss.ExamAttempt().Connect(ctx, &replayInput, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.False(t, replayed.ConnectionOpened)
	require.Equal(t, connected.Attempt.ID, replayed.Attempt.ID)
	require.Equal(t, connected.Connection.ID, replayed.Connection.ID)
	replayDocument, err := replayed.Configuration.CanonicalAdmission()
	require.NoError(t, err)
	require.True(t, bytes.Equal(replayDocument, frozenDocument), "replay regenerated frozen configuration from newer User Settings")
	require.NotEqual(t, nextSettingsRevision, replayed.Configuration.UserSettingsRevision, "replay regenerated frozen configuration from newer User Settings")
	testExamAttemptParticipationRenewal(t, ctx, ss, fixture, connected, credentialHash)

	access := store.CandidateAttemptAccess{AttemptID: input.AttemptID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, ConnectionID: input.ConnectionID, ContinuityCredentialHash: credentialHash}
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	require.NoError(t, err)
	require.Equal(t, input.AttemptID, presentation.AttemptID)
	require.Equal(t, fixture.revisionID, presentation.RuntimeCapabilities.ExamRevision.AdmissionRevisionID)
	require.Equal(t, fixture.revisionID, presentation.RuntimeCapabilities.ExamRevision.CurrentRevisionID)
	require.True(t, presentation.RuntimeCapabilities.FocusLossCollectionEnabled)
	require.NotEmpty(t, connected.Configuration.Digest)
	require.Equal(t, connected.Configuration.Digest, presentation.RuntimeCapabilities.AttemptConfiguration.Digest)
	pausedFocusInput := testExamAttemptFocusLoss(t, ctx, ss, fixture, connected, credentialHash, probes...)
	page, err := ss.ExamAttemptWorkspace().List(ctx, store.CandidateWorkspaceListOptions{Access: access, ExpectedCursor: -1, Limit: 200})
	require.NoError(t, err)
	require.False(t, page.HasMore)
	require.Len(t, page.Items, 2)
	require.Equal(t, "cmd", page.Items[0].Path)
	require.Equal(t, "cmd/main.go", page.Items[1].Path)
	require.True(t, page.Items[0].ContentVersion.IsZero())
	require.True(t, page.Items[1].ContentVersion.IsValid())
	content, err := ss.ExamAttemptWorkspace().ResolveFile(ctx, access, page.Items[1].EntryID)
	require.NoError(t, err)
	require.False(t, content.StarterObjectID.IsZero())
	require.False(t, content.AttemptObjectID.IsZero())
	require.Equal(t, page.Items[1].ContentVersion, content.ContentVersion)
	paused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision,
		PrivateReason: "verify established candidate read access while paused", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
		examCommand(fixture.manager.ID, "exam.sitting.pause", "attempt-pause", "attempt-pause"))
	require.NoError(t, err)
	_, err = ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	require.NoError(t, err)
	pausedFocusInput.AuditEventID, pausedFocusInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	pausedFocus, err := ss.ExamAttempt().RecordFocusLoss(ctx, pausedFocusInput)
	require.NoError(t, err)
	require.Equal(t, pausedFocusInput.Sequence, pausedFocus.AcceptedSequence)
	require.True(t, pausedFocus.Qualified)
	require.Equal(t, 1, pausedFocus.WindowIncidentCount)
	_, err = ss.ExamAttemptWorkspace().List(ctx, store.CandidateWorkspaceListOptions{Access: access, ExpectedCursor: -1, Limit: 200})
	require.NoError(t, err)
	_, err = ss.ExamAttemptWorkspace().ResolveFile(ctx, access, page.Items[1].EntryID)
	require.NoError(t, err)
	pausedReplayInput := *input
	pausedReplayInput.AuditEventID, pausedReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	pausedReplay, err := ss.ExamAttempt().Connect(ctx, &pausedReplayInput, command)
	require.NoError(t, err)
	require.True(t, pausedReplay.Replayed)
	require.False(t, pausedReplay.ConnectionOpened)
	require.Equal(t, connected.Connection.ID, pausedReplay.Connection.ID)
	freshWhilePaused := *input
	freshWhilePaused.AttemptID, freshWhilePaused.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	freshWhilePaused.ParticipationID, freshWhilePaused.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	freshWhilePaused.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
	freshWhilePaused.AuditEventID, freshWhilePaused.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().Connect(ctx, &freshWhilePaused,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-connect-paused", "attempt-connect-paused"))
	var pausedConflict *store.ErrConflict
	require.ErrorAs(t, err, &pausedConflict)
	require.Equal(t, "exam_sitting_state", pausedConflict.Constraint)
	resumed, err := ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: paused.Value.Sitting.Revision,
		PrivateReason: "continue Attempt persistence conformance", ChangedAt: model.NowUTC(),
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
		examCommand(fixture.manager.ID, "exam.sitting.resume", "attempt-resume", "attempt-resume"))
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingOpen, resumed.Value.Sitting.State)
	openConnectionInput := *input
	openConnectionInput.Security = model.ConnectSecurity{Kind: "resume", ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, PolicyDigest: connected.Security.Policy.Digest}
	openConnectionInput.AttemptID, openConnectionInput.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	openConnectionInput.ParticipationID, openConnectionInput.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	openConnectionInput.AuditEventID, openConnectionInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	convergedOpen, err := ss.ExamAttempt().Connect(ctx, &openConnectionInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-open-connection", "attempt-open-connection"))
	require.NoError(t, err)
	require.False(t, convergedOpen.FirstAdmission)
	require.False(t, convergedOpen.ConnectionOpened)
	require.Equal(t, connected.Connection.ID, convergedOpen.Connection.ID)
	require.Equal(t, connected.Participation.ID, convergedOpen.Participation.ID)
	alternateSessionInput := openConnectionInput
	alternateSessionInput.SessionID = alternateSession.ID
	alternateSessionInput.DesktopRegistrationID = alternateSession.DesktopRegistrationID
	alternateSessionInput.DPoPKeyThumbprint = alternateSession.DPoPKeyThumbprint
	alternateSessionInput.AuditEventID, alternateSessionInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().Connect(ctx, &alternateSessionInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-open-other-session", "attempt-open-other-session"))
	require.True(t, store.IsNotFound(err), "Connect(from Session revoked at active-entry) error")
	revokedAlternate, err := ss.Session().Get(ctx, alternateSession.ID)
	require.NoError(t, err)
	require.True(t, revokedAlternate.RevokedAt.Valid)
	require.False(t, revokedAlternate.RevokedAt.Time.Before(revokedAlternate.CreatedAt))
	wrongSession := access
	wrongSession.SessionID = model.NewSessionID()
	_, err = ss.ExamAttempt().GetCandidatePresentation(ctx, wrongSession)
	require.True(t, store.IsNotFound(err), "candidate access from another Session error")

	manager, err := ss.ExamAttempt().Get(ctx, fixture.examID, input.AttemptID)
	require.NoError(t, err)
	require.Equal(t, input.AttemptID, manager.Attempt.ID)
	require.NotNil(t, manager.LatestParticipation)
	require.NotNil(t, manager.CurrentConnection)
	listed, err := ss.ExamAttempt().List(ctx, store.ExamAttemptManagerListOptions{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
		ExcludeCandidateUserID: fixture.manager.ID, Limit: 201})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, input.AttemptID, listed[0].Attempt.ID)
	activity, err := ss.ExamAttempt().ListCandidateActivity(ctx, store.CandidateExamActivityListOptions{
		CandidateUserID: fixture.candidate.ID, DesktopSessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		Limit: 50,
	})
	require.NoError(t, err)
	require.NotNil(t, activity)
	require.False(t, activity.ServerTime.IsZero())
	require.Len(t, activity.Items, 1)
	require.Equal(t, fixture.sitting.ID, activity.Items[0].SittingID)
	require.Equal(t, store.CandidateExamActivityInProgress, activity.Items[0].ActivityState)
	require.Equal(t, store.CandidateExamAccessResumable, activity.Items[0].AccessState)
	require.NotNil(t, activity.Items[0].Attempt)
	require.Equal(t, input.AttemptID, activity.Items[0].Attempt.ID)
	require.Len(t, activity.Items[0].AllowedActions, 1)
	require.Equal(t, store.CandidateExamActionResume, activity.Items[0].AllowedActions[0])
	statuses, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID,
		ReallowAuthorized: true, Limit: 50,
	})
	require.NoError(t, err)
	require.NotNil(t, statuses)
	require.False(t, statuses.ServerTime.IsZero())
	require.Equal(t, fixture.examID, statuses.ExamID)
	require.Equal(t, fixture.sitting.ID, statuses.SittingID)
	require.Equal(t, model.ExamSittingOpen, statuses.SittingState)
	require.Len(t, statuses.Items, 1)
	require.Equal(t, fixture.candidate.ID, statuses.Items[0].Candidate.UserID)
	require.NotNil(t, statuses.Items[0].Attempt)
	require.Equal(t, input.AttemptID, statuses.Items[0].Attempt.ID)
	require.Equal(t, store.SittingCandidateConnected, statuses.Items[0].Presence.State)
	require.True(t, statuses.Items[0].Presence.LastLeaseRenewedAt.Valid)
	require.True(t, statuses.Items[0].Presence.LeaseExpiresAt.Valid)
	testConcurrentExamAttemptAdmission(t, ctx, ss, fixture)

	closeInput := &store.ExamAttemptConnectionClose{ConnectionID: input.ConnectionID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, Reason: model.AttemptConnectionCloseTransport,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	foreignClose := *closeInput
	foreignClose.SessionID = model.NewSessionID()
	foreignClose.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	_, err = ss.ExamAttempt().CloseConnection(ctx, &foreignClose)
	require.True(t, store.IsNotFound(err), "CloseConnection(foreign Session) error")
	closed, err := ss.ExamAttempt().CloseConnection(ctx, closeInput)
	require.NoError(t, err)
	require.True(t, closed.Changed)
	require.Equal(t, model.AttemptConnectionClosed, closed.Connection.State)
	closeInput.AuditEventID, closeInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	closedAgain, err := ss.ExamAttempt().CloseConnection(ctx, closeInput)
	require.NoError(t, err)
	require.False(t, closedAgain.Changed)
	require.Equal(t, model.AttemptConnectionCloseTransport, closedAgain.Connection.CloseReason)
	wrongCredentialInput := *input
	wrongCredentialInput.AttemptID, wrongCredentialInput.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	wrongCredentialInput.ParticipationID, wrongCredentialInput.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	wrongCredentialInput.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
	wrongCredentialInput.AuditEventID, wrongCredentialInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().Connect(ctx, &wrongCredentialInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-wrong-credential", "attempt-wrong-credential"))
	requireConflictConstraint(t, err, "attempt_participation_credential")

	wrongReconnectSession := *input
	wrongReconnectSession.AttemptID, wrongReconnectSession.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	wrongReconnectSession.ParticipationID, wrongReconnectSession.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	wrongReconnectSession.SessionID = model.NewSessionID()
	wrongReconnectSession.AuditEventID, wrongReconnectSession.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().Connect(ctx, &wrongReconnectSession,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-wrong-session", "attempt-wrong-session"))
	require.True(t, store.IsNotFound(err), "Connect(reconnect with foreign Session) error")

	reconnectInput := *input
	reconnectInput.Security = model.ConnectSecurity{Kind: "resume", ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, PolicyDigest: connected.Security.Policy.Digest}
	reconnectInput.AttemptID, reconnectInput.WorkspaceID = model.NewExamAttemptID(), model.NewExamAttemptWorkspaceID()
	reconnectInput.ParticipationID, reconnectInput.ConnectionID = model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	reconnectInput.AuditEventID, reconnectInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	reconnected, err := ss.ExamAttempt().Connect(ctx, &reconnectInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-reconnect", "attempt-reconnect"))
	require.NoError(t, err)
	require.False(t, reconnected.FirstAdmission)
	require.True(t, reconnected.ConnectionOpened)
	require.Equal(t, connected.Attempt.ID, reconnected.Attempt.ID)
	require.Equal(t, connected.Workspace.ID, reconnected.Workspace.ID)
	require.Equal(t, connected.Participation.ID, reconnected.Participation.ID)
	require.Equal(t, connected.Participation.Generation, reconnected.Participation.Generation)
	require.Equal(t, reconnectInput.ConnectionID, reconnected.Connection.ID)
	closeInput.ConnectionID = reconnected.Connection.ID
	closeInput.AuditEventID, closeInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().CloseConnection(ctx, closeInput)
	require.NoError(t, err)

	endedAt := model.NowUTC()
	endedMembership, err := ss.ClassMember().SeedEnd(ctx, fixture.membership.ID, fixture.membership.Revision, endedAt)
	require.NoError(t, err)
	// Membership timestamps have millisecond precision while PostgreSQL decides
	// current eligibility with a higher-precision clock. Cross the millisecond
	// boundary before asserting that the just-ended relationship is inactive.
	time.Sleep(100 * time.Millisecond)
	revokedReplayInput := *input
	revokedReplayInput.AuditEventID, revokedReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().Connect(ctx, &revokedReplayInput, command)
	require.True(t, store.IsNotFound(err), "Connect(exact replay after membership revoke) error")
	revokedFreshInput := reconnectInput
	revokedFreshInput.ConnectionID = model.NewAttemptConnectionID()
	revokedFreshInput.AuditEventID, revokedFreshInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().Connect(ctx, &revokedFreshInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-revoked", "attempt-revoked"))
	require.True(t, store.IsNotFound(err), "Connect(fresh after membership revoke) error")
	restored, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: fixture.class.ID, UserID: fixture.candidate.ID,
		StartsAt: model.TimeUTC(endedAt)})
	require.NoError(t, err)
	fixture.membership = restored.Membership
	restoredInput := reconnectInput
	restoredInput.ConnectionID = model.NewAttemptConnectionID()
	restoredInput.AuditEventID, restoredInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	restoredConnection, err := ss.ExamAttempt().Connect(ctx, &restoredInput,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "attempt-restored", "attempt-restored"))
	require.NoError(t, err)
	require.False(t, restoredConnection.FirstAdmission, "Connect(after membership restore) = %#v; ended membership=%#v", restoredConnection, endedMembership)
	require.True(t, restoredConnection.ConnectionOpened, "Connect(after membership restore) = %#v; ended membership=%#v", restoredConnection, endedMembership)
	require.Equal(t, connected.Participation.Generation, restoredConnection.Participation.Generation, "Connect(after membership restore) = %#v; ended membership=%#v", restoredConnection, endedMembership)
	closeInput.ConnectionID = restoredConnection.Connection.ID
	closeInput.AuditEventID, closeInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().CloseConnection(ctx, closeInput)
	require.NoError(t, err)
	closedReplayInput := *input
	closedReplayInput.AuditEventID, closedReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	closedReplay, err := ss.ExamAttempt().Connect(ctx, &closedReplayInput, command)
	require.NoError(t, err)
	require.True(t, closedReplay.Replayed)
	require.False(t, closedReplay.ConnectionOpened)
	require.Equal(t, model.AttemptConnectionClosed, closedReplay.Connection.State)
	require.Equal(t, model.AttemptConnectionCloseTransport, closedReplay.Connection.CloseReason)
	_, err = ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	require.True(t, store.IsNotFound(err), "candidate access after close error")
	if len(probes) != 0 && probes[0].SetParticipationLeaseExpired != nil {
		probes[0].SetParticipationLeaseExpired(t, ctx, input.ParticipationID)
		expiredReplay := *input
		expiredReplay.AuditEventID, expiredReplay.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
		_, replayErr := ss.ExamAttempt().Connect(ctx, &expiredReplay, command)
		var conflict *store.ErrConflict
		require.ErrorAs(t, replayErr, &conflict)
		require.Equal(t, "attempt_participation_expired", conflict.Constraint)
		otherExpired := testExamAttemptParticipationExpiry(t, ctx, ss, fixture, probes[0])
		batchAudit := saveExamAttemptSystemAudit(t, ctx, ss, fixture)
		batchInput := &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
			Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
			SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: batchAudit.ID, AuditAt: model.NowUTC()}
		batchExpired, batchErr := ss.ExamAttempt().ExpireParticipation(ctx, batchInput)
		require.NoError(t, batchErr)
		require.NotEqual(t, otherExpired.Evidence.ID, batchExpired.Evidence.ID, "installation-wide expiry outcomes = %#v, %#v", batchExpired, otherExpired)
		require.NotEqual(t, otherExpired.Flag.ID, batchExpired.Flag.ID, "installation-wide expiry outcomes = %#v, %#v", batchExpired, otherExpired)
		require.NotEqual(t, otherExpired.Suspension.ID, batchExpired.Suspension.ID, "installation-wide expiry outcomes = %#v, %#v", batchExpired, otherExpired)
		require.Equal(t, connected.Participation.Generation, batchExpired.Participation.Generation, "installation-wide expiry outcomes = %#v, %#v", batchExpired, otherExpired)
		require.False(t, batchExpired.ConnectionClosed, "installation-wide expiry outcomes = %#v, %#v", batchExpired, otherExpired)
		require.Equal(t, model.AttemptConnectionCloseTransport, batchExpired.Connection.CloseReason, "installation-wide expiry outcomes = %#v, %#v", batchExpired, otherExpired)
	}
	currentSitting, err := ss.ExamSitting().Get(ctx, fixture.examID, fixture.sitting.ID)
	require.NoError(t, err)
	closeAt := model.NowUTC()
	closing, err := ss.ExamSitting().EarlyClose(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: currentSitting.Sitting.Revision,
		FinalizeJob:   newExamSittingFinalizeJob(t, fixture.sitting.ID, currentSitting.Sitting.Revision+1, closeAt),
		PrivateReason: "finish Attempt persistence conformance", ChangedAt: closeAt,
		AuditEventID: saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID).ID, AuditAt: model.NowUTC()},
		examCommand(fixture.manager.ID, "exam.sitting.close", "attempt-close-sitting", "attempt-close-sitting"))
	require.NoError(t, err)
	closingReplayInput := *input
	closingReplayInput.AuditEventID, closingReplayInput.AuditAt = saveExamAttemptAudit(t, ctx, ss, fixture).ID, model.NowUTC()
	_, err = ss.ExamAttempt().Connect(ctx, &closingReplayInput, command)
	requireConflictConstraint(t, err, "exam_sitting_state")
	unfinished, err := ss.ExamSitting().FinishSealing(ctx, &store.ExamSittingFinishSealing{SittingID: fixture.sitting.ID,
		AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, fixture.sitting.ID, fixture.unitID).ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.False(t, unfinished.Changed)
	require.Equal(t, model.ExamSittingClosing, unfinished.Value.Sitting.State)
	require.Equal(t, model.ExamSittingClosing, closing.Value.Sitting.State)
	if len(probes) != 0 && probes[0].FocusLossPersistence != nil {
		testExamAttemptFocusLossPolicies(t, ctx, ss, probes[0])
	}
}

func TestExamAttemptCompatibilityPolicySerialization(
	t *testing.T,
	ss store.Store,
	serialize DesktopCompatibilityPolicySerializationProbe,
) {
	t.Helper()
	require.NotNil(t, serialize, "Desktop Compatibility Policy serialization probe is required")
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	input := &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
	}
	prepareExamAttemptConnect(t, ctx, ss, input)
	command := examCommand(
		fixture.candidate.ID,
		store.ExamAttemptConnectOperation,
		"attempt-policy-serialization",
		"attempt-policy-serialization",
	)
	var connected *store.ExamAttemptConnectResult
	err := serialize(t, ctx, func() error {
		var connectErr error
		connected, connectErr = ss.ExamAttempt().Connect(ctx, input, command)
		return connectErr
	})
	require.NoError(t, err)
	require.NotNil(t, connected)
	require.NotNil(t, connected.Attempt)
	require.Equal(t, input.AttemptID, connected.Attempt.ID)
	policy, err := ss.DesktopCompatibilityPolicy().Get(ctx)
	require.NoError(t, err)
	require.Equal(t, input.DesktopCompatibilityPolicyRevision+1, policy.Revision, "serialized policy revision")
}

// TestExamAttemptConnectCandidateLockOrder verifies that Connect takes the
// candidate User before its Class-enrollment fence, so it waits behind an
// Invitation acceptance for the same candidate instead of deadlocking it.
func TestExamAttemptConnectCandidateLockOrder(t *testing.T, ss store.Store, probe CandidateLockProbe) {
	t.Helper()
	require.NotNil(t, probe, "Attempt candidate lock probe is required")
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	input := &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
	}
	prepareExamAttemptConnect(t, ctx, ss, input)
	command := examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation,
		"attempt-candidate-lock-order", "attempt-candidate-lock-order")
	var connected *store.ExamAttemptConnectResult
	err := probe(t, ctx, fixture.candidate.ID, CandidateFence{Kind: CandidateFenceClassEnrollment, AcademicPeriodID: fixture.class.AcademicPeriodID}, func() error {
		var connectErr error
		connected, connectErr = ss.ExamAttempt().Connect(ctx, input, command)
		return connectErr
	})
	require.NoError(t, err)
	require.NotNil(t, connected)
	require.NotNil(t, connected.Attempt)
	require.Equal(t, input.AttemptID, connected.Attempt.ID)
}

// TestSessionRevocationAttemptFence verifies the database-owned security
// boundary shared by every Session revocation path.
func TestSessionRevocationAttemptFence(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	credentialHash := model.HashToken(model.NewCredentialToken())
	input := &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credentialHash,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
	}
	prepareExamAttemptConnect(t, ctx, ss, input)
	connected, err := ss.ExamAttempt().Connect(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "session-fence-connect", "session-fence-connect"))
	require.NoError(t, err)
	_, err = ss.Session().Withdraw(ctx, fixture.session.ID, fixture.candidate.ID,
		model.NowUTC(), model.SessionRevocationInactiveUser)
	require.NoError(t, err)
	statuses, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID,
		ReallowAuthorized: true, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, statuses.Items, 1)
	require.NotNil(t, statuses.Items[0].Attempt)
	require.Equal(t, model.ExamAttemptSuspended, statuses.Items[0].Attempt.State)
	require.NotNil(t, statuses.Items[0].Suspension)
	require.Equal(t, model.AttemptSuspensionCandidateReasonSecureContinuityLost, statuses.Items[0].Suspension.CandidateReason)
	require.True(t, statuses.Items[0].Suspension.ReallowAvailable)
	require.Equal(t, store.SittingCandidateSuspended, statuses.Items[0].Presence.State)
	reallowAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	reallowed, err := ss.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, AttemptID: connected.Attempt.ID,
		SuspensionID: statuses.Items[0].Suspension.ID, ActorUserID: fixture.manager.ID,
		ExpectedRevision: statuses.Items[0].Attempt.Revision,
		PrivateReason:    "verified Session revocation recovery",
		AuditEventID:     reallowAudit.ID, AuditAt: model.NowUTC(),
	}, examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "session-fence-reallow", "session-fence-reallow"))
	require.NoError(t, err)
	require.Equal(t, model.ExamAttemptReady, reallowed.Attempt.State)
	require.Equal(t, model.AttemptSuspensionClosed, reallowed.Suspension.State)
	// PostgreSQL time decides the re-allow; the caller supplies no clock.
	require.True(t, reallowed.Suspension.EndedAt.Valid, "re-allow time = suspension %#v attempt %#v", reallowed.Suspension, reallowed.Attempt)
	require.False(t, reallowed.Suspension.EndedAt.Time.Before(reallowed.Suspension.StartedAt), "re-allow time = suspension %#v attempt %#v", reallowed.Suspension, reallowed.Attempt)
	require.True(t, reallowed.Attempt.UpdatedAt.Equal(reallowed.Suspension.EndedAt.Time), "re-allow time = suspension %#v attempt %#v", reallowed.Suspension, reallowed.Attempt)
}

// TestSessionExpiryAttemptFence verifies that authoritative idle/absolute
// expiry enforcement takes the same atomic Attempt fence as explicit Session
// revocation.
func TestSessionExpiryAttemptFence(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	credentialHash := model.HashToken(model.NewCredentialToken())
	input := &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(),
		WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credentialHash,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
	}
	prepareExamAttemptConnect(t, ctx, ss, input)
	connected, err := ss.ExamAttempt().Connect(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "session-expiry-fence-connect", "session-expiry-fence-connect"))
	require.NoError(t, err)
	expired, err := ss.Session().EnforceExpiry(
		ctx,
		fixture.session.ID,
		fixture.candidate.ID,
		fixture.session.IdleExpiresAt.Add(time.Second),
	)
	require.NoError(t, err)
	require.NotNil(t, expired)
	require.True(t, expired.Expired)
	require.NotNil(t, expired.Session)
	require.Equal(t, model.SessionRevocationExpired, expired.Session.RevocationReason)
	statuses, err := ss.ExamAttempt().ListSittingCandidateStatuses(ctx, store.SittingCandidateStatusListOptions{
		ExamID: fixture.examID, SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID,
		ReallowAuthorized: true, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, statuses.Items, 1)
	require.NotNil(t, statuses.Items[0].Attempt)
	require.Equal(t, connected.Attempt.ID, statuses.Items[0].Attempt.ID)
	require.Equal(t, model.ExamAttemptSuspended, statuses.Items[0].Attempt.State)
	require.NotNil(t, statuses.Items[0].Suspension)
	require.Equal(t, model.AttemptSuspensionCandidateReasonSecureContinuityLost, statuses.Items[0].Suspension.CandidateReason)
	require.Equal(t, store.SittingCandidateSuspended, statuses.Items[0].Presence.State)
}

func testExamAttemptParticipationExpiry(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture, probe ExamAttemptSQLProbe) *store.ExamAttemptParticipationExpiryResult {
	t.Helper()
	candidate := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent,
		StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID,
		StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	session := saveRegisteredDesktopSession(t, ctx, ss, candidate.ID, fixture.institutionID)
	expiryFixture := fixture
	expiryFixture.candidate, expiryFixture.session = candidate, session
	credentialHash := model.HashToken(model.NewCredentialToken())
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
		DesktopRegistrationID: session.DesktopRegistrationID, DPoPKeyThumbprint: session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credentialHash,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, expiryFixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect,
		examCommand(candidate.ID, store.ExamAttemptConnectOperation, "attempt-expiry-connect", "attempt-expiry-connect"))
	require.NoError(t, err)
	if probe.FenceRenewalPastDeadline != nil {
		renewal := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1, AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
			ConnectionID: connected.Connection.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
			DesktopRegistrationID: session.DesktopRegistrationID, DPoPKeyThumbprint: session.DPoPKeyThumbprint,
			Generation: connected.Participation.Generation, Sequence: 1, ContinuityCredentialHash: credentialHash}
		prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
		renewErr := probe.FenceRenewalPastDeadline(t, ctx, connected.Participation.ID, func() error {
			_, contenderErr := ss.ExamAttempt().RenewParticipation(ctx, renewal)
			return contenderErr
		})
		requireConflictConstraint(t, renewErr, "attempt_participation_expired")
	}
	probe.SetParticipationLeaseExpired(t, ctx, connected.Participation.ID)

	due, err := ss.ExamAttempt().ListExpiredParticipations(ctx, 200)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(due), 2, "installation-wide expiry batch")
	var selected *store.ExamAttemptParticipationExpiryDue
	for index := range due {
		if due[index].ParticipationID == connected.Participation.ID {
			selected = &due[index]
			break
		}
	}
	require.NotNil(t, selected)
	require.Equal(t, connected.Attempt.ID, selected.AttemptID)
	require.Equal(t, connected.Participation.Generation, selected.Generation)
	require.Equal(t, fixture.examID, selected.ExamID)
	require.Equal(t, fixture.sitting.ID, selected.SittingID)
	require.Equal(t, fixture.class.ID, selected.ClassID)
	require.Equal(t, candidate.ID, selected.CandidateUserID)
	require.False(t, selected.LeaseExpiresAt.IsZero())
	resolved, err := ss.ExamAttempt().ResolveParticipationExpiry(ctx, connected.Attempt.ID, connected.Participation.ID,
		connected.Participation.Generation)
	require.NoError(t, err)
	require.Equal(t, selected.AttemptID, resolved.AttemptID, "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	require.Equal(t, selected.ParticipationID, resolved.ParticipationID, "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	require.Equal(t, selected.Generation, resolved.Generation, "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	require.True(t, resolved.LeaseExpiresAt.Equal(selected.LeaseExpiresAt), "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	require.Equal(t, selected.ExamID, resolved.ExamID, "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	require.Equal(t, selected.SittingID, resolved.SittingID, "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	require.Equal(t, selected.ClassID, resolved.ClassID, "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	require.Equal(t, selected.CandidateUserID, resolved.CandidateUserID, "ResolveParticipationExpiry() = %#v, listed = %#v", resolved, selected)
	failedInput := &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()}
	_, err = ss.ExamAttempt().ExpireParticipation(ctx, failedInput)
	require.Error(t, err, "ExpireParticipation(with missing Audit) succeeded")
	unchanged, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.ExamAttemptActive, unchanged.Attempt.State)
	require.Equal(t, connected.Attempt.Revision, unchanged.Attempt.Revision)
	require.NotNil(t, unchanged.LatestParticipation)
	require.Equal(t, model.AttemptParticipationActive, unchanged.LatestParticipation.State)
	require.Nil(t, unchanged.ActiveSuspension)

	audit := saveExamAttemptSystemAudit(t, ctx, ss, expiryFixture)
	input := &store.ExamAttemptParticipationExpiry{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: audit.ID, AuditAt: model.NowUTC()}
	concurrentAudit := saveExamAttemptSystemAudit(t, ctx, ss, expiryFixture)
	concurrentInput := *input
	concurrentInput.EvidenceID, concurrentInput.FlagID, concurrentInput.SuspensionID = model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	concurrentInput.AuditEventID, concurrentInput.AuditAt = concurrentAudit.ID, model.NowUTC()
	results := make(chan *store.ExamAttemptParticipationExpiryResult, 2)
	errorsFound := make(chan error, 2)
	start := make(chan struct{})
	for _, proposal := range []*store.ExamAttemptParticipationExpiry{input, &concurrentInput} {
		go func(proposal *store.ExamAttemptParticipationExpiry) {
			<-start
			result, expireErr := ss.ExamAttempt().ExpireParticipation(ctx, proposal)
			results <- result
			errorsFound <- expireErr
		}(proposal)
	}
	close(start)
	first, second := <-results, <-results
	require.NoError(t, <-errorsFound)
	require.NoError(t, <-errorsFound)
	require.NotEqual(t, second.Replayed, first.Replayed, "ExpireParticipation(concurrent) = %#v, %#v", first, second)
	require.Equal(t, second.Evidence.ID, first.Evidence.ID, "ExpireParticipation(concurrent) = %#v, %#v", first, second)
	require.Equal(t, second.Flag.ID, first.Flag.ID, "ExpireParticipation(concurrent) = %#v, %#v", first, second)
	require.Equal(t, second.Suspension.ID, first.Suspension.ID, "ExpireParticipation(concurrent) = %#v, %#v", first, second)
	require.True(t, first.DatabaseTime.Equal(second.DatabaseTime), "ExpireParticipation(concurrent) = %#v, %#v", first, second)
	expired := first
	if expired.Replayed {
		expired = second
	}
	require.NotNil(t, expired)
	require.False(t, expired.Replayed)
	require.NotNil(t, expired.Attempt)
	require.NotNil(t, expired.Participation)
	require.NotNil(t, expired.Connection)
	require.NotNil(t, expired.Evidence)
	require.NotNil(t, expired.Flag)
	require.NotNil(t, expired.Suspension)
	require.Equal(t, fixture.examID, expired.ExamID)
	require.Equal(t, fixture.sitting.ID, expired.SittingID)
	require.Equal(t, fixture.class.ID, expired.ClassID)
	require.Equal(t, candidate.ID, expired.CandidateUserID)
	require.Equal(t, model.ExamAttemptSuspended, expired.Attempt.State)
	require.Equal(t, int64(2), expired.Attempt.Revision)
	require.Equal(t, model.AttemptParticipationEnded, expired.Participation.State)
	require.Equal(t, model.AttemptParticipationEndLeaseExpired, expired.Participation.EndReason)
	require.Equal(t, model.AttemptConnectionClosed, expired.Connection.State)
	require.Equal(t, model.AttemptConnectionCloseLeaseExpired, expired.Connection.CloseReason)
	require.True(t, expired.ConnectionClosed)
	require.Equal(t, selected.LeaseExpiresAt, expired.Evidence.ObservedAt)
	require.Equal(t, model.IntegrityFlagOpen, expired.Flag.State)
	require.Equal(t, model.AttemptSuspensionActive, expired.Suspension.State)
	require.Equal(t, model.AttemptSuspensionCandidateReasonSecureContinuityLost, expired.Suspension.CandidateReason)
	require.False(t, expired.DatabaseTime.IsZero())
	firstProposalWon := expired.Evidence.ID == input.EvidenceID && expired.Flag.ID == input.FlagID && expired.Suspension.ID == input.SuspensionID
	secondProposalWon := expired.Evidence.ID == concurrentInput.EvidenceID && expired.Flag.ID == concurrentInput.FlagID && expired.Suspension.ID == concurrentInput.SuspensionID
	require.NotEqual(t, secondProposalWon, firstProposalWon, "ExpireParticipation retained mixed or unknown proposal IDs: %#v", expired)
	requireSuccessfulAudit(t, ctx, ss, audit.ID)
	requireSuccessfulAudit(t, ctx, ss, concurrentAudit.ID)
	managerSnapshot, err := ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	require.NoError(t, err)
	require.NotNil(t, managerSnapshot.ActiveSuspension)
	require.Equal(t, expired.Suspension.ID, managerSnapshot.ActiveSuspension.ID)
	managerList, err := ss.ExamAttempt().List(ctx, store.ExamAttemptManagerListOptions{ExamID: fixture.examID,
		SittingID: fixture.sitting.ID, ExcludeCandidateUserID: fixture.manager.ID, Limit: 201})
	require.NoError(t, err)
	foundActiveSuspension := false
	for _, snapshot := range managerList {
		if snapshot.Attempt.ID == connected.Attempt.ID && snapshot.ActiveSuspension != nil && snapshot.ActiveSuspension.ID == expired.Suspension.ID {
			foundActiveSuspension = true
		}
	}
	require.True(t, foundActiveSuspension, "List(active Suspension) = %#v", managerList)

	replayAudit := saveExamAttemptSystemAudit(t, ctx, ss, expiryFixture)
	replayInput := *input
	replayInput.EvidenceID, replayInput.FlagID, replayInput.SuspensionID = model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	replayInput.AuditEventID, replayInput.AuditAt = replayAudit.ID, model.NowUTC()
	replayed, err := ss.ExamAttempt().ExpireParticipation(ctx, &replayInput)
	require.NoError(t, err)
	require.NotNil(t, replayed, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.True(t, replayed.Replayed, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.NotNil(t, replayed.Evidence, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.NotNil(t, replayed.Flag, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.NotNil(t, replayed.Suspension, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.Equal(t, expired.Evidence.ID, replayed.Evidence.ID, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.Equal(t, expired.Flag.ID, replayed.Flag.ID, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.Equal(t, expired.Suspension.ID, replayed.Suspension.ID, "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	require.True(t, replayed.DatabaseTime.Equal(expired.DatabaseTime), "ExpireParticipation(replay) = %#v, first = %#v", replayed, expired)
	requireSuccessfulAudit(t, ctx, ss, replayAudit.ID)

	reallowAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	reallowInput := &store.ExamAttemptReallow{ExamID: fixture.examID, SittingID: fixture.sitting.ID,
		AttemptID: expired.Attempt.ID, SuspensionID: expired.Suspension.ID, ActorUserID: fixture.manager.ID,
		ExpectedRevision: expired.Attempt.Revision, PrivateReason: "verified connectivity recovery",
		AuditEventID: reallowAudit.ID, AuditAt: model.NowUTC()}
	selfReallow := *reallowInput
	selfReallow.ActorUserID = expired.CandidateUserID
	selfReallow.ManagerOverride = true
	selfResult, selfErr := ss.ExamAttempt().ReallowAttempt(ctx, &selfReallow,
		examCommand(expired.CandidateUserID, store.ExamAttemptReallowOperation, "attempt-reallow-self", "attempt-reallow-self"))
	require.Nil(t, selfResult, "ReallowAttempt(self)")
	require.True(t, store.IsNotFound(selfErr), "ReallowAttempt(self)")
	reallowCommand := examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "attempt-reallow", "attempt-reallow")
	competingAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	competingInput := *reallowInput
	competingInput.PrivateReason = "second manager recovery decision"
	competingInput.AuditEventID, competingInput.AuditAt = competingAudit.ID, model.NowUTC()
	competingCommand := examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "attempt-reallow-competing", "attempt-reallow-competing")
	type reallowOutcome struct {
		index  int
		result *store.ExamAttemptReallowResult
		err    error
	}
	reallowOutcomes := make(chan reallowOutcome, 2)
	reallowStart := make(chan struct{})
	for index, proposal := range []*store.ExamAttemptReallow{reallowInput, &competingInput} {
		command := []*store.CommandIdempotency{reallowCommand, competingCommand}[index]
		go func(index int, proposal *store.ExamAttemptReallow, command *store.CommandIdempotency) {
			<-reallowStart
			result, reallowErr := ss.ExamAttempt().ReallowAttempt(ctx, proposal, command)
			reallowOutcomes <- reallowOutcome{index: index, result: result, err: reallowErr}
		}(index, proposal, command)
	}
	close(reallowStart)
	var reallowed *store.ExamAttemptReallowResult
	winner := -1
	for range 2 {
		outcome := <-reallowOutcomes
		if outcome.err == nil {
			require.Equal(t, -1, winner, "both concurrent ReallowAttempt calls succeeded")
			winner, reallowed = outcome.index, outcome.result
			continue
		}
		var conflict *store.ErrConflict
		require.ErrorAs(t, outcome.err, &conflict)
		require.False(t, conflict.Constraint != "attempt_suspension_active" && conflict.Constraint != "exam_attempt_revision")
	}
	require.GreaterOrEqual(t, winner, 0, "neither concurrent ReallowAttempt call succeeded")
	if winner == 1 {
		reallowAudit, reallowInput, reallowCommand = competingAudit, &competingInput, competingCommand
	}
	require.NotNil(t, reallowed)
	require.False(t, reallowed.Replayed)
	require.NotNil(t, reallowed.Attempt)
	require.NotNil(t, reallowed.Suspension)
	require.Equal(t, model.ExamAttemptReady, reallowed.Attempt.State)
	require.Equal(t, expired.Attempt.Revision+1, reallowed.Attempt.Revision)
	require.Equal(t, model.AttemptSuspensionClosed, reallowed.Suspension.State)
	require.True(t, reallowed.Suspension.EndedAt.Valid)
	require.Equal(t, fixture.manager.ID, reallowed.Suspension.ReallowedByUserID)
	require.Equal(t, candidate.ID, reallowed.CandidateUserID)
	managerSnapshot, err = ss.ExamAttempt().Get(ctx, fixture.examID, connected.Attempt.ID)
	require.NoError(t, err)
	require.Nil(t, managerSnapshot.ActiveSuspension)
	reallowEvent, err := ss.Audit().Get(ctx, reallowAudit.ID)
	require.NoError(t, err)
	require.False(t, bytes.Contains(reallowEvent.Result, []byte(reallowInput.PrivateReason)), "ReallowAttempt audit exposed the private reason")
	reallowReplayAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	reallowReplay := *reallowInput
	reallowReplay.AuditEventID, reallowReplay.AuditAt = reallowReplayAudit.ID, model.NowUTC()
	replayedReallow, err := ss.ExamAttempt().ReallowAttempt(ctx, &reallowReplay, reallowCommand)
	require.NoError(t, err)
	require.NotNil(t, replayedReallow)
	require.True(t, replayedReallow.Replayed)
	require.Equal(t, reallowed.Attempt.Revision, replayedReallow.Attempt.Revision)
	require.Equal(t, reallowed.Suspension.ID, replayedReallow.Suspension.ID)
	expiryAfterReallowAudit := saveExamAttemptSystemAudit(t, ctx, ss, fixture)
	expiryAfterReallow := *input
	expiryAfterReallow.EvidenceID, expiryAfterReallow.FlagID, expiryAfterReallow.SuspensionID =
		model.NewIntegrityEvidenceID(), model.NewIntegrityFlagID(), model.NewAttemptSuspensionID()
	expiryAfterReallow.AuditEventID, expiryAfterReallow.AuditAt = expiryAfterReallowAudit.ID, model.NowUTC()
	retainedExpiry, err := ss.ExamAttempt().ExpireParticipation(ctx, &expiryAfterReallow)
	require.NoError(t, err)
	require.NotNil(t, retainedExpiry, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.True(t, retainedExpiry.Replayed, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.Equal(t, model.ExamAttemptSuspended, retainedExpiry.Attempt.State, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.Equal(t, expired.Attempt.Revision, retainedExpiry.Attempt.Revision, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.Equal(t, model.AttemptSuspensionActive, retainedExpiry.Suspension.State, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.False(t, retainedExpiry.Suspension.EndedAt.Valid, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.True(t, retainedExpiry.Suspension.ReallowedByUserID.IsZero(), "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.Equal(t, expired.Evidence.ID, retainedExpiry.Evidence.ID, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.Equal(t, expired.Flag.ID, retainedExpiry.Flag.ID, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.Equal(t, expired.Suspension.ID, retainedExpiry.Suspension.ID, "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	require.True(t, retainedExpiry.DatabaseTime.Equal(expired.DatabaseTime), "ExpireParticipation(replay after re-allow) = %#v, first = %#v", retainedExpiry, expired)
	requireSuccessfulAudit(t, ctx, ss, expiryAfterReallowAudit.ID)
	reallowDifferentAudit := saveExamAttemptReallowAudit(t, ctx, ss, fixture)
	reallowDifferent := reallowReplay
	reallowDifferent.AuditEventID = reallowDifferentAudit.ID
	_, err = ss.ExamAttempt().ReallowAttempt(ctx, &reallowDifferent,
		examCommand(fixture.manager.ID, store.ExamAttemptReallowOperation, "attempt-reallow-different", "attempt-reallow-different"))
	requireConflictConstraint(t, err, "attempt_suspension_active")

	nextConnect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
		DesktopRegistrationID: session.DesktopRegistrationID, DPoPKeyThumbprint: session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, expiryFixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, nextConnect)
	next, err := ss.ExamAttempt().Connect(ctx, nextConnect,
		examCommand(candidate.ID, store.ExamAttemptConnectOperation, "attempt-next-generation", "attempt-next-generation"))
	require.NoError(t, err)
	require.Equal(t, expired.Participation.Generation+1, next.Participation.Generation)
	require.Equal(t, expired.Attempt.ID, next.Attempt.ID)
	require.Equal(t, connected.Workspace.ID, next.Workspace.ID)
	require.True(t, next.ConnectionOpened)

	due, err = ss.ExamAttempt().ListExpiredParticipations(ctx, 200)
	require.NoError(t, err)
	for _, candidate := range due {
		require.NotEqual(t, connected.Participation.ID, candidate.ParticipationID, "expired Participation remained due: %#v", due)
	}
	access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
		DesktopRegistrationID: session.DesktopRegistrationID, DPoPKeyThumbprint: session.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
	_, err = ss.ExamAttempt().GetCandidatePresentation(ctx, access)
	require.True(t, store.IsNotFound(err), "candidate access after expiry error")
	return expired
}

func saveExamAttemptSystemAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionExamSittingParticipate),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: fixture.sitting.ID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: fixture.class.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return audit
}

func saveExamAttemptReallowAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: fixture.manager.ID, Action: string(model.ActionExamSittingManage),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: fixture.sitting.ID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: fixture.class.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return audit
}

func testExamAttemptParticipationRenewal(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture,
	connected *store.ExamAttemptConnectResult, credentialHash string,
) {
	t.Helper()
	input := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1,
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, ConnectionID: connected.Connection.ID,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID, Generation: connected.Participation.Generation,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		Sequence: 1, ContinuityCredentialHash: credentialHash,
	}
	prepareParticipationRenewalCoverage(t, ctx, ss, input)
	renewed, err := ss.ExamAttempt().RenewParticipation(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, renewed)
	require.Equal(t, input.AttemptID, renewed.AttemptID)
	require.Equal(t, input.ParticipationID, renewed.ParticipationID)
	require.Equal(t, input.Generation, renewed.Generation)
	require.Equal(t, input.Sequence, renewed.AcceptedSequence)
	require.False(t, renewed.Duplicate)
	require.False(t, renewed.DatabaseTime.IsZero())
	require.Equal(t, model.AttemptParticipationInitialLease, renewed.LeaseExpiresAt.Sub(renewed.DatabaseTime))

	duplicate, err := ss.ExamAttempt().RenewParticipation(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, duplicate, "RenewParticipation(duplicate) = %#v, first = %#v", duplicate, renewed)
	require.True(t, duplicate.Duplicate, "RenewParticipation(duplicate) = %#v, first = %#v", duplicate, renewed)
	require.Equal(t, renewed.AcceptedSequence, duplicate.AcceptedSequence, "RenewParticipation(duplicate) = %#v, first = %#v", duplicate, renewed)
	require.True(t, duplicate.DatabaseTime.Equal(renewed.DatabaseTime), "RenewParticipation(duplicate) = %#v, first = %#v", duplicate, renewed)
	require.True(t, duplicate.LeaseExpiresAt.Equal(renewed.LeaseExpiresAt), "RenewParticipation(duplicate) = %#v, first = %#v", duplicate, renewed)

	input.Sequence = 2
	advanced, err := ss.ExamAttempt().RenewParticipation(ctx, input)
	require.NoError(t, err)
	require.False(t, advanced.Duplicate, "RenewParticipation(advanced) = %#v, first = %#v", advanced, renewed)
	require.Equal(t, int64(2), advanced.AcceptedSequence, "RenewParticipation(advanced) = %#v, first = %#v", advanced, renewed)
	require.False(t, advanced.DatabaseTime.Before(renewed.DatabaseTime), "RenewParticipation(advanced) = %#v, first = %#v", advanced, renewed)
	require.Equal(t, model.AttemptParticipationInitialLease, advanced.LeaseExpiresAt.Sub(advanced.DatabaseTime), "RenewParticipation(advanced) = %#v, first = %#v", advanced, renewed)
	input.Sequence = 1
	_, err = ss.ExamAttempt().RenewParticipation(ctx, input)
	requireConflictConstraint(t, err, "attempt_participation_sequence")
	input.Sequence = 3
	input.Generation++
	_, err = ss.ExamAttempt().RenewParticipation(ctx, input)
	requireConflictConstraint(t, err, "attempt_participation_generation")
	input.Generation--
	input.ContinuityCredentialHash = model.HashToken(model.NewCredentialToken())
	_, err = ss.ExamAttempt().RenewParticipation(ctx, input)
	requireConflictConstraint(t, err, "attempt_participation_credential")
}

type examAttemptFixture struct {
	candidate     *model.User
	manager       *model.User
	session       *model.Session
	sitting       *model.ExamSitting
	class         *model.Class
	examID        model.ExamID
	revisionID    model.ExamRevisionID
	unitID        model.AcademicUnitID
	institutionID model.InstitutionID
	membership    *model.ClassMember
}

func newExamAttemptFixture(t *testing.T, ctx context.Context, ss store.Store) examAttemptFixture {
	return newExamAttemptFixtureWithPolicies(t, ctx, ss, nil, nil)
}

func newExamAttemptFixtureWithFocusLoss(t *testing.T, ctx context.Context, ss store.Store, focus *model.FocusLossPolicy) examAttemptFixture {
	return newExamAttemptFixtureWithPolicies(t, ctx, ss, focus, nil)
}

func newExamAttemptFixtureWithBrowserPolicy(t *testing.T, ctx context.Context, ss store.Store,
	browser *model.BrowserPolicy,
) examAttemptFixture {
	return newExamAttemptFixtureWithPolicies(t, ctx, ss, nil, browser)
}

func newExamAttemptFixtureWithPolicies(t *testing.T, ctx context.Context, ss store.Store, focus *model.FocusLossPolicy,
	browser *model.BrowserPolicy,
) examAttemptFixture {
	t.Helper()
	return newExamAttemptFixtureWithCapacity(t, ctx, ss, focus, browser, model.ExamCapacityPolicy{})
}

func newExamAttemptFixtureWithCapacity(t *testing.T, ctx context.Context, ss store.Store, focus *model.FocusLossPolicy,
	browser *model.BrowserPolicy, capacity model.ExamCapacityPolicy,
) examAttemptFixture {
	t.Helper()
	unit, programme := saveProgrammeParents(t, ctx, ss, "attempt-unit")
	if !capacity.IsZero() {
		institution, err := ss.Institution().GetSingleton(ctx)
		require.NoError(t, err)
		institution.ExamCapacity = capacity
		_, err = UpdateInstitution(ctx, ss, institution)
		require.NoError(t, err)
	}
	level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "attempt-level")
	now := model.NowUTC()
	period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "attempt-period", model.MillisFromTime(now.Add(-time.Hour)))
	class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "attempt-class")
	manager := saveUser(t, ctx, ss)
	created := createCatalogExam(t, ctx, ss, programme.AcademicUnitID, manager.ID, now, "attempt-exam")
	draftRevision := int64(1)
	if focus != nil {
		updated, updateErr := ss.ExamAuthoring().UpdateDraftFocusLoss(ctx,
			newExamDraftFocusLossUpdate(t, ctx, ss, created.Value.Exam.ID, manager.ID, draftRevision, *focus, now.Add(time.Millisecond)),
			examCommand(manager.ID, "exam.draft.focus_loss.configure", "attempt-focus-policy", "attempt-focus-policy"))
		require.NoError(t, updateErr)
		draftRevision = updated.Value.Draft.Revision
	}
	if browser != nil {
		audit, auditErr := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: manager.ID, Action: string(model.ActionExamManage),
			Resource:  model.Resource{Type: model.ResourceExam, ID: created.Value.Exam.ID.String()},
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: programme.AcademicUnitID.String(),
			Status: model.AuditStatusAttempt, NodeID: "test-node"})
		require.NoError(t, auditErr)
		updated, updateErr := ss.ExamAuthoring().UpdateDraftBrowserPolicy(ctx, &store.ExamDraftBrowserPolicyUpdate{
			ExamID: created.Value.Exam.ID, ActorUserID: manager.ID, ExpectedRevision: draftRevision,
			Policy: browser.Clone(), UpdatedAt: now.Add(time.Millisecond),
			AuditEventID: audit.ID, AuditAt: model.NowUTC(),
		}, examCommand(manager.ID, "exam.draft.browser_policy.configure", "attempt-browser-policy", "attempt-browser-policy"))
		require.NoError(t, updateErr)
		draftRevision = updated.Value.Draft.Revision
	}
	directory := starterWorkspaceMutation(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, draftRevision,
		model.NewStarterWorkspaceEntryID(), "cmd", now.Add(time.Millisecond))
	_, err := ss.ExamStarterWorkspace().CreateDirectory(ctx, directory,
		examCommand(manager.ID, "exam.starter_workspace.directory.create", "attempt-dir", "attempt-dir"))
	require.NoError(t, err)
	draftRevision++
	file := reserveStarterWorkspaceObject(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, draftRevision,
		model.NewStarterWorkspaceEntryID(), "cmd/main.go", now.Add(2*time.Millisecond), 13)
	_, err = ss.ExamStarterWorkspace().CreateFile(ctx, file,
		examCommand(manager.ID, "exam.starter_workspace.file.create", "attempt-file", "attempt-file"))
	require.NoError(t, err)
	draftRevision++
	publication := examRevisionPublication(t, ctx, ss, created.Value.Exam.ID, manager.ID, programme.AcademicUnitID, draftRevision, now.Add(3*time.Millisecond))
	published, err := ss.ExamRevision().Publish(ctx, publication, examCommand(manager.ID, "exam.revision.publish", "attempt-revision", "attempt-revision"))
	require.NoError(t, err)
	// PostgreSQL decides that a new start is strictly future. A loaded host
	// can spend the one-second lead before the statement runs, so a refused
	// fresh schedule is retried with a new start rather than failing the fixture.
	var sitting *model.ExamSitting
	var start time.Time
	for scheduleAttempt := 1; ; scheduleAttempt++ {
		var end time.Time
		start, end = model.NowUTC().Add(time.Second), model.NowUTC().Add(time.Hour)
		sitting, err = model.NewExamSitting(model.NewExamSittingID(), created.Value.Exam.ID, published.Revision.ID, class.ID, start, end, now)
		require.NoError(t, err)
		openJob, deadlineJob := newExamSittingLifecycleJobs(t, sitting.ID, 1, start, end)
		key := "attempt-sitting-" + string(rune('0'+scheduleAttempt))
		_, err = ss.ExamSitting().Schedule(ctx, &store.ExamSittingSchedule{Sitting: sitting, OpenJob: openJob, DeadlineJob: deadlineJob, ActorUserID: manager.ID,
			AuditEventID: saveExamSittingAudit(t, ctx, ss, manager.ID, created.Value.Exam.ID, programme.AcademicUnitID).ID, AuditAt: model.NowUTC(),
			Mail: newExamSittingMailFanout(t, manager.ID, store.ExamSittingMailScheduled, model.MailTemplateExamSittingScheduled)},
			examCommand(manager.ID, "exam.sitting.schedule", key, key))
		var conflict *store.ErrConflict
		if scheduleAttempt < 5 && errors.As(err, &conflict) && conflict.Constraint == "exam_sitting_not_future" {
			continue
		}
		require.NoError(t, err)
		break
	}
	time.Sleep(time.Until(start) + 100*time.Millisecond)
	opened, err := ss.ExamSitting().AdvanceDue(ctx, &store.ExamSittingDueAdvance{SittingID: sitting.ID, AuditEventID: saveExamSittingSystemAudit(t, ctx, ss, sitting.ID, programme.AcademicUnitID).ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.Equal(t, model.ExamSittingOpen, opened.Value.Sitting.State)
	candidate := saveUser(t, ctx, ss)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: now.Add(-time.Hour)})
	require.NoError(t, err)
	enrollment, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: class.ID, UserID: candidate.ID, StartsAt: now.Add(-time.Minute)})
	require.NoError(t, err)
	session := saveRegisteredDesktopSession(t, ctx, ss, candidate.ID, unit.InstitutionID)
	return examAttemptFixture{candidate: candidate, manager: manager, session: session, sitting: opened.Value.Sitting, class: class,
		examID: created.Value.Exam.ID, revisionID: published.Revision.ID, unitID: programme.AcademicUnitID,
		institutionID: unit.InstitutionID, membership: enrollment.Membership}
}

func saveRegisteredDesktopSession(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID, institutionID model.InstitutionID) *model.Session {
	t.Helper()
	now := model.NowUTC()
	transaction, handle, proof, state, verifier := newDesktopAuthorizationTransaction(now, institutionID)
	_, err := ss.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
	require.NoError(t, err)
	binding := bindAndAuthenticateDesktopAuthorization(t, ctx, ss, handle, proof, state, userID)
	code := model.NewCredentialToken()
	issueAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institutionID, userID, "exam-attempt-session-issue")
	_, err = ss.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
		BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(code),
		ExpectedUserID: userID, CodeLifetime: 45 * time.Second, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		AuditEventID: issueAudit.ID, AuditAt: model.NowUTC(),
	})
	require.NoError(t, err)
	exchangeAudit := saveDesktopAuthorizationAudit(t, ctx, ss, institutionID, userID, "exam-attempt-session-exchange")
	result, err := ss.BrowserAuthentication().Exchange(ctx, desktopAuthorizationExchange(now, code, state, verifier, exchangeAudit))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Session)
	require.NotNil(t, result.Registration)
	require.Equal(t, result.Registration.ID, result.Session.DesktopRegistrationID)
	require.NotEmpty(t, result.Session.DPoPKeyThumbprint)
	return result.Session
}

func testConcurrentExamAttemptAdmission(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) {
	t.Helper()
	candidate := saveUser(t, ctx, ss)
	_, err := ss.Affiliation().Save(ctx, &model.Affiliation{UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: model.NowUTC().Add(-time.Hour)})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: fixture.class.ID, UserID: candidate.ID, StartsAt: model.NowUTC().Add(-time.Minute)})
	require.NoError(t, err)
	session := saveRegisteredDesktopSession(t, ctx, ss, candidate.ID, fixture.institutionID)
	concurrentFixture := fixture
	concurrentFixture.candidate, concurrentFixture.session = candidate, session
	credentialHash := model.HashToken(model.NewCredentialToken())
	inputs := [2]*store.ExamAttemptConnect{}
	commands := [2]*store.CommandIdempotency{}
	for index := range inputs {
		inputs[index] = &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: candidate.ID, SessionID: session.ID,
			DesktopRegistrationID: session.DesktopRegistrationID, DPoPKeyThumbprint: session.DPoPKeyThumbprint,
			AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
			ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credentialHash,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, concurrentFixture).ID, AuditAt: model.NowUTC()}
		prepareExamAttemptConnect(t, ctx, ss, inputs[index])
		commands[index] = examCommand(candidate.ID, store.ExamAttemptConnectOperation,
			"attempt-concurrent-retry", "attempt-concurrent-retry")
		// The duplicate must wait out the winner's whole transaction to
		// replay it; a loaded host can exceed the fixture's one-second wait.
		commands[index].Wait = 30 * time.Second
	}
	start := make(chan struct{})
	results := make(chan *store.ExamAttemptConnectResult, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			result, connectErr := ss.ExamAttempt().Connect(ctx, inputs[index], commands[index])
			results <- result
			errorsFound <- connectErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for connectErr := range errorsFound {
		require.NoError(t, connectErr)
	}
	connected := make([]*store.ExamAttemptConnectResult, 0, 2)
	for result := range results {
		connected = append(connected, result)
	}
	require.Len(t, connected, 2)
	require.NotNil(t, connected[0])
	require.NotNil(t, connected[1])
	require.Equal(t, connected[1].Attempt.ID, connected[0].Attempt.ID)
	require.Equal(t, connected[1].Workspace.ID, connected[0].Workspace.ID)
	require.Equal(t, connected[1].Participation.ID, connected[0].Participation.ID)
	require.Equal(t, connected[1].Connection.ID, connected[0].Connection.ID)
	require.NotEqual(t, connected[1].Replayed, connected[0].Replayed)
	require.NotEqual(t, connected[1].ConnectionOpened, connected[0].ConnectionOpened)
	closeResult, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{ConnectionID: connected[0].Connection.ID,
		CandidateUserID: candidate.ID, SessionID: session.ID, Reason: model.AttemptConnectionCloseTransport,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, concurrentFixture).ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)
	require.True(t, closeResult.Changed)
}

// requireConflictConstraint asserts that err is a Store conflict on the named
// constraint.
func requireConflictConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, constraint, conflict.Constraint)
}

func saveExamAttemptAudit(t *testing.T, ctx context.Context, ss store.Store, fixture examAttemptFixture) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, newExamAttemptAudit(fixture))
	require.NoError(t, err)
	return audit
}

func newExamAttemptAudit(fixture examAttemptFixture) *model.AuditEvent {
	return &model.AuditEvent{ActorID: fixture.candidate.ID, Action: string(model.ActionExamSittingParticipate),
		Resource: model.Resource{Type: model.ResourceExamSitting, ID: fixture.sitting.ID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: fixture.class.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"}
}
