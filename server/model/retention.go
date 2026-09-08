// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"time"
)

const (
	RetentionPreviewLifetime = time.Hour
	RetentionMaximumPageSize = 200
	ExamExportMaximumDays    = 7
)

type RetentionCategory string

const (
	RetentionCategoryWork                RetentionCategory = "work"
	RetentionCategoryIntegrity           RetentionCategory = "integrity"
	RetentionCategoryBrowserActivity     RetentionCategory = "browser_activity"
	RetentionCategorySecurityOperational RetentionCategory = "security_operational"
)

func (c RetentionCategory) IsValid() bool {
	return c == RetentionCategoryWork || c == RetentionCategoryIntegrity || c == RetentionCategoryBrowserActivity || c == RetentionCategorySecurityOperational
}

type RetentionControlState string

const (
	RetentionControlDisabled RetentionControlState = "disabled"
	RetentionControlEnabled  RetentionControlState = "enabled"
	RetentionControlPaused   RetentionControlState = "paused"
)

// RetentionControl records deliberate approval separately from configuration.
// Approval applies only to one exact Policy revision. A pause cancels pending
// retirement; enabling again always starts a new grace period for each record.
type RetentionControl struct {
	InstitutionID          InstitutionID
	Revision               int64
	State                  RetentionControlState
	ApprovedPolicyRevision int64
	ApprovedPreviewID      RetentionPreviewID
	ChangedByUserID        UserID
	UpdatedAt              time.Time
}

func (c *RetentionControl) Validate() error {
	if c == nil || !c.InstitutionID.IsValid() || c.Revision < 1 || c.UpdatedAt.IsZero() {
		return errors.New("model: invalid retention control")
	}
	switch c.State {
	case RetentionControlDisabled:
		if c.ApprovedPolicyRevision != 0 || !c.ApprovedPreviewID.IsZero() {
			return errors.New("model: disabled retention has approval")
		}
	case RetentionControlEnabled, RetentionControlPaused:
		if c.ApprovedPolicyRevision < 1 || !c.ApprovedPreviewID.IsValid() || !c.ChangedByUserID.IsValid() {
			return errors.New("model: invalid retention approval")
		}
	default:
		return errors.New("model: invalid retention control state")
	}
	return nil
}

func (c *RetentionControl) Permits(policy *RetentionPolicy) bool {
	return c.Validate() == nil && policy.Validate() == nil &&
		c.State == RetentionControlEnabled && c.InstitutionID == policy.InstitutionID &&
		c.ApprovedPolicyRevision == policy.Revision && policy.DeletionGraceDays > 0
}

// RetentionPreview contains aggregate counts, never examination content. Its
// timestamp and bounded lifetime describe a review of eligibility, not a frozen
// list of deletion commands. Every scheduled and final transition rechecks the
// live policy, completion, holds, and source protections independently.
type RetentionPreview struct {
	ID                  RetentionPreviewID
	InstitutionID       InstitutionID
	PolicyRevision      int64
	CreatedByUserID     UserID
	CreatedAt           time.Time
	ExpiresAt           time.Time
	Work                RetentionPreviewCounts
	Integrity           RetentionPreviewCounts
	BrowserActivity     RetentionPreviewCounts
	SecurityOperational RetentionPreviewCounts
	Audit               RetentionExpiryCounts
	Receipts            RetentionExpiryCounts
}

type RetentionPreviewCounts struct {
	Total            int64
	Eligible         int64
	AwaitingDeadline int64
	Incomplete       int64
	Held             int64
	Unconfigured     int64
	SupportingWork   int64
	ExportProtected  int64
	Retired          int64
}

func (p *RetentionPreview) Validate() error {
	if p == nil || !p.ID.IsValid() || !p.InstitutionID.IsValid() || p.PolicyRevision < 1 ||
		!p.CreatedByUserID.IsValid() || p.CreatedAt.IsZero() || !p.ExpiresAt.After(p.CreatedAt) ||
		p.ExpiresAt.Sub(p.CreatedAt) > RetentionPreviewLifetime {
		return errors.New("model: invalid retention preview")
	}
	for _, c := range []RetentionPreviewCounts{p.Work, p.Integrity, p.BrowserActivity, p.SecurityOperational} {
		if c.Total < 0 || c.Eligible < 0 || c.AwaitingDeadline < 0 || c.Incomplete < 0 ||
			c.Held < 0 || c.Unconfigured < 0 || c.SupportingWork < 0 || c.ExportProtected < 0 || c.Retired < 0 ||
			c.Total != c.Eligible+c.AwaitingDeadline+c.Incomplete+c.Held+c.Unconfigured+c.SupportingWork+c.ExportProtected+c.Retired {
			return errors.New("model: invalid retention preview counts")
		}
	}
	if err := p.Audit.Validate(); err != nil {
		return err
	}
	if err := p.Receipts.Validate(); err != nil {
		return err
	}
	return nil
}

type RetentionBlocker string

const (
	RetentionBlockerNone           RetentionBlocker = ""
	RetentionBlockerRetired        RetentionBlocker = "retired"
	RetentionBlockerIncomplete     RetentionBlocker = "records_incomplete"
	RetentionBlockerHold           RetentionBlocker = "preservation_hold"
	RetentionBlockerUnconfigured   RetentionBlocker = "retention_unconfigured"
	RetentionBlockerSupportingWork RetentionBlocker = "supporting_integrity"
	RetentionBlockerExport         RetentionBlocker = "export_in_progress"
	RetentionBlockerDeadline       RetentionBlocker = "retention_period"
)

