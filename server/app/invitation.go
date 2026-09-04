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
	"strings"
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type IssueStudentClassInvitationCommand struct {
	TargetEmail, ClassID                                                      string
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
	TargetEmail, AcademicUnitID, RoleID                                       string
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
	TargetEmail, AcademicUnitID, RoleID string
	IntendedStartsAt, IntendedEndsAt    int64
	IdempotencyKey                      string
	batchDuplicate                      bool
	batchCanonicalKey                   string
	onboardingImportID                  model.OnboardingImportID
	onboardingImportRowNumber           int
}

type IssueInstitutionRoleInvitationCommand struct {
	TargetEmail, InstitutionID, RoleID string
	IntendedStartsAt, IntendedEndsAt   int64
	IdempotencyKey                     string
	batchDuplicate                     bool
	batchCanonicalKey                  string
	onboardingImportID                 model.OnboardingImportID
	onboardingImportRowNumber          int
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
	ID                        string
	ExpectedRevision          int64
	IdempotencyKey            string
	BatchScopeType            model.RoleScopeType
	BatchScopeID              string
	batchDuplicate            bool
	batchCanonicalKey         string
	batchCanonicalFingerprint [sha256.Size]byte
}

type RevokeInvitationCommand struct {
	ID                        string
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
	ID, Purpose, TargetEmail, ClassID, AcademicUnitID, InstitutionID, RoleID  string
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

func (operation InvitationBatchOperation) IsValid() bool {
	switch operation {
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
	InvitationID, TargetEmail, RoleID                                         string
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
	Get(context.Context, string) (*model.Class, error)
}
type invitationPeriodReader interface {
	Get(context.Context, string) (*model.AcademicPeriod, error)
}
type invitationAcademicUnitReader interface {
	Get(context.Context, string) (*model.AcademicUnit, error)
}
type invitationRoleReader interface {
	Get(context.Context, string) (*model.Role, error)
}
type invitationAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
	CanDelegateActionsAtScope(context.Context, Invocation, []string, model.RoleScopeType, string) error
	Visibility(context.Context, Invocation, model.Action) (store.InvitationVisibilityScope, error)
}
type invitationPasswordHasher interface{ Hash(string) (string, error) }
type invitationAttemptLimiter interface {
	Check(context.Context, string, string) error
}
type invitationMailPreparer interface {
	Enabled() bool
	PrepareInvitation(*model.Invitation, string) (*preparedDirectMail, error)
	PrepareInvitationResend(*model.Invitation, string, model.UserID, time.Time) (*preparedDirectMail, error)
	PrepareInvitationRevocation(*model.Invitation, model.UserID, time.Time) (*preparedDirectMail, error)
	PrepareInvitationAccepted(appmail.NoticePreparation) (*preparedDirectMail, error)
}

type invitationMembershipEffects interface {
	classMemberEffects
	authorizationInvalidationEffects
}

type invitationService struct {
	store                   store.InvitationStore
	classes                 invitationClassReader
	periods                 invitationPeriodReader
	academicUnits           invitationAcademicUnitReader
	roles                   invitationRoleReader
	authorization           invitationAuthorizer
	mail                    invitationMailPreparer
	hasher                  invitationPasswordHasher
	audit                   mutationAuditor
	attempts                invitationAttemptLimiter
	membershipEffects       invitationMembershipEffects
	nodeID, publicURL       string
	newClaim                func() string
	now                     func() time.Time
	recentAuthenticationTTL time.Duration
}

type invitationAuthorizationAdapter struct{ authorization *accessControlService }

func (a invitationAuthorizationAdapter) Authorize(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource) error {
	if a.authorization == nil {
		return NewError("invitation.unavailable")
	}
	return a.authorization.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata())
}

func (a invitationAuthorizationAdapter) CanDelegateActionsAtScope(ctx context.Context, invocation Invocation, actions []string, scopeType model.RoleScopeType, scopeID string) error {
	if a.authorization == nil {
		return NewError("invitation.unavailable")
	}
	allowed, err := a.authorization.canDelegateActionsAtScope(ctx, invocation.Principal(), actions, scopeType, scopeID)
	if err != nil {
		return err
	}
	if !allowed {
		return authorizationDeniedError("invitationAuthorizationAdapter.CanDelegateActionsAtScope")
	}
	return nil
}

func (a invitationAuthorizationAdapter) Visibility(ctx context.Context, invocation Invocation, action model.Action) (store.InvitationVisibilityScope, error) {
	if a.authorization == nil || (action != model.ActionInvitationView && action != model.ActionInvitationManage) {
		return store.InvitationVisibilityScope{}, NewError("invitation.unavailable")
	}
	constraint, err := a.authorization.authorizedScopes(ctx, invocation.Principal(), action, model.ResourceInstitution)
	if err != nil {
		return store.InvitationVisibilityScope{}, err
	}
	institution, err := a.authorization.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return store.InvitationVisibilityScope{}, invitationError(err)
	}
	allowed := constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 || len(constraint.ClassIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if len(constraint.AcademicUnitRootIDs) > 0 {
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: constraint.AcademicUnitRootIDs[0]}
		scopeType, scopeID = model.RoleScopeAcademicUnit, resource.ID
	} else if len(constraint.ClassIDs) > 0 {
		resource = model.Resource{Type: model.ResourceClass, ID: constraint.ClassIDs[0]}
		scopeType, scopeID = model.RoleScopeClass, resource.ID
	}
	if err = a.authorization.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource, scopeType, scopeID, invocation.RequestMetadata(), allowed); err != nil {
		return store.InvitationVisibilityScope{}, err
	}
	if !allowed {
		return store.InvitationVisibilityScope{}, authorizationDeniedError("invitationAuthorizationAdapter.Visibility")
	}
	return store.InvitationVisibilityScope{InstitutionWide: constraint.InstitutionWide,
		AcademicUnitRootIDs: append([]string(nil), constraint.AcademicUnitRootIDs...), ClassIDs: append([]string(nil), constraint.ClassIDs...)}, nil
}

type invitationAuditAdapter struct{ audit mutationAuditAdapter }

func (a invitationAuditAdapter) Begin(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource, operation string, value, prior map[string]any) (string, error) {
	return a.audit.Begin(ctx, invocation, action, resource, operation, value, prior)
}
func (a invitationAuditAdapter) BeginAtScope(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (string, error) {
	return a.audit.BeginAtScope(ctx, invocation, action, resource, scopeType, scopeID, operation, value, prior)
}
func (a invitationAuditAdapter) Fail(ctx context.Context, auditID, errorCode string) error {
	return a.audit.Fail(ctx, auditID, errorCode)
}

func newInvitationService(persistence store.InvitationStore, classes invitationClassReader, periods invitationPeriodReader,
	academicUnits invitationAcademicUnitReader, roles invitationRoleReader,
	authorization invitationAuthorizer, mail invitationMailPreparer, hasher invitationPasswordHasher, audit mutationAuditor,
	attempts invitationAttemptLimiter, membershipEffects invitationMembershipEffects, nodeID, publicURL string,
	recentAuthenticationTTL time.Duration, newClaim func() string, now func() time.Time,
) (*invitationService, error) {
	if persistence == nil || classes == nil || periods == nil || academicUnits == nil || roles == nil || authorization == nil || mail == nil || hasher == nil || audit == nil ||
		attempts == nil || membershipEffects == nil || nodeID == "" || publicURL == "" || recentAuthenticationTTL <= 0 || newClaim == nil || now == nil {
		return nil, errors.New("invitation service dependencies are invalid")
	}
	return &invitationService{store: persistence, classes: classes, periods: periods, academicUnits: academicUnits, roles: roles, authorization: authorization,
		mail: mail, hasher: hasher, audit: audit, attempts: attempts, membershipEffects: membershipEffects,
		nodeID: nodeID, publicURL: publicURL,
		recentAuthenticationTTL: recentAuthenticationTTL, newClaim: newClaim, now: now}, nil
}

func (a *App) IssueTeacherAcademicUnitInvitation(ctx context.Context, invocation Invocation, command IssueTeacherAcademicUnitInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueTeacherAcademicUnit(ctx, invocation, command)
}

func (a *App) IssueAcademicUnitRoleInvitation(ctx context.Context, invocation Invocation, command IssueAcademicUnitRoleInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueAcademicUnitRole(ctx, invocation, command)
}

func (a *App) IssueInstitutionRoleInvitation(ctx context.Context, invocation Invocation, command IssueInstitutionRoleInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueInstitutionRole(ctx, invocation, command)
}

func (a *App) IssueStudentClassInvitation(ctx context.Context, invocation Invocation, command IssueStudentClassInvitationCommand) (InvitationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.IssueStudentClass(ctx, invocation, command)
}

func (a *App) AcceptStudentClassInvitation(ctx context.Context, invocation Invocation, command AcceptStudentClassInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptStudentClass(ctx, invocation, command)
}

func (a *App) AcceptTeacherAcademicUnitInvitation(ctx context.Context, invocation Invocation, command AcceptTeacherAcademicUnitInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptTeacherAcademicUnit(ctx, invocation, command)
}

func (a *App) AcceptAcademicUnitRoleInvitation(ctx context.Context, invocation Invocation, command AcceptAcademicUnitRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptAcademicUnitRole(ctx, invocation, command)
}

func (a *App) AcceptInstitutionRoleInvitation(ctx context.Context, invocation Invocation, command AcceptInstitutionRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	if a == nil || a.invitations == nil {
		return nil, NewError("invitation.unavailable")
	}
	return a.invitations.AcceptInstitutionRole(ctx, invocation, command)
}

func (a *App) ListInvitations(ctx context.Context, invocation Invocation, query ListInvitationsQuery) (InvitationAdministrationPage, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationPage{}, NewError("invitation.unavailable")
	}
	return a.invitations.List(ctx, invocation, query)
}

