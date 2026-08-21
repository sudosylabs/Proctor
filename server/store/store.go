// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package store defines Proctor's durable persistence contracts.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	CommandOutcomeMaxBytes = 64 * 1024
)

// CommandIdempotency is the bounded, transport-neutral identity of one
// retryable application command. Raw client keys and commands never cross the
// Store boundary.
type CommandIdempotency struct {
	UserID             model.UserID
	Operation          string
	KeyDigest          [sha256.Size]byte
	FingerprintVersion int
	Fingerprint        [sha256.Size]byte
	OutcomeVersion     int
	Retention          time.Duration
	Wait               time.Duration
	// OnboardingImportID and OnboardingImportRowNumber bind an administrative
	// import or progression command to the parent/row fence that must commit
	// with its ordinary mutation. They are absent for other commands.
	OnboardingImportID        model.OnboardingImportID
	OnboardingImportRowNumber int
	// Authorization is present only for closed academic-administration batch
	// commands. PostgreSQL rechecks these current permissions in the same
	// transaction as the ordinary aggregate mutation.
	Authorization *CommandAuthorization
	Batch         *CommandBatch
}

// CommandBatch binds one retained item outcome to its semantic duplicate
// group. DuplicateOfKeyDigest is absent for the canonical item. Duplicate is
// set by the Store when the retained disposition is non-canonical.
type CommandBatch struct {
	GroupDigest          [sha256.Size]byte
	DuplicateOfKeyDigest [sha256.Size]byte
	Duplicate            bool
}

// CommandAuthorization is the bounded terminal authority required by one
// academic-administration batch row. It contains no derived Role state;
// PostgreSQL resolves every action from current durable bindings and Roles.
type CommandAuthorization struct {
	Principal        model.Principal
	ScopeType        model.RoleScopeType
	ScopeID          string
	Actions          []model.Action
	DelegatedActions []model.Action
	// ClassMemberID binds a Class end or transfer to its relationship so
	// PostgreSQL can lock the affected User before hierarchy state, then resolve
	// and authorize the relationship's current Class.
	ClassMemberID model.ClassMemberID
	// RecipientUserID lets PostgreSQL preserve the canonical User-before-
	// hierarchy lock order for Class enrollment.
	RecipientUserID model.UserID
}

// AcademicUnitCommandResult and AcademicPeriodCommandResult report whether a
// named mutation executed or returned its previously committed outcome.
type AcademicUnitCommandResult struct {
	Value    *model.AcademicUnit
	Replayed bool
}

type AcademicPeriodCommandResult struct {
	Value    *model.AcademicPeriod
	Replayed bool
}

// ExamAuthoringSnapshot is the bounded authoring view returned by the Exam
// aggregate store. Managers are counted but never hydrated as an unbounded set.
type ExamAuthoringSnapshot struct {
	Exam                *model.Exam
	Draft               *model.ExamDraft
	OwnerUserID         model.UserID
	ManagerCount        int
	ActorIsManager      bool
	ResourceCount       int
	HasStarterWorkspace bool
}

// ExamAccessSnapshot is the minimal projection needed to authorize an Exam
// operation before authored Draft content is loaded.
type ExamAccessSnapshot struct {
	Exam           *model.Exam
	ActorIsManager bool
}

type ExamAuthoringCommandResult struct {
	Value    *ExamAuthoringSnapshot
	Replayed bool
}

type ExamArchiveFilter string

const (
	ExamArchiveActive   ExamArchiveFilter = "active"
	ExamArchiveArchived ExamArchiveFilter = "archived"
	ExamArchiveAll      ExamArchiveFilter = "all"
)

// ExamListVisibility is the bounded, persistence-ready result of current role
// authorization. OrdinaryMembershipAt is the same authorization decision time
// at which persistence must require the actor's current exact Academic Unit
// membership and Exam Manager relationship. Override visibility requires
// neither relationship and does not manufacture either one.
type ExamListVisibility struct {
	ActorUserID                 model.UserID
	OrdinaryMembershipAt        time.Time
	OrdinaryInstitutionWide     bool
	OrdinaryAcademicUnitRootIDs []string
	OverrideInstitutionWide     bool
	OverrideAcademicUnitRootIDs []string
}

type ExamListOptions struct {
	AcademicUnitID  model.AcademicUnitID
	ArchiveFilter   ExamArchiveFilter
	BeforeUpdatedAt time.Time
	BeforeExamID    model.ExamID
	Limit           int
	Visibility      ExamListVisibility
}

// ExamSummary is the bounded catalog projection. Authored Markdown, policy,
// resources, and Starter Workspace state are deliberately absent.
type ExamSummary struct {
	ID             model.ExamID
	AcademicUnitID model.AcademicUnitID
	CreatorUserID  model.UserID
	OwnerUserID    model.UserID
	Title          string
	UpdatedAt      time.Time
	ArchivedAt     model.OptionalTime
	Revision       int64
	ManagerCount   int
}

// ExamManagerSummary is the bounded management projection. It deliberately
// contains relationship provenance rather than a User profile.
type ExamManagerSummary struct {
	Manager   model.ExamManager
	IsCreator bool
	IsOwner   bool
}

type ExamManagerListOptions struct {
	ExamID          model.ExamID
	BeforeGrantedAt time.Time
	BeforeUserID    model.UserID
	Limit           int
}

// ExamManagerMutation carries the common revision fence, authorization result,
// and audit attempt for one named relationship or ownership transition.
type ExamManagerMutation struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	TargetUserID     model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	ChangedAt        int64
	AuditEventID     string
	AuditAt          int64
	Notices          []ExamManagerMail
}

// ExamManagerMail is one fully prepared direct notification inserted with an
// Exam management transition. Ownership transfer carries exactly two entries;
// addition and removal carry exactly one.
type ExamManagerMail struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
}

type ExamManagerCommandResult struct {
	Exam     *model.Exam
	Manager  *model.ExamManager
	Replayed bool
}

type ExamArchive struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	ArchivedAt       int64
	AuditEventID     string
	AuditAt          int64
}

type ExamArchiveCommandResult struct {
	Value    *model.Exam
	Replayed bool
}

// ExamAuthoringCreation is the complete first Exam aggregate and the durable
// audit attempt that its successful transaction must complete.
type ExamAuthoringCreation struct {
	Exam         *model.Exam
	Draft        *model.ExamDraft
	Manager      *model.ExamManager
	AuditEventID string
	AuditAt      int64
}

// ExamDraftTextUpdate changes only the authored text fields of one Draft.
// Presence is retained so later Draft fields remain outside this operation.
type ExamDraftTextUpdate struct {
	ExamID               model.ExamID
	ActorUserID          model.UserID
	ManagerOverride      bool
	ExpectedRevision     int64
	Title                *string
	InstructionsMarkdown *string
	UpdatedAt            int64
	AuditEventID         string
	AuditAt              int64
}

// ExamDraftFocusLossUpdate replaces only the typed Focus Loss rule. The Store
// reconstructs and validates the complete policy so Connection Loss cannot be
// supplied or weakened by a caller.
type ExamDraftFocusLossUpdate struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	FocusLoss        model.FocusLossPolicy
	UpdatedAt        int64
	AuditEventID     string
	AuditAt          int64
}

// ExamDraftExecutionProfileUpdate replaces the complete authored terminal
// choice. Installation resources and host addresses never enter this value.
type ExamDraftExecutionProfileUpdate struct {
	ExamID           model.ExamID
	ActorUserID      model.UserID
	ManagerOverride  bool
	ExpectedRevision int64
	Profile          model.ExecutionProfile
	UpdatedAt        int64
	AuditEventID     string
	AuditAt           int64
}

