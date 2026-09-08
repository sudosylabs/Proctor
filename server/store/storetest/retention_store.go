// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// RetentionSQLProbe changes only fixture dates or observes database guards.
// Production transitions always use the authoritative database clock.
type RetentionSQLProbe struct {
	AgeCompletion               func(*testing.T, context.Context, model.ExamSittingID)
	ExpireGrace                 func(*testing.T, context.Context, model.SubmissionID)
	AssertSealedGuards          func(*testing.T, context.Context, model.SubmissionID)
	AssertPrivateContentRemoved func(*testing.T, context.Context, model.SubmissionID)
	ConcurrentPeer              store.RetentionStore
}

func TestRetentionStore(t *testing.T, ss store.Store, probe RetentionSQLProbe) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f, subID, objectID, sealInput, sealKey := newRetentionFixture(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator", Permissions: model.AllActions(), BuiltIn: true})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: f.manager.ID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{UserID: f.manager.ID, AcademicUnitID: f.unitID, StartsAt: model.NowUTC().Add(-time.Hour)})
	requireNoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, f.manager.ID, true)
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: f.sitting.ID, SubmissionID: subID}
	closeRecordsSitting(t, ctx, ss, &f)
	waiver := &store.ExamRecordsReviewWaiver{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsComplete), ReasonCode: "review_not_required", PrivateReason: "Private waiver that must expire."}
	_, err = ss.ExamRecords().WaiveReview(ctx, waiver, examCommand(f.manager.ID, store.ExamRecordsWaiveReviewOperation, "retention-waiver", "retention-waiver"))
	requireNoError(t, err)
	completeScope := scope
	completeScope.SubmissionID = ""
	_, err = ss.ExamRecords().CompleteRecords(ctx, &store.ExamRecordsCompletion{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, completeScope, principal, model.ActionExamRecordsComplete), ExpectedRevision: 1}, examCommand(f.manager.ID, store.ExamRecordsCompleteOperation, "retention-complete", "retention-complete"))
	requireNoError(t, err)
	control, err := ss.Retention().GetControl(ctx)
	requireNoError(t, err)
	if control.State != model.RetentionControlDisabled {
		t.Fatal("new policy enabled cleanup")
	}
	policy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	settings := model.RetentionPolicySettings{SubmissionRetentionDays: 1, IntegrityRetentionDays: 1, AuditRetentionDays: 1, ExportRetentionDays: 1, DeletionGraceDays: 1}
	replace := func(key string) {
		input := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: policy.Revision, Settings: settings}
		prepareRetentionPolicyAttempt(t, ctx, ss, f.institutionID, input)
		result, err := ss.RetentionPolicy().Replace(ctx, input, retentionPolicyCommand(f.manager.ID, key))
		requireNoError(t, err)
		policy = result.Policy
	}
	replace("retention-initial")
	mutation := func(action model.Action) store.RetentionMutation {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(action), Resource: model.Resource{Type: model.ResourceInstitution, ID: f.institutionID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "retention-test"})
		requireNoError(t, err)
		return store.RetentionMutation{Principal: principal, AuditEventID: a.ID.String(), AuditAt: model.GetMillis(), RecentAuthenticationTTL: 10 * time.Minute}
	}
	preview := func(key string) *model.RetentionPreview {
		p, err := ss.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView), PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: policy.Revision}, examCommand(f.manager.ID, store.RetentionPreviewOperation, key, key))
		requireNoError(t, err)
		return p
	}
	before := preview("before-deadline")
	if before.Work.Eligible != 0 || before.Integrity.Eligible != 0 {
		t.Fatal("fresh completion was immediately eligible")
	}
	probe.AgeCompletion(t, ctx, f.sitting.ID)
	p := preview("eligible")
	if p.Work.Eligible != 1 || p.Integrity.Eligible != 1 {
		t.Fatalf("eligible counts=%#v", p)
	}
	change := func(state model.RetentionControlState, key string) *model.RetentionControl {
		current, err := ss.Retention().GetControl(ctx)
		requireNoError(t, err)
		input := &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage), ExpectedRevision: current.Revision, ExpectedPolicyRevision: policy.Revision, State: state}
		if state == model.RetentionControlEnabled {
			input.PreviewID = preview(key + "-preview").ID
		}
		keyValue := examCommand(f.manager.ID, store.RetentionControlOperation, key, key)
		missing := *input
		missing.AuditEventID = model.NewId()
		if _, err = ss.Retention().ChangeControl(ctx, &missing, keyValue); err == nil {
			t.Fatal("control committed without critical audit")
		}
		result, err := ss.Retention().ChangeControl(ctx, input, keyValue)
		requireNoError(t, err)
		input.RetentionMutation = mutation(model.ActionRetentionCleanupManage)
		replay, err := ss.Retention().ChangeControl(ctx, input, keyValue)
		requireNoError(t, err)
		if !replay.Replayed || replay.Control.Revision != result.Control.Revision {
			t.Fatal("control retry reapplied mutation")
		}
		return result.Control
	}
	control = change(model.RetentionControlEnabled, "enable")
	storedPolicy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	if !control.Permits(storedPolicy) || !storedPolicy.AutomaticDeletionEnabled {
		t.Fatal("approval projection disagrees")
	}
	reconcile := func(expected store.RetentionReconciliationResult) {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceSubmission, ID: subID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "retention-job"})
		requireNoError(t, err)
		result, err := ss.Retention().ReconcileSubmission(ctx, &store.RetentionReconciliation{SubmissionID: subID, AuditEventID: a.ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
		if result == nil || *result != expected {
			t.Fatalf("reconcile=%#v expected=%#v", result, expected)
		}
	}
	reconcile(store.RetentionReconciliationResult{Scheduled: 2})
	reconcile(store.RetentionReconciliationResult{})
	probe.AssertSealedGuards(t, ctx, subID)

	notices, err := ss.Retention().ListNotices(ctx, principal.UserID, "", 100)
	requireNoError(t, err)
	if len(notices) != 2 {
		t.Fatalf("manager/operator notices=%d, want one per category", len(notices))
	}
	candidateNotices, err := ss.Retention().ListNotices(ctx, f.candidate.ID, "", 100)
	requireNoError(t, err)
	if len(candidateNotices) != 0 {
		t.Fatal("candidate notices enabled by default")
	}
	var queuedNotice model.RetentionNotice
	for _, n := range notices {
		if n.Category == model.RetentionCategoryWork {
			queuedNotice = n
		} else {
			requireNoError(t, ss.Retention().CompleteNotice(ctx, &store.RetentionNoticeMail{RetirementID: n.RetirementID, RecipientUserID: principal.UserID, FailureCode: "mail.preparation_unavailable"}))
		}
	}
	occurrence, delivery, mailJob := userTokenMailFixture(t, principal.UserID, model.NewMailOccurrenceID(), model.MailOccurrenceExamManagement, model.MailTemplateExamRetentionScheduled, model.JobTypeMailDeliver, model.NowUTC(), queuedNotice.RetireAfter)
	prepareNotice := &store.RetentionNoticeMail{RetirementID: queuedNotice.RetirementID, RecipientUserID: principal.UserID, Mail: &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: mailJob}}
	wrong := *prepareNotice
	wrong.RecipientUserID = f.candidate.ID
	if err := ss.Retention().CompleteNotice(ctx, &wrong); !store.IsNotFound(err) {
		t.Fatal("unselected recipient accepted")
	}
	requireNoError(t, ss.Retention().CompleteNotice(ctx, prepareNotice))
	// A retry with a freshly prepared delivery still keeps the first delivery.
	otherOccurrence, otherDelivery, otherJob := userTokenMailFixture(t, principal.UserID, model.NewMailOccurrenceID(), model.MailOccurrenceExamManagement, model.MailTemplateExamRetentionScheduled, model.JobTypeMailDeliver, model.NowUTC(), queuedNotice.RetireAfter)
	retryNotice := *prepareNotice
	retryNotice.Mail = &store.PreparedMail{Occurrence: otherOccurrence, Delivery: otherDelivery, Job: otherJob}
	requireNoError(t, ss.Retention().CompleteNotice(ctx, &retryNotice))
	if _, err := ss.Mail().GetDelivery(ctx, otherDelivery.ID); !store.IsNotFound(err) {
		t.Fatal("notice retry created duplicate delivery")
	}
	notices, err = ss.Retention().ListNotices(ctx, principal.UserID, "", 100)
	requireNoError(t, err)
	for _, n := range notices {
		if n.Category == model.RetentionCategoryIntegrity && (n.DeliveryState != "failed" || n.DeliveryErrorCode != "mail.preparation_unavailable") {
			t.Fatal("delivery preparation failure not visible")
		}
	}
	page, err := ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 1})
	requireNoError(t, err)
	if len(page.Items) != 4 || page.Items[0].Record.SharedPublishedObjects != 1 || page.Items[0].Retirement == nil {
		t.Fatalf("grace projection=%#v", page)
	}
	firstRetirement := page.Items[0].Retirement.ID
	hold, err := ss.ExamRecords().CreateHold(ctx, &store.ExamRecordsHoldCreation{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "records_review", PrivateReason: "Preserve while checking."}, examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "hold-during-grace", "hold-during-grace"))
	requireNoError(t, err)

	staleDelivery, err := ss.Mail().StartDelivery(ctx, delivery.ID, delivery.Revision, model.NowUTC())
	requireNoError(t, err)
	if staleDelivery.State != model.MailDeliverySuppressed || staleDelivery.PublicFailureCode != model.MailDeliveryObsoleteCode {
		t.Fatal("held retirement sent a stale notice")
	}
	notices, err = ss.Retention().ListNotices(ctx, principal.UserID, "", 100)
	requireNoError(t, err)
	for _, n := range notices {
		if !n.CancelledAt.Valid || n.State != model.RetentionRetirementCancelled {
			t.Fatal("hold left notice date active")
		}
	}
	page, err = ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 1})
	requireNoError(t, err)
	for _, item := range page.Items {
		if item.Retirement != nil || item.Eligibility.Blocker != model.RetentionBlockerHold {
			t.Fatal("hold left a pending retirement")
		}
	}
	reconcile(store.RetentionReconciliationResult{})
	release := &store.ExamRecordsHoldRelease{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionRetentionHoldRelease), HoldID: hold.Hold.ID, ExpectedRevision: hold.Hold.Revision, ReasonCode: "case_closed", PrivateReason: "Preservation review is finished.", RecentAuthenticationTTL: 10 * time.Minute}
	_, err = ss.ExamRecords().ReleaseHold(ctx, release, examCommand(f.manager.ID, store.ExamRecordsReleaseHoldOperation, "release-hold", "release-hold"))
	requireNoError(t, err)
	reconcile(store.RetentionReconciliationResult{Scheduled: 2})
	page, err = ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 1})
	requireNoError(t, err)
	if page.Items[0].Retirement.ID == firstRetirement {
		t.Fatal("released hold reused old grace")
	}
	settings.SubmissionRetentionDays = 2
	settings.CandidateNotices = true
	replace("longer-policy")
	control, err = ss.Retention().GetControl(ctx)
	requireNoError(t, err)
	if control.State != model.RetentionControlPaused || policy.AutomaticDeletionEnabled {
		t.Fatal("policy change retained approval")
	}
	reconcile(store.RetentionReconciliationResult{})
	change(model.RetentionControlEnabled, "approve-longer")
	reconcile(store.RetentionReconciliationResult{Scheduled: 2})

	candidateNotices, err = ss.Retention().ListNotices(ctx, f.candidate.ID, "", 100)
	requireNoError(t, err)
	if len(candidateNotices) != 2 {
		t.Fatal("opt-in candidate notices missing")
	}

	probe.ExpireGrace(t, ctx, subID)
	// Different callers race the authoritative hold and irreversible retirement
	// transitions. Exactly one ordering must win; neither may partially commit.
	raceHold := &store.ExamRecordsHoldCreation{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "records_review", PrivateReason: "Concurrent preservation request."}
	raceAudit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceSubmission, ID: subID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: f.institutionID.String(), Status: model.AuditStatusAttempt, NodeID: "retention-peer"})
	requireNoError(t, err)
	holdResult := make(chan *store.ExamRecordsHoldResult, 1)
	holdError := make(chan error, 1)
	retireResult := make(chan *store.RetentionReconciliationResult, 1)
	retireError := make(chan error, 1)
	startRace := make(chan struct{})
	go func() {
		<-startRace
		r, e := ss.ExamRecords().CreateHold(ctx, raceHold, examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "racing-hold", "racing-hold"))
		holdResult <- r
		holdError <- e
	}()
	peer := probe.ConcurrentPeer
	if peer == nil {
		peer = ss.Retention()
	}
	go func() {
		<-startRace
		r, e := peer.ReconcileSubmission(ctx, &store.RetentionReconciliation{SubmissionID: subID, AuditEventID: raceAudit.ID.String(), AuditAt: model.GetMillis()})
		retireResult <- r
		retireError <- e
	}()
	close(startRace)
	racedHold, racedHoldErr := <-holdResult, <-holdError
	racedRetirement, racedRetirementErr := <-retireResult, <-retireError
	requireNoError(t, racedRetirementErr)
	if racedHoldErr == nil {
		if racedRetirement.Retired != 0 {
			t.Fatal("hold and retirement both committed")
		}
		releaseRace := &store.ExamRecordsHoldRelease{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionRetentionHoldRelease), HoldID: racedHold.Hold.ID, ExpectedRevision: racedHold.Hold.Revision, ReasonCode: "case_closed", RecentAuthenticationTTL: 10 * time.Minute}
		_, err = ss.ExamRecords().ReleaseHold(ctx, releaseRace, examCommand(f.manager.ID, store.ExamRecordsReleaseHoldOperation, "release-racing-hold", "release-racing-hold"))
		requireNoError(t, err)
		reconcile(store.RetentionReconciliationResult{Scheduled: 2})
		probe.ExpireGrace(t, ctx, subID)
		reconcile(store.RetentionReconciliationResult{Retired: 2})
	} else if !store.IsConflict(racedHoldErr) || racedRetirement.Retired != 2 {
		t.Fatalf("invalid race outcome: hold=%v retirement=%#v", racedHoldErr, racedRetirement)
	}
	reconcile(store.RetentionReconciliationResult{})
	page, err = ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 1})
	requireNoError(t, err)
	for _, item := range page.Items {
		if item.Record.Category == model.RetentionCategoryBrowserActivity || item.Record.Category == model.RetentionCategorySecurityOperational {
			if item.Eligibility.Blocker != model.RetentionBlockerUnconfigured || item.Retirement != nil {
				t.Fatalf("indefinite independent category retired: %#v", item)
			}
			continue
		}
		if item.Eligibility.Blocker != model.RetentionBlockerRetired || item.Retirement.State != model.RetentionRetirementCommitted {
			t.Fatalf("retired projection=%#v", item)
		}
	}
	if _, err = ss.ExamSubmission().Get(ctx, subID); !store.IsNotFound(err) {
		t.Fatalf("retired work remained readable: %v", err)
	}
	if _, err = ss.ExamIntegrityReview().Get(ctx, subID); !store.IsNotFound(err) && !store.IsConflict(err) {
		t.Fatalf("retired integrity remained readable: %v", err)
	}
	sealInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
	sealInput.AuditAt = model.GetMillis()
	if result, err := ss.ExamSubmission().Seal(ctx, sealInput, sealKey); err == nil && result != nil {
		t.Fatal("seal retry exposed a retired receipt")
	}
	probe.AssertPrivateContentRemoved(t, ctx, subID)
	objects, err := ss.Retention().BeginPurgeBatch(ctx, 100)
	requireNoError(t, err)
	if len(objects) != 2 || (objects[0].ObjectID != objectID && objects[1].ObjectID != objectID) {
		t.Fatalf("purge candidates=%#v", objects)
	}
	if err := ss.Retention().CompletePurge(ctx, &store.RetentionPurgeCompletion{RetirementID: model.NewRetentionRetirementID(), ObjectID: objectID}); !store.IsNotFound(err) {
		t.Fatal("unrelated purge completion was accepted")
	}
	for range 2 {
		for _, object := range objects {
			requireNoError(t, ss.Retention().CompletePurge(ctx, &store.RetentionPurgeCompletion{RetirementID: object.RetirementID, ObjectID: object.ObjectID}))
		}
	}
	objects, err = ss.Retention().BeginPurgeBatch(ctx, 100)
	requireNoError(t, err)
	if len(objects) != 0 {
		t.Fatal("observed objects ignored their retry interval")
	}
	page, err = ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 1})
	requireNoError(t, err)
	if page.Items[0].Retirement.PurgePending != 1 || page.Items[0].Retirement.PurgeVerified != 1 {
		t.Fatal("an unknown upload writer was reported as a completed purge")
	}
	_, err = ss.ExamRecords().CreateHold(ctx, &store.ExamRecordsHoldCreation{ExamRecordsMutation: recordsMutation(t, ctx, ss, f.unitID, scope, principal, model.ActionExamRecordsHold), HoldID: model.NewRetentionHoldID(), ReasonCode: "records_review", PrivateReason: "Cannot restore retired data."}, examCommand(f.manager.ID, store.ExamRecordsCreateHoldOperation, "hold-after-retirement", "hold-after-retirement"))
	if !store.IsConflict(err) {
		t.Fatalf("fully retired hold accepted: %v", err)
	}
}

