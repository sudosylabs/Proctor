// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// Before is an immutable upper event-time bound shared by all pages of one
// finite scan. A zero value selects the database clock for the first page.
type RetentionExpiryListOptions struct {
	Kind         model.RetentionExpiryKind
	CleanupAudit bool
	AfterID      string
	Before       time.Time
	Limit        int
}

type RetentionExpiryPage struct {
	InstitutionID model.InstitutionID
	Before        time.Time
	Items         []model.RetentionExpiryRecord
	HasMore       bool
}

type RetentionExpiryReconciliation struct {
	Kind         model.RetentionExpiryKind
	RecordID     string
	AuditEventID string
	AuditAt      int64
}

type RetentionExpiryResult struct {
	Scheduled bool
	Cancelled bool
	Expired   bool
}

// RetentionCleanupAuditReconciliation coalesces the cleanup workflow's own
// terminal audit history. A prepared actorless attempt is inserted only when
// a bounded batch changes state, then completed with scalar counts atomically.
// A no-change scan persists neither an audit nor another work item.
type RetentionCleanupAuditReconciliation struct {
	AfterID string
	Before  time.Time
	Limit   int
	Audit   *model.AuditEvent
}
type RetentionCleanupAuditResult struct {
	Before    time.Time
	AfterID   string
	HasMore   bool
	Examined  int
	Changed   int
	Scheduled int
	Cancelled int
	Expired   int
}
