// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type IssueStudentClassInvitationCommand struct {
	TargetEmail                                                               string
	ClassID                                                                   model.ClassID
	IntendedStartsAt, IntendedEndsAt                                          int64
	SuggestedUsername, SuggestedDisplayName                                   string
	SuggestedFirstName, SuggestedLastName, SuggestedLocale, SuggestedTimezone string
	IdempotencyKey                                                            string
	batchDuplicate                                                            bool
	batchCanonicalKey                                                         string
	onboardingImportID                                                        model.OnboardingImportID
	onboardingImportRowNumber                                                 int
}

type AcceptStudentClassInvitationCommand struct {
	Claim, Password, Username, DisplayName, FirstName, LastName, Locale, Timezone, Source string
	browserTransaction                                                                    *store.BrowserInvitationTransactionProof
}

type IssueTeacherAcademicUnitInvitationCommand struct {
	TargetEmail                                                               string
	AcademicUnitID                                                            model.AcademicUnitID
	RoleID                                                                    model.RoleID
	IntendedStartsAt, IntendedEndsAt                                          int64
	SuggestedUsername, SuggestedDisplayName                                   string
	SuggestedFirstName, SuggestedLastName, SuggestedLocale, SuggestedTimezone string
	IdempotencyKey                                                            string
	batchDuplicate                                                            bool
	batchCanonicalKey                                                         string
	onboardingImportID                                                        model.OnboardingImportID
	onboardingImportRowNumber                                                 int
}

type IssueAcademicUnitRoleInvitationCommand struct {
	TargetEmail                      string
	AcademicUnitID                   model.AcademicUnitID
	RoleID                           model.RoleID
	IntendedStartsAt, IntendedEndsAt int64
	IdempotencyKey                   string
	batchDuplicate                   bool
	batchCanonicalKey                string
	onboardingImportID               model.OnboardingImportID
	onboardingImportRowNumber        int
}

type IssueInstitutionRoleInvitationCommand struct {
	TargetEmail                      string
	InstitutionID                    model.InstitutionID
	RoleID                           model.RoleID
	IntendedStartsAt, IntendedEndsAt int64
	IdempotencyKey                   string
	batchDuplicate                   bool
	batchCanonicalKey                string
	onboardingImportID               model.OnboardingImportID
	onboardingImportRowNumber        int
}

// authorizedScopedRoleInvitationIssue is the scope-specific result of the
// caller's authorization, resource, and delegation checks. The shared issue
// path must not infer or repeat those checks.
type authorizedScopedRoleInvitationIssue struct {
	targetEmail                      string
	purpose                          model.InvitationPurpose
	resource                         model.Resource
	scopeType                        model.RoleScopeType
	scopeID, operation               string
	academicUnitID                   model.AcademicUnitID
	role                             *model.Role
	intendedStartsAt, intendedEndsAt int64
	idempotency                      *store.CommandIdempotency
	batchDuplicate                   bool
	batchCanonicalKey                string
	onboardingImportID               model.OnboardingImportID
	onboardingImportRowNumber        int
}

type AcceptTeacherAcademicUnitInvitationCommand struct {
	Claim, Password, Username, DisplayName, FirstName, LastName, Locale, Timezone, Source string
	browserTransaction                                                                    *store.BrowserInvitationTransactionProof
}

type AcceptAcademicUnitRoleInvitationCommand struct {
	Claim, Source string
}

type AcceptInstitutionRoleInvitationCommand struct {
	Claim, Source string
}

// InvitationView intentionally excludes the target mailbox, claim digest, and
// raw claim. Delivery is the only boundary allowed to observe the raw claim.
type InvitationView struct {
	ID               model.InvitationID
	Purpose          model.InvitationPurpose
	State            model.InvitationState
	ClassID          model.ClassID
	AcademicPeriodID model.AcademicPeriodID
	AcademicUnitID   model.AcademicUnitID
	RoleID           model.RoleID
	RoleActions      []string
	IntendedStartsAt time.Time
	IntendedEndsAt   model.OptionalTime
	ExpiresAt        time.Time
	Replayed         bool
	NoOp             bool
}

type InvitationAcceptanceView struct {
	Invitation         InvitationView
	User               *model.User
	Affiliation        *model.Affiliation
	ClassMember        *model.ClassMember
	AcademicUnitMember *model.AcademicUnitMember
	RoleBinding        *model.RoleBinding
	Replayed           bool
}

type InvitationDeliveryView struct {
	TemplateKey       model.MailTemplateKey
	State             model.MailDeliveryState
	MaskedRecipient   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Deadline          time.Time
	AcceptedAt        model.OptionalTime
	PublicFailureCode string
}