func newRetentionFixture(t *testing.T, ctx context.Context, ss store.Store) (examAttemptFixture, model.SubmissionID, model.AttemptWorkspaceObjectID, *store.ExamSubmissionSeal, *store.CommandIdempotency) {
	t.Helper()
	f := newExamAttemptFixture(t, ctx, ss)
	connected, focus := connectFocusLossFixture(t, ctx, ss, f, "retention-connect")
	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID, ContinuityCredentialHash: focus.ContinuityCredentialHash}
	// This reservation models a remote write whose process disappeared before
	// confirming readiness. Retirement must keep its exact reconciliation key.
	_, err := ss.ExamAttemptWorkspace().ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()})
	requireNoError(t, err)
	object, err := ss.ExamAttemptWorkspace().ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()})
	requireNoError(t, err)
	_, err = ss.ExamAttemptWorkspace().MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 12, SHA256: strings.Repeat("a", 64)}})
	requireNoError(t, err)
	created, err := ss.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationCreateFile, EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: "private-answer.txt", ObjectID: object.ID, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}, examCommand(f.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, "retention-file", "retention-file"))
	requireNoError(t, err)
	sealAccess := store.ExamSubmissionSealAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, ConnectionID: connected.Connection.ID, CandidateUserID: f.candidate.ID, SessionID: f.session.ID, ContinuityCredentialHash: focus.ContinuityCredentialHash, ExpectedCurrentRevisionID: f.sitting.ExamRevisionID, ExpectedWorkspaceCursor: created.Change.Cursor}
	target, err := ss.ExamSubmission().ResolveSealTarget(ctx, sealAccess)
	requireNoError(t, err)
	input := &store.ExamSubmissionSeal{SubmissionID: model.NewSubmissionID(), Access: sealAccess, AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.MillisFromTime(target.SealAt)}
	attachSubmissionReceipt(t, f.candidate, input)
	key := examCommand(f.candidate.ID, store.ExamSubmissionSealOperation, "retention-seal", "retention-seal")
	sealed, err := ss.ExamSubmission().Seal(ctx, input, key)
	requireNoError(t, err)
	return f, sealed.Receipt.SubmissionID, object.ID, input, key
}