func (a *App) GetInvitation(ctx context.Context, invocation Invocation, id string) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.GetAdministration(ctx, invocation, id)
}

func (a *App) ResendInvitation(ctx context.Context, invocation Invocation, command ResendInvitationCommand) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.Resend(ctx, invocation, command)
}

func (a *App) RevokeInvitation(ctx context.Context, invocation Invocation, command RevokeInvitationCommand) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.Revoke(ctx, invocation, command)
}

func (a *App) ReplaceInvitation(ctx context.Context, invocation Invocation, command ReplaceInvitationCommand) (InvitationAdministrationView, error) {
	if a == nil || a.invitations == nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable")
	}
	return a.invitations.Replace(ctx, invocation, command)
}

func (a *App) RunInvitationBatch(ctx context.Context, invocation Invocation, command RunInvitationBatchCommand) (InvitationBatchResult, error) {
	if a == nil || a.invitations == nil {
		return InvitationBatchResult{}, NewError("invitation.unavailable")
	}
	return a.invitations.RunBatch(ctx, invocation, command)
}

func (s *invitationService) RunBatch(ctx context.Context, invocation Invocation, command RunInvitationBatchCommand) (InvitationBatchResult, error) {
	command.ScopeID = strings.TrimSpace(command.ScopeID)
	if !command.Operation.IsValid() || !model.IsValidId(command.ScopeID) || command.IdempotencyKey == "" ||
		len(command.Items) < 1 || len(command.Items) > MaximumInvitationBatchItems || !invitationBatchScopeMatchesOperation(command.Operation, command.ScopeType) {
		return InvitationBatchResult{}, NewError("request.invalid")
	}
	resource, err := s.invitationBatchAuthorizationResource(ctx, command.ScopeType, command.ScopeID)
	if err != nil {
		return InvitationBatchResult{}, err
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchManage, resource); err != nil {
		return InvitationBatchResult{}, err
	}
	if command.Operation == InvitationBatchAcademicUnitRoleCreate || command.Operation == InvitationBatchInstitutionRoleCreate {
		if err = requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
			return InvitationBatchResult{}, err
		}
	}

	result := InvitationBatchResult{Operation: command.Operation, Items: make([]InvitationBatchItemResult, 0, len(command.Items))}
	itemKeyCounts := make(map[string]int, len(command.Items))
	itemsByKey := make(map[string]InvitationBatchItemCommand, len(command.Items))
	for _, item := range command.Items {
		itemKeyCounts[item.IdempotencyKey]++
		itemsByKey[item.IdempotencyKey] = item
	}
	duplicateWinners := invitationBatchDuplicateWinners(command.Operation, command.Items, itemKeyCounts)
	for index, item := range command.Items {
		outcome := InvitationBatchItemResult{Index: index}
		if err = validateInvitationBatchItem(command.Operation, item); err != nil {
			outcome.Status, outcome.ErrorCode = InvitationBatchItemFailed, "request.invalid"
			result.append(outcome)
			continue
		}
		if itemKeyCounts[item.IdempotencyKey] != 1 {
			outcome.Status, outcome.ErrorCode = InvitationBatchItemFailed, "request.invalid"
			result.append(outcome)
			continue
		}
		duplicateKey := invitationBatchDuplicateKey(command.Operation, item)
		itemKey := invitationBatchItemIdempotencyKey(command.IdempotencyKey, item.IdempotencyKey)
		winner := duplicateWinners[duplicateKey]
		duplicate := duplicateKey != "" && winner != item.IdempotencyKey
		canonicalKey := invitationBatchItemIdempotencyKey(command.IdempotencyKey, winner)
		var canonicalFingerprint [sha256.Size]byte
		if duplicate && (command.Operation == InvitationBatchResend || command.Operation == InvitationBatchRevoke) {
			canonicalItem := itemsByKey[winner]
			canonical, canonicalErr := newCommandIdempotency(invocation, invitationBatchLifecycleOperation(command.Operation), canonicalKey,
				invitationLifecycleIdempotencySemantic{ID: strings.TrimSpace(canonicalItem.InvitationID), Revision: canonicalItem.ExpectedRevision})
			if canonicalErr != nil || canonical == nil {
				outcome.Status, outcome.ErrorCode = InvitationBatchItemFailed, "request.invalid"
				result.append(outcome)
				continue
			}
			canonicalFingerprint = canonical.Fingerprint
		}
		outcome.InvitationID, outcome.Status, err = s.runInvitationBatchItem(ctx, invocation, command, item, itemKey, duplicate, canonicalKey, canonicalFingerprint)
		if err != nil {
			outcome.Status, outcome.ErrorCode = InvitationBatchItemFailed, invitationBatchPublicErrorCode(err)
		}
		result.append(outcome)
	}
	return result, nil
}

func invitationBatchDuplicateWinners(operation InvitationBatchOperation, items []InvitationBatchItemCommand, itemKeyCounts map[string]int) map[string]string {
	winners := make(map[string]string, len(items))
	for _, item := range items {
		if validateInvitationBatchItem(operation, item) != nil || itemKeyCounts[item.IdempotencyKey] != 1 {
			continue
		}
		duplicateKey := invitationBatchDuplicateKey(operation, item)
		if duplicateKey == "" {
			continue
		}
		if winner, exists := winners[duplicateKey]; !exists || item.IdempotencyKey < winner {
			winners[duplicateKey] = item.IdempotencyKey
		}
	}
	return winners
}

func (result *InvitationBatchResult) append(item InvitationBatchItemResult) {
	result.Items = append(result.Items, item)
	switch item.Status {
	case InvitationBatchItemSucceeded:
		result.Succeeded++
	case InvitationBatchItemNoOp:
		result.NoOp++
	case InvitationBatchItemFailed:
		result.Failed++
	}
}

func invitationBatchScopeMatchesOperation(operation InvitationBatchOperation, scopeType model.RoleScopeType) bool {
	switch operation {
	case InvitationBatchStudentClassCreate:
		return scopeType == model.RoleScopeClass
	case InvitationBatchTeacherAcademicUnitCreate, InvitationBatchAcademicUnitRoleCreate:
		return scopeType == model.RoleScopeAcademicUnit
	case InvitationBatchInstitutionRoleCreate:
		return scopeType == model.RoleScopeInstitution
	case InvitationBatchResend, InvitationBatchRevoke:
		return scopeType == model.RoleScopeInstitution || scopeType == model.RoleScopeAcademicUnit || scopeType == model.RoleScopeClass
	default:
		return false
	}
}

func (s *invitationService) invitationBatchAuthorizationResource(ctx context.Context, scopeType model.RoleScopeType, scopeID string) (model.Resource, error) {
	switch scopeType {
	case model.RoleScopeInstitution:
		return model.Resource{Type: model.ResourceInstitution, ID: scopeID}, nil
	case model.RoleScopeAcademicUnit:
		return model.Resource{Type: model.ResourceAcademicUnit, ID: scopeID}, nil
	case model.RoleScopeClass:
		return model.Resource{Type: model.ResourceClass, ID: scopeID}, nil
	default:
		return model.Resource{}, NewError("request.invalid")
	}
}

func invitationBatchItemIdempotencyKey(batchKey, itemKey string) string {
	return fmt.Sprintf("%d:%s:%s", len(batchKey), batchKey, itemKey)
}

func bindOnboardingImportCommand(idempotency *store.CommandIdempotency, id model.OnboardingImportID, rowNumber int) {
	if idempotency == nil || !id.IsValid() || rowNumber < 1 {
		return
	}
	idempotency.OnboardingImportID = id
	idempotency.OnboardingImportRowNumber = rowNumber
}

func invitationBatchLifecycleOperation(operation InvitationBatchOperation) string {
	if operation == InvitationBatchResend {
		return "invitation.resend.v1"
	}
	if operation == InvitationBatchRevoke {
		return "invitation.revoke.v1"
	}
	return ""
}

func (s *invitationService) recordInvitationCommandDuplicate(ctx context.Context, idempotency *store.CommandIdempotency, attempt mutationAttempt, input store.InvitationBatchDuplicate) (*store.InvitationBatchCommandResult, error) {
	return runAuditedMutation(ctx, s.audit, attempt, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationBatchCommandResult, error) {
			input.AuditEventID, input.AuditAt = reference.ID, reference.MutationAtMillis
			return s.store.RecordBatchDuplicate(ctx, &input, idempotency)
		}, invitationMutationError)
}

func (s *invitationService) recordDuplicateInvitationView(ctx context.Context, idempotency *store.CommandIdempotency, attempt mutationAttempt, input store.InvitationBatchDuplicate, canonicalKey string) (InvitationView, error) {
	if idempotency == nil || canonicalKey == "" {
		return InvitationView{}, NewError("request.invalid")
	}
	input.ActorUserID = attempt.Invocation.Principal().UserID
	input.CanonicalOperation = idempotency.Operation
	input.CanonicalKeyDigest = sha256.Sum256([]byte(canonicalKey))
	result, err := s.recordInvitationCommandDuplicate(ctx, idempotency, attempt, input)
	if err != nil {
		return InvitationView{}, err
	}
	if result.Duplicate {
		return InvitationView{}, NewError("onboarding_batch.duplicate")
	}
	if !result.InvitationID.IsValid() {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	return InvitationView{ID: result.InvitationID, Replayed: true}, nil
}

func (s *invitationService) recoverInvitationIssueOutcome(ctx context.Context, idempotency *store.CommandIdempotency, attempt mutationAttempt) (InvitationView, bool, error) {
	if idempotency == nil {
		return InvitationView{}, false, nil
	}
	found, err := s.store.FindCommandOutcome(ctx, idempotency)
	if store.IsNotFound(err) {
		return InvitationView{}, false, nil
	}
	if err == nil && found.Invitation != nil {
		attempt.Value = found.Invitation.Auditable()
	}
	replayed, err := runAuditedMutation(ctx, s.audit, attempt, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			value, replayErr := s.store.ReplayIssue(ctx, idempotency, reference.ID, reference.MutationAtMillis)
			if replayErr == nil && value.Duplicate {
				return nil, NewError("onboarding_batch.duplicate")
			}
			return value, replayErr
		}, invitationMutationError)
	if err != nil {
		return InvitationView{}, true, err
	}
	view := invitationView(replayed.Invitation)
	view.Replayed = true
	return view, true, nil
}

