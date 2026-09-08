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
	"time"

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
	requireNoError(t, err)
	return a
}

func expiryRecord(t *testing.T, ctx context.Context, ss store.Store, kind model.RetentionExpiryKind, id string) model.RetentionExpiryRecord {
	t.Helper()
	options := store.RetentionExpiryListOptions{Kind: kind, Limit: 100}
	for {
		p, err := ss.Retention().ListExpiryRecords(ctx, options)
		requireNoError(t, err)
		if p == nil || p.Before.IsZero() || len(p.Items) > 100 {
			t.Fatal("invalid expiry page")
		}
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
			t.Fatalf("expiry record %s missing", id)
		}
		options.Before = p.Before
	}
}

func TestRetentionExpiryStore(t *testing.T, ss store.Store, probe RetentionExpirySQLProbe) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	installed, err := ss.Installation().Bootstrap(ctx, testInstallationBootstrap(946))
	requireNoError(t, err)
	principal := saveRecordsPrincipal(t, ctx, ss, installed.Administrator.ID, true)
	policy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	settings := model.RetentionPolicySettings{AuditRetentionDays: 1, DeletionGraceDays: 1}
	replace := func(key string) {
		input := &store.RetentionPolicyReplacement{RetentionMutation: store.RetentionMutation{Principal: principal, RecentAuthenticationTTL: time.Hour}, ExpectedRevision: policy.Revision, Settings: settings}
		prepareRetentionPolicyAttempt(t, ctx, ss, installed.Institution.ID, input)
		r, err := ss.RetentionPolicy().Replace(ctx, input, retentionPolicyCommand(principal.UserID, key))
		requireNoError(t, err)
		policy = r.Policy
	}
	mutation := func(action model.Action) store.RetentionMutation {
		a, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(action), Resource: model.Resource{Type: model.ResourceInstitution, ID: installed.Institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: installed.Institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "retention-expiry-test"})
		requireNoError(t, err)
		return store.RetentionMutation{Principal: principal, AuditEventID: a.ID.String(), AuditAt: model.GetMillis(), RecentAuthenticationTTL: time.Hour}
	}
	preview := func(key string) *model.RetentionPreview {
		p, err := ss.Retention().CreatePreview(ctx, &store.RetentionPreviewCreation{RetentionMutation: mutation(model.ActionRetentionPolicyView), PreviewID: model.NewRetentionPreviewID(), ExpectedPolicyRevision: policy.Revision}, examCommand(principal.UserID, store.RetentionPreviewOperation, key, key))
		requireNoError(t, err)
		return p
	}
	control := func(state model.RetentionControlState, key string) {
		current, err := ss.Retention().GetControl(ctx)
		requireNoError(t, err)
		input := &store.RetentionControlChange{RetentionMutation: mutation(model.ActionRetentionCleanupManage), State: state, ExpectedRevision: current.Revision, ExpectedPolicyRevision: policy.Revision}
		if state == model.RetentionControlEnabled {
			input.PreviewID = preview(key + "-preview").ID
		}
		_, err = ss.Retention().ChangeControl(ctx, input, examCommand(principal.UserID, store.RetentionControlOperation, key, key))
		requireNoError(t, err)
	}
	saveTarget := func(status model.AuditStatus) *model.AuditEvent {
		a, saveErr := ss.Audit().Save(ctx, &model.AuditEvent{Action: "audit.view", Resource: model.Resource{Type: model.ResourceInstitution, ID: installed.Institution.ID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: installed.Institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "retention-expiry-test"})
		requireNoError(t, saveErr)
		if status != model.AuditStatusAttempt {
			a, err = ss.Audit().Complete(ctx, a.ID.String(), status, "", nil, model.GetMillis())
			requireNoError(t, err)
		}
		probe.AgeAudit(t, ctx, a.ID.String())
		return a
	}
	makeInput := func(id string) *store.RetentionExpiryReconciliation {
		a := saveExpirySystemAudit(t, ctx, ss, installed.Institution.ID)
		return &store.RetentionExpiryReconciliation{Kind: model.RetentionExpiryAudit, RecordID: id, AuditEventID: a.ID.String(), AuditAt: model.GetMillis()}
	}
	reconcile := func(id string, expected store.RetentionExpiryResult) {
		r, err := ss.Retention().ReconcileExpiry(ctx, makeInput(id))
		requireNoError(t, err)
		if r == nil || *r != expected {
			t.Fatalf("expiry result=%#v expected=%#v", r, expected)
		}
	}
	target := saveTarget(model.AuditStatusSuccess)
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()); r.Blocker != model.RetentionExpiryUnconfigured {
		t.Fatalf("zero audit period=%#v", r)
	}
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	replace("expiry-policy")
	reconcile(target.ID.String(), store.RetentionExpiryResult{}) // configuration alone is insufficient
	control(model.RetentionControlEnabled, "expiry-enable")
	pending := saveTarget(model.AuditStatusAttempt)
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, pending.ID.String()); r.Blocker != model.RetentionExpiryUnfinished {
		t.Fatal("pending audit eligible")
	}
	reconcile(pending.ID.String(), store.RetentionExpiryResult{})
	p := preview("expiry-counts")
	if p.Audit.Eligible < 1 || p.Audit.Unfinished < 1 || p.Audit.Validate() != nil {
		t.Fatalf("audit preview=%#v", p.Audit)
	}
	missing := makeInput(target.ID.String())
	missing.AuditEventID = model.NewId()
	if _, err = ss.Retention().ReconcileExpiry(ctx, missing); err == nil {
		t.Fatal("expiry scheduled without required audit")
	}
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	original := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String())
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()); !r.ScheduledAt.Time.Equal(original.ScheduledAt.Time) {
		t.Fatal("natural retry extended grace")
	}
	unpin := probe.PinAudit(t, ctx, target.ID.String(), principal.UserID)
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()); r.Blocker != model.RetentionExpiryReferenced || r.ScheduledAt.Valid {
		t.Fatal("new durable reference did not cancel grace")
	}
	unpin()
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()); !r.ScheduledAt.Time.After(original.ScheduledAt.Time) {
		t.Fatal("resumed eligibility reused grace")
	}
	control(model.RetentionControlPaused, "expiry-pause")
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()); r.ScheduledAt.Valid {
		t.Fatal("paused control left expiry scheduled")
	}
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	control(model.RetentionControlEnabled, "expiry-resume")
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	settings.AuditRetentionDays = 2
	replace("expiry-longer")
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()); r.ScheduledAt.Valid {
		t.Fatal("new policy retained old expiry approval")
	}
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	control(model.RetentionControlEnabled, "expiry-new-approval")
	reconcile(target.ID.String(), store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryAudit, target.ID.String())
	bad := makeInput(target.ID.String())
	release := probe.RejectCompletion(t, ctx, bad.AuditEventID)
	if _, err = ss.Retention().ReconcileExpiry(ctx, bad); err == nil {
		t.Fatal("expiry committed without atomic audit completion")
	}
	release()
	if _, err = ss.Audit().Get(ctx, target.ID.String()); err != nil {
		t.Fatal("failed audit completion removed target")
	}
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, target.ID.String()); !r.ScheduledAt.Valid {
		t.Fatal("failed audit completion lost grace")
	}
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
		requireNoError(t, e)
		if r.Expired {
			expired++
		}
	}
	if expired != 1 {
		t.Fatalf("concurrent expiry count=%d", expired)
	}
	if _, err = ss.Audit().Get(ctx, target.ID.String()); !store.IsNotFound(err) {
		t.Fatal("expired audit remained readable")
	}
	// Same audit retry returns its committed outcome even after the target left.
	a, err := ss.Retention().ReconcileExpiry(ctx, first)
	requireNoError(t, err)
	b, err := ss.Retention().ReconcileExpiry(ctx, first)
	requireNoError(t, err)
	if *a != *b {
		t.Fatal("unknown commit replay changed its result")
	}
	reconcile(target.ID.String(), store.RetentionExpiryResult{})
	if _, err = ss.Audit().Get(ctx, pending.ID.String()); err != nil {
		t.Fatal("unfinished audit removed")
	}
	// Worker-generated audit belongs to its own event age and was not swept
	// merely because its target was old.
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, first.AuditEventID); r.Blocker != model.RetentionExpiryDeadline {
		t.Fatalf("cleanup audit did not get its own age: %#v", r)
	}
}

