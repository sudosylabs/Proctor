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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *invitationService) RunInvitationBatch(ctx context.Context, invocation invocation.Invocation, command RunInvitationBatchCommand) (InvitationBatchResult, error) {
	command.ScopeID = strings.TrimSpace(command.ScopeID)
	if !command.Operation.IsValid() || !model.IsValidOpaqueID(command.ScopeID) || command.IdempotencyKey == "" ||
		len(command.Items) < 1 || len(command.Items) > MaximumInvitationBatchItems || !invitationBatchScopeMatchesOperation(command.Operation, command.ScopeType) {
		return InvitationBatchResult{}, apperror.New("request.invalid")
	}
	resource, err := s.invitationBatchAuthorizationResource(ctx, command.ScopeType, command.ScopeID)
	if err != nil {
		return InvitationBatchResult{}, err
	}
	if err = s.deps.Authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchManage, resource); err != nil {
		return InvitationBatchResult{}, err
	}
	if command.Operation == InvitationBatchAcademicUnitRoleCreate || command.Operation == InvitationBatchInstitutionRoleCreate {
		if err = requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
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
			canonical, canonicalErr := idempotency.Prepare(invocation, invitationBatchLifecycleOperation(command.Operation), canonicalKey,
				invitationLifecycleIdempotencySemantic{ID: strings.TrimSpace(canonicalItem.InvitationID.String()), Revision: canonicalItem.ExpectedRevision})
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

func (r *InvitationBatchResult) append(item InvitationBatchItemResult) {
	r.Items = append(r.Items, item)
	switch item.Status {
	case InvitationBatchItemSucceeded:
		r.Succeeded++
	case InvitationBatchItemNoOp:
		r.NoOp++
	case InvitationBatchItemFailed:
		r.Failed++
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
		return model.Resource{}, apperror.New("request.invalid")
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
		return "invitation.resend"
	}
	if operation == InvitationBatchRevoke {
		return "invitation.revoke"
	}
	return ""
}

func (s *invitationService) recordInvitationCommandDuplicate(ctx context.Context, idempotency *store.CommandIdempotency, attempt mutationAttempt, input store.InvitationBatchDuplicate) (*store.InvitationBatchCommandResult, error) {
	return runAuditedMutation(ctx, s.deps.Audit, attempt, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationBatchCommandResult, error) {
			input.AuditEventID, input.AuditAt = reference.ID, reference.MutationAt
			return s.deps.Store.RecordBatchDuplicate(ctx, &input, idempotency)
		}, invitationStoreErrors.Translate)
}

func (s *invitationService) recordDuplicateInvitationView(ctx context.Context, idempotency *store.CommandIdempotency, attempt mutationAttempt, input store.InvitationBatchDuplicate, canonicalKey string) (InvitationView, error) {
	if idempotency == nil || canonicalKey == "" {
		return InvitationView{}, apperror.New("request.invalid")
	}
	input.ActorUserID = attempt.Invocation.Principal().UserID
	input.CanonicalOperation = idempotency.Operation
	input.CanonicalKeyDigest = sha256.Sum256([]byte(canonicalKey))
	result, err := s.recordInvitationCommandDuplicate(ctx, idempotency, attempt, input)
	if err != nil {
		return InvitationView{}, err
	}
	if result.Duplicate {
		return InvitationView{}, apperror.New("onboarding_batch.duplicate")
	}
	if !result.InvitationID.IsValid() {
		return InvitationView{}, apperror.New("invitation.unavailable")
	}
	return InvitationView{ID: result.InvitationID, Replayed: true}, nil
}

func (s *invitationService) recoverInvitationIssueOutcome(ctx context.Context, idempotency *store.CommandIdempotency, attempt mutationAttempt) (InvitationView, bool, error) {
	if idempotency == nil {
		return InvitationView{}, false, nil
	}
	found, err := s.deps.Store.FindCommandOutcome(ctx, idempotency)
	if store.IsNotFound(err) {
		return InvitationView{}, false, nil
	}
	if err == nil && found.Invitation != nil {
		attempt.Value = found.Invitation.Auditable()
	}
	replayed, err := runAuditedMutation(ctx, s.deps.Audit, attempt, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationCommandResult, error) {
			value, replayErr := s.deps.Store.ReplayIssue(ctx, &store.InvitationReplay{AuditEventID: reference.ID, AuditAt: reference.MutationAt}, idempotency)
			if replayErr == nil && value.Duplicate {
				return nil, apperror.New("onboarding_batch.duplicate")
			}
			return value, replayErr
		}, invitationStoreErrors.Translate)
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
	found, err := s.deps.Store.FindCommandOutcome(ctx, idempotency)
	if store.IsNotFound(err) {
		return InvitationAdministrationView{}, false, nil
	}
	if err == nil && found.Invitation != nil {
		attempt.Value = found.Invitation.Auditable()
	}
	replayed, err := runAuditedMutation(ctx, s.deps.Audit, attempt, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationCommandResult, error) {
			value, replayErr := s.deps.Store.ReplayAdministration(ctx, &store.InvitationReplay{AuditEventID: reference.ID, AuditAt: reference.MutationAt}, idempotency)
			if replayErr == nil && value.Duplicate {
				return nil, apperror.New("onboarding_batch.duplicate")
			}
			return value, replayErr
		}, invitationStoreErrors.Translate)
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
		if item.InvitationID != "" || item.ExpectedRevision != 0 || strings.TrimSpace(item.TargetEmail) == "" || strings.TrimSpace(item.RoleID.String()) == "" {
			return errors.New("invalid teacher Invitation batch item")
		}
	case InvitationBatchAcademicUnitRoleCreate, InvitationBatchInstitutionRoleCreate:
		if item.InvitationID != "" || item.ExpectedRevision != 0 || strings.TrimSpace(item.TargetEmail) == "" || strings.TrimSpace(item.RoleID.String()) == "" || hasSuggestions {
			return errors.New("invalid Role Invitation batch item")
		}
	case InvitationBatchResend, InvitationBatchRevoke:
		if strings.TrimSpace(item.InvitationID.String()) == "" || item.ExpectedRevision < 1 || item.TargetEmail != "" || item.RoleID != "" ||
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
		return mailbox + "\x00" + strings.TrimSpace(item.RoleID.String())
	case InvitationBatchResend, InvitationBatchRevoke:
		return strings.TrimSpace(item.InvitationID.String())
	default:
		return ""
	}
}