func (s *invitationService) recoverInvitationAdministrationOutcome(ctx context.Context, idempotency *store.CommandIdempotency, attempt mutationAttempt) (InvitationAdministrationView, bool, error) {
	if idempotency == nil {
		return InvitationAdministrationView{}, false, nil
	}
	found, err := s.store.FindCommandOutcome(ctx, idempotency)
	if store.IsNotFound(err) {
		return InvitationAdministrationView{}, false, nil
	}
	if err == nil && found.Invitation != nil {
		attempt.Value = found.Invitation.Auditable()
	}
	replayed, err := runAuditedMutation(ctx, s.audit, attempt, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationCommandResult, error) {
			value, replayErr := s.store.ReplayAdministration(ctx, idempotency, reference.ID, reference.MutationAtMillis)
			if replayErr == nil && value.Duplicate {
				return nil, NewError("onboarding_batch.duplicate")
			}
			return value, replayErr
		}, invitationMutationError)
	if err != nil {
		return InvitationAdministrationView{}, true, err
	}
	view := invitationAdministrationView(replayed.Record)
	view.Replayed = true
	return view, true, nil
}

func validateInvitationBatchItem(operation InvitationBatchOperation, item InvitationBatchItemCommand) error {
	if !validInvitationBatchItemKey(item.IdempotencyKey) {
		return errors.New("invalid Invitation batch item key")
	}
	hasSuggestions := item.SuggestedUsername != "" || item.SuggestedDisplayName != "" || item.SuggestedFirstName != "" || item.SuggestedLastName != "" || item.SuggestedLocale != "" || item.SuggestedTimezone != ""
	switch operation {
	case InvitationBatchStudentClassCreate:
		if item.InvitationID != "" || item.RoleID != "" || item.ExpectedRevision != 0 || strings.TrimSpace(item.TargetEmail) == "" {
			return errors.New("invalid student Invitation batch item")
		}
	case InvitationBatchTeacherAcademicUnitCreate:
		if item.InvitationID != "" || item.ExpectedRevision != 0 || strings.TrimSpace(item.TargetEmail) == "" || strings.TrimSpace(item.RoleID) == "" {
			return errors.New("invalid teacher Invitation batch item")
		}
	case InvitationBatchAcademicUnitRoleCreate, InvitationBatchInstitutionRoleCreate:
		if item.InvitationID != "" || item.ExpectedRevision != 0 || strings.TrimSpace(item.TargetEmail) == "" || strings.TrimSpace(item.RoleID) == "" || hasSuggestions {
			return errors.New("invalid Role Invitation batch item")
		}
	case InvitationBatchResend, InvitationBatchRevoke:
		if strings.TrimSpace(item.InvitationID) == "" || item.ExpectedRevision < 1 || item.TargetEmail != "" || item.RoleID != "" ||
			item.IntendedStartsAt != 0 || item.IntendedEndsAt != 0 || hasSuggestions {
			return errors.New("invalid Invitation lifecycle batch item")
		}
	default:
		return errors.New("invalid Invitation batch operation")
	}
	return nil
}

func validInvitationBatchItemKey(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '.' || character == '_' || character == '~' {
			continue
		}
		return false
	}
	return true
}

func invitationBatchDuplicateKey(operation InvitationBatchOperation, item InvitationBatchItemCommand) string {
	mailbox := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(item.TargetEmail)))
	switch operation {
	case InvitationBatchStudentClassCreate:
		return mailbox
	case InvitationBatchTeacherAcademicUnitCreate, InvitationBatchAcademicUnitRoleCreate, InvitationBatchInstitutionRoleCreate:
		return mailbox + "\x00" + strings.TrimSpace(item.RoleID)
	case InvitationBatchResend, InvitationBatchRevoke:
		return strings.TrimSpace(item.InvitationID)
	default:
		return ""
	}
}

