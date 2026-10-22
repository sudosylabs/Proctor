// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type RetentionExpirySQLProbe struct {
	DatabaseTime       func(*testing.T, context.Context) time.Time
	AgeCleanupHistory  func(*testing.T, context.Context)
	ExpireCleanupGrace func(*testing.T, context.Context)
	CleanupAuditCount  func(*testing.T, context.Context) int
	AgeAudit           func(*testing.T, context.Context, string)
	ExpireGrace        func(*testing.T, context.Context, model.RetentionExpiryKind, string)
	PinAudit           func(*testing.T, context.Context, string, model.UserID) func()
	RejectCompletion   func(*testing.T, context.Context, string) func()
	AgeReceipt         func(*testing.T, context.Context, model.RetentionRetirementID)
	FinishReceiptPurge func(*testing.T, context.Context, model.RetentionRetirementID)
	AssertMarkers      func(*testing.T, context.Context, model.SubmissionID)
	ConcurrentPeer     store.RetentionStore
}

func saveExpirySystemAudit(t *testing.T, ctx context.Context, ss store.Store, institution model.InstitutionID) *model.AuditEvent {
	t.Helper()
	a, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.String(), Status: model.AuditStatusAttempt, NodeID: "retention-expiry-test"})
	require.NoError(t, err)
	return a
}

func expiryRecord(t *testing.T, ctx context.Context, ss store.Store, kind model.RetentionExpiryKind, id string) model.RetentionExpiryRecord {
	t.Helper()
	options := store.RetentionExpiryListOptions{Kind: kind, Limit: 100}
	for {
		p, err := ss.Retention().ListExpiryRecords(ctx, options)
		require.NoError(t, err)
		require.NotNil(t, p, "invalid expiry page")
		require.False(t, p.Before.IsZero(), "invalid expiry page")
		require.LessOrEqual(t, len(p.Items), 100, "invalid expiry page")
		for _, r := range p.Items {
			if r.ID == id {
				return r
			}
			options.AfterID = r.ID
		}
		if !p.HasMore {
			if kind == model.RetentionExpiryAudit && !options.CleanupAudit {
				options.CleanupAudit = true
				options.AfterID = ""
				options.Before = time.Time{}
				continue
			}
			require.FailNow(t, fmt.Sprintf("expiry record %s missing", id))
		}
		options.Before = p.Before
	}
}