func (s *invitationService) runInvitationBatchItem(ctx context.Context, invocation invocation.Invocation, batch RunInvitationBatchCommand, item InvitationBatchItemCommand, itemKey string, duplicate bool, canonicalKey string, canonicalFingerprint [sha256.Size]byte) (model.InvitationID, InvitationBatchItemStatus, error) {
	switch batch.Operation {
	case InvitationBatchStudentClassCreate:
		view, err := s.IssueStudentClassInvitation(ctx, invocation, IssueStudentClassInvitationCommand{TargetEmail: item.TargetEmail, ClassID: model.ClassID(batch.ScopeID),
			IntendedStartsAt: item.IntendedStartsAt, IntendedEndsAt: item.IntendedEndsAt, SuggestedUsername: item.SuggestedUsername,
			SuggestedDisplayName: item.SuggestedDisplayName, SuggestedFirstName: item.SuggestedFirstName,
			SuggestedLastName: item.SuggestedLastName, SuggestedLocale: item.SuggestedLocale, SuggestedTimezone: item.SuggestedTimezone, IdempotencyKey: itemKey,
			batchDuplicate: duplicate, batchCanonicalKey: canonicalKey, onboardingImportID: batch.onboardingImportID,
			onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchTeacherAcademicUnitCreate:
		view, err := s.IssueTeacherAcademicUnitInvitation(ctx, invocation, IssueTeacherAcademicUnitInvitationCommand{TargetEmail: item.TargetEmail,
			AcademicUnitID: model.AcademicUnitID(batch.ScopeID), RoleID: item.RoleID, IntendedStartsAt: item.IntendedStartsAt, IntendedEndsAt: item.IntendedEndsAt,
			SuggestedUsername: item.SuggestedUsername, SuggestedDisplayName: item.SuggestedDisplayName,
			SuggestedFirstName: item.SuggestedFirstName, SuggestedLastName: item.SuggestedLastName,
			SuggestedLocale: item.SuggestedLocale, SuggestedTimezone: item.SuggestedTimezone, IdempotencyKey: itemKey, batchDuplicate: duplicate, batchCanonicalKey: canonicalKey,
			onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchAcademicUnitRoleCreate:
		view, err := s.IssueAcademicUnitRoleInvitation(ctx, invocation, IssueAcademicUnitRoleInvitationCommand{TargetEmail: item.TargetEmail,
			AcademicUnitID: model.AcademicUnitID(batch.ScopeID), RoleID: item.RoleID, IntendedStartsAt: item.IntendedStartsAt,
			IntendedEndsAt: item.IntendedEndsAt, IdempotencyKey: itemKey, batchDuplicate: duplicate, batchCanonicalKey: canonicalKey,
			onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchInstitutionRoleCreate:
		view, err := s.IssueInstitutionRoleInvitation(ctx, invocation, IssueInstitutionRoleInvitationCommand{TargetEmail: item.TargetEmail,
			InstitutionID: model.InstitutionID(batch.ScopeID), RoleID: item.RoleID, IntendedStartsAt: item.IntendedStartsAt,
			IntendedEndsAt: item.IntendedEndsAt, IdempotencyKey: itemKey, batchDuplicate: duplicate, batchCanonicalKey: canonicalKey,
			onboardingImportID: batch.onboardingImportID, onboardingImportRowNumber: batch.onboardingImportRowNumber})
		return invitationBatchViewOutcome(view, err)
	case InvitationBatchResend:
		view, err := s.ResendInvitation(ctx, invocation, ResendInvitationCommand{ID: model.InvitationID(strings.TrimSpace(item.InvitationID.String())), ExpectedRevision: item.ExpectedRevision,
			IdempotencyKey: itemKey, BatchScopeType: batch.ScopeType, BatchScopeID: batch.ScopeID,
			batchDuplicate: duplicate, batchCanonicalKey: canonicalKey, batchCanonicalFingerprint: canonicalFingerprint})
		return invitationBatchAdministrationOutcome(view, err)
	case InvitationBatchRevoke:
		view, err := s.RevokeInvitation(ctx, invocation, RevokeInvitationCommand{ID: model.InvitationID(strings.TrimSpace(item.InvitationID.String())), ExpectedRevision: item.ExpectedRevision,
			IdempotencyKey: itemKey, BatchScopeType: batch.ScopeType, BatchScopeID: batch.ScopeID,
			batchDuplicate: duplicate, batchCanonicalKey: canonicalKey, batchCanonicalFingerprint: canonicalFingerprint})
		return invitationBatchAdministrationOutcome(view, err)
	default:
		return "", "", apperror.New("request.invalid")
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
	if failure, ok := apperror.As(err); ok {
		switch failure.Code() {
		case "request.invalid", "resource.not_found", "authorization.denied", "authorization.request.invalid",
			"authorization.unavailable", "audit.unavailable", "administration.unavailable",
			"authentication.token_invalid", "authentication.strong_required", "authentication.reauthentication_required",
			"invitation.invalid", "invitation.class_period_conflict", "invitation.conflict",
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