func (s *invitationService) runInvitationBatchItem(ctx context.Context, invocation Invocation, batch RunInvitationBatchCommand, item InvitationBatchItemCommand, itemKey string, duplicate bool, canonicalKey string, canonicalFingerprint [sha256.Size]byte) (model.InvitationID, InvitationBatchItemStatus, error) {
	switch batch.Operation {
	case InvitationBatchStudentClassCreate:
		view, err := s.IssueStudentClass(ctx, invocation, IssueStudentClassInvitationCommand{TargetEmail: item.TargetEmail, ClassID: batch.ScopeID,
			IntendedStartsAt: item.IntendedStartsAt, IntendedEndsAt: item.IntendedEndsAt, SuggestedUsername: item.SuggestedUsername,
			SuggestedDisplayName: item.SuggestedDisplayName, SuggestedFirstName: item.SuggestedFirstName,
			SuggestedLastName: item.SuggestedLastName, SuggestedLocale: item.SuggestedLocale, SuggestedTimezone: item.SuggestedTimezone, IdempotencyKey: itemKey,
			batchDuplicate: duplicate, batchCanonicalKey: canonicalKey, onboardingImportID: batch.onboardingImportID,
			onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchTeacherAcademicUnitCreate:
		view, err := s.IssueTeacherAcademicUnit(ctx, invocation, IssueTeacherAcademicUnitInvitationCommand{TargetEmail: item.TargetEmail,
			AcademicUnitID: batch.ScopeID, RoleID: item.RoleID, IntendedStartsAt: item.IntendedStartsAt, IntendedEndsAt: item.IntendedEndsAt,
			SuggestedUsername: item.SuggestedUsername, SuggestedDisplayName: item.SuggestedDisplayName,
			SuggestedFirstName: item.SuggestedFirstName, SuggestedLastName: item.SuggestedLastName,
			SuggestedLocale: item.SuggestedLocale, SuggestedTimezone: item.SuggestedTimezone, IdempotencyKey: itemKey, batchDuplicate: duplicate, batchCanonicalKey: canonicalKey,
			onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchAcademicUnitRoleCreate:
		view, err := s.IssueAcademicUnitRole(ctx, invocation, IssueAcademicUnitRoleInvitationCommand{TargetEmail: item.TargetEmail,
			AcademicUnitID: batch.ScopeID, RoleID: item.RoleID, IntendedStartsAt: item.IntendedStartsAt,
			IntendedEndsAt: item.IntendedEndsAt, IdempotencyKey: itemKey, batchDuplicate: duplicate, batchCanonicalKey: canonicalKey,
			onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchInstitutionRoleCreate:
		view, err := s.IssueInstitutionRole(ctx, invocation, IssueInstitutionRoleInvitationCommand{TargetEmail: item.TargetEmail,
			InstitutionID: batch.ScopeID, RoleID: item.RoleID, IntendedStartsAt: item.IntendedStartsAt,
			IntendedEndsAt: item.IntendedEndsAt, IdempotencyKey: itemKey, batchDuplicate: duplicate, batchCanonicalKey: canonicalKey,
			onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchResend:
		view, err := s.Resend(ctx, invocation, ResendInvitationCommand{ID: item.InvitationID, ExpectedRevision: item.ExpectedRevision,
			IdempotencyKey: itemKey, BatchScopeType: batch.ScopeType, BatchScopeID: batch.ScopeID,
			batchDuplicate: duplicate, batchCanonicalKey: canonicalKey, batchCanonicalFingerprint: canonicalFingerprint})
		return invitationBatchAdministrationOutcome(view, err)
	case InvitationBatchRevoke:
		view, err := s.Revoke(ctx, invocation, RevokeInvitationCommand{ID: item.InvitationID, ExpectedRevision: item.ExpectedRevision,
			IdempotencyKey: itemKey, BatchScopeType: batch.ScopeType, BatchScopeID: batch.ScopeID,
			batchDuplicate: duplicate, batchCanonicalKey: canonicalKey, batchCanonicalFingerprint: canonicalFingerprint})
		return invitationBatchAdministrationOutcome(view, err)
	default:
		return "", "", NewError("request.invalid")
	}
}

func invitationBatchViewOutcome(view InvitationView, err error) (model.InvitationID, InvitationBatchItemStatus, error) {
	if err != nil {
		return "", "", err
	}
	if view.NoOp || view.Replayed {
		return view.ID, InvitationBatchItemNoOp, nil
	}
	return view.ID, InvitationBatchItemSucceeded, nil
}

func invitationBatchAdministrationOutcome(view InvitationAdministrationView, err error) (model.InvitationID, InvitationBatchItemStatus, error) {
	return invitationBatchViewOutcome(view.InvitationView, err)
}

func invitationBatchPublicErrorCode(err error) string {
	if failure, ok := As(err); ok {
		switch failure.Code() {
		case "request.invalid", "resource.not_found", "authorization.denied", "authorization.request.invalid",
			"authorization.unavailable", "audit.unavailable", "administration.unavailable",
			"authentication.invalid_token", "authentication.strong_required", "authentication.reauthentication_required",
			"invitation.invalid", "invitation.class_period_invalid", "invitation.conflict",
			"invitation.role_not_delegable", "invitation.mail_unavailable", "invitation.unavailable",
			"student_progression.conflict", "student_progression.target_conflict",
			"idempotency.conflict", "idempotency.in_progress", "onboarding_batch.duplicate":
			return failure.Code()
		}
	}
	return "invitation.unavailable"
}

func invitationPurposeRequiresInteractiveBatch(purpose model.InvitationPurpose) bool {
	return purpose == model.InvitationPurposeAcademicUnitRole || purpose == model.InvitationPurposeInstitutionRole
}

func (s *invitationService) List(ctx context.Context, invocation Invocation, query ListInvitationsQuery) (InvitationAdministrationPage, error) {
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationView)
	if err != nil {
		return InvitationAdministrationPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	targetEmail := strings.ToLower(strings.TrimSpace(query.TargetEmail))
	if query.Limit < 1 || query.Limit > 200 || (query.Purpose != "" && !query.Purpose.IsValid()) ||
		(query.State != "" && !query.State.IsValid()) || (targetEmail != "" && !model.IsValidEmail(targetEmail)) ||
		(query.TargetID != "" && !model.IsValidId(query.TargetID)) ||
		(query.BeforeID.IsValid() != !query.BeforeCreatedAt.IsZero()) {
		return InvitationAdministrationPage{}, NewError("invitation.query.invalid")
	}
	page, err := s.store.List(ctx, store.InvitationListOptions{Visibility: visibility, Purpose: query.Purpose, State: query.State,
		TargetEmail: targetEmail, TargetID: strings.TrimSpace(query.TargetID), CreatedAfter: query.CreatedAfter,
		CreatedBefore: query.CreatedBefore, BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID, Limit: query.Limit})
	if err != nil {
		return InvitationAdministrationPage{}, invitationError(err)
	}
	result := InvitationAdministrationPage{Items: []InvitationAdministrationView{}, More: page.More}
	for _, item := range page.Items {
		result.Items = append(result.Items, invitationAdministrationView(item))
	}
	return result, nil
}

func (s *invitationService) GetAdministration(ctx context.Context, invocation Invocation, rawID string) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(rawID))
	if err != nil {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationView)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	record, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	return invitationAdministrationView(record), nil
}

func (s *invitationService) Resend(ctx context.Context, invocation Invocation, command ResendInvitationCommand) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(command.ID))
	if err != nil || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	if command.BatchScopeID != "" {
		if current.Invitation == nil || current.Invitation.ScopeType != command.BatchScopeType || current.Invitation.ScopeID != command.BatchScopeID {
			return InvitationAdministrationView{}, NewError("resource.not_found")
		}
		if invitationPurposeRequiresInteractiveBatch(current.Invitation.Purpose) {
			if err = requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
				return InvitationAdministrationView{}, err
			}
		}
	}
	idempotency, err := newCommandIdempotency(invocation, "invitation.resend.v1", command.IdempotencyKey,
		invitationLifecycleIdempotencySemantic{ID: id.String(), Revision: command.ExpectedRevision})
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "resend", Value: current.Invitation.Auditable()}
	if recovered, ok, recoverErr := s.recoverInvitationAdministrationOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	if command.batchDuplicate {
		if !s.mail.Enabled() {
			return InvitationAdministrationView{}, NewError("invitation.mail_unavailable")
		}
		view, duplicateErr := s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{LifecycleID: id, ExpectedRevision: command.ExpectedRevision,
				CanonicalFingerprint: command.batchCanonicalFingerprint}, command.batchCanonicalKey)
		return InvitationAdministrationView{InvitationView: view}, duplicateErr
	}
	result, err := runAuditedMutation(ctx, s.audit, attempt, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationCommandResult, error) {
			if idempotency != nil {
				replayed, replayErr := s.store.ReplayAdministration(ctx, idempotency, reference.ID, reference.MutationAtMillis)
				if replayErr == nil {
					if replayed.Duplicate {
						return nil, NewError("onboarding_batch.duplicate")
					}
					return replayed, nil
				}
				if !store.IsNotFound(replayErr) {
					return nil, replayErr
				}
			}
			if !s.mail.Enabled() {
				return nil, NewError("invitation.mail_unavailable")
			}
			at := model.TimeFromMillis(reference.MutationAtMillis)
			rawClaim := s.newClaim()
			if !model.IsValidCredentialToken(rawClaim) {
				return nil, NewError("invitation.unavailable")
			}
			actionURL, linkErr := accountCredentialLink(s.publicURL, "/join", rawClaim)
			if linkErr != nil {
				return nil, NewError("invitation.unavailable").Wrap(linkErr)
			}
			prepared, prepareErr := s.mail.PrepareInvitationResend(current.Invitation, actionURL, invocation.Principal().UserID, at)
			if prepareErr != nil {
				return nil, NewError("invitation.mail_unavailable").Wrap(prepareErr)
			}
			input := &store.InvitationResend{ID: id, ExpectedRevision: command.ExpectedRevision,
				ClaimHash: model.HashInvitationClaim(rawClaim), Occurrence: prepared.Occurrence, Delivery: prepared.Delivery,
				DeliveryJob: prepared.Job, ActorUserID: invocation.Principal().UserID,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}
			if idempotency != nil {
				value, storeErr := s.store.ResendIdempotently(ctx, input, idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, NewError("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			record, storeErr := s.store.Resend(ctx, input)
			return &store.InvitationAdministrationCommandResult{Record: record}, storeErr
		}, invitationMutationError)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	view := invitationAdministrationView(result.Record)
	view.Replayed = result.Replayed
	return view, nil
}

func (s *invitationService) Revoke(ctx context.Context, invocation Invocation, command RevokeInvitationCommand) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(command.ID))
	if err != nil || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	if command.BatchScopeID != "" {
		if current.Invitation == nil || current.Invitation.ScopeType != command.BatchScopeType || current.Invitation.ScopeID != command.BatchScopeID {
			return InvitationAdministrationView{}, NewError("resource.not_found")
		}
		if invitationPurposeRequiresInteractiveBatch(current.Invitation.Purpose) {
			if err = requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
				return InvitationAdministrationView{}, err
			}
		}
	}
	idempotency, err := newCommandIdempotency(invocation, "invitation.revoke.v1", command.IdempotencyKey,
		invitationLifecycleIdempotencySemantic{ID: id.String(), Revision: command.ExpectedRevision})
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "revoke", Value: current.Invitation.Auditable()}
	if recovered, ok, recoverErr := s.recoverInvitationAdministrationOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	if command.batchDuplicate {
		view, duplicateErr := s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{LifecycleID: id, ExpectedRevision: command.ExpectedRevision,
				CanonicalFingerprint: command.batchCanonicalFingerprint}, command.batchCanonicalKey)
		return InvitationAdministrationView{InvitationView: view}, duplicateErr
	}
	result, err := runAuditedMutation(ctx, s.audit, attempt, s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationCommandResult, error) {
			if idempotency != nil {
				replayed, replayErr := s.store.ReplayAdministration(ctx, idempotency, reference.ID, reference.MutationAtMillis)
				if replayErr == nil {
					if replayed.Duplicate {
						return nil, NewError("onboarding_batch.duplicate")
					}
					return replayed, nil
				}
				if !store.IsNotFound(replayErr) {
					return nil, replayErr
				}
			}
			at := model.TimeFromMillis(reference.MutationAtMillis)
			prepared, prepareErr := s.mail.PrepareInvitationRevocation(current.Invitation, invocation.Principal().UserID, at)
			if prepareErr != nil {
				return nil, NewError("invitation.mail_unavailable").Wrap(prepareErr)
			}
			input := &store.InvitationRevocation{ID: id, ExpectedRevision: command.ExpectedRevision,
				ActorUserID: invocation.Principal().UserID, RevocationNotice: &store.PreparedMail{Occurrence: prepared.Occurrence,
					Delivery: prepared.Delivery, Job: prepared.Job}, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}
			if idempotency != nil {
				value, storeErr := s.store.RevokeIdempotently(ctx, input, idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, NewError("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			record, storeErr := s.store.Revoke(ctx, input)
			return &store.InvitationAdministrationCommandResult{Record: record}, storeErr
		}, invitationMutationError)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	view := invitationAdministrationView(result.Record)
	view.Replayed = result.Replayed
	return view, nil
}

func (s *invitationService) Replace(ctx context.Context, invocation Invocation, command ReplaceInvitationCommand) (InvitationAdministrationView, error) {
	id, err := model.ParseInvitationID(strings.TrimSpace(command.ID))
	if err != nil || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, NewError("request.invalid").WithField("field", "invitation_id").Wrap(err)
	}
	visibility, err := s.authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationError(err)
	}
	if !s.mail.Enabled() {
		return InvitationAdministrationView{}, NewError("invitation.mail_unavailable")
	}
	replacement, rawClaim, err := s.prepareReplacement(ctx, invocation, command)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationAdministrationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(replacement, actionURL)
	if err != nil {
		return InvitationAdministrationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	currentAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "supersede", Value: current.Invitation.Auditable()}
	replacementAttempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate,
		Resource: invitationResource(replacement), ScopeType: replacement.ScopeType, ScopeID: replacement.ScopeID,
		Operation: "replacement_issue", Value: replacement.Auditable()}
	result, err := runAuditedMutation(ctx, s.audit, currentAttempt, s.now,
		func(ctx context.Context, currentReference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
			return runAuditedMutation(ctx, s.audit, replacementAttempt,
				func() time.Time { return model.TimeFromMillis(currentReference.MutationAtMillis) },
				func(ctx context.Context, replacementReference mutationAttemptReference) (*store.InvitationAdministrationRecord, error) {
					return s.store.Replace(ctx, &store.InvitationReplacement{CurrentID: id, ExpectedCurrentRevision: command.ExpectedRevision,
						Replacement: replacement, Lifetime: model.InvitationLifetime, Occurrence: prepared.Occurrence,
						Delivery: prepared.Delivery, DeliveryJob: prepared.Job, ActorUserID: invocation.Principal().UserID,
						CurrentAuditEventID: currentReference.ID, ReplacementAuditEventID: replacementReference.ID,
						AuditAt: currentReference.MutationAtMillis})
				}, invitationMutationError)
		}, invitationMutationError)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	return invitationAdministrationView(result), nil
}

