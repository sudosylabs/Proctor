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

type RetentionExpiryKind string

const (
	RetentionExpiryAudit   RetentionExpiryKind = "audit"
	RetentionExpiryReceipt RetentionExpiryKind = "receipt"
)

func (k RetentionExpiryKind) IsValid() bool {
	return k == RetentionExpiryAudit || k == RetentionExpiryReceipt
}

type RetentionExpiryBlocker string

const (
	RetentionExpiryEligible        RetentionExpiryBlocker = ""
	RetentionExpiryDeadline        RetentionExpiryBlocker = "retention_period"
	RetentionExpiryUnconfigured    RetentionExpiryBlocker = "retention_unconfigured"
	RetentionExpiryUnfinished      RetentionExpiryBlocker = "unfinished"
	RetentionExpiryReferenced      RetentionExpiryBlocker = "referenced"
	RetentionExpiryHeld            RetentionExpiryBlocker = "preservation_hold"
	RetentionExpiryPurgePending    RetentionExpiryBlocker = "purge_pending"
	RetentionExpirySourceProtected RetentionExpiryBlocker = "export_in_progress"
)

// RetentionExpiryCounts partitions audit and receipt history by the first
// applicable blocker. Counts never include audit projections or private reasons.
type RetentionExpiryCounts struct {
	Total            int64
	Eligible         int64
	AwaitingDeadline int64
	Unconfigured     int64
	Unfinished       int64
	Referenced       int64
	Held             int64
	PurgePending     int64
	SourceProtected  int64
}

func (c RetentionExpiryCounts) Validate() error {
	if c.Total < 0 || c.Eligible < 0 || c.AwaitingDeadline < 0 || c.Unconfigured < 0 || c.Unfinished < 0 ||
		c.Referenced < 0 || c.Held < 0 || c.PurgePending < 0 || c.SourceProtected < 0 ||
		c.Total != c.Eligible+c.AwaitingDeadline+c.Unconfigured+c.Unfinished+c.Referenced+c.Held+c.PurgePending+c.SourceProtected {
		return errors.New("model: invalid retention expiry counts")
	}
	return nil
}

// RetentionExpiryRecord is a content-free lifecycle projection. EventAt is the
// audit's terminal event or the receipt's retirement/cancellation event. An
// attached completed recovery or released hold can extend its own evidence age.
// Grace starts only when all current dependencies permit expiry.
type RetentionExpiryRecord struct {
	Kind         RetentionExpiryKind
	ID           string
	EventAt      time.Time
	Blocker      RetentionExpiryBlocker
	EligibleAt   OptionalTime
	ScheduledAt  OptionalTime
	ExpiresAfter OptionalTime
}

func (r RetentionExpiryRecord) Validate() error {
	if !r.Kind.IsValid() || !IsValidId(r.ID) || r.EventAt.IsZero() ||
		r.ScheduledAt.Valid != r.ExpiresAfter.Valid ||
		(r.ScheduledAt.Valid && (r.ScheduledAt.Time.IsZero() || !r.ExpiresAfter.Time.After(r.ScheduledAt.Time))) {
		return errors.New("model: invalid retention expiry record")
	}
	switch r.Blocker {
	case RetentionExpiryEligible, RetentionExpiryDeadline:
		if !r.EligibleAt.Valid || r.EligibleAt.Time.Before(r.EventAt) {
			return errors.New("model: invalid retention expiry deadline")
		}
	case RetentionExpiryUnconfigured, RetentionExpiryUnfinished, RetentionExpiryReferenced, RetentionExpiryHeld, RetentionExpiryPurgePending, RetentionExpirySourceProtected:
	default:
		return errors.New("model: invalid retention expiry blocker")
	}
	return nil
}
