// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const MaximumAcademicAdministrationBatchItems = 200

type AcademicAdministrationBatchOperation string

const (
	AcademicAdministrationAffiliationAdd        AcademicAdministrationBatchOperation = "affiliation.add"
	AcademicAdministrationAffiliationEnd        AcademicAdministrationBatchOperation = "affiliation.end"
	AcademicAdministrationAcademicUnitMemberAdd AcademicAdministrationBatchOperation = "academic_unit_member.add"
	AcademicAdministrationAcademicUnitMemberEnd AcademicAdministrationBatchOperation = "academic_unit_member.end"
	AcademicAdministrationClassEnroll           AcademicAdministrationBatchOperation = "class.enroll"
	AcademicAdministrationClassEnd              AcademicAdministrationBatchOperation = "class.end"
	AcademicAdministrationClassTransfer         AcademicAdministrationBatchOperation = "class.transfer"
	AcademicAdministrationRoleBindingCreate     AcademicAdministrationBatchOperation = "role_binding.create"
	AcademicAdministrationRoleBindingEnd        AcademicAdministrationBatchOperation = "role_binding.end"
	AcademicAdministrationUserEnable            AcademicAdministrationBatchOperation = "user.enable"
	AcademicAdministrationUserDisable           AcademicAdministrationBatchOperation = "user.disable"
	AcademicAdministrationUserSessionsRevoke    AcademicAdministrationBatchOperation = "user_sessions.revoke"
)

func (operation AcademicAdministrationBatchOperation) IsValid() bool {
	switch operation {
	case AcademicAdministrationAffiliationAdd, AcademicAdministrationAffiliationEnd,
		AcademicAdministrationAcademicUnitMemberAdd, AcademicAdministrationAcademicUnitMemberEnd,
		AcademicAdministrationClassEnroll, AcademicAdministrationClassEnd, AcademicAdministrationClassTransfer,
		AcademicAdministrationRoleBindingCreate, AcademicAdministrationRoleBindingEnd,
		AcademicAdministrationUserEnable, AcademicAdministrationUserDisable,
		AcademicAdministrationUserSessionsRevoke:
		return true
	default:
		return false
	}
}

func (operation AcademicAdministrationBatchOperation) requiresStrongRecentSession() bool {
	switch operation {
	case AcademicAdministrationRoleBindingCreate, AcademicAdministrationRoleBindingEnd,
		AcademicAdministrationUserEnable, AcademicAdministrationUserDisable,
		AcademicAdministrationUserSessionsRevoke:
		return true
	default:
		return false
	}
}

// AcademicAdministrationBatchItemCommand is a closed union. Fields that are
// not owned by the batch operation make only that row invalid.
type AcademicAdministrationBatchItemCommand struct {
	IdempotencyKey  string
	UserID          string
	RelationshipID  string
	RoleID          string
	AffiliationKind model.AffiliationKind
	StartAt         int64
	EndAt           int64
}

type RunAcademicAdministrationBatchCommand struct {
	Operation                     AcademicAdministrationBatchOperation
	ScopeType                     model.RoleScopeType
	ScopeID                       string
	IdempotencyKey                string
	Items                         []AcademicAdministrationBatchItemCommand
	onboardingImportID            model.OnboardingImportID
	onboardingImportRowNumber     int
	progression                   bool
	progressionSourceAuditID      string
	progressionDestinationAuditID string
}

type AcademicAdministrationBatchItemResult struct {
	Index      int
	Status     InvitationBatchItemStatus
	ResourceID string
	ErrorCode  string
}

type AcademicAdministrationBatchResult struct {
	Operation AcademicAdministrationBatchOperation
	Items     []AcademicAdministrationBatchItemResult
	Succeeded int
	NoOp      int
	Failed    int
}

type academicAdministrationBatchAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type academicAdministrationOutcomeLookup interface {
	Has(context.Context, *store.CommandIdempotency) (bool, error)
}

