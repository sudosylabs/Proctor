// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	RetentionPreviewOperation = "retention.preview.v1"
	RetentionControlOperation = "retention.control.v1"
)

type RetentionMutation struct {
	Principal               model.Principal
	AuditEventID            string
	AuditAt                 int64
	RecentAuthenticationTTL time.Duration
}

type RetentionPreviewCreation struct {
	RetentionMutation
	PreviewID              model.RetentionPreviewID
	ExpectedPolicyRevision int64
}

type RetentionControlChange struct {
	RetentionMutation
	ExpectedRevision       int64
	ExpectedPolicyRevision int64
	PreviewID              model.RetentionPreviewID
	State                  model.RetentionControlState
}

type RetentionControlResult struct {
	Control  *model.RetentionControl
	Replayed bool
}

type RetentionRecordListOptions struct {
	AfterSubmissionID model.SubmissionID
	Limit             int
}

type RetentionRecordItem struct {
	Record      model.RetentionRecord
	Eligibility model.RetentionEligibility
	Retirement  *model.RetentionRetirement
}

type RetentionRecordPage struct {
	PolicyRevision int64
	AsOf           time.Time
	Items          []RetentionRecordItem
	HasMore        bool
}

// RetentionReconciliation is one bounded, naturally idempotent unit of
// institution-approved work. Persistence uses its own clock and authoritative
// state; neither the worker nor a preview supplies eligibility assertions.
type RetentionReconciliation struct {
	SubmissionID model.SubmissionID
	AuditEventID string
	AuditAt      int64
}

type RetentionReconciliationResult struct {
	Scheduled int
	Cancelled int
	Retired   int
}

// RetentionPurgeObject names one exact immutable backend key after all allowed
// references were released. Keys remain internal to the content boundary. A
// successful delete acknowledgement does not complete this record: completion
// requires a subsequent independent observation that the current key is absent.
type RetentionPurgeObject struct {
	RetirementID model.RetentionRetirementID
	ObjectID     model.AttemptWorkspaceObjectID
}

type RetentionPurgeCompletion struct {
	RetirementID model.RetentionRetirementID
	ObjectID     model.AttemptWorkspaceObjectID
}

type RetentionNoticeMail struct {
	RetirementID    model.RetentionRetirementID
	RecipientUserID model.UserID
	Mail            *PreparedMail
	FailureCode     string
}

// RetentionStore owns review, explicit policy approval, per-record grace,
// cancellation and the irreversible logical retirement boundary. Saving a
// policy never enables this workflow. Mutations reauthorize the current
// protected administrator Session, including replay, and complete critical
// audit atomically. Reconcile locks Policy/control, Exam, Sitting and Submission
// in that order, rechecks completion and every hold/source protection, and
// creates durable recipient notices in the same transaction as grace.
//
// Only Commit retirement may remove sealed content. It leaves a content-free
// receipt, closes late ingestion, and reserves exact unreferenced objects for
// retryable physical purge. A later hold or policy change cannot revive content.
// Ordinary mutations retain their immutable-record guards.
type RetentionStore interface {
	GetControl(context.Context) (*model.RetentionControl, error)
	CreatePreview(context.Context, *RetentionPreviewCreation, *CommandIdempotency) (*model.RetentionPreview, error)
	GetPreview(context.Context, model.RetentionPreviewID) (*model.RetentionPreview, error)
	ChangeControl(context.Context, *RetentionControlChange, *CommandIdempotency) (*RetentionControlResult, error)
	ListRecords(context.Context, RetentionRecordListOptions) (*RetentionRecordPage, error)
	ReconcileSubmission(context.Context, *RetentionReconciliation) (*RetentionReconciliationResult, error)
	// BeginPurgeBatch atomically selects at most limit eligible exact keys in
	// oldest-due order and defers their next attempt for one hour before I/O.
	// Concurrent callers skip a selected batch. A lost result may defer keys
	// without attempting them, but never records absence or releases references.
	// Callers reserve their bounded work budget before beginning this mutation.
	BeginPurgeBatch(context.Context, int) ([]RetentionPurgeObject, error)
	CompletePurge(context.Context, *RetentionPurgeCompletion) error
	ListNotices(context.Context, model.UserID, model.RetentionRetirementID, int) ([]model.RetentionNotice, error)
	ListPendingNotices(context.Context, int) ([]model.RetentionNotice, error)
	CompleteNotice(context.Context, *RetentionNoticeMail) error
	// Expiry is naturally idempotent, policy/control fenced, and completes its
	// required system audit atomically. Referenced or unfinished records cannot
	// expire. A resumed eligible record receives a fresh positive grace period;
	// receipt deletion never changes permanent Submission retirement markers.
	ListExpiryRecords(context.Context, RetentionExpiryListOptions) (*RetentionExpiryPage, error)
	ReconcileExpiry(context.Context, *RetentionExpiryReconciliation) (*RetentionExpiryResult, error)
	ReconcileCleanupAuditExpiry(context.Context, *RetentionCleanupAuditReconciliation) (*RetentionCleanupAuditResult, error)
}
