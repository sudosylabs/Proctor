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

// MailTestEnqueue is the named aggregate that commits one controlled test
// occurrence, its encrypted recipient delivery, its delivery Job, and the
// successful operator audit as one PostgreSQL transaction.
type MailTestEnqueue struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
	AuditEvent *model.AuditEvent
}

type MailDeliveryCompletionKind string

const (
	MailDeliveryCompletionAccepted MailDeliveryCompletionKind = "accepted"
	MailDeliveryCompletionRetry    MailDeliveryCompletionKind = "retry"
	MailDeliveryCompletionFailed   MailDeliveryCompletionKind = "failed"
	MailDeliveryCompletionExpired  MailDeliveryCompletionKind = "expired"
	MailDeliveryCompletionSuppress MailDeliveryCompletionKind = "suppress"
)

type MailDeliveryCompletion struct {
	DeliveryID        model.MailDeliveryID
	ExpectedRevision  int64
	Kind              MailDeliveryCompletionKind
	PublicFailureCode string
	At                time.Time
}

// MailDeliveryListOptions is a bounded, server-side operator query. The
// (BeforeCreatedAt, BeforeID) pair is the decoded opaque keyset cursor.
type MailDeliveryListOptions struct {
	States          []model.MailDeliveryState
	TemplateKeys    []model.MailTemplateKey
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	BeforeCreatedAt time.Time
	BeforeID        model.MailDeliveryID
	Limit           int
}

// MailDeliveryMutation fences an audited operator transition against the
// delivery revision observed after authorization.
type MailDeliveryMutation struct {
	ID               model.MailDeliveryID
	ExpectedRevision int64
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

type MailSendClass string

const (
	MailSendCredential MailSendClass = "credential"
	MailSendOrdinary   MailSendClass = "ordinary"
)

type MailSendPermit struct {
	Allowed    bool
	RetryAfter time.Duration
}

// MailMaintenanceResult reports one bounded, convergent maintenance page.
type MailMaintenanceResult struct {
	Affected   int
	More       bool
	Deliveries []MailMaintenanceDelivery
}

// MailMaintenanceDelivery is the bounded, identifier-free observation for a
// delivery transitioned by maintenance rather than the ordinary worker.
type MailMaintenanceDelivery struct {
	TemplateKey       model.MailTemplateKey
	State             model.MailDeliveryState
	PublicFailureCode string
	AttemptCount      int
	ProcessingLatency time.Duration
}

type MailQueueCount struct {
	TemplateKey       model.MailTemplateKey
	State             model.MailDeliveryState
	PublicFailureCode string
	Count             int64
	OldestObservedAt  time.Time
}

// MailQueueSnapshot contains only bounded operational metadata. It never
// includes recipients, rendered content, payloads, or provider responses.
type MailQueueSnapshot struct {
	Counts         []MailQueueCount
	OldestQueuedAt time.Time
	More           bool
}

// MailStore owns durable mail-domain state. It deliberately exposes named
// lifecycle operations rather than a raw transaction callback.
type MailStore interface {
	EnqueueTest(context.Context, *MailTestEnqueue) (*model.MailDelivery, error)
	GetDelivery(context.Context, model.MailDeliveryID) (*model.MailDelivery, error)
	ListDeliveries(context.Context, MailDeliveryListOptions) ([]*model.MailDelivery, error)
	StartDelivery(context.Context, *MailDeliveryStart) (*model.MailDelivery, error)
	CompleteDelivery(context.Context, *MailDeliveryCompletion) (*model.MailDelivery, error)
	CancelDelivery(context.Context, *MailDeliveryMutation) (*model.MailDelivery, error)
	RetryDelivery(context.Context, *MailDeliveryMutation) (*model.MailDelivery, error)
	AcquireSendPermit(context.Context, MailSendClass) (*MailSendPermit, error)
	SuppressOutstanding(context.Context, *MailOutstandingSuppression) (*MailMaintenanceResult, error)
	SuppressExpired(context.Context, int) (*MailMaintenanceResult, error)
	CleanupTerminal(context.Context, int) (*MailMaintenanceResult, error)
	QueueSnapshot(context.Context) (*MailQueueSnapshot, error)
	ActivePayloadKeyIDs(context.Context) ([]string, error)
	InspectKeyState(context.Context) (*MailKeyState, error)
	StartRekey(context.Context, *MailRekeyStart) (*MailRekeyOperation, error)
	ListRekeyTargets(context.Context, *MailRekeyTargetPageRequest) (*MailRekeyTargetPage, error)
	ReplaceRekeyTarget(context.Context, *MailRekeyReplacement) (bool, error)
	ProveRekey(context.Context, *MailRekeyProofRequest) (*MailRekeyProof, error)
}

// PreparedMail groups one already-rendered direct occurrence and its work.
// Named aggregate inputs use it only where the mail is conditionally applied.
type PreparedMail struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
}

// MailDeliveryStart is the input to MailStore.StartDelivery.
type MailDeliveryStart struct {
	ID               model.MailDeliveryID
	ExpectedRevision int64
	At               time.Time
}

// MailOutstandingSuppression is the input to MailStore.SuppressOutstanding.
type MailOutstandingSuppression struct {
	Code  string
	Limit int
}