func TestRetentionExpiryStore(t *testing.T, ss store.Store, probe RetentionExpirySQLProbe) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(946))
	require.NoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, installed.Administrator.ID, true)
	policy, err := ss.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	settings := model.RetentionPolicySettings{AuditRetentionDays: 1, DeletionGraceDays: 1}
	replace := func(key string) {
		input := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: policy.Revision, Settings: settings}
		prepareRetentionPolicyAttempt(t, ctx, ss, installed.Institution.ID, input)
		r, err := ss.RetentionPolicy().Replace(ctx, input, retentionPolicyCommand(principal.UserID, key))
		require.NoError(t, err)
		policy = r.Policy
	}
	mutation := func(action model.Action) store.RetentionMutation {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(action), Resource: model.Resource{Type: model.ResourceInstitution, ID: installed.Institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: installed.Institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "retention-expiry-test"})
		require.NoError(t, err)
		return store.RetentionMutation{Principal: principal, AuditEventID: a.ID, AuditAt: model.NowUTC(), RecentAuthenticationTTL: time.Hour}
	}
	preview := func(key string) *model.RetentionPreview {
		p, err := ss.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView), PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: policy.Revision}, examCommand(principal.UserID, store.RetentionPreviewOperation, key, key))
		require.NoError(t, err)
		return p
	}
	control := func(state model.RetentionControlState, key string) {
		current, err := ss.Retention().GetControl(ctx)
		require.NoError(t, err)
		input := &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage), State: state, ExpectedRevision: current.Revision, ExpectedPolicyRevision: policy.Revision}
		if state == model.RetentionControlEnabled {
			input.PreviewID = preview(key + "-preview").ID
		}
		_, err = ss.Retention().ChangeControl(ctx, input, examCommand(principal.UserID, store.RetentionControlOperation, key, key))
		require.NoError(t, err)
	}
	saveTarget := func(status model.AuditStatus) *model.AuditEvent {
		a, saveErr := ss.Audit().Save(ctx, &model.AuditEvent{Action: "audit.view", Resource: model.Resource{Type: model.ResourceInstitution, ID: installed.Institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: installed.Institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "retention-expiry-test"})
		require.NoError(t, saveErr)
		if status != model.AuditStatusAttempt {
			a, err = ss.Audit().Complete(ctx, a.ID, status, "", nil, model.NowUTC())
			require.NoError(t, err)
		}
		probe.AgeAudit(t, ctx, a.ID.String())
		return a
	}
	makeInput := func(id string) *store.RetentionExpiryReconciliation {
		a := saveExpirySystemAudit(t, ctx, ss, installed.Institution.ID)
		return &store.RetentionExpiryReconciliation{Kind: model.RetentionExpiryAudit, RecordID: id, AuditEventID: a.ID, AuditAt: model.NowUTC()}
	}
	reconcile := func(id string, expected store.RetentionExpiryResult) {
		r, err := ss.Retention().ReconcileExpiry(ctx, makeInput(id))
		require.NoError(t, err)
		require.NotNil(t, r, "expiry result")
		require.Equal(t, expected, *r, "expiry result")
	}
	target := saveTarget(model.AuditStatusSuccess)
	require.Equal(t, model.RetentionExpiryUnconfigured, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()).Blocker)
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	replace("expiry-policy")
	reconcile(target.ID.String(), store.RetentionExpiryResult{}) // configuration alone is insufficient
	control(model.RetentionControlEnabled, "expiry-enable")
	pending := saveTarget(model.AuditStatusAttempt)
	require.Equal(t, model.RetentionExpiryUnfinished, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, pending.ID.String()).Blocker, "pending audit eligible")
	reconcile(pending.ID.String(), store.RetentionExpiryResult{})
	p := preview("expiry-counts")
	require.GreaterOrEqual(t, p.Audit.Eligible, int64(1))
	require.GreaterOrEqual(t, p.Audit.Unfinished, int64(1))
	require.NoError(t, p.Audit.Validate())
	missing := makeInput(target.ID.String())
	missing.AuditEventID = model.NewAuditEventID()
	_, err = ss.Retention().ReconcileExpiry(ctx, missing)
	require.Error(t, err, "expiry scheduled without required audit")
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	original := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String())
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	require.True(t, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()).ScheduledAt.Time.Equal(original.ScheduledAt.Time), "natural retry extended grace")
	unpin := probe.PinAudit(t, ctx, target.ID.String(), principal.UserID)
	r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String())
	require.Equal(t, model.RetentionExpiryReferenced, r.Blocker, "new durable reference did not cancel grace")
	require.False(t, r.ScheduledAt.Valid, "new durable reference did not cancel grace")
	unpin()
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	require.True(t, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()).ScheduledAt.Time.After(original.ScheduledAt.Time), "resumed eligibility reused grace")
	control(model.RetentionControlPaused, "expiry-pause")
	require.False(t, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()).ScheduledAt.Valid, "paused control left expiry scheduled")
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	control(model.RetentionControlEnabled, "expiry-resume")
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	settings.AuditRetentionDays = 2
	replace("expiry-longer")
	require.False(t, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()).ScheduledAt.Valid, "new policy retained old expiry approval")
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	control(model.RetentionControlEnabled, "expiry-new-approval")
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryAudit, target.ID.String())
	bad := makeInput(target.ID.String())
	release := probe.RejectCompletion(t, ctx, bad.AuditEventID.String())
	_, err = ss.Retention().ReconcileExpiry(ctx, bad)
	require.Error(t, err, "expiry committed without atomic audit completion")
	release()
	_, err = ss.Audit().Get(ctx, target.ID)
	require.NoError(t, err, "failed audit completion removed target")
	require.True(t, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()).ScheduledAt.Valid, "failed audit completion lost grace")
	// Two nodes using distinct audit attempts may only expire once.
	first, second := makeInput(target.ID.String()), makeInput(target.ID.String())
	peer := probe.ConcurrentPeer
	if peer == nil {
		peer = ss.Retention()
	}
	results := make(chan *store.RetentionExpiryResult, 2)
	errs := make(chan error, 2)
	start := make(chan struct{})
	for i, owner := range []store.RetentionStore{ss.Retention(), peer} {
		input := []*store.RetentionExpiryReconciliation{first, second}[i]
		go func() { <-start; r, e := owner.ReconcileExpiry(ctx, input); results <- r; errs <- e }()
	}
	close(start)
	expired := 0
	for range 2 {
		r, e := <-results, <-errs
		require.NoError(t, e)
		if r.Expired {
			expired++
		}
	}
	require.Equal(t, 1, expired, "concurrent expiry count")
	_, err = ss.Audit().Get(ctx, target.ID)
	require.True(t, store.IsNotFound(err), "expired audit remained readable")
	// Same audit retry returns its committed outcome even after the target left.
	a, err := ss.Retention().ReconcileExpiry(ctx, first)
	require.NoError(t, err)
	b, err := ss.Retention().ReconcileExpiry(ctx, first)
	require.NoError(t, err)
	require.Equal(t, *b, *a, "unknown commit replay changed its result")
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	_, err = ss.Audit().Get(ctx, pending.ID)
	require.NoError(t, err, "unfinished audit removed")
	// Worker-generated audit belongs to its own event age and was not swept
	// merely because its target was old.
	require.Equal(t, model.RetentionExpiryDeadline, expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, first.AuditEventID.String()).Blocker)
}