func (s *invitationService) prepareReplacement(ctx context.Context, invocation Invocation, command ReplaceInvitationCommand) (*model.Invitation, string, error) {
	purpose := model.InvitationPurpose(strings.TrimSpace(command.Purpose))
	if !purpose.IsValid() {
		return nil, "", NewError("request.invalid").WithField("field", "purpose")
	}
	at := model.TimeUTC(s.now())
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return nil, "", NewError("invitation.unavailable")
	}
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	endsAt := model.OptionalTimeFromMillis(command.IntendedEndsAt)
	actor := invocation.Principal().UserID
	switch purpose {
	case model.InvitationPurposeStudentClass:
		classID, err := model.ParseClassID(strings.TrimSpace(command.ClassID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "class_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceClass, ID: classID.String()}
		for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage} {
			if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		class, err := s.classes.Get(ctx, classID.String())
		if err != nil || class.ID != classID {
			return nil, "", invitationError(err)
		}
		period, err := s.periods.Get(ctx, class.AcademicPeriodID.String())
		if err != nil || period.ID != class.AcademicPeriodID {
			return nil, "", invitationError(err)
		}
		if startsAt.IsZero() {
			startsAt = period.StartsAt
		}
		value, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(),
			TargetEmail: command.TargetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
			IntendedStartsAt: startsAt, IntendedEndsAt: endsAt,
			Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
				FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
			InviterUserID: actor, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
			ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if err != nil {
			return nil, "", domainInvalid("invitation.invalid", err)
		}
		return value, rawClaim, nil
	case model.InvitationPurposeTeacherAcademicUnit, model.InvitationPurposeAcademicUnitRole:
		unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
		}
		roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "role_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
		required := []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}
		if purpose == model.InvitationPurposeAcademicUnitRole {
			required[1] = model.ActionRoleBindingManage
		}
		for _, action := range required {
			if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		unit, err := s.academicUnits.Get(ctx, unitID.String())
		if err != nil || unit.ID != unitID || unit.IsArchived() {
			return nil, "", invitationError(err)
		}
		role, err := s.roles.Get(ctx, roleID.String())
		if err != nil || role.ID != roleID || role.IsArchived() {
			return nil, "", invitationError(err)
		}
		if err = validateInvitationDelegableRole(role, model.RoleScopeAcademicUnit); err != nil {
			return nil, "", err
		}
		if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
			return nil, "", err
		}
		if startsAt.IsZero() {
			startsAt = at
		}
		if purpose == model.InvitationPurposeTeacherAcademicUnit {
			value, modelErr := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{ID: model.NewInvitationID(),
				TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
				IntendedStartsAt: startsAt, IntendedEndsAt: endsAt,
				Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
					FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
				InviterUserID: actor, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
				ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
			if modelErr != nil {
				return nil, "", domainInvalid("invitation.invalid", modelErr)
			}
			return value, rawClaim, nil
		}
		value, modelErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose,
			TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
			IntendedStartsAt: startsAt, IntendedEndsAt: endsAt, InviterUserID: actor,
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if modelErr != nil {
			return nil, "", domainInvalid("invitation.invalid", modelErr)
		}
		return value, rawClaim, nil
	case model.InvitationPurposeInstitutionRole:
		if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
			return nil, "", err
		}
		institutionID, err := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "institution_id").Wrap(err)
		}
		roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
		if err != nil {
			return nil, "", NewError("request.invalid").WithField("field", "role_id").Wrap(err)
		}
		resource := model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}
		for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
			if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return nil, "", err
			}
		}
		role, err := s.roles.Get(ctx, roleID.String())
		if err != nil || role.ID != roleID || role.IsArchived() {
			return nil, "", invitationError(err)
		}
		if err = validateInvitationDelegableRole(role, model.RoleScopeInstitution); err != nil {
			return nil, "", err
		}
		if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeInstitution, institutionID.String()); err != nil {
			return nil, "", err
		}
		if startsAt.IsZero() {
			startsAt = at
		}
		value, modelErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose,
			TargetEmail: command.TargetEmail, RoleID: role.ID, RoleActions: role.Permissions, IntendedStartsAt: startsAt,
			IntendedEndsAt: endsAt, InviterUserID: actor, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(),
			ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: at})
		if modelErr != nil {
			return nil, "", domainInvalid("invitation.invalid", modelErr)
		}
		return value, rawClaim, nil
	default:
		return nil, "", NewError("request.invalid").WithField("field", "purpose")
	}
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

func (s *invitationService) IssueStudentClass(ctx context.Context, invocation Invocation, command IssueStudentClassInvitationCommand) (InvitationView, error) {
	classID := strings.TrimSpace(command.ClassID)
	parsedClassID, err := model.ParseClassID(classID)
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "class_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceClass, ID: classID}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionInvitationCreate, resource); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionClassMembersManage, resource); err != nil {
		return InvitationView{}, err
	}
	class, err := s.classes.Get(ctx, classID)
	if err != nil {
		return InvitationView{}, invitationError(err)
	}
	period, err := s.periods.Get(ctx, class.AcademicPeriodID.String())
	if err != nil {
		return InvitationView{}, invitationError(err)
	}
	if class.ID != parsedClassID || period.ID != class.AcademicPeriodID {
		return InvitationView{}, NewError("invitation.class_period_invalid")
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := newCommandIdempotency(invocation, "invitation.student_class.issue.v1", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate, Resource: resource,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), Operation: "issue_student_class",
		Value: map[string]any{"purpose": model.InvitationPurposeStudentClass, "class_id": class.ID.String(), "academic_period_id": period.ID.String()}}
	if recovered, ok, recoverErr := s.recoverInvitationIssueOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	issuedAt := model.TimeUTC(s.now())
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	if startsAt.IsZero() {
		startsAt = period.StartsAt
	}
	if command.onboardingImportID.IsValid() && !command.batchDuplicate {
		candidate, candidateErr := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
			ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
			IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
			Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
				FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
			InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		_, noOp, noOpErr := s.store.ResolveOnboardingInvitationNoOp(ctx, candidate)
		if noOpErr != nil {
			return InvitationView{}, invitationMutationError(noOpErr)
		}
		if noOp {
			return s.completeOnboardingInvitationNoOp(ctx, invocation, attempt, candidate, idempotency)
		}
	}
	if !s.mail.Enabled() {
		return InvitationView{}, NewError("invitation.mail_unavailable")
	}
	if command.batchDuplicate {
		candidate, candidateErr := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
			ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
			IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
			Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
				FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
			InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		attempt.Value = candidate.Auditable()
		return s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{Candidate: candidate}, command.batchCanonicalKey)
	}
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
		IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
		Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
			FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
		InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(),
		ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: issuedAt,
	})
	if err != nil {
		return InvitationView{}, domainInvalid("invitation.invalid", err)
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	attempt.Value = invitation.Auditable()
	created, err := runAuditedMutation(ctx, s.audit, attempt,
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			input := &store.StudentClassInvitationIssue{Invitation: invitation, Occurrence: prepared.Occurrence,
				Delivery: prepared.Delivery, DeliveryJob: prepared.Job, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}
			if idempotency != nil {
				value, storeErr := s.store.IssueStudentClassIdempotently(ctx, input, idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, NewError("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			value, storeErr := s.store.IssueStudentClass(ctx, input)
			return &store.InvitationCommandResult{Invitation: value}, storeErr
		}, invitationMutationError)
	if err != nil {
		return InvitationView{}, err
	}
	view := invitationView(created.Invitation)
	view.Replayed = created.Replayed
	view.NoOp = created.NoOp
	return view, nil
}

func (s *invitationService) IssueTeacherAcademicUnit(ctx context.Context, invocation Invocation, command IssueTeacherAcademicUnitInvitationCommand) (InvitationView, error) {
	unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionInvitationCreate, resource); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionAcademicUnitMembersManage, resource); err != nil {
		return InvitationView{}, err
	}
	unit, err := s.academicUnits.Get(ctx, unitID.String())
	if err != nil || unit.ID != unitID || unit.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	role, err := s.roles.Get(ctx, roleID.String())
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	if err = validateInvitationDelegableRole(role, model.RoleScopeAcademicUnit); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
		return InvitationView{}, err
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := newCommandIdempotency(invocation, "invitation.teacher_academic_unit.issue.v1", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate, Resource: resource,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), Operation: "issue_teacher_academic_unit",
		Value: map[string]any{"purpose": model.InvitationPurposeTeacherAcademicUnit, "academic_unit_id": unit.ID.String(), "role_id": role.ID.String()}}
	if recovered, ok, recoverErr := s.recoverInvitationIssueOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	issuedAt := model.TimeUTC(s.now())
	startsAt := model.TimeFromMillis(command.IntendedStartsAt)
	if startsAt.IsZero() {
		startsAt = issuedAt
	}
	if command.onboardingImportID.IsValid() && !command.batchDuplicate {
		candidate, candidateErr := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
			ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID,
			RoleActions: role.Permissions, IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
			Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
				FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
			InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		_, noOp, noOpErr := s.store.ResolveOnboardingInvitationNoOp(ctx, candidate)
		if noOpErr != nil {
			return InvitationView{}, invitationMutationError(noOpErr)
		}
		if noOp {
			return s.completeOnboardingInvitationNoOp(ctx, invocation, attempt, candidate, idempotency)
		}
	}
	if !s.mail.Enabled() {
		return InvitationView{}, NewError("invitation.mail_unavailable")
	}
	if command.batchDuplicate {
		candidate, candidateErr := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
			ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID,
			RoleActions: role.Permissions, IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
			Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
				FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
			InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		attempt.Value = candidate.Auditable()
		return s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{Candidate: candidate}, command.batchCanonicalKey)
	}
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	invitation, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: command.TargetEmail, AcademicUnitID: unit.ID, RoleID: role.ID,
		RoleActions: role.Permissions, IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(command.IntendedEndsAt),
		Suggestions: model.InvitationProfileSuggestions{Username: command.SuggestedUsername, DisplayName: command.SuggestedDisplayName,
			FirstName: command.SuggestedFirstName, LastName: command.SuggestedLastName, Locale: command.SuggestedLocale, Timezone: command.SuggestedTimezone},
		InviterUserID: invocation.Principal().UserID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(),
		ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: issuedAt,
	})
	if err != nil {
		return InvitationView{}, domainInvalid("invitation.invalid", err)
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	attempt.Value = invitation.Auditable()
	created, err := runAuditedMutation(ctx, s.audit, attempt,
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			input := &store.TeacherAcademicUnitInvitationIssue{Invitation: invitation,
				Lifetime:   model.InvitationLifetime,
				Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}
			if idempotency != nil {
				value, storeErr := s.store.IssueTeacherAcademicUnitIdempotently(ctx, input, idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, NewError("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			value, storeErr := s.store.IssueTeacherAcademicUnit(ctx, input)
			return &store.InvitationCommandResult{Invitation: value}, storeErr
		}, invitationMutationError)
	if err != nil {
		return InvitationView{}, err
	}
	view := invitationView(created.Invitation)
	view.Replayed = created.Replayed
	view.NoOp = created.NoOp
	return view, nil
}

func (s *invitationService) IssueAcademicUnitRole(ctx context.Context, invocation Invocation, command IssueAcademicUnitRoleInvitationCommand) (InvitationView, error) {
	unitID, err := model.ParseAcademicUnitID(strings.TrimSpace(command.AcademicUnitID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "academic_unit_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unitID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return InvitationView{}, err
		}
	}
	unit, err := s.academicUnits.Get(ctx, unitID.String())
	if err != nil || unit.ID != unitID || unit.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	role, err := s.roles.Get(ctx, roleID.String())
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	if err = validateInvitationDelegableRole(role, model.RoleScopeAcademicUnit); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unitID.String()); err != nil {
		return InvitationView{}, err
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := newCommandIdempotency(invocation, "invitation.academic_unit_role.issue.v1", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	return s.issueAuthorizedScopedRole(ctx, invocation, authorizedScopedRoleInvitationIssue{
		targetEmail: command.TargetEmail, purpose: model.InvitationPurposeAcademicUnitRole,
		resource: resource, scopeType: model.RoleScopeAcademicUnit, scopeID: unit.ID.String(), operation: "issue_academic_unit_role",
		academicUnitID: unit.ID, role: role, intendedStartsAt: command.IntendedStartsAt, intendedEndsAt: command.IntendedEndsAt,
		idempotency: idempotency, batchDuplicate: command.batchDuplicate, batchCanonicalKey: command.batchCanonicalKey,
		onboardingImportID: command.onboardingImportID, onboardingImportRowNumber: command.onboardingImportRowNumber,
	})
}

func (s *invitationService) IssueInstitutionRole(ctx context.Context, invocation Invocation, command IssueInstitutionRoleInvitationCommand) (InvitationView, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
		return InvitationView{}, err
	}
	institutionID, err := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "institution_id").Wrap(err)
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return InvitationView{}, NewError("request.invalid").WithField("field", "role_id").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return InvitationView{}, err
		}
	}
	role, err := s.roles.Get(ctx, roleID.String())
	if err != nil || role.ID != roleID || role.IsArchived() {
		return InvitationView{}, invitationError(err)
	}
	if err = validateInvitationDelegableRole(role, model.RoleScopeInstitution); err != nil {
		return InvitationView{}, err
	}
	if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeInstitution, institutionID.String()); err != nil {
		return InvitationView{}, err
	}
	semantic := command
	semantic.IdempotencyKey = ""
	idempotency, err := newCommandIdempotency(invocation, "invitation.institution_role.issue.v1", command.IdempotencyKey, semantic)
	if err != nil {
		return InvitationView{}, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	return s.issueAuthorizedScopedRole(ctx, invocation, authorizedScopedRoleInvitationIssue{
		targetEmail: command.TargetEmail, purpose: model.InvitationPurposeInstitutionRole,
		resource: resource, scopeType: model.RoleScopeInstitution, scopeID: institutionID.String(), operation: "issue_institution_role",
		role: role, intendedStartsAt: command.IntendedStartsAt, intendedEndsAt: command.IntendedEndsAt,
		idempotency: idempotency, batchDuplicate: command.batchDuplicate, batchCanonicalKey: command.batchCanonicalKey,
		onboardingImportID: command.onboardingImportID, onboardingImportRowNumber: command.onboardingImportRowNumber,
	})
}

