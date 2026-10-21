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

// UserListOptions is a bounded persistence query. IncludeDisabled is effective
// only with an explicit InstitutionWide visibility scope.
type UserListOptions struct {
	ID                           model.UserID
	Query                        string
	AfterUsername                string
	AfterID                      model.UserID
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
	AcademicUnitRootIDs []model.AcademicUnitID
	// For UserStore directory queries, ClassMemberInstitutionWide,
	// ClassMemberAcademicUnitRootIDs, and ClassIDs are class.members.view roster
	// visibility. They expose current Class members only and never Academic Unit
	// members or Role-Binding holders.
	ClassMemberInstitutionWide     bool
	ClassMemberAcademicUnitRootIDs []model.AcademicUnitID
	ClassIDs                       []model.ClassID
	// ActiveAt is the UTC microsecond decision time shared with current role
	// authorization. Visibility anchors include their start and exclude their end.
	ActiveAt time.Time
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
	AuditEventID     model.AuditEventID
	AuditAt          int64
}

// UserDisabledStateChange is the complete durable input for changing whether
// a user may operate. Disabling also revokes every active session in the same
// transaction. AuditEventID identifies an already-persisted attempt that must
// be completed successfully before any state change may commit.
type UserDisabledStateChange struct {
	ID               model.UserID
	ExpectedRevision int64
	Disabled         bool
	Capabilities     AccessDeploymentCapabilities
	Occurrence       *model.MailOccurrence
	Delivery         *model.MailDelivery
	DeliveryJob      *model.Job
	ChangedAt        int64
	RevocationReason model.SessionRevocationReason
	AuditEventID     model.AuditEventID
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
	Get(context.Context, model.UserID) (*model.User, error)
	GetByUsername(context.Context, string) (*model.User, error)
	GetByEmail(context.Context, string) (*model.User, error)
	List(context.Context, UserListOptions) ([]*model.User, error)
	GetCurrentContext(context.Context, model.UserID, int) (*CurrentUserContext, error)
	MatchVisibility(context.Context, model.UserID, UserVisibilityScope) (UserVisibilityMatch, error)
	UpdateProfile(context.Context, *UserProfileUpdate) (*model.User, error)
	SetDisabled(context.Context, *UserDisabledStateChange) (*UserDisabledStateResult, error)
	UpdateLastLogin(context.Context, model.UserID, int64) error
}

type CurrentUserProductArea string

const (
	CurrentUserProductAreaAccount         CurrentUserProductArea = "account"
	CurrentUserProductAreaAdministration  CurrentUserProductArea = "administration"
	CurrentUserProductAreaExamManagement  CurrentUserProductArea = "exam_management"
	CurrentUserProductAreaSettings        CurrentUserProductArea = "settings"
	CurrentUserProductAreaStudentActivity CurrentUserProductArea = "student_activity"
)

type CurrentUserManagementScope struct {
	ScopeType   model.RoleScopeType
	ScopeID     string
	DisplayName string
}

type CurrentUserAttemptSelector struct {
	AttemptID model.ExamAttemptID
	SittingID model.ExamSittingID
	State     model.ExamAttemptState
}

// CurrentUserContext is a bounded, credential-free navigation projection.
// It is never an authorization receipt and deliberately omits Role names,
// permission arrays, provider data, and examination content.
type CurrentUserContext struct {
	UserID                  model.UserID
	Username                string
	DisplayName             string
	NoCurrentAffiliation    bool
	NoAssignedAccess        bool
	AvailableProductAreas   []CurrentUserProductArea
	ManagementScopes        []CurrentUserManagementScope
	ManagementScopesHasMore bool
	UnresolvedAttempt       *CurrentUserAttemptSelector
}
