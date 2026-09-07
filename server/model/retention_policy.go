// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"math"
	"time"
)

// RetentionPolicyMaxDays bounds record retention and grace configuration; it
// is not a recommended period. Exports have their own finite lifetime ceiling.
const RetentionPolicyMaxDays = 36500

var (
	ErrRetentionPolicyRevisionConflict = errors.New("retention policy revision conflict")
	errRetentionPolicyInvalid          = errors.New("retention policy is invalid")
)

// RetentionPolicy records the Institution's retention preferences. Saving it
// does not authorize deletion; separate revision-bound approval is required.
type RetentionPolicy struct {
	InstitutionID InstitutionID
	Revision      int64
	RetentionPolicySettings
	// AutomaticDeletionEnabled is an authoritative read projection of the
	// separate control for this exact revision, never writable configuration.
	AutomaticDeletionEnabled bool
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// RetentionPolicySettings is a complete replacement. Zero means no configured
// expiry (or no configured grace period), never immediate deletion. Days are
// durations of 24 hours; no calendar or timezone-dependent calculation is used.
type RetentionPolicySettings struct {
	SubmissionRetentionDays int
	IntegrityRetentionDays  int
	AuditRetentionDays      int
	ExportRetentionDays     int
	DeletionGraceDays       int
	CandidateNotices        bool
}

// NewInitialRetentionPolicy preserves all records without selecting retention
// periods for the Institution. It is persisted atomically with bootstrap.
func NewInitialRetentionPolicy(institutionID InstitutionID, at time.Time) *RetentionPolicy {
	at = TimeUTC(at)
	return &RetentionPolicy{InstitutionID: institutionID, Revision: 1, CreatedAt: at, UpdatedAt: at}
}

func (s RetentionPolicySettings) Validate() error {
	for _, days := range []int{s.SubmissionRetentionDays, s.IntegrityRetentionDays,
		s.AuditRetentionDays, s.DeletionGraceDays} {
		if days < 0 || days > RetentionPolicyMaxDays {
			return errRetentionPolicyInvalid
		}
	}
	if s.ExportRetentionDays < 0 || s.ExportRetentionDays > ExamExportMaximumDays {
		return errRetentionPolicyInvalid
	}
	return nil
}

func (p *RetentionPolicy) Validate() error {
	if p == nil || !p.InstitutionID.IsValid() || p.Revision < 1 ||
		p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) {
		return errRetentionPolicyInvalid
	}
	return p.RetentionPolicySettings.Validate()
}

// Replace applies an optimistic replacement. A no-op preserves the revision
// and time. A rejected replacement leaves the policy unchanged.
func (p *RetentionPolicy) Replace(expectedRevision int64, settings RetentionPolicySettings, at time.Time) error {
	if p.Validate() != nil || settings.Validate() != nil || at.IsZero() {
		return errRetentionPolicyInvalid
	}
	if expectedRevision != p.Revision {
		return ErrRetentionPolicyRevisionConflict
	}
	at = TimeUTC(at)
	if at.Before(p.UpdatedAt) {
		return errRetentionPolicyInvalid
	}
	if p.RetentionPolicySettings == settings {
		return nil
	}
	if p.Revision == math.MaxInt64 {
		return errRetentionPolicyInvalid
	}
	p.RetentionPolicySettings = settings
	p.AutomaticDeletionEnabled = false
	p.Revision++
	p.UpdatedAt = at
	return nil
}

func (p *RetentionPolicy) Clone() *RetentionPolicy {
	if p == nil {
		return nil
	}
	clone := *p
	return &clone
}

// Auditable contains only bounded policy values, never examination content.
func (p *RetentionPolicy) Auditable() map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"institution_id": p.InstitutionID.String(), "revision": p.Revision,
		"submission_retention_days":  p.SubmissionRetentionDays,
		"integrity_retention_days":   p.IntegrityRetentionDays,
		"audit_retention_days":       p.AuditRetentionDays,
		"export_retention_days":      p.ExportRetentionDays,
		"deletion_grace_days":        p.DeletionGraceDays,
		"candidate_notices":          p.CandidateNotices,
		"automatic_deletion_enabled": p.AutomaticDeletionEnabled,
	}
}

var _ Auditable = (*RetentionPolicy)(nil)
