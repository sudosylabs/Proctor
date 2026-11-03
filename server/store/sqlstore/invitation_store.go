// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLInvitationStore struct{ *SQLStore }

const invitationTerminalRetention = 90 * 24 * time.Hour

type invitationRow struct {
	ID                           string                  `db:"id"`
	CreatedAt                    time.Time               `db:"created_at"`
	UpdatedAt                    time.Time               `db:"updated_at"`
	Revision                     int64                   `db:"revision"`
	Purpose                      model.InvitationPurpose `db:"purpose"`
	State                        model.InvitationState   `db:"state"`
	TargetEmail                  string                  `db:"target_email"`
	ClassID                      sql.NullString          `db:"class_id"`
	AcademicPeriodID             sql.NullString          `db:"academic_period_id"`
	AcademicUnitID               sql.NullString          `db:"academic_unit_id"`
	RoleID                       sql.NullString          `db:"role_id"`
	RoleActions                  pq.StringArray          `db:"role_actions"`
	IntendedStartsAt             time.Time               `db:"intended_start_at"`
	IntendedEndsAt               sql.NullTime            `db:"intended_end_at"`
	SuggestedUsername            string                  `db:"suggested_username"`
	SuggestedDisplayName         string                  `db:"suggested_display_name"`
	SuggestedFirstName           string                  `db:"suggested_first_name"`
	SuggestedLastName            string                  `db:"suggested_last_name"`
	SuggestedLocale              string                  `db:"suggested_locale"`
	SuggestedTimezone            string                  `db:"suggested_timezone"`
	InviterUserID                string                  `db:"inviter_user_id"`
	ScopeType                    model.RoleScopeType     `db:"scope_type"`
	ScopeID                      string                  `db:"scope_id"`
	ClaimHash                    string                  `db:"claim_hash"`
	ExpiresAt                    time.Time               `db:"expires_at"`
	AcceptedAt                   sql.NullTime            `db:"accepted_at"`
	AcceptedUserID               sql.NullString          `db:"accepted_user_id"`
	AcceptedAffiliationID        sql.NullString          `db:"accepted_affiliation_id"`
	AcceptedClassMemberID        sql.NullString          `db:"accepted_class_member_id"`
	AcceptedAcademicUnitMemberID sql.NullString          `db:"accepted_academic_unit_member_id"`
	AcceptedRoleBindingID        sql.NullString          `db:"accepted_role_binding_id"`
}

const invitationColumns = `id,created_at,updated_at,revision,purpose,state,target_email,class_id,academic_period_id,academic_unit_id,role_id,role_actions,intended_start_at,intended_end_at,suggested_username,suggested_display_name,suggested_first_name,suggested_last_name,suggested_locale,suggested_timezone,inviter_user_id,scope_type,scope_id,claim_hash,expires_at,accepted_at,accepted_user_id,accepted_affiliation_id,accepted_class_member_id,accepted_academic_unit_member_id,accepted_role_binding_id`

func newSQLInvitationStore(sqlStore *SQLStore) store.InvitationStore {
	return &SQLInvitationStore{SQLStore: sqlStore}
}

func newSQLOnboardingImportStore(sqlStore *SQLStore) store.OnboardingImportStore {
	return &SQLInvitationStore{SQLStore: sqlStore}
}

const (
	invitationOutcomeKindInvitation = "invitation"
	invitationOutcomeKindDuplicate  = "duplicate"
	invitationOutcomeKindNoOp       = "no_op"
)

type invitationCommandOutcome struct {
	Kind         string                      `json:"kind"`
	InvitationID string                      `json:"invitation_id,omitempty"`
	Delivery     *invitationDeliveryDocument `json:"delivery,omitempty"`
}

func (invitationCommandOutcome) outcomeDocument() {}