// InvitationAdministrationView is purpose-built for authorized operators. It
// includes the approved recipient and lifecycle fields while keeping claim
// material, rendered mail, provider identity, and transport internals private.
type InvitationAdministrationView struct {
	InvitationView
	TargetEmail    string
	InviterUserID  model.UserID
	AcceptedUserID model.UserID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Revision       int64
	Delivery       *InvitationDeliveryView
}

type InvitationAdministrationPage struct {
	Items []InvitationAdministrationView
	More  bool
}

type ListInvitationsQuery struct {
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

type ResendInvitationCommand struct {
	ID                        model.InvitationID
	ExpectedRevision          int64
	IdempotencyKey            string
	BatchScopeType            model.RoleScopeType
	BatchScopeID              string
	batchDuplicate            bool
	batchCanonicalKey         string
	batchCanonicalFingerprint [sha256.Size]byte
}

type RevokeInvitationCommand struct {
	ID                        model.InvitationID
	ExpectedRevision          int64
	IdempotencyKey            string
	BatchScopeType            model.RoleScopeType
	BatchScopeID              string
	batchDuplicate            bool
	batchCanonicalKey         string
	batchCanonicalFingerprint [sha256.Size]byte
}

type invitationLifecycleIdempotencySemantic struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type ReplaceInvitationCommand struct {
	ID                                                                        model.InvitationID
	Purpose, TargetEmail                                                      string
	ClassID                                                                   model.ClassID
	AcademicUnitID                                                            model.AcademicUnitID
	InstitutionID                                                             model.InstitutionID
	RoleID                                                                    model.RoleID
	ExpectedRevision, IntendedStartsAt, IntendedEndsAt                        int64
	SuggestedUsername, SuggestedDisplayName                                   string
	SuggestedFirstName, SuggestedLastName, SuggestedLocale, SuggestedTimezone string
}

const MaximumInvitationBatchItems = 200

type InvitationBatchOperation string

const (
	InvitationBatchStudentClassCreate        InvitationBatchOperation = "student_class.create"
	InvitationBatchTeacherAcademicUnitCreate InvitationBatchOperation = "teacher_academic_unit.create"
	InvitationBatchAcademicUnitRoleCreate    InvitationBatchOperation = "academic_unit_role.create"
	InvitationBatchInstitutionRoleCreate     InvitationBatchOperation = "institution_role.create"
	InvitationBatchResend                    InvitationBatchOperation = "resend"
	InvitationBatchRevoke                    InvitationBatchOperation = "revoke"
)

// IsValid reports whether o is a supported Invitation batch operation.
func (o InvitationBatchOperation) IsValid() bool {
	switch o {
	case InvitationBatchStudentClassCreate, InvitationBatchTeacherAcademicUnitCreate,
		InvitationBatchAcademicUnitRoleCreate, InvitationBatchInstitutionRoleCreate,
		InvitationBatchResend, InvitationBatchRevoke:
		return true
	default:
		return false
	}
}

// InvitationBatchItemCommand is a closed union interpreted only according to
// its batch's single operation. Non-applicable fields make that row invalid.
type InvitationBatchItemCommand struct {
	InvitationID                                                              model.InvitationID
	TargetEmail                                                               string
	RoleID                                                                    model.RoleID
	IdempotencyKey                                                            string
	ExpectedRevision, IntendedStartsAt, IntendedEndsAt                        int64
	SuggestedUsername, SuggestedDisplayName                                   string
	SuggestedFirstName, SuggestedLastName, SuggestedLocale, SuggestedTimezone string
}

type RunInvitationBatchCommand struct {
	Operation                 InvitationBatchOperation
	ScopeType                 model.RoleScopeType
	ScopeID                   string
	IdempotencyKey            string
	Items                     []InvitationBatchItemCommand
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type InvitationBatchItemStatus string

const (
	InvitationBatchItemSucceeded InvitationBatchItemStatus = "succeeded"
	InvitationBatchItemNoOp      InvitationBatchItemStatus = "no_op"
	InvitationBatchItemFailed    InvitationBatchItemStatus = "failed"
)

type InvitationBatchItemResult struct {
	Index        int
	Status       InvitationBatchItemStatus
	InvitationID model.InvitationID
	ErrorCode    string
}

type InvitationBatchResult struct {
	Operation InvitationBatchOperation
	Items     []InvitationBatchItemResult
	Succeeded int
	NoOp      int
	Failed    int
}

func (v InvitationView) String() string {
	return fmt.Sprintf("Invitation{%s %s %s %s}", v.ID, v.Purpose, v.State, v.ClassID)
}

type invitationClassReader interface {
	Get(context.Context, model.ClassID) (*model.Class, error)
}
type invitationPeriodReader interface {
	Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error)
}
type invitationAcademicUnitReader interface {
	Get(context.Context, model.AcademicUnitID) (*model.AcademicUnit, error)
}
type invitationRoleReader interface {
	Get(context.Context, model.RoleID) (*model.Role, error)
}
type invitationAuthorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
	AuthorizeActionDelegation(context.Context, invocation.Invocation, []string, model.RoleScopeType, string) error
	Visibility(context.Context, invocation.Invocation, model.Action) (store.InvitationVisibilityScope, error)
}
type invitationPasswordHasher interface {
	Hash(context.Context, string) (string, error)
}
type invitationAttemptLimiter interface {
	Check(context.Context, string, string) error
}
type invitationMailPreparer interface {
	Enabled() bool
	PrepareInvitation(*model.Invitation, string) (*store.PreparedMail, error)
	PrepareInvitationResend(*model.Invitation, string, model.UserID, time.Time) (*store.PreparedMail, error)
	PrepareInvitationRevocation(*model.Invitation, model.UserID, time.Time) (*store.PreparedMail, error)
	PrepareInvitationAccepted(mail.NoticePreparation) (*store.PreparedMail, error)
}