func TestRetentionReceiptExpiryStore(t *testing.T, ss store.Store, retentionProbe RetentionSQLProbe, probe RetentionExpirySQLProbe) {
	t.Helper()
	TestRetentionStore(t, ss, retentionProbe)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	page, err := ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 100})
	requireNoError(t, err)
	policy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	var work, integrity *model.RetentionRetirement
	for _, item := range page.Items {
		if item.Record.Category == model.RetentionCategoryWork {
			work = item.Retirement
		} else if item.Record.Category == model.RetentionCategoryIntegrity {
			integrity = item.Retirement
		}
	}
	if work == nil || integrity == nil {
		t.Fatal("retirement fixture missing")
	}
	reconcile := func(id model.RetentionRetirementID, expected store.RetentionExpiryResult) {
		a := saveExpirySystemAudit(t, ctx, ss, policy.InstitutionID)
		r, err := ss.Retention().ReconcileExpiry(ctx, &store.RetentionExpiryReconciliation{Kind: model.RetentionExpiryReceipt, RecordID: id.String(), AuditEventID: a.ID.String(), AuditAt: model.GetMillis()})
		requireNoError(t, err)
		if r == nil || *r != expected {
			t.Fatalf("receipt expiry=%#v want=%#v", r, expected)
		}
	}
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryReceipt, integrity.ID.String()); r.Blocker != model.RetentionExpiryDeadline {
		t.Fatal("receipt did not use own retirement event")
	}
	probe.AgeReceipt(t, ctx, work.ID)
	probe.AgeReceipt(t, ctx, integrity.ID)
	if r := expiryRecord(t, ctx, ss, model.RetentionExpiryReceipt, work.ID.String()); r.Blocker != model.RetentionExpiryPurgePending {
		t.Fatalf("unknown writer did not protect receipt: %#v", r)
	}
	reconcile(work.ID, store.RetentionExpiryResult{})
	reconcile(integrity.ID, store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryReceipt, integrity.ID.String())
	reconcile(integrity.ID, store.RetentionExpiryResult{Expired: true})
	probe.AssertMarkers(t, ctx, work.Scope.SubmissionID)
	if _, err = ss.ExamIntegrityReview().Get(ctx, work.Scope.SubmissionID); !store.IsConflict(err) && !store.IsNotFound(err) {
		t.Fatal("expired receipt restored integrity access")
	}
	// A finished writer with independent absence is the only way to release the
	// physical-purge dependency. This probe supplies that external fixture fact.
	probe.FinishReceiptPurge(t, ctx, work.ID)
	reconcile(work.ID, store.RetentionExpiryResult{Scheduled: true})
	probe.ExpireGrace(t, ctx, model.RetentionExpiryReceipt, work.ID.String())
	reconcile(work.ID, store.RetentionExpiryResult{Expired: true})
	reconcile(work.ID, store.RetentionExpiryResult{})
	probe.AssertMarkers(t, ctx, work.Scope.SubmissionID)
	if _, err = ss.ExamSubmission().Get(ctx, work.Scope.SubmissionID); !store.IsNotFound(err) {
		t.Fatal("expired receipt restored submission content")
	}
	page, err = ss.Retention().ListRecords(ctx, store.RetentionRecordListOptions{Limit: 100})
	requireNoError(t, err)
	for _, item := range page.Items {
		if item.Record.Category == model.RetentionCategoryBrowserActivity || item.Record.Category == model.RetentionCategorySecurityOperational {
			if item.Retirement != nil || item.Eligibility.Blocker != model.RetentionBlockerUnconfigured {
				t.Fatal("receipt expiry changed an independently retained category")
			}
			continue
		}
		if item.Retirement != nil || item.Eligibility.Blocker != model.RetentionBlockerRetired {
			t.Fatal("minimal history expiry changed logical retirement")
		}
	}
}