type academicAdministrationBatchCommands interface {
	CreateAffiliation(context.Context, Invocation, CreateAffiliationCommand) (*model.Affiliation, error)
	EndAffiliation(context.Context, Invocation, EndAffiliationCommand) (*model.Affiliation, error)
	CreateAcademicUnitMember(context.Context, Invocation, CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
	EndAcademicUnitMember(context.Context, Invocation, EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
	EnrollClassMember(context.Context, Invocation, EnrollClassMemberCommand) (*model.ClassEnrollment, error)
	EndClassMember(context.Context, Invocation, EndClassMemberCommand) (*model.ClassMember, error)
	CreateRoleBinding(context.Context, Invocation, CreateRoleBindingCommand) (*model.RoleBinding, error)
	EndRoleBinding(context.Context, Invocation, EndRoleBindingCommand) (*model.RoleBinding, error)
	SetUserEnabled(context.Context, Invocation, SetUserEnabledCommand) (*model.User, error)
	RevokeUserSessions(context.Context, Invocation, RevokeUserSessionsCommand) error
}

type academicAdministrationBatchService struct {
	commands      academicAdministrationBatchCommands
	authorization academicAdministrationBatchAuthorizer
	outcomes      academicAdministrationOutcomeLookup
	now           func() time.Time
	recentTTL     time.Duration
}

type academicAdministrationCommandServices struct {
	affiliations        *affiliationService
	academicUnitMembers *academicUnitMemberService
	classMembers        *classMemberService
	roleBindings        *roleBindingService
	accountStates       *accountStateService
	sessions            *sessionAdministrationService
}

func (s academicAdministrationCommandServices) CreateAffiliation(ctx context.Context, invocation Invocation, command CreateAffiliationCommand) (*model.Affiliation, error) {
	return s.affiliations.Create(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) EndAffiliation(ctx context.Context, invocation Invocation, command EndAffiliationCommand) (*model.Affiliation, error) {
	return s.affiliations.End(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) CreateAcademicUnitMember(ctx context.Context, invocation Invocation, command CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	return s.academicUnitMembers.Create(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) EndAcademicUnitMember(ctx context.Context, invocation Invocation, command EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	return s.academicUnitMembers.End(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) EnrollClassMember(ctx context.Context, invocation Invocation, command EnrollClassMemberCommand) (*model.ClassEnrollment, error) {
	return s.classMembers.Enroll(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) EndClassMember(ctx context.Context, invocation Invocation, command EndClassMemberCommand) (*model.ClassMember, error) {
	return s.classMembers.End(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) CreateRoleBinding(ctx context.Context, invocation Invocation, command CreateRoleBindingCommand) (*model.RoleBinding, error) {
	return s.roleBindings.Create(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) EndRoleBinding(ctx context.Context, invocation Invocation, command EndRoleBindingCommand) (*model.RoleBinding, error) {
	return s.roleBindings.End(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) SetUserEnabled(ctx context.Context, invocation Invocation, command SetUserEnabledCommand) (*model.User, error) {
	return s.accountStates.SetEnabled(ctx, invocation, command)
}
func (s academicAdministrationCommandServices) RevokeUserSessions(ctx context.Context, invocation Invocation, command RevokeUserSessionsCommand) error {
	return s.sessions.RevokeAll(ctx, invocation, command)
}

func (a *App) RunAcademicAdministrationBatch(ctx context.Context, invocation Invocation, command RunAcademicAdministrationBatchCommand) (AcademicAdministrationBatchResult, error) {
	if a == nil || a.academicAdministrationBatches == nil {
		return AcademicAdministrationBatchResult{}, NewError("administration.unavailable")
	}
	return a.academicAdministrationBatches.Run(ctx, invocation, command)
}

func newAcademicAdministrationBatchService(commands academicAdministrationBatchCommands, authorization academicAdministrationBatchAuthorizer, outcomes academicAdministrationOutcomeLookup, now func() time.Time, recentTTL time.Duration) *academicAdministrationBatchService {
	return &academicAdministrationBatchService{commands: commands, authorization: authorization, outcomes: outcomes, now: now, recentTTL: recentTTL}
}

func (s *academicAdministrationBatchService) Run(ctx context.Context, invocation Invocation, command RunAcademicAdministrationBatchCommand) (AcademicAdministrationBatchResult, error) {
	command.ScopeID = strings.TrimSpace(command.ScopeID)
	if s == nil || s.commands == nil || s.authorization == nil || s.now == nil || !command.Operation.IsValid() ||
		!model.IsValidId(command.ScopeID) || command.IdempotencyKey == "" || len(command.Items) < 1 ||
		len(command.Items) > MaximumAcademicAdministrationBatchItems || !academicAdministrationBatchScopeMatches(command.Operation, command.ScopeType) {
		return AcademicAdministrationBatchResult{}, NewError("request.invalid")
	}
	resource, err := academicAdministrationBatchResource(command.ScopeType, command.ScopeID)
	if err != nil {
		return AcademicAdministrationBatchResult{}, err
	}
	batchAction := model.ActionOnboardingBatchManage
	if command.progression {
		batchAction = model.ActionAcademicProgressionManage
	}
	if err = s.authorization.Authorize(ctx, invocation, batchAction, resource); err != nil {
		return AcademicAdministrationBatchResult{}, err
	}
	if command.Operation.requiresStrongRecentSession() {
		if err = requireStrongRecentSession(invocation.Principal(), s.now(), s.recentTTL); err != nil {
			return AcademicAdministrationBatchResult{}, err
		}
	}

	result := AcademicAdministrationBatchResult{Operation: command.Operation, Items: make([]AcademicAdministrationBatchItemResult, len(command.Items))}
	keyCounts := make(map[string]int, len(command.Items))
	for _, item := range command.Items {
		keyCounts[item.IdempotencyKey]++
	}
	winners := academicAdministrationDuplicateWinners(command, keyCounts)
	order := make([]int, len(command.Items))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool {
		leftItem, rightItem := command.Items[order[left]], command.Items[order[right]]
		leftKey := academicAdministrationBatchDuplicateKey(command, leftItem)
		rightKey := academicAdministrationBatchDuplicateKey(command, rightItem)
		leftDuplicate := leftKey != "" && winners[leftKey] != "" && winners[leftKey] != leftItem.IdempotencyKey
		rightDuplicate := rightKey != "" && winners[rightKey] != "" && winners[rightKey] != rightItem.IdempotencyKey
		if leftDuplicate != rightDuplicate {
			return !leftDuplicate
		}
		return leftItem.IdempotencyKey < rightItem.IdempotencyKey
	})
	for _, index := range order {
		item := command.Items[index]
		outcome := AcademicAdministrationBatchItemResult{Index: index}
		if validateAcademicAdministrationBatchItem(command.Operation, item) != nil || keyCounts[item.IdempotencyKey] != 1 {
			outcome.Status, outcome.ErrorCode = InvitationBatchItemFailed, "request.invalid"
			result.Items[index] = outcome
			continue
		}
		itemKey := invitationBatchItemIdempotencyKey(command.IdempotencyKey, item.IdempotencyKey)
		retained := false
		if s.outcomes != nil {
			lookup := &store.CommandIdempotency{UserID: invocation.Principal().UserID, Operation: academicAdministrationCommandOperation(command.Operation), KeyDigest: sha256.Sum256([]byte(itemKey)), Retention: commandIdempotencyRetention, Wait: commandIdempotencyWait}
			retained, err = s.outcomes.Has(ctx, lookup)
			if err != nil {
				outcome.Status, outcome.ErrorCode = InvitationBatchItemFailed, "administration.unavailable"
				result.Items[index] = outcome
				continue
			}
		}
		duplicateKey := academicAdministrationBatchDuplicateKey(command, item)
		winner := winners[duplicateKey]
		metadata := &store.CommandBatch{GroupDigest: sha256.Sum256([]byte("academic-administration-batch.v1\x00" + duplicateKey))}
		if winner != item.IdempotencyKey {
			canonicalKey := invitationBatchItemIdempotencyKey(command.IdempotencyKey, winner)
			metadata.DuplicateOfKeyDigest = sha256.Sum256([]byte(canonicalKey))
		}
		outcome.ResourceID, outcome.Status, err = s.runItem(ctx, invocation, command, item, itemKey, metadata, retained)
		if err != nil {
			outcome.Status, outcome.ErrorCode = InvitationBatchItemFailed, academicAdministrationBatchPublicErrorCode(err)
		}
		result.Items[index] = outcome
	}
	for _, outcome := range result.Items {
		switch outcome.Status {
		case InvitationBatchItemSucceeded:
			result.Succeeded++
		case InvitationBatchItemNoOp:
			result.NoOp++
		case InvitationBatchItemFailed:
			result.Failed++
		}
	}
	return result, nil
}

func academicAdministrationCommandOperation(operation AcademicAdministrationBatchOperation) string {
	switch operation {
	case AcademicAdministrationAffiliationAdd:
		return "affiliation.add.v1"
	case AcademicAdministrationAffiliationEnd:
		return "affiliation.end.v1"
	case AcademicAdministrationAcademicUnitMemberAdd:
		return "academic_unit_member.add.v1"
	case AcademicAdministrationAcademicUnitMemberEnd:
		return "academic_unit_member.end.v1"
	case AcademicAdministrationClassEnroll, AcademicAdministrationClassTransfer:
		return "class_member.enroll.v1"
	case AcademicAdministrationClassEnd:
		return "class_member.end.v1"
	case AcademicAdministrationRoleBindingCreate:
		return "role_binding.create.v1"
	case AcademicAdministrationRoleBindingEnd:
		return "role_binding.end.v1"
	case AcademicAdministrationUserEnable, AcademicAdministrationUserDisable:
		return "user.enabled_state.v1"
	case AcademicAdministrationUserSessionsRevoke:
		return "user_sessions.revoke.v1"
	default:
		return ""
	}
}

func academicAdministrationDuplicateWinners(command RunAcademicAdministrationBatchCommand, keyCounts map[string]int) map[string]string {
	winners := make(map[string]string, len(command.Items))
	for _, item := range command.Items {
		if keyCounts[item.IdempotencyKey] != 1 || validateAcademicAdministrationBatchItem(command.Operation, item) != nil {
			continue
		}
		key := academicAdministrationBatchDuplicateKey(command, item)
		if winner, exists := winners[key]; !exists || item.IdempotencyKey < winner {
			winners[key] = item.IdempotencyKey
		}
	}
	return winners
}

func academicAdministrationBatchDuplicateKey(batch RunAcademicAdministrationBatchCommand, item AcademicAdministrationBatchItemCommand) string {
	base := string(batch.Operation) + "\x00" + string(batch.ScopeType) + "\x00" + strings.TrimSpace(batch.ScopeID) + "\x00"
	switch batch.Operation {
	case AcademicAdministrationAffiliationAdd:
		return base + strings.TrimSpace(item.UserID) + "\x00" + string(item.AffiliationKind) + "\x00" +
			strconv.FormatInt(item.StartAt, 10) + "\x00" + strconv.FormatInt(item.EndAt, 10)
	case AcademicAdministrationAcademicUnitMemberAdd, AcademicAdministrationClassEnroll,
		AcademicAdministrationUserEnable, AcademicAdministrationUserDisable, AcademicAdministrationUserSessionsRevoke:
		key := base + strings.TrimSpace(item.UserID)
		if batch.Operation == AcademicAdministrationAcademicUnitMemberAdd || batch.Operation == AcademicAdministrationClassEnroll {
			key += "\x00" + strconv.FormatInt(item.StartAt, 10) + "\x00" + strconv.FormatInt(item.EndAt, 10)
		}
		return key
	case AcademicAdministrationClassTransfer:
		return base + strings.TrimSpace(item.UserID) + "\x00" + strings.TrimSpace(item.RelationshipID) + "\x00" +
			strconv.FormatInt(item.StartAt, 10) + "\x00" + strconv.FormatInt(item.EndAt, 10)
	case AcademicAdministrationRoleBindingCreate:
		return base + strings.TrimSpace(item.UserID) + "\x00" + strings.TrimSpace(item.RoleID) + "\x00" +
			strconv.FormatInt(item.StartAt, 10) + "\x00" + strconv.FormatInt(item.EndAt, 10)
	default:
		return base + strings.TrimSpace(item.RelationshipID)
	}
}

func (result *AcademicAdministrationBatchResult) append(item AcademicAdministrationBatchItemResult) {
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

func academicAdministrationBatchScopeMatches(operation AcademicAdministrationBatchOperation, scopeType model.RoleScopeType) bool {
	switch operation {
	case AcademicAdministrationAcademicUnitMemberAdd, AcademicAdministrationAcademicUnitMemberEnd:
		return scopeType == model.RoleScopeAcademicUnit
	case AcademicAdministrationClassEnroll, AcademicAdministrationClassEnd, AcademicAdministrationClassTransfer:
		return scopeType == model.RoleScopeClass
	case AcademicAdministrationRoleBindingCreate, AcademicAdministrationRoleBindingEnd:
		return scopeType == model.RoleScopeInstitution || scopeType == model.RoleScopeAcademicUnit || scopeType == model.RoleScopeClass
	case AcademicAdministrationAffiliationAdd, AcademicAdministrationAffiliationEnd:
		return scopeType == model.RoleScopeInstitution
	case AcademicAdministrationUserEnable, AcademicAdministrationUserDisable, AcademicAdministrationUserSessionsRevoke:
		return scopeType == model.RoleScopeInstitution
	default:
		return false
	}
}

func academicAdministrationBatchResource(scopeType model.RoleScopeType, scopeID string) (model.Resource, error) {
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

func validateAcademicAdministrationBatchItem(operation AcademicAdministrationBatchOperation, item AcademicAdministrationBatchItemCommand) error {
	if !validInvitationBatchItemKey(item.IdempotencyKey) {
		return errors.New("invalid academic administration batch item key")
	}
	userID, relationshipID, roleID := strings.TrimSpace(item.UserID), strings.TrimSpace(item.RelationshipID), strings.TrimSpace(item.RoleID)
	hasUser, hasRelationship, hasRole := userID != "", relationshipID != "", roleID != ""
	validBounds := item.StartAt >= 0 && item.EndAt >= 0 && (item.EndAt == 0 || item.EndAt > item.StartAt)
	switch operation {
	case AcademicAdministrationAffiliationAdd:
		if !hasUser || hasRelationship || hasRole || !item.AffiliationKind.IsValid() || !validBounds {
			return errors.New("invalid affiliation add item")
		}
	case AcademicAdministrationAffiliationEnd, AcademicAdministrationAcademicUnitMemberEnd,
		AcademicAdministrationClassEnd, AcademicAdministrationRoleBindingEnd:
		if hasUser || !hasRelationship || hasRole || item.AffiliationKind != "" || item.StartAt != 0 || item.EndAt != 0 {
			return errors.New("invalid relationship end item")
		}
	case AcademicAdministrationAcademicUnitMemberAdd:
		if !hasUser || hasRelationship || hasRole || item.AffiliationKind != "" || item.EndAt != 0 || !validBounds {
			return errors.New("invalid Academic Unit membership add item")
		}
	case AcademicAdministrationClassEnroll:
		if !hasUser || hasRelationship || hasRole || item.AffiliationKind != "" || !validBounds {
			return errors.New("invalid relationship add item")
		}
	case AcademicAdministrationClassTransfer:
		if !hasUser || !hasRelationship || hasRole || item.AffiliationKind != "" || !validBounds {
			return errors.New("invalid Class transfer item")
		}
	case AcademicAdministrationRoleBindingCreate:
		if !hasUser || hasRelationship || !hasRole || item.AffiliationKind != "" || !validBounds {
			return errors.New("invalid Role Binding create item")
		}
	case AcademicAdministrationUserEnable, AcademicAdministrationUserDisable, AcademicAdministrationUserSessionsRevoke:
		if !hasUser || hasRelationship || hasRole || item.AffiliationKind != "" || item.StartAt != 0 || item.EndAt != 0 {
			return errors.New("invalid User operation item")
		}
	default:
		return errors.New("invalid academic administration batch operation")
	}
	if hasUser && !model.IsValidId(userID) || hasRelationship && !model.IsValidId(relationshipID) || hasRole && !model.IsValidId(roleID) {
		return errors.New("invalid academic administration identifier")
	}
	return nil
}

func (s *academicAdministrationBatchService) runItem(ctx context.Context, invocation Invocation, batch RunAcademicAdministrationBatchCommand, item AcademicAdministrationBatchItemCommand, itemKey string, metadata *store.CommandBatch, retained bool) (string, InvitationBatchItemStatus, error) {
	authority := academicAdministrationBatchAuthority(invocation, batch)
	if model.IsValidId(strings.TrimSpace(item.UserID)) {
		authority.RecipientUserID = model.UserID(strings.TrimSpace(item.UserID))
	}
	if batch.Operation == AcademicAdministrationClassTransfer || batch.Operation == AcademicAdministrationClassEnd {
		authority.ClassMemberID = model.ClassMemberID(strings.TrimSpace(item.RelationshipID))
	}
	switch batch.Operation {
	case AcademicAdministrationAffiliationAdd:
		var replayed bool
		value, err := s.commands.CreateAffiliation(ctx, invocation, CreateAffiliationCommand{UserID: item.UserID, Kind: item.AffiliationKind, StartAt: item.StartAt, EndAt: item.EndAt, IdempotencyKey: itemKey, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationAffiliationEnd:
		var replayed bool
		value, err := s.commands.EndAffiliation(ctx, invocation, EndAffiliationCommand{ID: item.RelationshipID, IdempotencyKey: itemKey, BatchScopeType: batch.ScopeType, BatchScopeID: batch.ScopeID, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationAcademicUnitMemberAdd:
		var replayed bool
		value, err := s.commands.CreateAcademicUnitMember(ctx, invocation, CreateAcademicUnitMemberCommand{AcademicUnitID: batch.ScopeID, UserID: item.UserID, StartAt: item.StartAt, IdempotencyKey: itemKey, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationAcademicUnitMemberEnd:
		var replayed bool
		value, err := s.commands.EndAcademicUnitMember(ctx, invocation, EndAcademicUnitMemberCommand{ID: item.RelationshipID, IdempotencyKey: itemKey, BatchScopeID: batch.ScopeID, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationClassEnroll, AcademicAdministrationClassTransfer:
		var replayed bool
		value, err := s.commands.EnrollClassMember(ctx, invocation, EnrollClassMemberCommand{ClassID: batch.ScopeID, UserID: item.UserID, StartAt: item.StartAt, EndAt: item.EndAt, ExpectedPreviousID: item.RelationshipID, RequireTransfer: batch.Operation == AcademicAdministrationClassTransfer, IdempotencyKey: itemKey, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, studentProgression: batch.progression, progressionSourceAuditID: batch.progressionSourceAuditID, progressionDestinationAuditID: batch.progressionDestinationAuditID, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		if err != nil || value == nil || value.Membership == nil {
			return "", "", err
		}
		if metadata.Duplicate {
			return "", "", NewError("onboarding_batch.duplicate")
		}
		status := InvitationBatchItemSucceeded
		if replayed {
			status = InvitationBatchItemNoOp
		}
		return value.Membership.ID.String(), status, nil
	case AcademicAdministrationClassEnd:
		var replayed bool
		value, err := s.commands.EndClassMember(ctx, invocation, EndClassMemberCommand{ID: item.RelationshipID, IdempotencyKey: itemKey, BatchScopeID: batch.ScopeID, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationRoleBindingCreate:
		var replayed bool
		value, err := s.commands.CreateRoleBinding(ctx, invocation, CreateRoleBindingCommand{UserID: item.UserID, RoleID: item.RoleID, ScopeType: batch.ScopeType, ScopeID: batch.ScopeID, StartAt: item.StartAt, EndAt: item.EndAt, IdempotencyKey: itemKey, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationRoleBindingEnd:
		var replayed bool
		value, err := s.commands.EndRoleBinding(ctx, invocation, EndRoleBindingCommand{ID: item.RelationshipID, IdempotencyKey: itemKey, BatchScopeType: batch.ScopeType, BatchScopeID: batch.ScopeID, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationUserEnable, AcademicAdministrationUserDisable:
		var replayed bool
		value, err := s.commands.SetUserEnabled(ctx, invocation, SetUserEnabledCommand{ID: item.UserID, Enabled: batch.Operation == AcademicAdministrationUserEnable, IdempotencyKey: itemKey, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return academicAdministrationModelOutcome(value, replayed, metadata, err)
	case AcademicAdministrationUserSessionsRevoke:
		var replayed bool
		err := s.commands.RevokeUserSessions(ctx, invocation, RevokeUserSessionsCommand{UserID: item.UserID, IdempotencyKey: itemKey, batchReplayed: &replayed, batchAuthorization: authority, batchMetadata: metadata, batchRetainedOutcome: retained, onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		if err != nil {
			return "", "", err
		}
		if metadata.Duplicate {
			return "", "", NewError("onboarding_batch.duplicate")
		}
		if replayed {
			return item.UserID, InvitationBatchItemNoOp, nil
		}
		return item.UserID, InvitationBatchItemSucceeded, nil
	default:
		return "", "", NewError("request.invalid")
	}
}

func academicAdministrationBatchAuthority(invocation Invocation, batch RunAcademicAdministrationBatchCommand) *store.CommandAuthorization {
	var action model.Action
	switch batch.Operation {
	case AcademicAdministrationAffiliationAdd, AcademicAdministrationAffiliationEnd,
		AcademicAdministrationUserEnable, AcademicAdministrationUserDisable:
		action = model.ActionUserManage
	case AcademicAdministrationAcademicUnitMemberAdd, AcademicAdministrationAcademicUnitMemberEnd:
		action = model.ActionAcademicUnitMembersManage
	case AcademicAdministrationClassEnroll, AcademicAdministrationClassEnd, AcademicAdministrationClassTransfer:
		action = model.ActionClassMembersManage
	case AcademicAdministrationRoleBindingCreate, AcademicAdministrationRoleBindingEnd:
		action = model.ActionRoleBindingManage
	case AcademicAdministrationUserSessionsRevoke:
		action = model.ActionSessionManage
	}
	actions := []model.Action{model.ActionOnboardingBatchManage, action}
	if batch.progression {
		actions = []model.Action{model.ActionAcademicProgressionManage, action}
	}
	return &store.CommandAuthorization{
		Principal: invocation.Principal(), ScopeType: batch.ScopeType, ScopeID: batch.ScopeID,
		Actions: actions,
	}
}

func bindAcademicAdministrationAuthorization(command *store.CommandIdempotency, authority *store.CommandAuthorization) {
	if command != nil {
		command.Authorization = authority
	}
}

func bindAcademicAdministrationBatch(command *store.CommandIdempotency, batch *store.CommandBatch) {
	if command != nil {
		command.Batch = batch
	}
}

func academicAdministrationModelOutcome(value any, replayed bool, metadata *store.CommandBatch, err error) (string, InvitationBatchItemStatus, error) {
	if err != nil {
		return "", "", err
	}
	if metadata != nil && metadata.Duplicate {
		return "", "", NewError("onboarding_batch.duplicate")
	}
	var id string
	switch typed := value.(type) {
	case *model.Affiliation:
		if typed != nil {
			id = typed.ID.String()
		}
	case *model.AcademicUnitMember:
		if typed != nil {
			id = typed.ID.String()
		}
	case *model.ClassMember:
		if typed != nil {
			id = typed.ID.String()
		}
	case *model.RoleBinding:
		if typed != nil {
			id = typed.ID.String()
		}
	case *model.User:
		if typed != nil {
			id = typed.ID.String()
		}
	}
	if id == "" {
		return "", "", NewError("administration.unavailable")
	}
	status := InvitationBatchItemSucceeded
	if replayed {
		status = InvitationBatchItemNoOp
	}
	return id, status, nil
}

func academicAdministrationBatchPublicErrorCode(err error) string {
	if failure, ok := As(err); ok {
		switch failure.Code() {
		case "request.invalid", "resource.not_found", "authorization.denied", "authorization.request.invalid",
			"authorization.unavailable", "audit.unavailable", "administration.unavailable", "mail.unavailable",
			"authentication.invalid_token", "authentication.strong_required", "authentication.reauthentication_required",
			"affiliation.invalid", "affiliation.conflict", "affiliation.student_has_active_enrollment",
			"academic_unit_member.invalid", "academic_unit_member.conflict", "class_member.invalid",
			"class_member.student_affiliation_required", "class.enrollment_conflict", "role_binding.invalid",
			"role_binding.conflict", "role_binding.last_system_admin", "role_binding.system_admin_requires_institution_scope",
			"user.invalid", "user.conflict", "user.last_system_admin", "session.not_found",
			"idempotency.conflict", "idempotency.in_progress", "onboarding_batch.duplicate":
			return failure.Code()
		}
	}
	return "administration.unavailable"
}