type invitationMembershipEffects interface {
	classMemberEffects
	authorizationInvalidationEffects
}

// invitationServiceDependencies are the collaborators of invitationService;
// newInvitationService validates them once and invitationService keeps them in deps.
type invitationServiceDependencies struct {
	Store                   store.InvitationStore
	Classes                 invitationClassReader
	Periods                 invitationPeriodReader
	AcademicUnits           invitationAcademicUnitReader
	Roles                   invitationRoleReader
	Authorization           invitationAuthorizer
	Mail                    invitationMailPreparer
	Hasher                  invitationPasswordHasher
	Audit                   mutationAuditor
	Attempts                invitationAttemptLimiter
	MembershipEffects       invitationMembershipEffects
	NodeID                  string
	PublicURL               string
	RecentAuthenticationTTL time.Duration
	NewClaim                func() string
	Now                     func() time.Time
}

type invitationService struct {
	deps invitationServiceDependencies
}

type invitationAuthorizationAdapter struct{ authorization *accessControlService }

func (a invitationAuthorizationAdapter) Authorize(ctx context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource) error {
	if a.authorization == nil {
		return apperror.New("invitation.unavailable")
	}
	return a.authorization.Authorize(ctx, invocation, action, resource)
}

func (a invitationAuthorizationAdapter) AuthorizeActionDelegation(ctx context.Context, invocation invocation.Invocation, actions []string, scopeType model.RoleScopeType, scopeID string) error {
	if a.authorization == nil {
		return apperror.New("invitation.unavailable")
	}
	allowed, err := a.authorization.canDelegateActionsAtScope(ctx, invocation.Principal(), actions, scopeType, scopeID)
	if err != nil {
		return err
	}
	if !allowed {
		return authorizationDeniedError()
	}
	return nil
}

func (a invitationAuthorizationAdapter) Visibility(ctx context.Context, invocation invocation.Invocation, action model.Action) (store.InvitationVisibilityScope, error) {
	if a.authorization == nil || (action != model.ActionInvitationView && action != model.ActionInvitationManage) {
		return store.InvitationVisibilityScope{}, apperror.New("invitation.unavailable")
	}
	constraint, err := a.authorization.authorizedScopes(ctx, invocation.Principal(), action, model.ResourceInstitution)
	if err != nil {
		return store.InvitationVisibilityScope{}, err
	}
	institution, err := a.authorization.deps.Resolver.deps.Institutions.GetSingleton(ctx)
	if err != nil {
		return store.InvitationVisibilityScope{}, invitationStoreErrors.Translate(err)
	}
	allowed := constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 || len(constraint.ClassIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if len(constraint.AcademicUnitRootIDs) > 0 {
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: constraint.AcademicUnitRootIDs[0].String()}
		scopeType, scopeID = model.RoleScopeAcademicUnit, resource.ID
	} else if len(constraint.ClassIDs) > 0 {
		resource = model.Resource{Type: model.ResourceClass, ID: constraint.ClassIDs[0].String()}
		scopeType, scopeID = model.RoleScopeClass, resource.ID
	}
	if err = a.authorization.deps.Audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource, scopeType, scopeID, invocation.RequestMetadata(), allowed); err != nil {
		return store.InvitationVisibilityScope{}, err
	}
	if !allowed {
		return store.InvitationVisibilityScope{}, authorizationDeniedError()
	}
	return store.InvitationVisibilityScope{InstitutionWide: constraint.InstitutionWide,
		AcademicUnitRootIDs: append([]model.AcademicUnitID(nil), constraint.AcademicUnitRootIDs...), ClassIDs: append([]model.ClassID(nil), constraint.ClassIDs...)}, nil
}