func (s *invitationService) issueAuthorizedScopedRole(ctx context.Context, invocation Invocation, issue authorizedScopedRoleInvitationIssue) (InvitationView, error) {
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationCreate, Resource: issue.resource,
		ScopeType: issue.scopeType, ScopeID: issue.scopeID, Operation: issue.operation,
		Value: map[string]any{"purpose": issue.purpose, "academic_unit_id": issue.academicUnitID.String(), "role_id": issue.role.ID.String()}}
	if recovered, ok, recoverErr := s.recoverInvitationIssueOutcome(ctx, issue.idempotency, attempt); ok {
		return recovered, recoverErr
	}
	issuedAt := model.TimeUTC(s.now())
	startsAt := model.TimeFromMillis(issue.intendedStartsAt)
	if startsAt.IsZero() {
		startsAt = issuedAt
	}
	if issue.onboardingImportID.IsValid() && !issue.batchDuplicate {
		candidate, candidateErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
			ID: model.NewInvitationID(), Purpose: issue.purpose,
			TargetEmail: issue.targetEmail, AcademicUnitID: issue.academicUnitID, RoleID: issue.role.ID, RoleActions: issue.role.Permissions,
			IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(issue.intendedEndsAt),
			InviterUserID: invocation.Principal().UserID, ScopeType: issue.scopeType, ScopeID: issue.scopeID,
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		_, noOp, noOpErr := s.store.ResolveOnboardingInvitationNoOp(ctx, candidate)
		if noOpErr != nil {
			return InvitationView{}, invitationMutationError(noOpErr)
		}
		if noOp {
			return s.completeOnboardingInvitationNoOp(ctx, invocation, attempt, candidate, issue.idempotency)
		}
	}
	if !s.mail.Enabled() {
		return InvitationView{}, NewError("invitation.mail_unavailable")
	}
	if issue.batchDuplicate {
		candidate, candidateErr := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
			ID: model.NewInvitationID(), Purpose: issue.purpose,
			TargetEmail: issue.targetEmail, AcademicUnitID: issue.academicUnitID, RoleID: issue.role.ID, RoleActions: issue.role.Permissions,
			IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(issue.intendedEndsAt),
			InviterUserID: invocation.Principal().UserID, ScopeType: issue.scopeType, ScopeID: issue.scopeID,
			ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: issuedAt,
		})
		if candidateErr != nil {
			return InvitationView{}, domainInvalid("invitation.invalid", candidateErr)
		}
		attempt.Value = candidate.Auditable()
		return s.recordDuplicateInvitationView(ctx, issue.idempotency, attempt,
			store.InvitationBatchDuplicate{Candidate: candidate}, issue.batchCanonicalKey)
	}
	rawClaim := s.newClaim()
	if !model.IsValidCredentialToken(rawClaim) {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	invitation, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{
		ID: model.NewInvitationID(), Purpose: issue.purpose,
		TargetEmail: issue.targetEmail, AcademicUnitID: issue.academicUnitID, RoleID: issue.role.ID, RoleActions: issue.role.Permissions,
		IntendedStartsAt: startsAt, IntendedEndsAt: model.OptionalTimeFromMillis(issue.intendedEndsAt),
		InviterUserID: invocation.Principal().UserID, ScopeType: issue.scopeType, ScopeID: issue.scopeID,
		ClaimHash: model.HashInvitationClaim(rawClaim), IssuedAt: issuedAt,
	})
	if err != nil {
		return InvitationView{}, domainInvalid("invitation.invalid", err)
	}
	actionURL, err := accountCredentialLink(s.publicURL, "/join", rawClaim)
	if err != nil {
		return InvitationView{}, NewError("invitation.unavailable").Wrap(err)
	}
	prepared, err := s.mail.PrepareInvitation(invitation, actionURL)
	if err != nil {
		return InvitationView{}, NewError("invitation.mail_unavailable").Wrap(err)
	}
	attempt.Value = invitation.Auditable()
	created, err := runAuditedMutation(ctx, s.audit, attempt,
		func() time.Time { return issuedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			input := &store.ScopedRoleInvitationIssue{Invitation: invitation, Lifetime: model.InvitationLifetime,
				Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}
			if issue.idempotency != nil {
				value, storeErr := s.store.IssueScopedRoleIdempotently(ctx, input, issue.idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, NewError("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			value, storeErr := s.store.IssueScopedRole(ctx, input)
			return &store.InvitationCommandResult{Invitation: value}, storeErr
		}, invitationMutationError)
	if err != nil {
		return InvitationView{}, err
	}
	view := invitationView(created.Invitation)
	view.Replayed = created.Replayed
	view.NoOp = created.NoOp
	return view, nil
}

func (s *invitationService) completeOnboardingInvitationNoOp(ctx context.Context, invocation Invocation, attempt mutationAttempt,
	candidate *model.Invitation, idempotency *store.CommandIdempotency) (InvitationView, error) {
	if candidate == nil || idempotency == nil || !idempotency.OnboardingImportID.IsValid() {
		return InvitationView{}, NewError("invitation.unavailable")
	}
	attempt.Value = candidate.Auditable()
	created, err := runAuditedMutation(ctx, s.audit, attempt,
		func() time.Time { return candidate.CreatedAt }, func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			switch candidate.Purpose {
			case model.InvitationPurposeStudentClass:
				return s.store.IssueStudentClassIdempotently(ctx, &store.StudentClassInvitationIssue{Invitation: candidate,
					AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}, idempotency)
			case model.InvitationPurposeTeacherAcademicUnit:
				return s.store.IssueTeacherAcademicUnitIdempotently(ctx, &store.TeacherAcademicUnitInvitationIssue{Invitation: candidate,
					Lifetime: model.InvitationLifetime, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}, idempotency)
			case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
				return s.store.IssueScopedRoleIdempotently(ctx, &store.ScopedRoleInvitationIssue{Invitation: candidate,
					Lifetime: model.InvitationLifetime, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis}, idempotency)
			default:
				return nil, store.NewErrInvalidInput("invitation", "purpose", candidate.Purpose)
			}
		}, invitationMutationError)
	if err != nil {
		return InvitationView{}, err
	}
	view := invitationView(created.Invitation)
	view.Replayed = created.Replayed
	view.NoOp = created.NoOp
	return view, nil
}

func validateInvitationDelegableRole(role *model.Role, scopeType model.RoleScopeType) error {
	if role == nil || role.IsArchived() || role.BuiltIn || role.Name == model.SystemAdministratorRoleName || len(role.Permissions) == 0 {
		return NewError("invitation.role_not_delegable")
	}
	for _, action := range role.Permissions {
		definition, ok := model.DefinitionForAction(model.Action(action))
		if !ok || definition.RelationshipOnly || !definition.SupportsRoleScope(scopeType) {
			return NewError("invitation.role_not_delegable")
		}
	}
	return nil
}

func (s *invitationService) AcceptStudentClass(ctx context.Context, invocation Invocation, command AcceptStudentClassInvitationCommand) (*InvitationAcceptanceView, error) {
	if err := s.attempts.Check(ctx, model.HashInvitationClaim(command.Claim), command.Source); err != nil {
		return nil, err
	}
	if !model.IsValidCredentialToken(command.Claim) {
		return nil, NewError("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(command.Claim)
	return s.acceptStudentClassByClaimHash(ctx, invocation, command, claimHash)
}

func (s *invitationService) acceptStudentClassByClaimHash(ctx context.Context, invocation Invocation, command AcceptStudentClassInvitationCommand, claimHash string) (*InvitationAcceptanceView, error) {
	if !model.IsValidTokenHash(claimHash) {
		return nil, NewError("invitation.invalid")
	}
	invitation, err := s.store.GetByClaimHash(ctx, claimHash)
	if err != nil {
		return nil, invalidInvitationError(err)
	}
	at := model.TimeUTC(s.now())
	timezone := command.Timezone
	if timezone == "" {
		timezone = invitation.Suggestions.Timezone
	}
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
	}
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{Username: command.Username, Email: invitation.TargetEmail,
		EmailVerified: true, DisplayName: command.DisplayName, FirstName: command.FirstName, LastName: command.LastName,
		Locale: command.Locale, Timezone: timezone}, at)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: hash}
	credential.PrepareCreate(model.NewPasswordCredentialID(), at)
	settings, err := prepareInitialUserSettingsDocument(user)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	effectiveStart := invitation.EffectiveStartsAt(at)
	affiliation := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: effectiveStart}
	affiliation.PrepareCreate(model.NewAffiliationID(), at)
	member := &model.ClassMember{ClassID: invitation.ClassID, AcademicPeriodID: invitation.AcademicPeriodID,
		UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
	member.PrepareCreate(model.NewClassMemberID(), at)
	prepared, err := s.mail.PrepareInvitationAccepted(appmail.NoticePreparation{Recipient: user, At: at})
	if err != nil {
		return nil, NewError("invitation.mail_unavailable").Wrap(err)
	}
	metadata := invocation.RequestMetadata()
	event := &model.AuditEvent{ActorID: user.ID, Action: "invitation.accept",
		Resource: model.Resource{Type: model.ResourceClass, ID: invitation.ClassID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: invitation.ClassID.String(), Status: model.AuditStatusSuccess, RequestID: metadata.RequestID,
		NodeID: s.nodeID, ClientType: "web", AuthMethod: "invitation", IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent}
	result, err := s.store.AcceptStudentClass(ctx, &store.StudentClassInvitationAcceptance{ClaimHash: claimHash,
		AcceptedAt: model.MillisFromTime(at), User: user, Settings: settings, PasswordCredential: credential,
		DefaultProfilePictureJob: defaultJob, Affiliation: affiliation, ClassMember: member,
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job, AuditEvent: event,
		RequiredActions:    []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage},
		BrowserTransaction: command.browserTransaction})
	if err != nil {
		return nil, invalidInvitationError(err)
	}
	if !result.Replayed && result.ClassMember != nil {
		if effectErr := s.membershipEffects.MembershipChanged(ctx, result.ClassMember.UserID,
			[]model.ClassID{result.ClassMember.ClassID}); effectErr != nil {
			s.membershipEffects.Report(ctx, "invitation.student_class_membership_changed", effectErr)
		}
	}
	return &InvitationAcceptanceView{Invitation: invitationView(result.Invitation), User: result.User,
		Affiliation: result.Affiliation, ClassMember: result.ClassMember, Replayed: result.Replayed}, nil
}