// ExamAuthoringStore owns atomic Exam authoring mutations and bounded reads.
// Create commits the Exam, its one Draft, creator-manager relation, audit
// success, and retry outcome together. Draft updates recheck the current
// manager relation unless an already-authorized override is explicit, reject
// archived or stale/no-op Drafts, and atomically commit only their supported
// fields, the Draft revision, audit success, and retry outcome. Exact replays
// return the committed projection without repeating the mutation. Reads remain
// bounded regardless of manager or future resource cardinality.
type ExamAuthoringStore interface {
	Create(context.Context, *ExamAuthoringCreation, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	UpdateDraftText(context.Context, *ExamDraftTextUpdate, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	UpdateDraftFocusLoss(context.Context, *ExamDraftFocusLossUpdate, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	UpdateDraftExecutionProfile(context.Context, *ExamDraftExecutionProfileUpdate, *CommandIdempotency) (*ExamAuthoringCommandResult, error)
	// List returns at most Limit summaries in descending (UpdatedAt, ExamID)
	// order, strictly before the optional complete cursor pair. The adapter must
	// apply exact-unit, archive-state, role-scope, current exact Academic Unit
	// membership, and current Manager predicates inside the same bounded query;
	// override scope is a separate branch that requires neither relationship and
	// never creates one. Manager counts and Draft titles are part of that query,
	// not follow-up reads. List never returns an unrestricted intermediate set.
	List(context.Context, ExamListOptions) ([]ExamSummary, error)
	// Archive first resolves a matching committed idempotent outcome. Otherwise
	// one transaction locks the Exam, rechecks Manager membership unless an
	// explicit override was authorized, rejects archived or stale state, records
	// the immutable archive time and next revision without deleting rows,
	// completes the supplied audit attempt, and commits the replayable outcome.
	// Concurrent new commands yield one commit; losers return a stable conflict.
	// Transient effects are outside this operation and run only after commit.
	Archive(context.Context, *ExamArchive, *CommandIdempotency) (*ExamArchiveCommandResult, error)
	// ListManagers returns at most Limit provenance-only relationships in
	// descending (GrantedAt, UserID) order, strictly before the optional complete
	// cursor pair.
	ListManagers(context.Context, ExamManagerListOptions) ([]ExamManagerSummary, error)
	// AddManager, RemoveManager, and TransferOwner each lock the Exam and recheck
	// its active state, revision, actor relationship unless override is explicit,
	// and target eligibility where required. The relationship/owner transition,
	// next Exam revision, successful audit, and replayable outcome commit in one
	// transaction. Owner membership is also protected by a deferred database
	// constraint. Exact replays do not repeat the transition.
	AddManager(context.Context, *ExamManagerMutation, *CommandIdempotency) (*ExamManagerCommandResult, error)
	RemoveManager(context.Context, *ExamManagerMutation, *CommandIdempotency) (*ExamManagerCommandResult, error)
	TransferOwner(context.Context, *ExamManagerMutation, *CommandIdempotency) (*ExamManagerCommandResult, error)
	Access(context.Context, model.ExamID, model.UserID) (*ExamAccessSnapshot, error)
	Get(context.Context, model.ExamID, model.UserID) (*ExamAuthoringSnapshot, error)
	Resolve(context.Context, model.ExamID) (*model.Exam, error)
}

// Store is the root persistence contract used by the application and platform.
// Concrete adapters expose each model store through this interface so callers
// do not depend on PostgreSQL implementation types.
// Catalog exposes the complete family of authoritative persistence contracts
// without lifecycle authority. Application construction borrows this view;
// the node runtime and Platform retain validation and closure ownership.
type Catalog interface {
	Institution() InstitutionStore
	AcademicUnit() AcademicUnitStore
	Programme() ProgrammeStore
	ProgrammeLevel() ProgrammeLevelStore
	AcademicPeriod() AcademicPeriodStore
	ExamAuthoring() ExamAuthoringStore
	ExamRevision() ExamRevisionStore
	ExamSitting() ExamSittingStore
	ExamAttempt() ExamAttemptStore
	ExecutionGrant() ExecutionGrantStore
	ExamAttemptWorkspace() ExamAttemptWorkspaceStore
	ExamSubmission() ExamSubmissionStore
	ExamIntegrityReview() ExamIntegrityReviewStore
	ExamResource() ExamResourceStore
	ExamCorrection() ExamCorrectionStore
	ExamStarterWorkspace() ExamStarterWorkspaceStore
	Class() ClassStore
	User() UserStore
	UserSettings() UserSettingsStore
	File() FileStore
	Job() JobStore
	Mail() MailStore
	ExternalIdentity() ExternalIdentityStore
	ExternalLoginState() ExternalLoginStateStore
	DesktopAuthorization() DesktopAuthorizationStore
	UserToken() UserTokenStore
	Invitation() InvitationStore
	OnboardingImport() OnboardingImportStore
	PersonalAccessToken() PersonalAccessTokenStore
	MFA() MFAStore
	Affiliation() AffiliationStore
	AcademicUnitMember() AcademicUnitMemberStore
	ClassMember() ClassMemberStore
	PasswordCredential() PasswordCredentialStore
	Session() SessionStore
	SessionCredential() SessionCredentialStore
	Role() RoleStore
	RoleBinding() RoleBindingStore
	Audit() AuditStore
	Installation() InstallationStore
	AccessPolicy() AccessPolicyStore
	ClusterDiscovery() ClusterDiscoveryStore
	ServingNodeLease() ServingNodeLeaseStore
	CommandOutcome() CommandOutcomeStore
}

type Store interface {
	Institution() InstitutionStore
	AcademicUnit() AcademicUnitStore
	Programme() ProgrammeStore
	ProgrammeLevel() ProgrammeLevelStore
	AcademicPeriod() AcademicPeriodStore
	ExamAuthoring() ExamAuthoringStore
	ExamRevision() ExamRevisionStore
	ExamSitting() ExamSittingStore
	ExamAttempt() ExamAttemptStore
	ExecutionGrant() ExecutionGrantStore
	ExamAttemptWorkspace() ExamAttemptWorkspaceStore
	ExamSubmission() ExamSubmissionStore
	ExamIntegrityReview() ExamIntegrityReviewStore
	ExamResource() ExamResourceStore
	ExamCorrection() ExamCorrectionStore
	ExamStarterWorkspace() ExamStarterWorkspaceStore
	Class() ClassStore
	User() UserStore
	UserSettings() UserSettingsStore
	File() FileStore
	Job() JobStore
	Mail() MailStore
	ExternalIdentity() ExternalIdentityStore
	ExternalLoginState() ExternalLoginStateStore
	DesktopAuthorization() DesktopAuthorizationStore
	UserToken() UserTokenStore
	Invitation() InvitationStore
	OnboardingImport() OnboardingImportStore
	PersonalAccessToken() PersonalAccessTokenStore
	MFA() MFAStore
	Affiliation() AffiliationStore
	AcademicUnitMember() AcademicUnitMemberStore
	ClassMember() ClassMemberStore
	PasswordCredential() PasswordCredentialStore
	Session() SessionStore
	SessionCredential() SessionCredentialStore
	Role() RoleStore
	RoleBinding() RoleBindingStore
	Audit() AuditStore
	Installation() InstallationStore
	AccessPolicy() AccessPolicyStore
	ClusterDiscovery() ClusterDiscoveryStore
	ServingNodeLease() ServingNodeLeaseStore
	CommandOutcome() CommandOutcomeStore

	Ping(context.Context) error
	GetDBSchemaVersion(context.Context) (int, error)
	GetLocalSchemaVersion() (int, error)
	ValidateSchema(context.Context) error
	Close() error
}

// CommandOutcomeStore owns bounded retention of completed client-command
// outcomes. Creation and replay remain part of each named aggregate mutation.
type CommandOutcomeStore interface {
	Has(context.Context, *CommandIdempotency) (bool, error)
	DeleteExpired(context.Context, int) (int64, error)
}

type JobEnqueue struct {
	Job *model.Job
}

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
	AuditEventID     string
	AuditAt          int64
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
	StartDelivery(context.Context, model.MailDeliveryID, int64, time.Time) (*model.MailDelivery, error)
	CompleteDelivery(context.Context, *MailDeliveryCompletion) (*model.MailDelivery, error)
	CancelDelivery(context.Context, *MailDeliveryMutation) (*model.MailDelivery, error)
	RetryDelivery(context.Context, *MailDeliveryMutation) (*model.MailDelivery, error)
	AcquireSendPermit(context.Context, MailSendClass) (*MailSendPermit, error)
	SuppressOutstanding(context.Context, string, int) (*MailMaintenanceResult, error)
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

type JobClaimRequest struct {
	Types         []model.JobType
	NodeID        string
	ClaimToken    model.JobClaimToken
	LeaseDuration time.Duration
}

type JobClaim struct {
	Job     *model.Job
	Attempt *model.JobAttempt
}

type JobHeartbeat struct {
	AttemptID     model.JobAttemptID
	ClaimToken    model.JobClaimToken
	LeaseDuration time.Duration
}

type JobCheckpoint struct {
	AttemptID         model.JobAttemptID
	ClaimToken        model.JobClaimToken
	Progress          *model.JobProgress
	CheckpointVersion int
	Checkpoint        json.RawMessage
}

type JobWorkReservation struct {
	AttemptID  model.JobAttemptID
	ClaimToken model.JobClaimToken
	Units      int
	Limit      int
}

type JobWorkReservationResult struct {
	Reserved bool
	Consumed int
}

type JobCompletionKind string

const (
	JobCompletionSucceeded        JobCompletionKind = "succeeded"
	JobCompletionRetryableFailure JobCompletionKind = "retryable_failure"
	JobCompletionRelinquished     JobCompletionKind = "relinquished"
	JobCompletionPermanentFailure JobCompletionKind = "permanent_failure"
	JobCompletionCanceled         JobCompletionKind = "canceled"
)

type JobCompletion struct {
	AttemptID       model.JobAttemptID
	ClaimToken      model.JobClaimToken
	Kind            JobCompletionKind
	RetryDelay      time.Duration
	ResultVersion   int
	Result          json.RawMessage
	PublicErrorCode string
}

type JobListOptions struct {
	Types           []model.JobType
	Statuses        []model.JobStatus
	BeforeCreatedAt time.Time
	BeforeID        model.JobID
	Limit           int
}

type JobAttemptListOptions struct {
	JobID        model.JobID
	BeforeNumber int
	Limit        int
}

type JobMutation struct {
	ID               model.JobID
	ExpectedRevision int64
	AuditEventID     string
	AuditAt          int64
}

type JobRetentionPolicy struct {
	Type                 model.JobType
	SucceededCanceledAge time.Duration
	FailedAge            time.Duration
}

type JobHistoryCleanup struct {
	ExcludeJobID     model.JobID
	Policies         []JobRetentionPolicy
	AfterCompletedAt time.Time
	AfterJobID       model.JobID
	Limit            int
}

type JobHistoryCleanupResult struct {
	Deleted         int64
	LastCompletedAt time.Time
	LastJobID       model.JobID
	Done            bool
}

// JobStore owns durable, at-least-once work claiming and its fencing rules.
type JobStore interface {
	// Enqueue atomically deduplicates one logical occurrence by type, key, and
	// the Job's persisted policy. Active dedupe admits a replacement after a
	// terminal outcome; permanent dedupe reserves the key even after retention
	// removes its Job history. The returned boolean reports whether the supplied
	// Job was inserted; a retained winner is returned when one still exists.
	Enqueue(context.Context, *JobEnqueue) (*model.Job, bool, error)
	// ClaimNext expires lost attempts, requeues their Jobs, then atomically claims
	// one eligible Job using SKIP LOCKED and creates its next append-only Attempt.
	// Availability, expiry, and lease timestamps use the primary database clock;
	// an exhausted expired attempt atomically fails its Job instead of stranding it.
	ClaimNext(context.Context, *JobClaimRequest) (*JobClaim, error)
	// Heartbeat extends only the live Attempt owning the exact claim token, with
	// both fence validation and the new expiry based on the database clock.
	Heartbeat(context.Context, *JobHeartbeat) (*model.JobAttempt, error)
	// Checkpoint atomically updates bounded Job progress only for the live token
	// before its database-clock lease expiry.
	Checkpoint(context.Context, *JobCheckpoint) (*model.Job, error)
	// ReserveWork atomically consumes an occurrence-wide unit budget for the
	// live fenced Attempt. Reservations remain consumed across retries so a
	// crashed worker can under-process, but can never repeat beyond the limit.
	ReserveWork(context.Context, *JobWorkReservation) (*JobWorkReservationResult, error)
	// Complete atomically closes the fenced Attempt and transitions its Job to a
	// terminal state or a future queued retry. An expired token cannot complete.
	Complete(context.Context, *JobCompletion) (*model.Job, error)
	Get(context.Context, model.JobID) (*model.Job, error)
	ListAttempts(context.Context, model.JobID) ([]model.JobAttempt, error)
	List(context.Context, JobListOptions) ([]*model.Job, error)
	ListAttemptsPage(context.Context, JobAttemptListOptions) ([]model.JobAttempt, error)
	// CancellationRequested observes the durable cancellation bit only while the
	// supplied Attempt still owns the live fence.
	CancellationRequested(context.Context, model.JobAttemptID, model.JobClaimToken) (bool, error)
	// CancelWithAudit and RetryWithAudit atomically mutate the Job and complete
	// an already-durable operator audit attempt.
	CancelWithAudit(context.Context, *JobMutation) (*model.Job, error)
	RetryWithAudit(context.Context, *JobMutation) (*model.Job, error)
	// DeleteTerminalHistory atomically selects and removes one bounded,
	// oldest-first page of retention-eligible terminal Jobs and their Attempts.
	// It revalidates per-type ages against the primary database clock, never
	// selects active work, and always excludes the caller's cleanup Job.
	DeleteTerminalHistory(context.Context, *JobHistoryCleanup) (*JobHistoryCleanupResult, error)
}

type FileUploadCreation struct {
	Entry    *model.FileEntry
	Revision *model.FileRevision
	Lease    *model.UploadLease
}

type FileUpload struct {
	Entry    *model.FileEntry
	Revision *model.FileRevision
	Lease    *model.UploadLease
}

type FileRevisionUploadCreation struct {
	EntryID  model.FileEntryID
	Revision *model.FileRevision
	Lease    *model.UploadLease
}

type ProfilePicturePublication struct {
	ActorID              model.UserID
	UserID               model.UserID
	ExpectedUserRevision int64
	EntryID              model.FileEntryID
	RevisionID           model.FileRevisionID
	LeaseID              model.UploadLeaseID
	Renditions           []model.FileRendition
	ChangedAt            time.Time
	AuditEventID         string
	AuditAt              int64
}

type ProfilePicturePublicationResult struct {
	User     *model.User
	Revision *model.FileRevision
}

type DefaultProfilePicturePublication struct {
	UserID               model.UserID
	ExpectedUserRevision int64
	EntryID              model.FileEntryID
	RevisionID           model.FileRevisionID
	LeaseID              model.UploadLeaseID
	Renditions           []model.FileRendition
	AttachedAt           time.Time
}

type ProfilePictureState struct {
	EntryID    model.FileEntryID
	RevisionID model.FileRevisionID
	Renditions []model.FileRendition
}

type ProfilePictureUploadDiscard struct {
	ActorID                   model.UserID
	UserID                    model.UserID
	ExpectedUserRevision      int64
	ExpectedActiveEntryID     model.FileEntryID
	ExpectedCurrentRevisionID model.FileRevisionID
	UploadEntryID             model.FileEntryID
	RevisionID                model.FileRevisionID
	LeaseID                   model.UploadLeaseID
}

type ProfilePictureRemoval struct {
	ActorID                   model.UserID
	UserID                    model.UserID
	ExpectedUserRevision      int64
	EntryID                   model.FileEntryID
	ExpectedCurrentRevisionID model.FileRevisionID
	ExpectedSHA256            string
	ChangedAt                 time.Time
	AuditEventID              string
	AuditAt                   int64
}

type FilePurgeCandidateKind string

const (
	FilePurgeCandidateExpiredLease   FilePurgeCandidateKind = "expired_lease"
	FilePurgeCandidateArchivedCustom FilePurgeCandidateKind = "archived_custom"
)

// FilePurgeCandidate is an authoritative metadata-derived unit of physical
// cleanup. Cursor and IDs are safe operational values; storage paths never
// cross this boundary.
type FilePurgeCandidate struct {
	Cursor       string
	Kind         FilePurgeCandidateKind
	LeaseID      model.UploadLeaseID
	EntryID      model.FileEntryID
	RevisionID   model.FileRevisionID
	RenditionIDs []model.FileRenditionID
}

type FilePurgeCandidateRequest struct {
	After string
	Limit int
}

type FilePurgeCandidatePage struct {
	Candidates []FilePurgeCandidate
}

// FilePurgeClaim is the durable tombstone created before physical content is
// removed. Reusing a claim makes cleanup recoverable after a worker crash.
type FilePurgeClaim struct {
	ID        string
	Candidate FilePurgeCandidate
}

// FileStore owns file metadata and the atomic publication of domain references.
type FileStore interface {
	// CreateUpload atomically creates a pristine entry, its first pending revision,
	// and an active bounded lease owned by the named actor.
	CreateUpload(context.Context, *FileUploadCreation) (*FileUpload, error)
	// CreateRevisionUpload atomically appends a pending revision and active lease
	// to an existing, unarchived entry without changing its current revision.
	CreateRevisionUpload(context.Context, *FileRevisionUploadCreation) (*FileUpload, error)
	// DiscardProfilePictureUpload removes only the named pending revision and lease
	// when the actor, user revision, active entry, and current revision still match.
	// If the upload created a distinct pristine entry, that entry is removed too;
	// the operation never changes the visible picture.
	DiscardProfilePictureUpload(context.Context, *ProfilePictureUploadDiscard) error
	RenewUploadLease(context.Context, model.UploadLeaseID, model.UserID, int64, int64, time.Time) (*model.UploadLease, error)
	// PublishProfilePicture atomically consumes the actor-owned, unexpired lease,
	// persists the complete rendition set, makes the revision current, updates the
	// user's active custom entry under optimistic concurrency, and completes the
	// pre-existing audit attempt. Any failed precondition rolls back every change.
	PublishProfilePicture(context.Context, *ProfilePicturePublication) (*ProfilePicturePublicationResult, error)
	// PublishDefaultProfilePicture atomically consumes the target-user-owned,
	// unexpired upload lease, publishes the complete rendition set, and attaches
	// the entry only while that User still has no default and its revision matches.
	// It advances User revision without changing visible-picture time.
	PublishDefaultProfilePicture(context.Context, *DefaultProfilePicturePublication) (*ProfilePicturePublicationResult, error)
	GetProfilePictureState(context.Context, model.UserID) (*ProfilePictureState, error)
	GetProfilePictureRendition(context.Context, model.UserID, string) (*model.FileRendition, error)
	// RemoveProfilePictureWithAudit atomically verifies the user's revision, active
	// custom entry, current file revision, and expected normalized checksum; it then
	// archives that entry, clears the custom relationship, advances the user, and
	// completes the pre-existing audit attempt. Races fail with no partial change.
	RemoveProfilePictureWithAudit(context.Context, *ProfilePictureRemoval) (*model.User, error)
	// ListPurgeCandidates returns a stable bounded page derived from leases and
	// semantic metadata using the database clock. It never discovers ownership
	// by listing VFS objects.
	ListPurgeCandidates(context.Context, *FilePurgeCandidateRequest) (*FilePurgeCandidatePage, error)
	// ClaimPurgeCandidate atomically revalidates eligibility against the database
	// clock and writes a durable tombstone before any physical content is removed.
	ClaimPurgeCandidate(context.Context, *FilePurgeCandidate) (*FilePurgeClaim, error)
	// CompletePurge deletes metadata only after the caller has idempotently
	// removed the claimed physical content.
	CompletePurge(context.Context, *FilePurgeClaim) error
}

// InstitutionStore persists the institution represented by this installation.
type InstitutionStore interface {
	Save(context.Context, *model.Institution) (*model.Institution, error)
	Get(context.Context, string) (*model.Institution, error)
	GetSingleton(context.Context) (*model.Institution, error)
	Update(context.Context, *model.Institution) (*model.Institution, error)
	UpdateWithAudit(context.Context, *InstitutionUpdate) (*model.Institution, error)
	Archive(context.Context, string, int64) error
}

type InstitutionUpdate struct {
	Institution  *model.Institution
	AuditEventID string
	AuditAt      int64
}

// AcademicUnitCreation is the complete durable input for creating an Academic
// Unit and completing its already-persisted mutation audit in one transaction.
type AcademicUnitCreation struct {
	Unit         *model.AcademicUnit
	AuditEventID string
	AuditAt      int64
}

type AcademicUnitUpdate struct {
	Unit         *model.AcademicUnit
	AuditEventID string
	AuditAt      int64
}

type AcademicUnitArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

// AcademicUnitStore persists nodes in the institution's academic-unit tree.
type AcademicUnitStore interface {
	Create(context.Context, *AcademicUnitCreation) (*model.AcademicUnit, error)
	CreateIdempotently(context.Context, *AcademicUnitCreation, *CommandIdempotency) (*AcademicUnitCommandResult, error)
	UpdateWithAudit(context.Context, *AcademicUnitUpdate) (*model.AcademicUnit, error)
	ArchiveWithAudit(context.Context, *AcademicUnitArchive) (*model.AcademicUnit, error)
	Save(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error)
	Get(context.Context, string) (*model.AcademicUnit, error)
	GetByName(context.Context, string, string) (*model.AcademicUnit, error)
	ListChildren(context.Context, string, string) ([]*model.AcademicUnit, error)
	ListAncestors(context.Context, string) ([]*model.AcademicUnit, error)
	Search(context.Context, string, string, int) ([]*model.AcademicUnit, error)
	Update(context.Context, *model.AcademicUnit) (*model.AcademicUnit, error)
	Archive(context.Context, string, int64) (*model.AcademicUnit, error)
}

// ProgrammeStore persists courses of study owned by academic units.
type ProgrammeCreation struct {
	Programme    *model.Programme
	AuditEventID string
	AuditAt      int64
}

type ProgrammeUpdate struct {
	Programme    *model.Programme
	AuditEventID string
	AuditAt      int64
}

type ProgrammeArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

type ProgrammeStore interface {
	Create(context.Context, *ProgrammeCreation) (*model.Programme, error)
	UpdateWithAudit(context.Context, *ProgrammeUpdate) (*model.Programme, error)
	ArchiveWithAudit(context.Context, *ProgrammeArchive) (*model.Programme, error)
	Save(context.Context, *model.Programme) (*model.Programme, error)
	Get(context.Context, string) (*model.Programme, error)
	GetByName(context.Context, string, string) (*model.Programme, error)
	ListByAcademicUnit(context.Context, string) ([]*model.Programme, error)
	SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Programme, error)
	Update(context.Context, *model.Programme) (*model.Programme, error)
	Archive(context.Context, string, int64) (*model.Programme, error)
}

// ProgrammeLevelStore persists reusable curriculum stages owned by programmes.
type ProgrammeLevelCreation struct {
	Level        *model.ProgrammeLevel
	AuditEventID string
	AuditAt      int64
}

type ProgrammeLevelUpdate struct {
	Level        *model.ProgrammeLevel
	AuditEventID string
	AuditAt      int64
}

type ProgrammeLevelArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

type ProgrammeLevelStore interface {
	Create(context.Context, *ProgrammeLevelCreation) (*model.ProgrammeLevel, error)
	UpdateWithAudit(context.Context, *ProgrammeLevelUpdate) (*model.ProgrammeLevel, error)
	ArchiveWithAudit(context.Context, *ProgrammeLevelArchive) (*model.ProgrammeLevel, error)
	Save(context.Context, *model.ProgrammeLevel) (*model.ProgrammeLevel, error)
	Get(context.Context, string) (*model.ProgrammeLevel, error)
	GetByName(context.Context, string, string) (*model.ProgrammeLevel, error)
	ListByProgramme(context.Context, string) ([]*model.ProgrammeLevel, error)
	SearchByProgramme(context.Context, string, string, int) ([]*model.ProgrammeLevel, error)
	Update(context.Context, *model.ProgrammeLevel) (*model.ProgrammeLevel, error)
	Archive(context.Context, string, int64) (*model.ProgrammeLevel, error)
}

// AcademicPeriodVisibilityScope constrains list/search to institution-owned
// periods plus unit-owned periods visible through authorized subtree roots.
type AcademicPeriodVisibilityScope struct {
	InstitutionID       string
	InstitutionWide     bool
	AcademicUnitRootIDs []string
}

// AcademicPeriodStore persists institution- or Academic-Unit-owned enrollment
// periods.
type AcademicPeriodCreation struct {
	Period       *model.AcademicPeriod
	AuditEventID string
	AuditAt      int64
}

type AcademicPeriodUpdate struct {
	Period       *model.AcademicPeriod
	AuditEventID string
	AuditAt      int64
}

type AcademicPeriodArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

type AcademicPeriodStore interface {
	Create(context.Context, *AcademicPeriodCreation) (*model.AcademicPeriod, error)
	CreateIdempotently(context.Context, *AcademicPeriodCreation, *CommandIdempotency) (*AcademicPeriodCommandResult, error)
	UpdateWithAudit(context.Context, *AcademicPeriodUpdate) (*model.AcademicPeriod, error)
	ArchiveWithAudit(context.Context, *AcademicPeriodArchive) (*model.AcademicPeriod, error)
	Save(context.Context, *model.AcademicPeriod) (*model.AcademicPeriod, error)
	Get(context.Context, string) (*model.AcademicPeriod, error)
	GetByOwnerName(context.Context, model.Resource, string) (*model.AcademicPeriod, error)
	ListVisible(context.Context, AcademicPeriodVisibilityScope, string, int) ([]*model.AcademicPeriod, error)
	Update(context.Context, *model.AcademicPeriod) (*model.AcademicPeriod, error)
	Archive(context.Context, string, int64) (*model.AcademicPeriod, error)
}

// ClassStore persists concrete programme-level rosters for academic periods.
type ClassCreation struct {
	Class        *model.Class
	AuditEventID string
	AuditAt      int64
}

type ClassUpdate struct {
	Class                  *model.Class
	ExpectedAcademicUnitID string
	ExpectedRevision       int64
	AuditEventID           string
	AuditAt                int64
}

type ClassArchive struct {
	ID                     string
	ExpectedAcademicUnitID string
	ExpectedRevision       int64
	ArchiveAt              int64
	AuditEventID           string
	AuditAt                int64
}

type ClassStore interface {
	Create(context.Context, *ClassCreation) (*model.Class, error)
	UpdateWithAudit(context.Context, *ClassUpdate) (*model.Class, error)
	ArchiveWithAudit(context.Context, *ClassArchive) (*model.Class, error)
	Save(context.Context, *model.Class) (*model.Class, error)
	Get(context.Context, string) (*model.Class, error)
	GetByName(context.Context, string, string, string) (*model.Class, error)
	ListByProgrammeLevel(context.Context, string) ([]*model.Class, error)
	ListByAcademicPeriod(context.Context, string) ([]*model.Class, error)
	SearchByAcademicUnit(context.Context, string, string, int) ([]*model.Class, error)
	GetAcademicUnitId(context.Context, string) (string, error)
	Update(context.Context, *model.Class) (*model.Class, error)
	Archive(context.Context, string, int64) (*model.Class, error)
}

// UserListOptions is a bounded persistence query. IncludeDisabled is effective
// only with an explicit InstitutionWide visibility scope.
type UserListOptions struct {
	ID                           string
	Query                        string
	AfterUsername                string
	AfterId                      string
	Limit                        int
	IncludeDisabled              bool
	MissingDefaultProfilePicture bool
	Visibility                   UserVisibilityScope
}

// UserVisibilityScope constrains user list/search results in persistence.
// Scoped and zero-value visibility always exclude disabled Users.
type UserVisibilityScope struct {
	// InstitutionWide and AcademicUnitRootIDs are user.view relationship
	// visibility: current Academic Unit membership, Class membership, and
	// active-Role bindings are visibility anchors.
	InstitutionWide     bool
	AcademicUnitRootIDs []string
	// For UserStore directory queries, ClassMemberInstitutionWide,
	// ClassMemberAcademicUnitRootIDs, and ClassIDs are class.members.view roster
	// visibility. They expose current Class members only and never Academic Unit
	// members or Role-Binding holders.
	ClassMemberInstitutionWide     bool
	ClassMemberAcademicUnitRootIDs []string
	ClassIDs                       []string
	ActiveAt                       int64
}

// UserVisibilityMatch identifies the authorized scope through which one User
// is visible. It lets callers record the exact subtree/class decision without
// assigning the target to an unrelated authorized scope when a principal has
// more than one binding.
type UserVisibilityMatch struct {
	ScopeType model.RoleScopeType
	ScopeID   string
}

type UserProfileUpdate struct {
	UserID           model.UserID
	Changes          model.UserProfileChanges
	ExpectedRevision int64
	AuditEventID     string
	AuditAt          int64
}

// UserDisabledStateChange is the complete durable input for changing whether
// a user may operate. Disabling also revokes every active session in the same
// transaction. AuditEventID identifies an already-persisted attempt that must
// be completed successfully before any state change may commit.
type UserDisabledStateChange struct {
	ID               string
	ExpectedRevision int64
	Disabled         bool
	Capabilities     AccessDeploymentCapabilities
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	DeliveryJob      *model.Job
	ChangedAt        int64
	RevocationReason string
	AuditEventID     string
	AuditAt          int64
	Command          *CommandIdempotency
	Replayed         bool
	NoOp             bool
}

// UserDisabledStateResult contains the committed user state and the minimal
// revocation facts needed for post-commit cache and realtime effects.
type UserDisabledStateResult struct {
	User               *model.User
	RevokedSessions    []*model.Session
	RevokedTokenHashes []string
}

// UserCreation is the named, import-safe transaction for creating one User.
// DefaultProfilePictureJob must be the typed, deduplicated generation intent
// prepared for User by the application. PasswordCredential is optional so
// external/imported accounts do not need to invent a local credential.
type UserCreation struct {
	User                     *model.User
	Settings                 *model.UserSettingsDocument
	PasswordCredential       *model.PasswordCredential
	DefaultProfilePictureJob *model.Job
}

type UserCreationResult struct {
	User               *model.User
	PasswordCredential *model.PasswordCredential
}

// PublicLocalUserRegistration is the complete anonymous self-registration
// transition. Persistence rechecks the current Access Policy and commits the
// unverified local account, initial settings work, successful audit, and
// frozen verification credential delivery as one transaction.
type PublicLocalUserRegistration struct {
	User                     *model.User
	Settings                 *model.UserSettingsDocument
	PasswordCredential       *model.PasswordCredential
	DefaultProfilePictureJob *model.Job
	VerificationToken        *model.UserToken
	TokenLifetime            time.Duration
	MailLifetime             time.Duration
	VerificationOccurrence   *model.MailOccurrence
	VerificationDelivery     *model.MailDelivery
	VerificationJob          *model.Job
	AuditEvent               *model.AuditEvent
}

type PublicLocalUserRegistrationResult struct {
	User       *model.User
	Token      *model.UserToken
	AuditEvent *model.AuditEvent
}

// UserStore persists login-capable accounts without their credentials.
type UserStore interface {
	// Create atomically persists the prepared User, its optional password
	// credential, and its matching active-deduplicated default-picture generation
	// intent. All future import-oriented creation must use this operation.
	Create(context.Context, *UserCreation) (*UserCreationResult, error)
	RegisterLocal(context.Context, *PublicLocalUserRegistration) (*PublicLocalUserRegistrationResult, error)
	Get(context.Context, string) (*model.User, error)
	GetByUsername(context.Context, string) (*model.User, error)
	GetByEmail(context.Context, string) (*model.User, error)
	List(context.Context, UserListOptions) ([]*model.User, error)
	MatchVisibility(context.Context, string, UserVisibilityScope) (UserVisibilityMatch, error)
	UpdateProfileWithAudit(context.Context, *UserProfileUpdate) (*model.User, error)
	SetDisabledWithAudit(context.Context, *UserDisabledStateChange) (*UserDisabledStateResult, error)
	UpdateLastLogin(context.Context, string, int64) error
}

// UserSettingsStore owns the exact current User Settings Document. Mutations
// are added only through named atomic contracts; it is not a generic file or
// configuration store.
type UserSettingsStore interface {
	Get(context.Context, model.UserID) (*model.UserSettingsDocument, error)
	Replace(context.Context, *UserSettingsReplacement, *CommandIdempotency) (*UserSettingsReplacementResult, error)
}

type UserSettingsReplacement struct {
	UserID           model.UserID
	Source           string
	FormatVersion    int
	ExpectedRevision model.UserSettingsRevision
	NextRevision     model.UserSettingsRevision
	UpdatedAt        time.Time
	AuditEvent       *model.AuditEvent
}

type UserSettingsReplacementResult struct {
	Revision      model.UserSettingsRevision `json:"revision"`
	FormatVersion int                        `json:"format_version"`
	UpdatedAt     time.Time                  `json:"updated_at"`
	Changed       bool                       `json:"changed"`
	Replayed      bool                       `json:"-"`
}

type ExternalIdentityResolution struct {
	Identity    *model.ExternalIdentity
	User        *model.User
	Provisioned bool
}

type ExternalIdentityResolutionRequest struct {
	Identity                 *model.ExternalIdentity
	User                     *model.User
	Settings                 *model.UserSettingsDocument
	Capabilities             AccessDeploymentCapabilities
	ProvisionAudit           *model.AuditEvent
	DefaultProfilePictureJob *model.Job
}

// ExternalIdentityLink is the proof-completed, application-prepared command
// that attaches one immutable provider subject to the exact current User.
type ExternalIdentityLink struct {
	Identity     *model.ExternalIdentity
	Capabilities AccessDeploymentCapabilities
	AuditEventID string
	AuditAt      int64
}

// ExternalIdentityUnlink removes one exact provider identity and revokes only
// Sessions that authenticated through that provider.
type ExternalIdentityUnlink struct {
	ID               model.ExternalIdentityID
	UserID           model.UserID
	Capabilities     AccessDeploymentCapabilities
	ChangedAt        int64
	RevocationReason string
	AuditEventID     string
	AuditAt          int64
}

type AuthenticationMethodMutationResult struct {
	Identity           *model.ExternalIdentity
	PasswordCredential *model.PasswordCredential
	RevokedSessions    []*model.Session
	RevokedTokenHashes []string
}

// ExternalIdentityStore persists provider-subject links and owns the
// transaction that either resolves an existing link or provisions a new user
// and link without email-based account merging.
type ExternalIdentityStore interface {
	Save(context.Context, *model.ExternalIdentity) (*model.ExternalIdentity, error)
	Get(context.Context, string) (*model.ExternalIdentity, error)
	GetByProviderSubject(context.Context, string, string) (*model.ExternalIdentity, error)
	ListByUser(context.Context, string) ([]*model.ExternalIdentity, error)
	ResolveOrProvision(context.Context, *ExternalIdentityResolutionRequest) (*ExternalIdentityResolution, error)
	LinkWithAudit(context.Context, *ExternalIdentityLink) (*AuthenticationMethodMutationResult, error)
	UnlinkWithAudit(context.Context, *ExternalIdentityUnlink) (*AuthenticationMethodMutationResult, error)
}

// ExternalLoginStateStore persists hashed, browser-bound, one-use login
// transactions so any node may receive the provider callback.
type ExternalLoginStateStore interface {
	// Save applies lifetime to one authoritative database timestamp. Callers
	// provide no absolute creation or expiry deadline.
	Save(context.Context, *model.ExternalLoginState, time.Duration) (*model.ExternalLoginState, error)
	// SaveInvitationAdmission resolves a raw-claim digest against one currently
	// pending Invitation and atomically binds its identity to the new browser
	// transaction. The digest is used only for the lookup and is not copied into
	// the external-login state.
	SaveInvitationAdmission(context.Context, *model.ExternalLoginState, time.Duration, string) (*model.ExternalLoginState, error)
	GetByStateHash(context.Context, string) (*model.ExternalLoginState, error)
	Consume(
		context.Context,
		string,
		string,
		string,
	) (*model.ExternalLoginState, error)
	Maintain(context.Context, int) (*ExternalLoginStateMaintenanceResult, error)
}

// ExternalLoginStateMaintenanceResult reports one bounded database-time pass.
// Terminalized is the number of abandoned provider-connection audit attempts
// completed as failures; Purged is safe state metadata removed after retention.
type ExternalLoginStateMaintenanceResult struct {
	Terminalized int
	Purged       int
	More         bool
}

type EmailVerificationResult struct {
	Token *model.UserToken
	User  *model.User
}

type PasswordResetResult struct {
	Token               *model.UserToken
	User                *model.User
	PasswordCredential  *model.PasswordCredential
	RevokedSessions     []*model.Session
	RevokedAccessHashes []string
}

// UserTokenMailIssue is the named aggregate that replaces an active
// purpose-specific token and commits its successful audit and frozen recovery
// delivery intent as one durable transition.
type UserTokenMailIssue struct {
	Token      *model.UserToken
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
	AuditEvent *model.AuditEvent
}

// UserEmailChange is the named transition that replaces the account address,
// invalidates prior verification credentials, and commits the new verification
// credential plus frozen old/new-address notifications atomically.
type UserEmailChange struct {
	UserID                                    model.UserID
	ExpectedRevision                          int64
	NewEmail                                  string
	Token                                     *model.UserToken
	TokenLifetime                             time.Duration
	WarningLifetime                           time.Duration
	WarningOccurrence, VerificationOccurrence *model.MailOccurrence
	WarningDelivery, VerificationDelivery     *model.MailDelivery
	WarningJob, VerificationJob               *model.Job
	AuditEventID                              string
	AuditAt                                   int64
}

type UserEmailChangeResult struct {
	User  *model.User
	Token *model.UserToken
}

// PrivilegedEmailVerification records a high-assurance administrator override
// and its frozen user notice in one transaction.
type PrivilegedEmailVerification struct {
	UserID           model.UserID
	ExpectedRevision int64
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	Job              *model.Job
	AuditEventID     string
	AuditAt          int64
}

// PasswordResetCompletion is the named aggregate that consumes one reset
// credential, changes the password, revokes Sessions, completes the security
// audit, and records the password-changed notification atomically.
type PasswordResetCompletion struct {
	TokenHash        string
	PasswordHash     string
	At               int64
	RevocationReason string
	AuditEvent       *model.AuditEvent
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	Job              *model.Job
}

// UserTokenStore owns issuance and single-use consumption of purpose-specific
// account credentials. Consumption methods include their account mutation,
// session revocation where applicable, and terminal audit in one transaction.
type UserTokenStore interface {
	Issue(
		context.Context,
		*UserTokenMailIssue,
	) (*model.UserToken, error)
	ChangeEmail(context.Context, *UserEmailChange) (*UserEmailChangeResult, error)
	VerifyEmailPrivileged(context.Context, *PrivilegedEmailVerification) (*model.User, error)
	Get(context.Context, model.UserTokenID) (*model.UserToken, error)
	GetByHash(
		context.Context,
		string,
		model.UserTokenPurpose,
	) (*model.UserToken, error)
	ConsumeEmailVerification(
		context.Context,
		string,
		int64,
		*model.AuditEvent,
	) (*EmailVerificationResult, error)
	ConsumePasswordReset(
		context.Context,
		*PasswordResetCompletion,
	) (*PasswordResetResult, error)
}

// StudentClassInvitationIssue is the named transaction that makes one
// pre-User Invitation and its recoverable delivery intent durable before
// completing the already-recorded security audit attempt.
type StudentClassInvitationIssue struct {
	Invitation   *model.Invitation
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	AuditEventID string
	AuditAt      int64
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
	AuditEventID string
	AuditAt      int64
}

// StudentClassInvitationAcceptance is the complete prepared package for one
// claim. Persistence resolves an existing User by the invited mailbox inside
// the transaction and uses the prepared User artifacts only when creation or
// missing local enrollment is required.
type StudentClassInvitationAcceptance struct {
	ClaimHash                string
	AcceptedAt               int64
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
	AcceptedAt               int64
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
	AuditEventID string
	AuditAt      int64
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
	AuditEventID    string
	AuditAt         int64
	RequiredActions []model.Action
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
	AcademicUnitRootIDs []string
	ClassIDs            []string
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
	AuditEventID         string
	AuditAt              int64
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
	AuditEventID     string
	AuditAt          int64
}

// InvitationRevocation terminalizes one pending Invitation. RevocationNotice
// is inserted only when an original Invitation delivery was SMTP Accepted.
type InvitationRevocation struct {
	ID               model.InvitationID
	ExpectedRevision int64
	ActorUserID      model.UserID
	RevocationNotice *PreparedMail
	AuditEventID     string
	AuditAt          int64
}

// PreparedMail groups one already-rendered direct occurrence and its work.
// Named aggregate inputs use it only where the mail is conditionally applied.
type PreparedMail struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
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
	CurrentAuditEventID     string
	ReplacementAuditEventID string
	AuditAt                 int64
}

const (
	OnboardingImportMaximumBytes   = 10 * 1024 * 1024
	OnboardingImportMaximumRows    = 50_000
	OnboardingImportMaximumColumns = 64
	OnboardingImportMaximumField   = 16 * 1024
	OnboardingImportPageSize       = 200
)

// OnboardingImport is the safe aggregate projection. Recipient data and the
// frozen row commands remain private to persistence and never enter Jobs,
// audit fields, logs, or administration list responses.
type OnboardingImport struct {
	ID                        model.OnboardingImportID
	Mode                      model.OnboardingImportMode
	State                     model.OnboardingImportState
	ScopeType                 model.RoleScopeType
	ScopeID                   string
	RoleID                    model.RoleID
	SourcePeriodID            model.AcademicPeriodID
	SourceClassID             model.ClassID
	DestinationPeriodID       model.AcademicPeriodID
	DestinationClassID        model.ClassID
	SourcePeriodRevision      int64
	SourceClassRevision       int64
	DestinationPeriodRevision int64
	DestinationClassRevision  int64
	EffectiveAt               time.Time
	ActorUserID               model.UserID
	PreviewDigest             string
	IgnoredHeaders            []string
	TotalRows                 int
	ValidRows                 int
	InvalidRows               int
	SucceededRows             int
	NoOpRows                  int
	FailedRows                int
	SkippedRows               int
	CommitPolicy              model.OnboardingImportCommitPolicy
	ParseJobID                model.JobID
	ExecutionJobID            model.JobID
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	ExpiresAt                 time.Time
	Revision                  int64
	FailureCode               string
	Principal                 model.Principal
}

// OnboardingImportRow is the detailed seven-day preview/report row. Command
// contains private normalized recipient/package input and must never be
// returned directly by a transport.
type OnboardingImportRow struct {
	ImportID                        model.OnboardingImportID
	RowNumber                       int
	Reference                       string
	Operation                       string
	ScopeType                       model.RoleScopeType
	ScopeID                         string
	TargetRevision                  int64
	RoleID                          model.RoleID
	RoleRevision                    int64
	Email                           string
	UserID                          model.UserID
	RelationshipID                  string
	RelationshipRevision            int64
	DestinationRelationshipID       string
	DestinationRelationshipRevision int64
	AffiliationKind                 model.AffiliationKind
	Username                        string
	DisplayName                     string
	FirstName                       string
	LastName                        string
	Locale                          string
	Timezone                        string
	StartsAt                        int64
	EndsAt                          int64
	PreviewStatus                   model.OnboardingImportRowStatus
	PreviewCode                     string
	Status                          model.OnboardingImportRowStatus
	PublicCode                      string
	InvitationID                    model.InvitationID
	ResourceID                      string
	UpdatedAt                       time.Time
}

type OnboardingImportCreation struct {
	Import             *OnboardingImport
	ParseJob           *model.Job
	AuditEventID       string
	SourceAuditEventID string
	AuditAt            int64
}

type OnboardingImportPreviewCompletion struct {
	ID               model.OnboardingImportID
	ExpectedRevision int64
	Digest           string
	IgnoredHeaders   []string
	Rows             []OnboardingImportRow
	At               time.Time
}

type OnboardingImportCommit struct {
	ID                 model.OnboardingImportID
	ActorUserID        model.UserID
	Principal          model.Principal
	ExpectedRevision   int64
	PreviewDigest      string
	Policy             model.OnboardingImportCommitPolicy
	IdempotencyKey     [sha256.Size]byte
	ExecutionJob       *model.Job
	At                 time.Time
	AuditEventID       string
	SourceAuditEventID string
	AuditAt            int64
}

type OnboardingImportCancellation struct {
	ID                 model.OnboardingImportID
	ActorUserID        model.UserID
	Principal          model.Principal
	At                 time.Time
	AuditEventID       string
	SourceAuditEventID string
	AuditAt            int64
}

type OnboardingImportRowCompletion struct {
	ID           model.OnboardingImportID
	RowNumber    int
	Status       model.OnboardingImportRowStatus
	PublicCode   string
	InvitationID model.InvitationID
	ResourceID   string
	At           time.Time
}

type OnboardingImportPage struct {
	Rows []OnboardingImportRow
	More bool
}

// InvitationStore owns durable Invitation issuance, acceptance,
// administration, and their retained idempotent outcomes. Raw claims never
// cross this boundary.
type InvitationStore interface {
	IssueStudentClass(context.Context, *StudentClassInvitationIssue) (*model.Invitation, error)
	IssueStudentClassIdempotently(context.Context, *StudentClassInvitationIssue, *CommandIdempotency) (*InvitationCommandResult, error)
	IssueTeacherAcademicUnit(context.Context, *TeacherAcademicUnitInvitationIssue) (*model.Invitation, error)
	IssueTeacherAcademicUnitIdempotently(context.Context, *TeacherAcademicUnitInvitationIssue, *CommandIdempotency) (*InvitationCommandResult, error)
	IssueScopedRole(context.Context, *ScopedRoleInvitationIssue) (*model.Invitation, error)
	IssueScopedRoleIdempotently(context.Context, *ScopedRoleInvitationIssue, *CommandIdempotency) (*InvitationCommandResult, error)
	ResolveOnboardingInvitationNoOp(context.Context, *model.Invitation) (*model.Invitation, bool, error)
	FindCommandOutcome(context.Context, *CommandIdempotency) (*InvitationCommandResult, error)
	ReplayIssue(context.Context, *CommandIdempotency, string, int64) (*InvitationCommandResult, error)
	ReplayAdministration(context.Context, *CommandIdempotency, string, int64) (*InvitationAdministrationCommandResult, error)
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

// OnboardingImportStore owns seven-day administrative import and progression
// aggregates, frozen private rows, execution fences, and retention.
type OnboardingImportStore interface {
	CreateOnboardingImport(context.Context, *OnboardingImportCreation) (*OnboardingImport, error)
	GetOnboardingImport(context.Context, model.OnboardingImportID) (*OnboardingImport, error)
	ListStudentProgressionRoster(context.Context, model.ClassID, time.Time, int) ([]*model.ClassMember, error)
	CompleteOnboardingImportPreview(context.Context, *OnboardingImportPreviewCompletion) (*OnboardingImport, error)
	CommitOnboardingImport(context.Context, *OnboardingImportCommit) (*OnboardingImport, error)
	ListOnboardingImportRows(context.Context, model.OnboardingImportID, int, int) (*OnboardingImportPage, error)
	CompleteOnboardingImportRow(context.Context, *OnboardingImportRowCompletion) (*OnboardingImport, error)
	FinishOnboardingImport(context.Context, model.OnboardingImportID, time.Time) (*OnboardingImport, error)
	CancelOnboardingImport(context.Context, *OnboardingImportCancellation) (*OnboardingImport, error)
	FailOnboardingImport(context.Context, model.OnboardingImportID, string, time.Time) (*OnboardingImport, error)
	ListExpiredOnboardingImports(context.Context, int, time.Time) ([]model.OnboardingImportID, error)
	PurgeOnboardingImport(context.Context, model.OnboardingImportID, time.Time) (bool, error)
}

type PersonalAccessTokenResolution struct {
	Token *model.PersonalAccessToken
	User  *model.User
}

// PersonalAccessTokenSecurityNotice is the complete ordinary-mail intent for
// one successful PAT transition. It deliberately carries neither the
// one-time credential nor the persisted token hash or complete scope list.
type PersonalAccessTokenSecurityNotice struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
	// ExpiresAt is the bounded safe expiry rendered into the frozen payload.
	// Persistence verifies it equals the committed token deadline.
	ExpiresAt time.Time
}

type PersonalAccessTokenMutationKind string

const (
	PersonalAccessTokenMutationCreate  PersonalAccessTokenMutationKind = "create"
	PersonalAccessTokenMutationEnable  PersonalAccessTokenMutationKind = "enable"
	PersonalAccessTokenMutationDisable PersonalAccessTokenMutationKind = "disable"
	PersonalAccessTokenMutationRevoke  PersonalAccessTokenMutationKind = "revoke"
)

// PersonalAccessTokenMutationPreparation is the bounded durable attempt that
// precedes rendering. PostgreSQL assigns its ActionAt. The Audit draft contains
// only safe bounded metadata and is inserted as one terminal event only when
// the preparation succeeds, explicitly fails, or is reconciled after expiry.
type PersonalAccessTokenMutationPreparation struct {
	UserID   string
	TokenID  string
	Kind     PersonalAccessTokenMutationKind
	Audit    *model.AuditEvent
	Lifetime time.Duration
}

type PreparedPersonalAccessTokenMutation struct {
	ID        string
	ActionAt  time.Time
	ExpiresAt time.Time
}

type PersonalAccessTokenMutationFailure struct {
	PreparationID string
	ErrorCode     string
}

type PersonalAccessTokenPreparationMaintenanceResult struct {
	Failed int
	More   bool
}

type PersonalAccessTokenMutationResult struct {
	Token *model.PersonalAccessToken
	Fresh bool
}

// PersonalAccessTokenCreationMutation couples the new hashed credential,
// terminal audit, and security notice in one transaction.
type PersonalAccessTokenCreationMutation struct {
	Token           *model.PersonalAccessToken
	MaximumActive   int
	MinimumLifetime time.Duration
	MaximumLifetime time.Duration
	PreparationID   string
	Notice          PersonalAccessTokenSecurityNotice
}

// PersonalAccessTokenStateMutation couples one fresh enable/disable state
// change, terminal audit, and matching security notice in one transaction.
type PersonalAccessTokenStateMutation struct {
	ID            string
	UserID        string
	Disabled      bool
	MaximumActive int
	PreparationID string
	Notice        PersonalAccessTokenSecurityNotice
}

// PersonalAccessTokenRevocation couples one fresh revocation, terminal audit,
// and matching security notice in one transaction.
type PersonalAccessTokenRevocation struct {
	ID            string
	UserID        string
	PreparationID string
	Notice        PersonalAccessTokenSecurityNotice
}

// PersonalAccessTokenStore persists hashed, explicitly scoped credentials.
// Resolve is authoritative and also performs the debounced last-used update.
type PersonalAccessTokenStore interface {
	PrepareMutation(context.Context, *PersonalAccessTokenMutationPreparation) (*PreparedPersonalAccessTokenMutation, error)
	FailMutation(context.Context, *PersonalAccessTokenMutationFailure) error
	MaintainMutationPreparations(context.Context, int) (*PersonalAccessTokenPreparationMaintenanceResult, error)
	Create(context.Context, *PersonalAccessTokenCreationMutation) (*PersonalAccessTokenMutationResult, error)
	Get(context.Context, string) (*model.PersonalAccessToken, error)
	ListByUser(context.Context, string) ([]*model.PersonalAccessToken, error)
	Resolve(context.Context, string, int64, int64) (*PersonalAccessTokenResolution, error)
	ChangeState(context.Context, *PersonalAccessTokenStateMutation) (*PersonalAccessTokenMutationResult, error)
	RevokeWithAudit(context.Context, *PersonalAccessTokenRevocation) (*PersonalAccessTokenMutationResult, error)
}

type MFAActivationResult struct {
	Credential        *model.MFACredential
	Session           *model.Session
	AccessTokenHashes []string
}

type MFADisableResult struct {
	AccessTokenHashes []string
}

// MFASecurityNotice is the complete durable ordinary-mail intent coupled to
// one successful MFA security transition. It deliberately carries no MFA
// secret, encrypted credential state, recovery code, or recovery-code hash.
type MFASecurityNotice struct {
	Occurrence *model.MailOccurrence
	Delivery   *model.MailDelivery
	Job        *model.Job
}

type MFAActivationMutation struct {
	CredentialID  string
	UserID        string
	TimeStep      int64
	RecoveryCodes []*model.MFARecoveryCode
	SessionID     string
	At            int64
	AuditEventID  string
	AuditAt       int64
	Notice        MFASecurityNotice
}

type MFARecoveryCodesRegeneration struct {
	UserID        string
	RecoveryCodes []*model.MFARecoveryCode
	At            int64
	AuditEventID  string
	AuditAt       int64
	Notice        MFASecurityNotice
}

type MFADisablement struct {
	UserID       string
	At           int64
	AuditEventID string
	AuditAt      int64
	Notice       MFASecurityNotice
}

// MFAStore owns the encrypted TOTP credential, hashed recovery codes, replay
// prevention, and the session-strength changes coupled to MFA lifecycle.
type MFAStore interface {
	SavePending(context.Context, *model.MFACredential) (*model.MFACredential, error)
	GetByUser(context.Context, string) (*model.MFACredential, error)
	Activate(context.Context, *MFAActivationMutation) (*MFAActivationResult, error)
	ConsumeSecondFactor(context.Context, string, int64, string, int64) error
	UpgradeSession(context.Context, string, string, int64) ([]string, error)
	ReplaceRecoveryCodes(context.Context, *MFARecoveryCodesRegeneration) error
	CountRecoveryCodes(context.Context, string) (int, error)
	Disable(context.Context, *MFADisablement) (*MFADisableResult, error)
}

// AffiliationStore persists non-exclusive institution relationships.
type AffiliationCreation struct {
	Affiliation  *model.Affiliation
	AuditEventID string
	AuditAt      int64
	Command      *CommandIdempotency
	Replayed     bool
	NoOp         bool
}

type AffiliationEnd struct {
	ID               string
	ExpectedRevision int64
	EndAt            int64
	AuditEventID     string
	AuditAt          int64
	Command          *CommandIdempotency
	Replayed         bool
	NoOp             bool
}

type AffiliationStore interface {
	Create(context.Context, *AffiliationCreation) (*model.Affiliation, error)
	EndWithAudit(context.Context, *AffiliationEnd) (*model.Affiliation, error)
	Save(context.Context, *model.Affiliation) (*model.Affiliation, error)
	Get(context.Context, string) (*model.Affiliation, error)
	ListByUser(context.Context, string) ([]*model.Affiliation, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.Affiliation, error)
	End(context.Context, string, int64, int64) (*model.Affiliation, error)
}

// AcademicUnitMemberStore persists organizational membership without roles.
type AcademicUnitMemberCreation struct {
	Member                    *model.AcademicUnitMember
	ExpectedRecipientRevision int64
	Notice                    *PreparedMail
	AuditEventID              string
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

type AcademicUnitMemberEnd struct {
	ID                        string
	ExpectedRevision          int64
	ExpectedRecipientRevision int64
	Notice                    *PreparedMail
	EndAt                     int64
	AuditEventID              string
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

type AcademicUnitMemberStore interface {
	Create(context.Context, *AcademicUnitMemberCreation) (*model.AcademicUnitMember, error)
	EndWithAudit(context.Context, *AcademicUnitMemberEnd) (*model.AcademicUnitMember, error)
	Save(context.Context, *model.AcademicUnitMember) (*model.AcademicUnitMember, error)
	Get(context.Context, string) (*model.AcademicUnitMember, error)
	ListByUser(context.Context, string) ([]*model.AcademicUnitMember, error)
	ListByAcademicUnit(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
	End(context.Context, string, int64, int64) (*model.AcademicUnitMember, error)
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
	ProgressionSourceAuditEventID      string
	ProgressionDestinationAuditEventID string
	Notice                             *PreparedMail
	AuditEventID                       string
	PreviousAuditEventID               string
	AuditAt                            int64
	Command                            *CommandIdempotency
	Replayed                           bool
	NoOp                               bool
}

type ClassMemberEnd struct {
	ID                        string
	ExpectedRevision          int64
	ExpectedRecipientRevision int64
	EndAt                     int64
	Notice                    *PreparedMail
	AuditEventID              string
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

// ClassMemberStore owns transactional student enrollment and history.
type ClassMemberStore interface {
	EnrollWithAudit(context.Context, *ClassMemberEnrollment) (*ClassEnrollmentResult, error)
	EndWithAudit(context.Context, *ClassMemberEnd) (*model.ClassMember, error)
	Enroll(context.Context, *model.ClassMember) (*ClassEnrollmentResult, error)
	Get(context.Context, string) (*model.ClassMember, error)
	ListByUser(context.Context, string) ([]*model.ClassMember, error)
	ListByClass(context.Context, string, int64) ([]*model.ClassMember, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.ClassMember, error)
	End(context.Context, string, int64, int64) (*model.ClassMember, error)
}

// PasswordCredentialStore persists one encoded password hash per local user.
type PasswordCredentialStore interface {
	Save(context.Context, *model.PasswordCredential) (*model.PasswordCredential, error)
	GetByUser(context.Context, string) (*model.PasswordCredential, error)
	Update(context.Context, *model.PasswordCredential) (*model.PasswordCredential, error)
	EnrollWithAudit(context.Context, *PasswordCredentialEnrollment) (*AuthenticationMethodMutationResult, error)
	RemoveWithAudit(context.Context, *PasswordCredentialRemoval) (*AuthenticationMethodMutationResult, error)
}

type PasswordCredentialEnrollment struct {
	Credential   *model.PasswordCredential
	Capabilities AccessDeploymentCapabilities
	AuditEventID string
	AuditAt      int64
}

type PasswordCredentialRemoval struct {
	UserID           model.UserID
	Capabilities     AccessDeploymentCapabilities
	ChangedAt        int64
	RevocationReason string
	AuditEventID     string
	AuditAt          int64
}

// SessionRevocation is the complete durable input for revoking one session
// under an already-persisted audit attempt. AuditEventID must identify an
// attempt that is completed successfully before the revocation may commit.
type SessionRevocation struct {
	SessionID    string
	UserID       string
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	RevokedAt    int64
	Reason       string
	AuditEventID string
	AuditAt      int64
}

// SessionRevocationResult contains the revoked session and token hashes needed
// for post-commit cache and realtime effects.
type SessionRevocationResult struct {
	Session     *model.Session
	TokenHashes []string
}

// UserSessionsRevocation is the complete durable input for revoking every
// active session belonging to one user under an already-persisted audit
// attempt.
type UserSessionsRevocation struct {
	UserID       string
	Occurrence   *model.MailOccurrence
	Delivery     *model.MailDelivery
	DeliveryJob  *model.Job
	RevokedAt    int64
	Reason       string
	AuditEventID string
	AuditAt      int64
	Command      *CommandIdempotency
	Replayed     bool
	NoOp         bool
}

// UserSessionsRevocationResult contains the revoked sessions and token hashes
// needed for post-commit cache and realtime effects.
type UserSessionsRevocationResult struct {
	Sessions    []*model.Session
	TokenHashes []string
}

// SessionStore persists sessions and owns atomic session lifecycle changes.
type SessionStore interface {
	Save(
		context.Context,
		*model.Session,
		[]*model.SessionCredential,
		int,
	) (*model.Session, []*model.SessionCredential, error)
	Get(context.Context, string) (*model.Session, error)
	ListByUser(context.Context, string) ([]*model.Session, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.Session, error)
	UpdateActivity(context.Context, string, int64, int64) error
	Revoke(context.Context, string, string, int64, string) ([]string, error)
	RevokeWithAudit(context.Context, *SessionRevocation) (*SessionRevocationResult, error)
	RevokeAllForUser(
		context.Context,
		string,
		int64,
		string,
	) ([]*model.Session, []string, error)
	RevokeAllForUserWithAudit(context.Context, *UserSessionsRevocation) (*UserSessionsRevocationResult, error)
}

type SessionRotation struct {
	Session             *model.Session
	AccessCredential    *model.SessionCredential
	RefreshCredential   *model.SessionCredential
	RevokedAccessHashes []string
	ReplayDetected      bool
}

// SessionCredentialStore resolves bearer credentials and atomically rotates
// refresh credentials with replay detection.
type SessionCredentialStore interface {
	GetSessionByTokenHash(
		context.Context,
		string,
		model.SessionCredentialKind,
	) (*model.SessionCredential, *model.Session, error)
	RotateRefresh(
		context.Context,
		string,
		*model.SessionCredential,
		*model.SessionCredential,
		int64,
		int64,
	) (*SessionRotation, error)
}

// RoleCreation is the durable input for creating a custom role under an
// already-persisted audit attempt.
type RoleCreation struct {
	Role         *model.Role
	AuditEventID string
	AuditAt      int64
}

// RoleUpdate is the durable input for updating a custom role under an
// already-persisted audit attempt.
type RoleUpdate struct {
	Role         *model.Role
	AuditEventID string
	AuditAt      int64
}

// RoleArchive is the durable input for archiving a custom role under an
// already-persisted audit attempt.
type RoleArchive struct {
	ID           string
	ArchiveAt    int64
	AuditEventID string
	AuditAt      int64
}

// RoleStore persists reusable permission sets independently of their scopes.
type RoleStore interface {
	Save(context.Context, *model.Role) (*model.Role, error)
	SaveWithAudit(context.Context, *RoleCreation) (*model.Role, error)
	Get(context.Context, string) (*model.Role, error)
	GetIncludingArchived(context.Context, string) (*model.Role, error)
	GetByName(context.Context, string) (*model.Role, error)
	GetByIds(context.Context, []string) ([]*model.Role, error)
	List(context.Context) ([]*model.Role, error)
	Update(context.Context, *model.Role) (*model.Role, error)
	UpdateWithAudit(context.Context, *RoleUpdate) (*model.Role, error)
	Archive(context.Context, string, int64) (*model.Role, error)
	ArchiveWithAudit(context.Context, *RoleArchive) (*model.Role, error)
}

// RoleBindingCreation is the durable input for creating a binding under an
// already-persisted audit attempt.
type RoleBindingCreation struct {
	Binding                   *model.RoleBinding
	ExpectedRoleUpdatedAt     time.Time
	ExpectedRolePermissions   []string
	ExpectedRecipientRevision int64
	Notice                    *PreparedMail
	AuditEventID              string
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

// RoleBindingEnd is the durable input for ending a binding under an
// already-persisted audit attempt.
type RoleBindingEnd struct {
	ID                        string
	EndAt                     int64
	ExpectedRecipientRevision int64
	Notice                    *PreparedMail
	Capabilities              AccessDeploymentCapabilities
	AuditEventID              string
	AuditAt                   int64
	Command                   *CommandIdempotency
	Replayed                  bool
	NoOp                      bool
}

// RoleBindingStore persists time-bounded role assignments. Scope references
// are validated transactionally because PostgreSQL cannot express a foreign
// key whose target table depends on scope_type.
type RoleBindingStore interface {
	Save(context.Context, *model.RoleBinding) (*model.RoleBinding, error)
	SaveWithAudit(context.Context, *RoleBindingCreation) (*model.RoleBinding, error)
	Get(context.Context, string) (*model.RoleBinding, error)
	ListByUser(context.Context, string) ([]*model.RoleBinding, error)
	ListVisibleByUser(context.Context, string, UserVisibilityScope) ([]*model.RoleBinding, error)
	ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.RoleBinding, error)
	End(context.Context, string, int64) (*model.RoleBinding, error)
	EndWithAudit(context.Context, *RoleBindingEnd) (*model.RoleBinding, error)
}

type AuditListOptions struct {
	ActorId    string
	Action     string
	Resource   *model.Resource
	BeforeTime int64
	BeforeId   string
	Limit      int
	Visibility AuditVisibilityScope
}

// AuditVisibilityScope constrains audit history in persistence. InstitutionWide
// explicitly retains operator visibility; its zero value denies all rows.
// AcademicInstitutionWide permits only events whose scope resolves to an
// Academic Unit or Class anywhere in the Institution and still requires the
// closed application-selected AllowedActions catalog. AcademicUnitRootIDs name
// current authorized subtrees. A subtree projection requires roots and actions.
type AuditVisibilityScope struct {
	InstitutionWide         bool
	AcademicInstitutionWide bool
	AcademicUnitRootIDs     []string
	AllowedActions          []string
}

// AuditStore owns authoritative security records. Events are append-oriented;
// Complete permits only the terminal transition from attempt to success/fail.
type AuditStore interface {
	Save(context.Context, *model.AuditEvent) (*model.AuditEvent, error)
	Get(context.Context, string) (*model.AuditEvent, error)
	Complete(context.Context, string, model.AuditStatus, string, []byte, int64) (*model.AuditEvent, error)
	List(context.Context, AuditListOptions) ([]*model.AuditEvent, error)
}

// InstallationBootstrap is the complete durable input for the one-time
// installation bootstrap transaction. PasswordHash is already encoded by the
// application password hasher and must never be logged or audited.
type InstallationBootstrap struct {
	BootstrapSecretDigest    [sha256.Size]byte
	CommandFingerprint       [sha256.Size]byte
	Institution              *model.Institution
	Administrator            *model.User
	AdministratorSettings    *model.UserSettingsDocument
	PasswordHash             string
	Role                     *model.Role
	RoleBinding              *model.RoleBinding
	AccessPolicy             *model.AccessPolicy
	AuditEvent               *model.AuditEvent
	DefaultProfilePictureJob *model.Job
}

// SystemAdministratorRoleReconciliation is the complete durable input for
// adding newly registered grantable actions to the protected built-in Role.
// Existing permissions are preserved so a rolling downgrade does not destroy
// actions unknown to the current binary.
type SystemAdministratorRoleReconciliation struct {
	RequiredPermissions []string
	ReconciledAt        int64
	AuditEvent          *model.AuditEvent
}

// SystemAdministratorRoleReconciliationResult reports the authoritative Role
// after reconciliation. A nil Role with Changed false means the installation
// is still pristine and therefore has nothing to reconcile.
type SystemAdministratorRoleReconciliationResult struct {
	Role             *model.Role
	Changed          bool
	AddedPermissions []string
}

// AdministratorRecovery is the secret-bearing input to the offline,
// installation-scoped recovery aggregate. RotatePasswordHash is already an
// encoded password hash; plaintext credentials never cross the Store boundary.
type AdministratorRecovery struct {
	InstitutionID      model.InstitutionID
	UserID             model.UserID
	EnableLocalLogin   bool
	RotatePasswordHash string
}

type AdministratorRecoveryResult struct {
	RecordID          string
	LocalLoginEnabled bool
	PasswordRotated   bool
}

// AdministratorRecoveryReconciliation is the bounded startup command that
// turns pending offline security records into ordinary durable audit events.
type AdministratorRecoveryReconciliation struct {
	NodeID string
}

type AdministratorRecoveryReconciliationResult struct {
	Reconciled int
}

// InstallationStore owns the cross-model transaction that makes a pristine
// database into an initialized logical Proctor installation.
type InstallationStore interface {
	Get(context.Context) (*model.InstallationState, error)
	Bootstrap(context.Context, *InstallationBootstrap) (*model.InstallationBootstrapResult, error)
	ReconcileSystemAdministratorRole(context.Context, *SystemAdministratorRoleReconciliation) (*SystemAdministratorRoleReconciliationResult, error)
	RecoverAdministratorAccess(context.Context, *AdministratorRecovery) (*AdministratorRecoveryResult, error)
	ReconcileAdministratorRecovery(context.Context, *AdministratorRecoveryReconciliation) (*AdministratorRecoveryReconciliationResult, error)
}