func TestRetentionCleanupAuditExpiryStore(t *testing.T, ss store.Store, probe RetentionExpirySQLProbe) {
	t.Helper()
	TestRetentionExpiryStore(t, ss, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	policy, err := ss.RetentionPolicy().Get(ctx)
	requireNoError(t, err)
	prepared := func() *model.AuditEvent {
		return &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: policy.InstitutionID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: policy.InstitutionID.String(), Status: model.AuditStatusAttempt, NodeID: "cleanup-expiry-test", ClientType: "system"}
	}
	for range 220 {
		a := prepared()
		a.Status = model.AuditStatusSuccess
		_, err := ss.Audit().Save(ctx, a)
		requireNoError(t, err)
	}
	before := probe.DatabaseTime(t, ctx)
	probe.AgeCleanupHistory(t, ctx)
	count := probe.CleanupAuditCount(t, ctx)
	if count < 220 {
		t.Fatal("cleanup audit seed missing")
	}
	bad := prepared()
	bad.NodeID = ""
	if _, err = ss.Retention().ReconcileCleanupAuditExpiry(ctx, &store.RetentionCleanupAuditReconciliation{Before: before, Limit: 100, Audit: bad}); err == nil {
		t.Fatal("changing batch accepted invalid critical audit")
	}
	if after := probe.CleanupAuditCount(t, ctx); after != count {
		t.Fatal("failed batch wrote audit history")
	}
	scan := func() int {
		before := probe.DatabaseTime(t, ctx)
		after := ""
		changed := 0
		passes := 0
		for {
			result, err := ss.Retention().ReconcileCleanupAuditExpiry(ctx, &store.RetentionCleanupAuditReconciliation{AfterID: after, Before: before, Limit: 100, Audit: prepared()})
			requireNoError(t, err)
			if result == nil || result.Examined > 100 || result.Changed > result.Examined || result.AfterID < after || !result.Before.Equal(before) {
				t.Fatalf("invalid bounded cleanup result=%#v", result)
			}
			changed += result.Changed
			passes++
			if passes > 10 {
				t.Fatal("cleanup scan grew its own history")
			}
			if !result.HasMore {
				return changed
			}
			after = result.AfterID
		}
	}
	if changed := scan(); changed != count {
		t.Fatalf("initial scheduled=%d want=%d", changed, count)
	}
	afterSchedule := probe.CleanupAuditCount(t, ctx)
	if afterSchedule-count != (count+99)/100 {
		t.Fatal("scheduling emitted more than one audit per changing batch")
	}
	if changed := scan(); changed != 0 || probe.CleanupAuditCount(t, ctx) != afterSchedule {
		t.Fatal("future grace/no-change scan created recursive audit")
	}
	// Whole repeated generations combine scheduling and deletion into one
	// summary per batch. The workflow's own history converges to a tiny bounded
	// tail instead of multiplying once for every scheduling/deletion event.
	previous := afterSchedule
	for cycle := range 6 {
		probe.AgeCleanupHistory(t, ctx)
		probe.ExpireCleanupGrace(t, ctx)
		scan()
		current := probe.CleanupAuditCount(t, ctx)
		if current > previous || current > 8 {
			t.Fatalf("cleanup generation %d multiplied history: before=%d after=%d", cycle, previous, current)
		}
		previous = current
		if changed := scan(); changed != 0 || probe.CleanupAuditCount(t, ctx) != current {
			t.Fatal("unchanged generation created a summary")
		}
	}
}

func TestRetentionAuditUnfinishedExamScope(t *testing.T, ss store.Store, probe RetentionExpirySQLProbe) {
	t.Helper()
	ctx := context.Background()
	f := newExamAttemptFixture(t, ctx, ss)
	a, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: f.manager.ID, Action: string(model.ActionExamManage), Resource: model.Resource{Type: model.ResourceExam, ID: f.examID.String()}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: f.unitID.String(), Status: model.AuditStatusSuccess, NodeID: "expiry-exam-test"})
	requireNoError(t, err)
	probe.AgeAudit(t, ctx, a.ID.String())
	record := expiryRecord(t, ctx, ss, model.RetentionExpiryAudit, a.ID.String())
	if record.Blocker != model.RetentionExpiryUnfinished {
		t.Fatalf("broad Exam audit ignored live Sitting descendants: %#v", record)
	}
}