// RetentionRecord is a content-free projection used for review and scheduling.
// SharedPublishedObjects reports references which will survive work retirement;
// retirement releases the Submission's reference, never the published material.
type RetentionRecord struct {
	Scope                  RetentionHoldScope
	Category               RetentionCategory
	CompletionRevision     int64
	CompletedAt            OptionalTime
	CompletionCurrent      bool
	HasIntegrity           bool
	Held                   bool
	ExportProtectedUntil   OptionalTime
	RetiredAt              OptionalTime
	SharedPublishedObjects int64
}

type RetentionEligibility struct {
	Blocker    RetentionBlocker
	EligibleAt OptionalTime
}

// Eligibility uses durations of 24 hours. Zero is indefinite/unconfigured.
// Supporting work uses the later configured deadline of the two categories;
// an indefinite integrity period protects that work indefinitely.
func (r RetentionRecord) Eligibility(policy *RetentionPolicy, at time.Time) (RetentionEligibility, error) {
	if policy.Validate() != nil || r.Scope.Validate() != nil || !r.Scope.SubmissionID.IsValid() ||
		!r.Category.IsValid() || at.IsZero() || r.SharedPublishedObjects < 0 ||
		(r.CompletionCurrent && (!r.CompletedAt.Valid || r.CompletedAt.Time.IsZero() || r.CompletionRevision < 1)) {
		return RetentionEligibility{}, errors.New("model: invalid retention record")
	}
	if r.RetiredAt.Valid {
		return RetentionEligibility{Blocker: RetentionBlockerRetired}, nil
	}
	if !r.CompletionCurrent {
		return RetentionEligibility{Blocker: RetentionBlockerIncomplete}, nil
	}
	var days int
	switch r.Category {
	case RetentionCategoryWork:
		days = policy.SubmissionRetentionDays
	case RetentionCategoryIntegrity:
		days = policy.IntegrityRetentionDays
	case RetentionCategoryBrowserActivity:
		days = policy.BrowserActivityRetentionDays
	case RetentionCategorySecurityOperational:
		days = policy.SecurityOperationalRetentionDays
	}
	var deadline OptionalTime
	if days > 0 {
		deadline = OptionalTimeFrom(r.CompletedAt.Time.Add(time.Duration(days) * 24 * time.Hour))
	}
	result := RetentionEligibility{EligibleAt: deadline}
	switch {
	case r.Held:
		result.Blocker = RetentionBlockerHold
	case days == 0:
		result.Blocker = RetentionBlockerUnconfigured
	case r.Category == RetentionCategoryWork && r.HasIntegrity && policy.IntegrityRetentionDays == 0:
		result.Blocker, result.EligibleAt = RetentionBlockerSupportingWork, OptionalTime{}
	default:
		if r.Category == RetentionCategoryWork && r.HasIntegrity && policy.IntegrityRetentionDays > days {
			result.EligibleAt = OptionalTimeFrom(r.CompletedAt.Time.Add(time.Duration(policy.IntegrityRetentionDays) * 24 * time.Hour))
		}
		if r.ExportProtectedUntil.Valid && r.ExportProtectedUntil.Time.After(at) {
			result.Blocker = RetentionBlockerExport
		} else if at.Before(result.EligibleAt.Time) {
			result.Blocker = RetentionBlockerDeadline
		}
	}
	return result, nil
}

type RetentionRetirementState string

const (
	RetentionRetirementGrace     RetentionRetirementState = "grace"
	RetentionRetirementCancelled RetentionRetirementState = "cancelled"
	RetentionRetirementCommitted RetentionRetirementState = "retired"
)

// RetentionRetirement is both the grace record and, after commit, the minimal
// receipt. It contains no path, answer, remark, evidence, or content digest.
type RetentionRetirement struct {
	ID                 RetentionRetirementID
	Scope              RetentionHoldScope
	Category           RetentionCategory
	State              RetentionRetirementState
	PolicyRevision     int64
	ControlRevision    int64
	CompletionRevision int64
	ScheduledAt        time.Time
	RetireAfter        time.Time
	RetiredAt          OptionalTime
	CancelledAt        OptionalTime
	CancellationReason string
	PurgePending       int64
	PurgeVerified      int64
}

func (r *RetentionRetirement) Validate() error {
	if r == nil || !r.ID.IsValid() || r.Scope.Validate() != nil || !r.Scope.SubmissionID.IsValid() ||
		!r.Category.IsValid() || r.PolicyRevision < 1 || r.ControlRevision < 1 || r.CompletionRevision < 1 ||
		r.ScheduledAt.IsZero() || !r.RetireAfter.After(r.ScheduledAt) || r.PurgePending < 0 || r.PurgeVerified < 0 {
		return errors.New("model: invalid retention retirement")
	}
	switch r.State {
	case RetentionRetirementGrace:
		if r.RetiredAt.Valid || r.CancelledAt.Valid || r.CancellationReason != "" || r.PurgePending != 0 || r.PurgeVerified != 0 {
			return errors.New("model: invalid retirement grace")
		}
	case RetentionRetirementCancelled:
		if r.RetiredAt.Valid || !r.CancelledAt.Valid || r.CancelledAt.Time.Before(r.ScheduledAt) || r.CancellationReason == "" {
			return errors.New("model: invalid cancelled retirement")
		}
	case RetentionRetirementCommitted:
		if !r.RetiredAt.Valid || r.RetiredAt.Time.Before(r.RetireAfter) || r.CancelledAt.Valid || r.CancellationReason != "" {
			return errors.New("model: invalid committed retirement")
		}
	default:
		return errors.New("model: invalid retirement state")
	}
	return nil
}