// invitationDeliveryDocument spells store.InvitationDeliverySummary inside a
// persisted outcome; the two convert directly.
type invitationDeliveryDocument struct {
	TemplateKey       model.MailTemplateKey   `json:"template_key"`
	State             model.MailDeliveryState `json:"state"`
	MaskedRecipient   string                  `json:"masked_recipient"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
	Deadline          time.Time               `json:"deadline"`
	AcceptedAt        model.OptionalTime      `json:"accepted_at"`
	PublicFailureCode string                  `json:"public_failure_code,omitempty"`
}

func encodeInvitationCommandOutcome(result *store.InvitationCommandResult) ([]byte, error) {
	if result != nil && result.NoOp && (result.Invitation == nil || result.Invitation.ID.IsValid()) {
		outcome := invitationCommandOutcome{Kind: invitationOutcomeKindNoOp}
		if result.Invitation != nil {
			outcome.InvitationID = result.Invitation.ID.String()
		}
		return encodeOutcome(outcome)
	}
	if result == nil || result.Invitation == nil || !result.Invitation.ID.IsValid() {
		return nil, store.NewErrInvalidInput("invitation", "command_outcome", nil)
	}
	return encodeOutcome(invitationCommandOutcome{Kind: invitationOutcomeKindInvitation, InvitationID: result.Invitation.ID.String()})
}

func decodeInvitationCommandOutcome(data []byte) (*store.InvitationCommandResult, error) {
	var outcome invitationCommandOutcome
	if err := decodeOutcome(data, &outcome); err != nil {
		return nil, err
	}
	if outcome.Kind == invitationOutcomeKindDuplicate {
		return &store.InvitationCommandResult{Duplicate: true}, nil
	}
	if outcome.Kind == invitationOutcomeKindNoOp {
		result := &store.InvitationCommandResult{NoOp: true}
		if outcome.InvitationID != "" {
			id, err := model.ParseInvitationID(outcome.InvitationID)
			if err != nil {
				return nil, invalidPersistedState("command_outcome", "invitation", err)
			}
			result.Invitation = &model.Invitation{ID: id}
		}
		return result, nil
	}
	id, err := model.ParseInvitationID(outcome.InvitationID)
	if err != nil || outcome.Kind != invitationOutcomeKindInvitation {
		return nil, invalidPersistedState("command_outcome", "invitation", err)
	}
	return &store.InvitationCommandResult{Invitation: &model.Invitation{ID: id}}, nil
}

func hydrateInvitationCommandOutcome(ctx context.Context, tx *sqlxTxWrapper, result *store.InvitationCommandResult) (*store.InvitationCommandResult, error) {
	if result == nil || result.Duplicate || result.NoOp && result.Invitation == nil {
		return result, nil
	}
	invitation, err := getInvitationInTransaction(ctx, tx, result.Invitation.ID)
	if err != nil {
		return nil, err
	}
	result.Invitation = invitation
	return result, nil
}

func invitationOnboardingOutcome(result *store.InvitationCommandResult) (onboardingImportCommandResult, error) {
	if result == nil || result.Duplicate {
		return onboardingImportCommandResult{}, store.NewErrInvalidInput("onboarding_import_row", "invitation_outcome", nil)
	}
	if result.NoOp {
		if result.Invitation != nil {
			return onboardingImportCommandResult{Status: model.OnboardingImportRowNoOp, InvitationID: result.Invitation.ID}, nil
		}
		return onboardingImportCommandResult{Status: model.OnboardingImportRowNoOp}, nil
	}
	if result.Invitation == nil || !result.Invitation.ID.IsValid() {
		return onboardingImportCommandResult{}, store.NewErrInvalidInput("onboarding_import_row", "invitation_outcome", nil)
	}
	return onboardingImportCommandResult{Status: model.OnboardingImportRowSucceeded, InvitationID: result.Invitation.ID}, nil
}

// ReplayIssue returns the retained outcome of an earlier Invitation issue
// command and completes the replay audit event; it never issues an Invitation
// itself.
func (s SQLInvitationStore) ReplayIssue(ctx context.Context, input *store.InvitationReplay, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	if input == nil {
		return nil, store.NewErrInvalidInput("invitation", "replay", nil)
	}
	auditID, auditAt := input.AuditEventID, input.AuditAt
	result, err := replayIdempotentMutation(ctx, s.SQLStore, "replay Invitation issue", idempotentMutation[*store.InvitationCommandResult]{
		command: command, auditEventID: auditID, decode: decodeInvitationCommandOutcome,
		hydrateReplay:     hydrateInvitationCommandOutcome,
		onboardingOutcome: invitationOnboardingOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationCommandResult, originalAuditID string) error {
			if value.Duplicate {
				return nil
			}
			if value.NoOp {
				return completeInvitationNoOpReplayAudit(ctx, tx, auditID, auditAt, value.Invitation, originalAuditID)
			}
			return completeInvitationReplayAudit(ctx, tx, auditID, auditAt, value.Invitation, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = true
	return result.Value, nil
}

func (s SQLInvitationStore) FindCommandOutcome(ctx context.Context, command *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	result, err := replayIdempotentMutation(ctx, s.SQLStore, "find Invitation issue outcome", idempotentMutation[*store.InvitationCommandResult]{
		command: command, decode: decodeInvitationCommandOutcome, hydrateReplay: hydrateInvitationCommandOutcome,
		onboardingOutcome: invitationOnboardingOutcome,
		freshAuditEventID: func(*store.InvitationCommandResult) (model.AuditEventID, error) { return "", nil },
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = true
	return result.Value, nil
}

// ReplayAdministration returns the retained outcome of an earlier Invitation
// administration command and completes the replay audit event; it never mutates
// the Invitation itself.
func (s SQLInvitationStore) ReplayAdministration(ctx context.Context, input *store.InvitationReplay, command *store.CommandIdempotency) (*store.InvitationAdministrationCommandResult, error) {
	if input == nil {
		return nil, store.NewErrInvalidInput("invitation", "replay", nil)
	}
	auditID, auditAt := input.AuditEventID, input.AuditAt
	result, err := replayIdempotentMutation(ctx, s.SQLStore, "replay Invitation administration", idempotentMutation[*store.InvitationAdministrationCommandResult]{
		command: command, auditEventID: auditID, decode: decodeInvitationAdministrationOutcome,
		hydrateReplay: hydrateInvitationAdministrationOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationAdministrationCommandResult, originalAuditID string) error {
			if value.Duplicate {
				return nil
			}
			return completeInvitationReplayAudit(ctx, tx, auditID, auditAt, value.Record.Invitation, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = true
	return result.Value, nil
}

func (s SQLInvitationStore) RecordBatchDuplicate(ctx context.Context, input *store.InvitationBatchDuplicate, command *store.CommandIdempotency) (*store.InvitationBatchCommandResult, error) {
	if err := validateInvitationBatchDuplicate(input, command); err != nil {
		return nil, store.NewErrInvalidInput("invitation", "batch_duplicate", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "record Invitation batch duplicate", idempotentMutation[*store.InvitationBatchCommandResult]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationBatchCommandResult, error) {
			if authorityErr := validateInvitationBatchDuplicateAuthority(ctx, tx, input, command); authorityErr != nil {
				return nil, authorityErr
			}
			if completeErr := completeInvitationBatchDuplicateAudit(ctx, tx, input, true, ""); completeErr != nil {
				return nil, completeErr
			}
			return &store.InvitationBatchCommandResult{Duplicate: true}, nil
		},
		encode: func(value *store.InvitationBatchCommandResult) ([]byte, error) {
			if value == nil || !value.Duplicate {
				return nil, store.NewErrInvalidInput("invitation", "batch_duplicate_outcome", nil)
			}
			return encodeOutcome(invitationCommandOutcome{Kind: invitationOutcomeKindDuplicate})
		},
		decode: func(data []byte) (*store.InvitationBatchCommandResult, error) {
			var outcome invitationCommandOutcome
			if err := decodeOutcome(data, &outcome); err != nil {
				return nil, err
			}
			switch outcome.Kind {
			case invitationOutcomeKindDuplicate:
				return &store.InvitationBatchCommandResult{Duplicate: true}, nil
			case invitationOutcomeKindInvitation:
				id, parseErr := model.ParseInvitationID(outcome.InvitationID)
				if parseErr != nil {
					return nil, invalidPersistedState("command_outcome", "batch_invitation_id", parseErr)
				}
				return &store.InvitationBatchCommandResult{InvitationID: id}, nil
			default:
				return nil, invalidPersistedState("command_outcome", "batch_kind", nil)
			}
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationBatchCommandResult, originalAuditID string) error {
			return completeInvitationBatchDuplicateAudit(ctx, tx, input, value.Duplicate, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func validateInvitationBatchDuplicate(input *store.InvitationBatchDuplicate, command *store.CommandIdempotency) error {
	if input == nil || command == nil || !input.ActorUserID.IsValid() || input.ActorUserID != command.UserID ||
		input.CanonicalOperation == "" || input.CanonicalOperation != command.Operation ||
		input.CanonicalKeyDigest == ([32]byte{}) || input.CanonicalKeyDigest == command.KeyDigest ||
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return store.NewErrInvalidInput("invitation", "batch_duplicate", nil)
	}
	hasCandidate := input.Candidate != nil
	hasLifecycle := input.LifecycleID.IsValid()
	if hasCandidate == hasLifecycle {
		return store.NewErrInvalidInput("invitation", "batch_duplicate_shape", nil)
	}
	if hasCandidate {
		if input.ExpectedRevision != 0 || input.Candidate.InviterUserID != input.ActorUserID ||
			input.Candidate.State != model.InvitationPending || input.Candidate.Validate() != nil {
			return store.NewErrInvalidInput("invitation", "batch_duplicate_candidate", nil)
		}
		return nil
	}
	if input.ExpectedRevision < 1 {
		return store.NewErrInvalidInput("invitation", "batch_duplicate_lifecycle", nil)
	}
	if input.CanonicalFingerprint == ([32]byte{}) {
		return store.NewErrInvalidInput("invitation", "batch_duplicate_canonical_fingerprint", nil)
	}
	return nil
}

func validateInvitationBatchDuplicateAuthority(ctx context.Context, tx *sqlxTxWrapper, input *store.InvitationBatchDuplicate, command *store.CommandIdempotency) error {
	at, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return err
	}
	if input.Candidate != nil {
		candidate := input.Candidate
		if !at.Before(candidate.ExpiresAt) || (candidate.IntendedEndsAt.Valid && !at.Before(candidate.IntendedEndsAt.Time)) {
			return store.NewErrConflict("invitation", "invitation_expired", nil)
		}
		switch candidate.Purpose {
		case model.InvitationPurposeStudentClass:
			if err = requireInvitationPolicy(ctx, tx); err == nil {
				err = validateStudentClassInvitationPackage(ctx, tx, candidate, at)
			}
			if err == nil {
				err = requireStudentClassInvitationAuthority(ctx, tx, candidate.InviterUserID, candidate.ClassID, at)
			}
		case model.InvitationPurposeTeacherAcademicUnit:
			if err = requireInvitationPolicy(ctx, tx); err == nil {
				err = validateTeacherAcademicUnitInvitationPackage(ctx, tx, candidate, at)
			}
			if err == nil {
				err = requireTeacherAcademicUnitInvitationAuthority(ctx, tx, candidate, at)
			}
		case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
			if err = requireExistingUserInvitationPolicy(ctx, tx); err == nil {
				err = validateScopedRoleInvitationPackage(ctx, tx, candidate, at)
			}
			if err == nil {
				err = requireScopedRoleInvitationAuthority(ctx, tx, candidate, at)
			}
		default:
			err = store.NewErrConflict("invitation", "invitation_purpose", nil)
		}
		return err
	}

	current, err := lockInvitation(ctx, tx, input.LifecycleID)
	if err != nil {
		return err
	}
	if err = requireInvitationManagementAuthority(ctx, tx, input.ActorUserID, current, at); err != nil {
		return err
	}
	var canonicalCommitted bool
	if err = tx.Get(ctx, &canonicalCommitted, `SELECT EXISTS(SELECT 1 FROM command_outcomes
		WHERE user_id=? AND operation=? AND key_digest=? AND fingerprint=?
		AND outcome->>'kind'='invitation' AND outcome->>'invitation_id'=?)`,
		command.UserID.String(), input.CanonicalOperation, input.CanonicalKeyDigest[:], input.CanonicalFingerprint[:], input.LifecycleID.String()); err != nil {
		return fmt.Errorf("find canonical Invitation batch outcome: %w", err)
	}
	if !canonicalCommitted {
		if current.Revision != input.ExpectedRevision || current.State != model.InvitationPending {
			return store.NewErrConflict("invitation", "invitation_revision", nil)
		}
		if !current.IsPendingAt(at) || (current.IntendedEndsAt.Valid && !at.Before(current.IntendedEndsAt.Time)) {
			return store.NewErrConflict("invitation", "invitation_lifecycle", nil)
		}
	}
	return nil
}

func completeInvitationBatchDuplicateAudit(ctx context.Context, tx *sqlxTxWrapper, input *store.InvitationBatchDuplicate, duplicate bool, originalAuditID string) error {
	status, code := model.AuditStatusSuccess, ""
	if duplicate {
		status, code = model.AuditStatusFail, "onboarding_batch.duplicate"
	}
	data := map[string]any{"batch_duplicate": duplicate}
	if model.AuditEventID(originalAuditID).IsValid() {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, input.AuditEventID, status, code, encoded, input.AuditAt)
	return err
}

func completeInvitationReplayAudit(ctx context.Context, tx *sqlxTxWrapper, auditID model.AuditEventID, at time.Time, invitation *model.Invitation, originalAuditID string) error {
	data := invitation.Auditable()
	data["idempotency_replayed"] = true
	data["original_audit_event_id"] = originalAuditID
	return completeInvitationLifecycleAudit(ctx, tx, auditID, at, data)
}

func completeInvitationNoOpReplayAudit(ctx context.Context, tx *sqlxTxWrapper, auditID model.AuditEventID, at time.Time, invitation *model.Invitation, originalAuditID string) error {
	data := map[string]any{"no_op": true, "idempotency_replayed": true, "original_audit_event_id": originalAuditID}
	if invitation != nil && invitation.ID.IsValid() {
		data["invitation_id"] = invitation.ID.String()
	}
	return completeInvitationLifecycleAudit(ctx, tx, auditID, at, data)
}

func completeInvitationNoOpAudit(ctx context.Context, tx *sqlxTxWrapper, auditID model.AuditEventID, at time.Time, invitation *model.Invitation) error {
	data := map[string]any{"no_op": true}
	if invitation != nil && invitation.ID.IsValid() {
		data["invitation_id"] = invitation.ID.String()
	}
	return completeInvitationLifecycleAudit(ctx, tx, auditID, at, data)
}
