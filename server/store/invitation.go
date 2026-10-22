// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// StudentClassInvitationIssue is the named transaction that makes one
// pre-User Invitation and its recoverable delivery intent durable before
// completing the already-recorded security audit attempt.
type StudentClassInvitationIssue struct {
	Invitation   *model.Invitation
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

// TeacherAcademicUnitInvitationIssue atomically persists one exact teacher
// relationship package, its recoverable credential delivery, and the
// successful mutation audit.
type TeacherAcademicUnitInvitationIssue struct {
	Invitation *model.Invitation
	// Lifetime is a bounded duration, never a node-selected absolute deadline.
	// SQL derives the authoritative lifecycle from one PostgreSQL timestamp.
	Lifetime     time.Duration
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

// StudentClassInvitationAcceptance is the complete prepared package for one
// claim. Persistence resolves an existing User by the invited mailbox inside
// the transaction and uses the prepared User artifacts only when creation or
// missing local enrollment is required.
type StudentClassInvitationAcceptance struct {
	ClaimHash                string
	AcceptedAt               time.Time
	User                     *model.User
	Settings                 *model.UserSettingsDocument
	PasswordCredential       *model.PasswordCredential
	DefaultProfilePictureJob *model.Job
	Affiliation              *model.Affiliation
	ClassMember              *model.ClassMember
	Occurrence               *model.MailOccurrence
	Delivery                 *model.MailDelivery
	DeliveryJob              *model.Job
	AuditEvent               *model.AuditEvent
	RequiredActions          []model.Action
	BrowserTransaction       *BrowserInvitationTransactionProof
}

// StudentClassInvitationAcceptanceResult is the authoritative accepted
// account and package. Replayed reports exact claim replay without repeating
// relationship, mail, audit, or Job effects.
type StudentClassInvitationAcceptanceResult struct {
	Invitation  *model.Invitation
	User        *model.User
	Affiliation *model.Affiliation
	ClassMember *model.ClassMember
	Replayed    bool
}

// TeacherAcademicUnitInvitationAcceptance is the complete prepared package
// for one teacher claim. Persistence resolves existing relationships and adds
// only missing effects inside the same transaction.
type TeacherAcademicUnitInvitationAcceptance struct {
	ClaimHash                string
	AcceptedAt               time.Time
	User                     *model.User
	Settings                 *model.UserSettingsDocument
	PasswordCredential       *model.PasswordCredential
	DefaultProfilePictureJob *model.Job
	Affiliation              *model.Affiliation
	AcademicUnitMember       *model.AcademicUnitMember
	RoleBinding              *model.RoleBinding
	Occurrence               *model.MailOccurrence
	Delivery                 *model.MailDelivery
	DeliveryJob              *model.Job
	AuditEvent               *model.AuditEvent
	RequiredActions          []model.Action
	BrowserTransaction       *BrowserInvitationTransactionProof
}

type TeacherAcademicUnitInvitationAcceptanceResult struct {
	Invitation         *model.Invitation
	User               *model.User
	Affiliation        *model.Affiliation
	AcademicUnitMember *model.AcademicUnitMember
	RoleBinding        *model.RoleBinding
	Replayed           bool
}

// ScopedRoleInvitationIssue atomically persists one existing-User Role
// Invitation, its recoverable claim delivery, and the successful issue audit.
type ScopedRoleInvitationIssue struct {
	Invitation   *model.Invitation
	Lifetime     time.Duration
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

// ScopedRoleInvitationAcceptance consumes one role-only claim for the exact
// authenticated User and adds only a missing compatible Role Binding. It
// intentionally carries no User/profile mutation or acceptance mail.
type ScopedRoleInvitationAcceptance struct {
	ClaimHash   string
	UserID      model.UserID
	RoleBinding *model.RoleBinding
	// AuditEventID identifies the already-persisted secret-free attempt. The
	// aggregate completes it atomically with acceptance or exact replay.
	AuditEventID       model.AuditEventID
	AuditAt            time.Time
	RequiredActions    []model.Action
	BrowserTransaction *BrowserInvitationTransactionProof
}

type ScopedRoleInvitationAcceptanceResult struct {
	Invitation  *model.Invitation
	User        *model.User
	RoleBinding *model.RoleBinding
	Replayed    bool
}

// ExternalIdentityInvitationAcceptance is the provider-proof-completed input
// for atomically linking one immutable subject and applying the exact pending
// Invitation package. ExternalStateID identifies the consumed, browser-bound
// redirect transaction; ProviderEmail is a verified provider mailbox hint,
// never an account-selection key.
type ExternalIdentityInvitationAcceptance struct {
	ExternalStateID          model.ExternalLoginStateID
	Identity                 *model.ExternalIdentity
	ProviderEmail            string
	User                     *model.User
	Settings                 *model.UserSettingsDocument
	DefaultProfilePictureJob *model.Job
	Affiliation              *model.Affiliation
	ClassMember              *model.ClassMember
	AcademicUnitMember       *model.AcademicUnitMember
	RoleBinding              *model.RoleBinding
	Notice                   *PreparedMail
	AuditEvent               *model.AuditEvent
	Capabilities             AccessDeploymentCapabilities
	RequiredActions          []model.Action
}

type ExternalIdentityInvitationAcceptanceResult struct {
	Invitation         *model.Invitation
	Identity           *model.ExternalIdentity
	User               *model.User
	Affiliation        *model.Affiliation
	ClassMember        *model.ClassMember
	AcademicUnitMember *model.AcademicUnitMember
	RoleBinding        *model.RoleBinding
}

type InvitationMaintenanceResult struct {
	Expired int
	Purged  int
	More    bool
}

// InvitationVisibilityScope is the bounded persistence-side authorization
// constraint for Invitation administration. AcademicUnitRootIDs name complete
// subtrees and ClassIDs name exact Class grants.
type InvitationVisibilityScope struct {
	InstitutionWide     bool
	AcademicUnitRootIDs []model.AcademicUnitID
	ClassIDs            []model.ClassID
}

// InvitationDeliverySummary is the approved operational projection of the
// newest delivery for an Invitation. It deliberately excludes ciphertext,
// rendered content, transport detail, Job identity, and Message-ID.
type InvitationDeliverySummary struct {
	TemplateKey       model.MailTemplateKey
	State             model.MailDeliveryState
	MaskedRecipient   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Deadline          time.Time
	AcceptedAt        model.OptionalTime
	PublicFailureCode string
}

type InvitationAdministrationRecord struct {
	Invitation *model.Invitation
	Delivery   *InvitationDeliverySummary
}

// InvitationCommandResult reports whether an Invitation issue command
// executed or recovered its previously committed outcome.
type InvitationCommandResult struct {
	Invitation *model.Invitation
	Replayed   bool
	Duplicate  bool
	NoOp       bool
}

// InvitationAdministrationCommandResult is the lifecycle-command equivalent
// for resend and revoke.
type InvitationAdministrationCommandResult struct {
	Record    *InvitationAdministrationRecord
	Replayed  bool
	Duplicate bool
}

// InvitationBatchCommandResult is the secret-free retained disposition of an
// item that was classified as a duplicate before its ordinary command ran.
type InvitationBatchCommandResult struct {
	InvitationID model.InvitationID
	Replayed     bool
	Duplicate    bool
}

type InvitationBatchDuplicate struct {
	Candidate            *model.Invitation
	LifecycleID          model.InvitationID
	ExpectedRevision     int64
	ActorUserID          model.UserID
	CanonicalOperation   string
	CanonicalKeyDigest   [sha256.Size]byte
	CanonicalFingerprint [sha256.Size]byte
	AuditEventID         model.AuditEventID
	AuditAt              time.Time
}

type InvitationListOptions struct {
	Visibility      InvitationVisibilityScope
	Purpose         model.InvitationPurpose
	State           model.InvitationState
	TargetEmail     string
	TargetID        string
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	BeforeCreatedAt time.Time
	BeforeID        model.InvitationID
	Limit           int
}

type InvitationPage struct {
	Items []*InvitationAdministrationRecord
	More  bool
}

// InvitationResend rotates the claim and atomically replaces every unsent
// credential delivery with one newly frozen occurrence.
type InvitationResend struct {
	ID               model.InvitationID
	ExpectedRevision int64
	ClaimHash        string
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	DeliveryJob      *model.Job
	ActorUserID      model.UserID
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

// InvitationRevocation terminalizes one pending Invitation. RevocationNotice
// is inserted only when an original Invitation delivery was SMTP Accepted.
type InvitationRevocation struct {
	ID               model.InvitationID
	ExpectedRevision int64
	ActorUserID      model.UserID
	RevocationNotice *PreparedMail
	AuditEventID     model.AuditEventID
	AuditAt          time.Time
}

// InvitationReplacement supersedes CurrentID and inserts Replacement plus its
// initial delivery in one authoritative transaction.
type InvitationReplacement struct {
	CurrentID               model.InvitationID
	ExpectedCurrentRevision int64
	Replacement             *model.Invitation
	Lifetime                time.Duration
	Occurrence              *model.MailOccurrence
	Delivery                *model.MailDelivery
	DeliveryJob             *model.Job
	ActorUserID             model.UserID
	CurrentAuditEventID     model.AuditEventID
	ReplacementAuditEventID model.AuditEventID
	AuditAt                 time.Time
}

// InvitationStore owns durable Invitation issuance, acceptance,
// administration, and their retained idempotent outcomes. Raw claims never
// cross this boundary.
// InvitationReplay is the audit attempt a retained Invitation outcome replay
// completes after fresh authorization.
type InvitationReplay struct {
	AuditEventID model.AuditEventID
	AuditAt      time.Time
}

type InvitationStore interface {
	IssueStudentClass(context.Context, *StudentClassInvitationIssue) (*model.Invitation, error)
	IssueStudentClassIdempotently(context.Context, *StudentClassInvitationIssue, *CommandIdempotency) (*InvitationCommandResult, error)
	IssueTeacherAcademicUnit(context.Context, *TeacherAcademicUnitInvitationIssue) (*model.Invitation, error)
	IssueTeacherAcademicUnitIdempotently(context.Context, *TeacherAcademicUnitInvitationIssue, *CommandIdempotency) (*InvitationCommandResult, error)
	IssueScopedRole(context.Context, *ScopedRoleInvitationIssue) (*model.Invitation, error)
	IssueScopedRoleIdempotently(context.Context, *ScopedRoleInvitationIssue, *CommandIdempotency) (*InvitationCommandResult, error)
	ResolveOnboardingInvitationNoOp(context.Context, *model.Invitation) (*model.Invitation, bool, error)
	FindCommandOutcome(context.Context, *CommandIdempotency) (*InvitationCommandResult, error)
	ReplayIssue(context.Context, *InvitationReplay, *CommandIdempotency) (*InvitationCommandResult, error)
	ReplayAdministration(context.Context, *InvitationReplay, *CommandIdempotency) (*InvitationAdministrationCommandResult, error)
	RecordBatchDuplicate(context.Context, *InvitationBatchDuplicate, *CommandIdempotency) (*InvitationBatchCommandResult, error)
	Get(context.Context, model.InvitationID) (*model.Invitation, error)
	GetByClaimHash(context.Context, string) (*model.Invitation, error)
	AcceptStudentClass(context.Context, *StudentClassInvitationAcceptance) (*StudentClassInvitationAcceptanceResult, error)
	AcceptTeacherAcademicUnit(context.Context, *TeacherAcademicUnitInvitationAcceptance) (*TeacherAcademicUnitInvitationAcceptanceResult, error)
	AcceptScopedRole(context.Context, *ScopedRoleInvitationAcceptance) (*ScopedRoleInvitationAcceptanceResult, error)
	AcceptExternalIdentity(context.Context, *ExternalIdentityInvitationAcceptance) (*ExternalIdentityInvitationAcceptanceResult, error)
	List(context.Context, InvitationListOptions) (*InvitationPage, error)
	GetForAdministration(context.Context, model.InvitationID, InvitationVisibilityScope) (*InvitationAdministrationRecord, error)
	Resend(context.Context, *InvitationResend) (*InvitationAdministrationRecord, error)
	ResendIdempotently(context.Context, *InvitationResend, *CommandIdempotency) (*InvitationAdministrationCommandResult, error)
	Revoke(context.Context, *InvitationRevocation) (*InvitationAdministrationRecord, error)
	RevokeIdempotently(context.Context, *InvitationRevocation, *CommandIdempotency) (*InvitationAdministrationCommandResult, error)
	Replace(context.Context, *InvitationReplacement) (*InvitationAdministrationRecord, error)
	Maintain(context.Context, int) (*InvitationMaintenanceResult, error)
}
