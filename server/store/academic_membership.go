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

// AcademicUnitMemberPageOptions selects a bounded page ordered by (UserID, ID).
// ActiveAt zero includes history; positive milliseconds select [start, end).
// The cursor tuple is exclusive and either entirely absent or entirely valid.
type AcademicUnitMemberPageOptions struct {
	AcademicUnitID model.AcademicUnitID
	ActiveAt       int64
	AfterUserID    model.UserID
	AfterID        model.AcademicUnitMemberID
	Limit          int
}

type AcademicUnitMemberPage struct {
	Members []*model.AcademicUnitMember
	HasMore bool
}

// ClassMemberPageOptions selects a bounded page ordered by (UserID, ID).
// ActiveAt zero includes history; positive milliseconds select [start, end).
// The cursor tuple is exclusive and either entirely absent or entirely valid.
type ClassMemberPageOptions struct {
	ClassID     model.ClassID
	ActiveAt    int64
	AfterUserID model.UserID
	AfterID     model.ClassMemberID
	Limit       int
}

type ClassMemberPage struct {
	Members []*model.ClassMember
	HasMore bool
}

// AffiliationStore persists non-exclusive institution relationships.
type AffiliationCreation struct {
	Affiliation  *model.Affiliation
	AuditEventID model.AuditEventID
	AuditAt      int64
	Command      *CommandIdempotency
	Replayed     bool
	NoOp         bool
}

type AffiliationEnd struct {
	ID               model.AffiliationID
	ExpectedRevision int64
	EndAt            int64
	AuditEventID     model.AuditEventID
	AuditAt          int64
	Command          *CommandIdempotency
	Replayed         bool
	NoOp             bool
}

type AffiliationStore interface {
	Create(context.Context, *AffiliationCreation) (*model.Affiliation, error)
	End(context.Context, *AffiliationEnd) (*model.Affiliation, error)
	Save(context.Context, *model.Affiliation) (*model.Affiliation, error)
	Get(context.Context, model.AffiliationID) (*model.Affiliation, error)
	ListByUser(context.Context, model.UserID) ([]*model.Affiliation, error)
	ListActiveByUser(context.Context, model.UserID, int64) ([]*model.Affiliation, error)
}

// AcademicUnitMemberStore persists organizational membership without roles.
type AcademicUnitMemberCreation struct {
	Member                    *model.AcademicUnitMember
	ExpectedRecipientRevision int64
	Notice                    *PreparedMail
	AuditEventID              model.AuditEventID
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

type AcademicUnitMemberEnd struct {
	ID                        model.AcademicUnitMemberID
	ExpectedRevision          int64
	ExpectedRecipientRevision int64
	Notice                    *PreparedMail
	EndAt                     int64
	AuditEventID              model.AuditEventID
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

type AcademicUnitMemberStore interface {
	Create(context.Context, *AcademicUnitMemberCreation) (*model.AcademicUnitMember, error)
	End(context.Context, *AcademicUnitMemberEnd) (*model.AcademicUnitMember, error)
	Save(context.Context, *model.AcademicUnitMember) (*model.AcademicUnitMember, error)
	Get(context.Context, model.AcademicUnitMemberID) (*model.AcademicUnitMember, error)
	ListByUser(context.Context, model.UserID) ([]*model.AcademicUnitMember, error)
	ListByAcademicUnit(context.Context, model.AcademicUnitID, int64) ([]*model.AcademicUnitMember, error)
	ListPageByAcademicUnit(context.Context, AcademicUnitMemberPageOptions) (*AcademicUnitMemberPage, error)
	// ListActiveByUser uses UTC microsecond precision and the interval [start, end).
	// Archived memberships are excluded; no active memberships is an empty list.
	ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.AcademicUnitMember, error)
}

type ClassEnrollmentResult struct {
	Membership *model.ClassMember
	Previous   *model.ClassMember
}

// ClassMemberEnrollment is the complete durable input for an enrollment or
// transfer and its already-persisted mutation audit. A transfer closes the
// previous membership and creates Member in the same transaction.
type ClassMemberEnrollment struct {
	Member                             *model.ClassMember
	ExpectedPreviousID                 model.ClassMemberID
	ExpectedRecipientRevision          int64
	StudentProgression                 bool
	ProgressionSourceAuditEventID      model.AuditEventID
	ProgressionDestinationAuditEventID model.AuditEventID
	Notice                             *PreparedMail
	AuditEventID                       model.AuditEventID
	PreviousAuditEventID               model.AuditEventID
	AuditAt                            int64
	Command                            *CommandIdempotency
	Replayed                           bool
	NoOp                               bool
}

type ClassMemberEnd struct {
	ID                        model.ClassMemberID
	ExpectedRevision          int64
	ExpectedRecipientRevision int64
	EndAt                     int64
	Notice                    *PreparedMail
	AuditEventID              model.AuditEventID
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

// ClassMemberStore owns transactional student enrollment and history.
type ClassMemberStore interface {
	Enroll(context.Context, *ClassMemberEnrollment) (*ClassEnrollmentResult, error)
	End(context.Context, *ClassMemberEnd) (*model.ClassMember, error)
	Get(context.Context, model.ClassMemberID) (*model.ClassMember, error)
	ListByUser(context.Context, model.UserID) ([]*model.ClassMember, error)
	ListByClass(context.Context, model.ClassID, int64) ([]*model.ClassMember, error)
	ListPageByClass(context.Context, ClassMemberPageOptions) (*ClassMemberPage, error)
	ListActiveByUser(context.Context, model.UserID, int64) ([]*model.ClassMember, error)
	// SeedEnroll and SeedEnd are unaudited fixture writes for conformance and
	// integration tests; production code never calls a Seed operation.
	SeedEnroll(context.Context, *model.ClassMember) (*ClassEnrollmentResult, error)
	SeedEnd(context.Context, model.ClassMemberID, int64, int64) (*model.ClassMember, error)
}