func TestRetentionReceiptExpiryStore(t *testing.T, ss store.Store, retentionProbe RetentionSQLProbe, probe RetentionExpirySQLProbe) {
	t.Helper()
	TestRetentionStore(t, ss, retentionProbe)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	page, err := ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 100})
	require.NoError(t, err)
	policy, err := ss.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	var work, integrity *model.RetentionRetirement
	for _, item := range page.Items {
		if item.Record.Category == model.RetentionCategoryWork {
			work = item.Retirement
		} else if item.Record.Category == model.RetentionCategoryIntegrity {
			integrity = item.Retirement
		}
	}
	require.NotNil(t, work, "retirement fixture missing")
	require.NotNil(t, integrity, "retirement fixture missing")
	reconcile := func(id model.RetentionRetirementID, expected store.RetentionExpiryResult) {
		a := saveExpirySystemAudit(t, ctx, ss, policy.InstitutionID)
		r, reconcileErr := ss.Retention().ReconcileExpiry(ctx, &store.RetentionExpiryReconciliation{Kind: model.RetentionExpiryReceipt, RecordID: id.String(), AuditEventID: a.ID, AuditAt: model.NowUTC()})
		require.NoError(t, reconcileErr)
		require.NotNil(t, r, "receipt expiry")
		require.Equal(t, expected, *r, "receipt expiry")
	}
	require.Equal(t, model.RetentionExpiryDeadline, expiryRecord(t, ctx, ss, model.RetentionExpiryReceipt, integrity.ID.String()).Blocker, "receipt did not use own retirement event")
	probe.AgeReceipt(t, ctx, work.ID)
	probe.AgeReceipt(t, ctx, integrity.ID)
	require.Equal(t, model.RetentionExpiryPurgePending, expiryRecord(t, ctx, ss, model.RetentionExpiryReceipt, work.ID.String()).Blocker)
	reconcile(work.ID, store.RetentionExpiryResult{})
	reconcile(integrity.ID, store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryReceipt, integrity.ID.String())
	reconcile(integrity.ID, store.RetentionExpiryResult{Expired: true})
	probe.AssertMarkers(t, ctx, work.Scope.SubmissionID)
	_, err = ss.ExamIntegrityReview().Get(ctx, work.Scope.SubmissionID)
	if !store.IsConflict(err) {
		require.True(t, store.IsNotFound(err), "expired receipt restored integrity access")
	}
	// A finished writer with independent absence is the only way to release the
	// physical-purge dependency. This probe supplies that external fixture fact.
	probe.FinishReceiptPurge(t, ctx, work.ID)
	reconcile(work.ID, store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryReceipt, work.ID.String())
	reconcile(work.ID, store.RetentionExpiryResult{Expired: true})
	reconcile(work.ID, store.RetentionExpiryResult{})
	probe.AssertMarkers(t, ctx, work.Scope.SubmissionID)
	_, err = ss.ExamSubmission().Get(ctx, work.Scope.SubmissionID)
	require.True(t, store.IsNotFound(err), "expired receipt restored submission content")
	page, err = ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 100})
	require.NoError(t, err)
	for _, item := range page.Items {
		if item.Record.Category == model.RetentionCategoryBrowserActivity || item.Record.Category == model.RetentionCategorySecurityOperational {
			require.Nil(t, item.Retirement, "receipt expiry changed an independently retained category")
			require.Equal(t, model.RetentionBlockerUnconfigured, item.Eligibility.Blocker, "receipt expiry changed an independently retained category")
			continue
		}
		require.Nil(t, item.Retirement, "minimal history expiry changed logical retirement")
		require.Equal(t, model.RetentionBlockerRetired, item.Eligibility.Blocker, "minimal history expiry changed logical retirement")
	}
}