func (s *invitationService) AcceptTeacherAcademicUnit(ctx context.Context, invocation Invocation, command AcceptTeacherAcademicUnitInvitationCommand) (*InvitationAcceptanceView, error) {
	if err := s.attempts.Check(ctx, model.HashInvitationClaim(command.Claim), command.Source); err != nil {
		return nil, err
	}
	if !model.IsValidCredentialToken(command.Claim) {
		return nil, NewError("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(command.Claim)
	return s.acceptTeacherAcademicUnitByClaimHash(ctx, invocation, command, claimHash)
}

func (s *invitationService) acceptTeacherAcademicUnitByClaimHash(ctx context.Context, invocation Invocation, command AcceptTeacherAcademicUnitInvitationCommand, claimHash string) (*InvitationAcceptanceView, error) {
	if !model.IsValidTokenHash(claimHash) {
		return nil, NewError("invitation.invalid")
	}
	invitation, err := s.store.GetByClaimHash(ctx, claimHash)
	if err != nil || invitation.Purpose != model.InvitationPurposeTeacherAcademicUnit {
		return nil, invalidInvitationError(err)
	}
	at := model.TimeUTC(s.now())
	timezone := command.Timezone
	if timezone == "" {
		timezone = invitation.Suggestions.Timezone
	}
	hash, err := s.hasher.Hash(command.Password)
	if err != nil {
		return nil, NewError("authentication.password.invalid").WithField("field", "password").Wrap(err)
	}
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{Username: command.Username, Email: invitation.TargetEmail,
		EmailVerified: true, DisplayName: command.DisplayName, FirstName: command.FirstName, LastName: command.LastName,
		Locale: command.Locale, Timezone: timezone}, at)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	credential := &model.PasswordCredential{UserID: user.ID, PasswordHash: hash}
	credential.PrepareCreate(model.NewPasswordCredentialID(), at)
	settings, err := prepareInitialUserSettingsDocument(user)
	if err != nil {
		return nil, NewError("invitation.user_invalid").Wrap(err)
	}
	effectiveStart := invitation.EffectiveStartsAt(at)
	affiliation := &model.Affiliation{UserID: user.ID, Kind: model.AffiliationTeacher, StartsAt: effectiveStart}
	affiliation.PrepareCreate(model.NewAffiliationID(), at)
	member := &model.AcademicUnitMember{AcademicUnitID: invitation.AcademicUnitID, UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
	member.PrepareCreate(model.NewAcademicUnitMemberID(), at)
	binding := &model.RoleBinding{UserID: user.ID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
		OriginAcademicUnitMemberID: member.ID,
		ScopeType:                  model.RoleScopeAcademicUnit, ScopeID: invitation.AcademicUnitID.String(), StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
	binding.PrepareCreate(model.NewRoleBindingID(), at)
	prepared, err := s.mail.PrepareInvitationAccepted(appmail.NoticePreparation{Recipient: user, At: at})
	if err != nil {
		return nil, NewError("invitation.mail_unavailable").Wrap(err)
	}
	metadata := invocation.RequestMetadata()
	event := &model.AuditEvent{ActorID: user.ID, Action: "invitation.accept",
		Resource: model.Resource{Type: model.ResourceAcademicUnit, ID: invitation.AcademicUnitID.String()}, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: invitation.AcademicUnitID.String(), Status: model.AuditStatusSuccess, RequestID: metadata.RequestID,
		NodeID: s.nodeID, ClientType: "web", AuthMethod: "invitation", IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent}
	result, err := s.store.AcceptTeacherAcademicUnit(ctx, &store.TeacherAcademicUnitInvitationAcceptance{ClaimHash: claimHash,
		AcceptedAt: model.MillisFromTime(at), User: user, Settings: settings, PasswordCredential: credential,
		DefaultProfilePictureJob: defaultJob, Affiliation: affiliation, AcademicUnitMember: member, RoleBinding: binding,
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job, AuditEvent: event,
		RequiredActions:    []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage},
		BrowserTransaction: command.browserTransaction})
	if err != nil {
		return nil, invalidInvitationError(err)
	}
	if !result.Replayed && result.User != nil {
		s.membershipEffects.InvalidateAuthorization(ctx, result.User.ID.String())
	}
	return &InvitationAcceptanceView{Invitation: invitationView(result.Invitation), User: result.User, Affiliation: result.Affiliation,
		AcademicUnitMember: result.AcademicUnitMember, RoleBinding: result.RoleBinding, Replayed: result.Replayed}, nil
}

func (s *invitationService) AcceptAcademicUnitRole(ctx context.Context, invocation Invocation, command AcceptAcademicUnitRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	return s.acceptScopedRole(ctx, invocation, command.Claim, command.Source, model.InvitationPurposeAcademicUnitRole)
}

func (s *invitationService) AcceptInstitutionRole(ctx context.Context, invocation Invocation, command AcceptInstitutionRoleInvitationCommand) (*InvitationAcceptanceView, error) {
	return s.acceptScopedRole(ctx, invocation, command.Claim, command.Source, model.InvitationPurposeInstitutionRole)
}

func (s *invitationService) AcceptExternalIdentity(ctx context.Context, state *model.ExternalLoginState,
	assertion *model.ExternalAuthenticationAssertion, capabilities store.AccessDeploymentCapabilities,
	metadata model.RequestMetadata, authenticationMethod string,
) (*store.ExternalIdentityInvitationAcceptanceResult, error) {
	if state == nil || state.Purpose != model.ExternalAuthenticationPurposeInvitationAdmission ||
		!state.ID.IsValid() || !state.InvitationID.IsValid() || !state.ConsumedAt.Valid || assertion == nil ||
		assertion.ProviderId != state.Provider || !assertion.EmailVerified {
		return nil, store.NewErrInvalidInput("invitation", "external_identity_acceptance", nil)
	}
	providerEmail := strings.ToLower(strings.TrimSpace(model.SanitizeUnicode(assertion.Email)))
	if !model.IsValidEmail(providerEmail) {
		return nil, store.NewErrInvalidInput("invitation", "provider_email", nil)
	}
	invitation, err := s.store.Get(ctx, state.InvitationID)
	if err != nil {
		return nil, err
	}
	at := model.TimeUTC(s.now())
	userCandidate := externalUserCandidate(assertion)
	userCandidate.Email, userCandidate.EmailVerified = invitation.TargetEmail, true
	user, defaultJob, preparationErr := prepareUserDefaultProfilePictureJob(userCandidate, at)
	var settings *model.UserSettingsDocument
	if preparationErr == nil {
		settings, preparationErr = prepareInitialUserSettingsDocument(user)
	}
	if preparationErr != nil {
		userCandidate.PrepareCreate(model.NewUserID(), at)
		user, settings, defaultJob = userCandidate, nil, nil
	}
	identity := &model.ExternalIdentity{UserID: user.ID, Provider: state.Provider, Subject: assertion.Subject,
		LastSeenAt: model.OptionalTimeFrom(at)}
	effectiveStart := invitation.EffectiveStartsAt(at)
	input := &store.ExternalIdentityInvitationAcceptance{ExternalStateID: state.ID, Identity: identity,
		ProviderEmail: providerEmail, User: user, Settings: settings, DefaultProfilePictureJob: defaultJob,
		Capabilities: capabilities}
	resource := model.Resource{}
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		input.Affiliation = &model.Affiliation{UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: effectiveStart}
		input.Affiliation.PrepareCreate(model.NewAffiliationID(), at)
		input.ClassMember = &model.ClassMember{ClassID: invitation.ClassID, AcademicPeriodID: invitation.AcademicPeriodID,
			UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
		input.ClassMember.PrepareCreate(model.NewClassMemberID(), at)
		input.RequiredActions = []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage}
		resource = model.Resource{Type: model.ResourceClass, ID: invitation.ClassID.String()}
	case model.InvitationPurposeTeacherAcademicUnit:
		input.Affiliation = &model.Affiliation{UserID: user.ID, Kind: model.AffiliationTeacher, StartsAt: effectiveStart}
		input.Affiliation.PrepareCreate(model.NewAffiliationID(), at)
		input.AcademicUnitMember = &model.AcademicUnitMember{AcademicUnitID: invitation.AcademicUnitID,
			UserID: user.ID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
		input.AcademicUnitMember.PrepareCreate(model.NewAcademicUnitMemberID(), at)
		input.RoleBinding = &model.RoleBinding{UserID: user.ID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
			OriginAcademicUnitMemberID: input.AcademicUnitMember.ID, ScopeType: model.RoleScopeAcademicUnit,
			ScopeID: invitation.AcademicUnitID.String(), StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
		input.RoleBinding.PrepareCreate(model.NewRoleBindingID(), at)
		input.RequiredActions = []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage}
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: invitation.AcademicUnitID.String()}
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		input.RoleBinding = &model.RoleBinding{UserID: user.ID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
			ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, StartsAt: effectiveStart, EndsAt: invitation.IntendedEndsAt}
		input.RoleBinding.PrepareCreate(model.NewRoleBindingID(), at)
		input.RequiredActions = []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage}
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: invitation.ScopeID}
		if invitation.Purpose == model.InvitationPurposeInstitutionRole {
			resource.Type = model.ResourceInstitution
		}
	default:
		return nil, store.NewErrInvalidInput("invitation", "purpose", nil)
	}
	if invitation.Purpose == model.InvitationPurposeStudentClass || invitation.Purpose == model.InvitationPurposeTeacherAcademicUnit {
		prepared, prepareErr := s.mail.PrepareInvitationAccepted(appmail.NoticePreparation{Recipient: user, At: at})
		if prepareErr == nil {
			input.Notice = &store.PreparedMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job}
		}
	}
	auditParameters, err := model.EncodeAuditData(map[string]string{"provider": state.Provider})
	if err != nil {
		return nil, err
	}
	input.AuditEvent = &model.AuditEvent{ActorID: user.ID, Action: "invitation.accept", Resource: resource,
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, Status: model.AuditStatusSuccess,
		RequestID: metadata.RequestID, NodeID: s.nodeID, ClientType: "web", AuthMethod: authenticationMethod,
		IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent, Parameters: auditParameters}
	result, err := s.store.AcceptExternalIdentity(ctx, input)
	if err != nil {
		return nil, err
	}
	if result.User != nil && (result.AcademicUnitMember != nil || result.RoleBinding != nil) {
		s.membershipEffects.InvalidateAuthorization(ctx, result.User.ID.String())
	}
	if result.ClassMember != nil {
		if effectErr := s.membershipEffects.MembershipChanged(ctx, result.ClassMember.UserID,
			[]model.ClassID{result.ClassMember.ClassID}); effectErr != nil {
			s.membershipEffects.Report(ctx, "invitation.external_student_class_membership_changed", effectErr)
		}
	}
	return result, nil
}