func newInvitationService(deps invitationServiceDependencies) (*invitationService, error) {
	if deps.Store == nil || deps.Classes == nil || deps.Periods == nil || deps.AcademicUnits == nil || deps.Roles == nil || deps.Authorization == nil || deps.Mail == nil || deps.Hasher == nil || deps.Audit == nil ||
		deps.Attempts == nil || deps.MembershipEffects == nil || deps.NodeID == "" || deps.PublicURL == "" || deps.RecentAuthenticationTTL <= 0 || deps.NewClaim == nil || deps.Now == nil {
		return nil, errors.New("invitation service dependencies are invalid")
	}
	return &invitationService{deps: deps}, nil
}

func invitationResource(invitation *model.Invitation) model.Resource {
	if invitation == nil {
		return model.Resource{}
	}
	typeByScope := map[model.RoleScopeType]model.ResourceType{model.RoleScopeInstitution: model.ResourceInstitution,
		model.RoleScopeAcademicUnit: model.ResourceAcademicUnit, model.RoleScopeClass: model.ResourceClass}
	return model.Resource{Type: typeByScope[invitation.ScopeType], ID: invitation.ScopeID}
}

func invitationAdministrationView(record *store.InvitationAdministrationRecord) InvitationAdministrationView {
	if record == nil || record.Invitation == nil {
		return InvitationAdministrationView{}
	}
	invitation := record.Invitation
	result := InvitationAdministrationView{InvitationView: invitationView(invitation), TargetEmail: invitation.TargetEmail,
		InviterUserID: invitation.InviterUserID, AcceptedUserID: invitation.AcceptedUserID,
		CreatedAt: invitation.CreatedAt, UpdatedAt: invitation.UpdatedAt, Revision: invitation.Revision}
	if record.Delivery != nil {
		delivery := record.Delivery
		result.Delivery = &InvitationDeliveryView{TemplateKey: delivery.TemplateKey, State: delivery.State,
			MaskedRecipient: delivery.MaskedRecipient, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt,
			Deadline: delivery.Deadline, AcceptedAt: delivery.AcceptedAt, PublicFailureCode: delivery.PublicFailureCode}
	}
	return result
}

func invitationView(invitation *model.Invitation) InvitationView {
	if invitation == nil {
		return InvitationView{}
	}
	return InvitationView{ID: invitation.ID, Purpose: invitation.Purpose, State: invitation.State, ClassID: invitation.ClassID,
		AcademicPeriodID: invitation.AcademicPeriodID, AcademicUnitID: invitation.AcademicUnitID, RoleID: invitation.RoleID,
		RoleActions: append([]string(nil), invitation.RoleActions...), IntendedStartsAt: invitation.IntendedStartsAt,
		IntendedEndsAt: invitation.IntendedEndsAt, ExpiresAt: invitation.ExpiresAt}
}

var invitationStoreErrors = apperror.StoreTable{
	NotFound:    "resource.not_found",
	Conflict:    "invitation.conflict",
	Invalid:     "invitation.invalid",
	Unavailable: "invitation.unavailable",
}

func invalidInvitationError(err error) error { return apperror.New("invitation.invalid").Wrap(err) }

type invitationAttemptAccounting struct {
	attempts *authenticationAttemptAccounting
	policy   LoginRateLimitPolicy
}

const invitationAcceptanceAttemptQualifier = "claim-accept"

func (a invitationAttemptAccounting) Check(ctx context.Context, identity, source string) error {
	if a.attempts == nil || a.policy.Window <= 0 || a.policy.MaximumAttempts <= 0 || a.policy.MaximumSourceAttempts <= 0 {
		return rateLimitUnavailableAppError(errors.New("invitation attempt accounting is unavailable"))
	}
	_, limited, err := a.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeInvitation, qualifier: invitationAcceptanceAttemptQualifier, window: a.policy.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentity, maximum: a.policy.MaximumAttempts, identity: identity},
			{dimension: authenticationAttemptDimensionSource, maximum: a.policy.MaximumSourceAttempts, source: source},
		},
	})
	if err != nil {
		return rateLimitUnavailableAppError(err)
	}
	if limited {
		return apperror.New("authentication.rate_limited")
	}
	return nil
}