func TestRetentionCleanupAuditExpiryStore(t *testing.T, ss store.Store, probe RetentionExpirySQLProbe) {
	t.Helper()
	TestRetentionExpiryStore(t, ss, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	policy, err := ss.RetentionPolicy().Get(ctx)
	require.NoError(t, err)
	prepared := func() *model.AuditEvent {
		return &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: policy.InstitutionID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: policy.InstitutionID.String(), Status: model.AuditStatusAttempt, NodeID: "cleanup-expiry-test", ClientType: "system"}
	}
	for range 220 {
		a := prepared()
		a.Status = model.AuditStatusSuccess
		_, err := ss.Audit().Save(ctx, a)
		require.NoError(t, err)
	}
	before := probe.DatabaseTime(t, ctx)
	probe.AgeCleanupHistory(t, ctx)
	count := probe.CleanupAuditCount(t, ctx)
	require.GreaterOrEqual(t, count, 220, "cleanup audit seed missing")
	bad := prepared()
	bad.NodeID = ""
	_, err = ss.Retention().ReconcileCleanupAuditExpiry(ctx, &store.RetentionCleanupAuditReconciliation{Before: before, Limit: 100, AuditEvent: bad})
	require.Error(t, err, "changing batch accepted invalid critical audit")
	require.Equal(t, count, probe.CleanupAuditCount(t, ctx), "failed batch wrote audit history")
	scan := func() int {
		before := probe.DatabaseTime(t, ctx)
		var after model.AuditEventID
		changed := 0
		passes := 0
		for {
			result, err := ss.Retention().ReconcileCleanupAuditExpiry(ctx, &store.RetentionCleanupAuditReconciliation{AfterID: after, Before: before, Limit: 100, AuditEvent: prepared()})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.LessOrEqual(t, result.Examined, 100)
			require.LessOrEqual(t, result.Changed, result.Examined)
			require.GreaterOrEqual(t, result.AfterID, after)
			require.True(t, result.Before.Equal(before))
			changed += result.Changed
			passes++
			require.LessOrEqual(t, passes, 10, "cleanup scan grew its own history")
			if !result.HasMore {
				return changed
			}
			after = result.AfterID
		}
	}
	require.Equal(t, count, scan(), "initial scheduled")
	afterSchedule := probe.CleanupAuditCount(t, ctx)
	require.Equal(t, (count+99)/100, afterSchedule-count, "scheduling emitted more than one audit per changing batch")
	require.Equal(t, 0, scan(), "future grace/no-change scan created recursive audit")
	require.Equal(t, afterSchedule, probe.CleanupAuditCount(t, ctx), "future grace/no-change scan created recursive audit")
	// Whole repeated generations combine scheduling and deletion into one
	// summary per batch. The workflow's own history converges to a tiny bounded
	// tail instead of multiplying once for every scheduling/deletion event.
	previous := afterSchedule
	for cycle := range 6 {
		probe.AgeCleanupHistory(t, ctx)
		probe.ExpireCleanupGrace(t, ctx)
		scan()
		current := probe.CleanupAuditCount(t, ctx)
		require.LessOrEqual(t, current, previous, "cleanup generation %d multiplied history: before=%d after", cycle, previous)
		require.LessOrEqual(t, current, 8, "cleanup generation %d multiplied history: before=%d after", cycle, previous)
		previous = current
		require.Equal(t, 0, scan(), "unchanged generation created a summary")
		require.Equal(t, current, probe.CleanupAuditCount(t, ctx), "unchanged generation created a summary")
	}
}

func TestRetentionAuditUnfinishedExamScope(t *testing.T, ss store.Store, probe RetentionExpirySQLProbe) {
	t.Helper()
	ctx := context.Background()
	f := newExamAttemptFixture(t, ctx, ss)
	a, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: f.manager.ID, Action: string(model.ActionExamManage), Resource: model.Resource{Type: model.ResourceExam, ID: f.examID.String()}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), Status: model.AuditStatusSuccess, NodeID: "expiry-exam-test"})
	require.NoError(t, err)
	probe.AgeAudit(t, ctx, a.ID.String())
	record := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, a.ID.String())
	require.Equal(t, model.RetentionExpiryUnfinished, record.Blocker)
}