func (s *invitationService) acceptScopedRole(ctx context.Context, invocation Invocation, claim, source string, purpose model.InvitationPurpose) (*InvitationAcceptanceView, error) {
	if err := s.attempts.Check(ctx, model.HashInvitationClaim(claim), source); err != nil {
		return nil, err
	}
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, invalidTokenAppError()
	}
	if !model.IsValidCredentialToken(claim) {
		return nil, NewError("invitation.invalid")
	}
	claimHash := model.HashInvitationClaim(claim)
	return s.acceptScopedRoleByClaimHash(ctx, invocation, claimHash, purpose, nil)
}

func (s *invitationService) acceptScopedRoleByClaimHash(
	ctx context.Context,
	invocation Invocation,
	claimHash string,
	purpose model.InvitationPurpose,
	browserTransaction *store.BrowserInvitationTransactionProof,
) (*InvitationAcceptanceView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess || !model.IsValidTokenHash(claimHash) {
		return nil, invalidTokenAppError()
	}
	invitation, err := s.store.GetByClaimHash(ctx, claimHash)
	if err != nil || invitation.Purpose != purpose {
		return nil, invalidInvitationError(err)
	}
	at := model.TimeUTC(s.now())
	binding := &model.RoleBinding{UserID: principal.UserID, RoleID: invitation.RoleID, OriginInvitationID: invitation.ID,
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, StartsAt: invitation.EffectiveStartsAt(at), EndsAt: invitation.IntendedEndsAt}
	binding.PrepareCreate(model.NewRoleBindingID(), at)
	if err = binding.Validate(); err != nil {
		return nil, NewError("invitation.invalid").Wrap(err)
	}
	resourceType := model.ResourceAcademicUnit
	if purpose == model.InvitationPurposeInstitutionRole {
		resourceType = model.ResourceInstitution
	}
	result, err := runAuditedMutation(ctx, s.audit, mutationAttempt{
		Invocation: invocation, Action: model.Action("invitation.accept"),
		Resource:  model.Resource{Type: resourceType, ID: invitation.ScopeID},
		ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID,
		Operation: "accept_scoped_role_invitation", Value: invitation.Auditable(),
	}, s.now, func(ctx context.Context, attempt mutationAttemptReference) (*store.ScopedRoleInvitationAcceptanceResult, error) {
		return s.store.AcceptScopedRole(ctx, &store.ScopedRoleInvitationAcceptance{ClaimHash: claimHash,
			UserID: principal.UserID, RoleBinding: binding, AuditEventID: attempt.ID, AuditAt: attempt.MutationAtMillis,
			RequiredActions:    []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage},
			BrowserTransaction: browserTransaction})
	}, invalidInvitationError)
	if err != nil {
		return nil, err
	}
	if !result.Replayed && result.User != nil {
		s.membershipEffects.InvalidateAuthorization(ctx, result.User.ID.String())
	}
	return &InvitationAcceptanceView{Invitation: invitationView(result.Invitation), User: result.User,
		RoleBinding: result.RoleBinding, Replayed: result.Replayed}, nil
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

func invitationError(err error) error {
	if mapped := idempotencyError(err); mapped != nil {
		return mapped
	}
	if store.IsNotFound(err) {
		return NewError("resource.not_found").Wrap(err)
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) {
		return NewError("invitation.conflict").Wrap(err)
	}
	var invalid *store.ErrInvalidInput
	if errors.As(err, &invalid) {
		return NewError("invitation.invalid").Wrap(err)
	}
	return NewError("invitation.unavailable").Wrap(err)
}

func invitationMutationError(err error) error {
	if _, ok := As(err); ok {
		return err
	}
	return invitationError(err)
}

func invalidInvitationError(err error) error { return NewError("invitation.invalid").Wrap(err) }

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
		return NewError("authentication.rate_limited")
	}
	return nil
}
