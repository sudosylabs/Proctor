// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"
)

func TestRetentionEligibility(t *testing.T) {
	completed := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	policy := NewInitialRetentionPolicy(NewInstitutionID(), completed)
	policy.RetentionPolicySettings = RetentionPolicySettings{
		SubmissionRetentionDays: 10, IntegrityRetentionDays: 20, DeletionGraceDays: 7,
	}
	base := RetentionRecord{
		Scope:    RetentionHoldScope{ExamID: NewExamID(), SittingID: NewExamSittingID(), SubmissionID: NewSubmissionID()},
		Category: RetentionCategoryWork, CompletionRevision: 3,
		CompletedAt: OptionalTimeFrom(completed), CompletionCurrent: true,
	}
	tests := []struct {
		name     string
		edit     func(*RetentionRecord, *RetentionPolicy)
		days     int
		want     RetentionBlocker
		deadline int
	}{
		{name: "before work deadline", days: 9, want: RetentionBlockerDeadline, deadline: 10},
		{name: "at work deadline", days: 10, deadline: 10},
		{name: "supporting work waits", days: 10, edit: func(r *RetentionRecord, _ *RetentionPolicy) { r.HasIntegrity = true }, want: RetentionBlockerDeadline, deadline: 20},
		{name: "supporting work reaches later deadline", days: 20, edit: func(r *RetentionRecord, _ *RetentionPolicy) { r.HasIntegrity = true }, deadline: 20},
		{name: "indefinite integrity protects supporting work", days: 100, edit: func(r *RetentionRecord, p *RetentionPolicy) { r.HasIntegrity = true; p.IntegrityRetentionDays = 0 }, want: RetentionBlockerSupportingWork},
		{name: "no integrity does not inherit indefinite period", days: 10, edit: func(_ *RetentionRecord, p *RetentionPolicy) { p.IntegrityRetentionDays = 0 }, deadline: 10},
		{name: "unconfigured work remains", days: 100, edit: func(_ *RetentionRecord, p *RetentionPolicy) { p.SubmissionRetentionDays = 0 }, want: RetentionBlockerUnconfigured},
		{name: "hold blocks", days: 100, edit: func(r *RetentionRecord, _ *RetentionPolicy) { r.Held = true }, want: RetentionBlockerHold, deadline: 10},
		{name: "late evidence stales completion", days: 100, edit: func(r *RetentionRecord, _ *RetentionPolicy) { r.CompletionCurrent = false }, want: RetentionBlockerIncomplete},
		{name: "export source protected", days: 10, edit: func(r *RetentionRecord, _ *RetentionPolicy) {
			r.ExportProtectedUntil = OptionalTimeFrom(completed.Add(11 * 24 * time.Hour))
		}, want: RetentionBlockerExport, deadline: 10},
		{name: "export source protection ends at deadline", days: 11, edit: func(r *RetentionRecord, _ *RetentionPolicy) {
			r.ExportProtectedUntil = OptionalTimeFrom(completed.Add(11 * 24 * time.Hour))
		}, deadline: 10},
		{name: "shared published objects survive independently", days: 10, edit: func(r *RetentionRecord, _ *RetentionPolicy) { r.SharedPublishedObjects = 3 }, deadline: 10},
		{name: "retired never revives after hold", days: 100, edit: func(r *RetentionRecord, _ *RetentionPolicy) { r.Held = true; r.RetiredAt = OptionalTimeFrom(completed) }, want: RetentionBlockerRetired},

		{name: "browser uses independent deadline", days: 2, edit: func(r *RetentionRecord, p *RetentionPolicy) {
			r.Category = RetentionCategoryBrowserActivity
			r.HasIntegrity = true
			p.BrowserActivityRetentionDays = 3
			p.IntegrityRetentionDays = 0
		}, want: RetentionBlockerDeadline, deadline: 3},
		{name: "native operational uses independent deadline", days: 4, edit: func(r *RetentionRecord, p *RetentionPolicy) {
			r.Category = RetentionCategorySecurityOperational
			p.SecurityOperationalRetentionDays = 4
		}, deadline: 4},
		{name: "indefinite browser is not inherited integrity expiry", days: 100, edit: func(r *RetentionRecord, p *RetentionPolicy) { r.Category = RetentionCategoryBrowserActivity }, want: RetentionBlockerUnconfigured},
		{name: "browser hold keeps own deadline", days: 100, edit: func(r *RetentionRecord, p *RetentionPolicy) {
			r.Category = RetentionCategoryBrowserActivity
			r.Held = true
			p.BrowserActivityRetentionDays = 2
		}, want: RetentionBlockerHold, deadline: 2},
		{name: "integrity uses own period", days: 19, edit: func(r *RetentionRecord, _ *RetentionPolicy) { r.Category = RetentionCategoryIntegrity }, want: RetentionBlockerDeadline, deadline: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, current := base, policy.Clone()
			if tt.edit != nil {
				tt.edit(&record, current)
			}
			got, err := record.Eligibility(current, completed.Add(time.Duration(tt.days)*24*time.Hour))
			if err != nil || got.Blocker != tt.want {
				t.Fatalf("Eligibility = %+v, %v; want blocker %q", got, err, tt.want)
			}
			if tt.deadline == 0 {
				if got.EligibleAt.Valid {
					t.Fatalf("unexpected deadline: %v", got.EligibleAt)
				}
			} else if !got.EligibleAt.Valid || !got.EligibleAt.Time.Equal(completed.Add(time.Duration(tt.deadline)*24*time.Hour)) {
				t.Fatalf("deadline = %v; want +%d days", got.EligibleAt, tt.deadline)
			}
		})
	}
}

func TestRetentionControlCannotAuthorizeAnotherRevision(t *testing.T) {
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	policy := NewInitialRetentionPolicy(NewInstitutionID(), at)
	policy.DeletionGraceDays = 7
	control := &RetentionControl{InstitutionID: policy.InstitutionID, Revision: 1,
		State: RetentionControlEnabled, ApprovedPolicyRevision: 1, ApprovedPreviewID: NewRetentionPreviewID(),
		ChangedByUserID: NewUserID(), UpdatedAt: at}
	if !control.Permits(policy) {
		t.Fatal("reviewed policy should be permitted")
	}
	policy.Revision++
	if control.Permits(policy) {
		t.Fatal("configuration change silently retained approval")
	}
	policy.Revision--
	control.State = RetentionControlPaused
	if control.Permits(policy) {
		t.Fatal("pause permitted retirement")
	}
	control.State = RetentionControlEnabled
	policy.DeletionGraceDays = 0
	if control.Permits(policy) {
		t.Fatal("unconfigured grace permitted retirement")
	}
}
