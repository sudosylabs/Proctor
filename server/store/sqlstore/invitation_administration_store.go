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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s SQLInvitationStore) Get(ctx context.Context, id model.InvitationID) (*model.Invitation, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("invitation", "id", nil)
	}
	var row invitationRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE id=?`, id.String()); err != nil {
		return nil, translateError("invitation", id.String(), err)
	}
	return row.model()
}

func (s SQLInvitationStore) GetByClaimHash(ctx context.Context, claimHash string) (*model.Invitation, error) {
	if !model.IsValidTokenHash(claimHash) {
		return nil, store.NewErrInvalidInput("invitation", "claim_hash", nil)
	}
	var row invitationRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE claim_hash=?`, claimHash); err != nil {
		return nil, translateError("invitation", "claim", err)
	}
	return row.model()
}

func (s SQLInvitationStore) List(ctx context.Context, options store.InvitationListOptions) (*store.InvitationPage, error) {
	if err := validateInvitationListOptions(options); err != nil {
		return nil, err
	}
	rows, err := s.listAdministrationRows(ctx, options, model.InvitationID(""))
	if err != nil {
		return nil, err
	}
	more := len(rows) > options.Limit
	if more {
		rows = rows[:options.Limit]
	}
	items := make([]*store.InvitationAdministrationRecord, 0, len(rows))
	for index := range rows {
		record, recordErr := s.administrationRecord(ctx, rows[index])
		if recordErr != nil {
			return nil, recordErr
		}
		items = append(items, record)
	}
	return &store.InvitationPage{Items: items, More: more}, nil
}

func (s SQLInvitationStore) GetForAdministration(ctx context.Context, id model.InvitationID, visibility store.InvitationVisibilityScope) (*store.InvitationAdministrationRecord, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("invitation", "id", nil)
	}
	options := store.InvitationListOptions{Visibility: visibility, Limit: 1}
	if err := validateInvitationListOptions(options); err != nil {
		return nil, err
	}
	rows, err := s.listAdministrationRows(ctx, options, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, store.NewErrNotFound("invitation", id.String())
	}
	return s.administrationRecord(ctx, rows[0])
}

func validateInvitationListOptions(options store.InvitationListOptions) error {
	visibility := options.Visibility
	if options.Limit < 1 || options.Limit > 200 || len(visibility.AcademicUnitRootIDs) > 256 || len(visibility.ClassIDs) > 256 ||
		(!visibility.InstitutionWide && len(visibility.AcademicUnitRootIDs) == 0 && len(visibility.ClassIDs) == 0) ||
		!validVisibilityIDs(visibility.AcademicUnitRootIDs) || !validVisibilityIDs(visibility.ClassIDs) ||
		(options.Purpose != "" && !options.Purpose.IsValid()) || (options.State != "" && !options.State.IsValid()) ||
		(options.TargetEmail != "" && (!model.IsValidEmail(options.TargetEmail) || options.TargetEmail != strings.ToLower(strings.TrimSpace(options.TargetEmail)))) ||
		(options.TargetID != "" && !model.IsValidOpaqueID(options.TargetID)) ||
		(!options.CreatedAfter.IsZero() && !options.CreatedBefore.IsZero() && !options.CreatedBefore.After(options.CreatedAfter)) ||
		(options.BeforeID.IsValid() != !options.BeforeCreatedAt.IsZero()) {
		return store.NewErrInvalidInput("invitation", "list_options", nil)
	}
	return nil
}

func (s SQLInvitationStore) listAdministrationRows(ctx context.Context, options store.InvitationListOptions, exactID model.InvitationID) ([]invitationRow, error) {
	query := `SELECT ` + invitationColumns + ` FROM invitations WHERE `
	args := []any{}
	conditions := []string{}
	visibility := options.Visibility
	if !visibility.InstitutionWide {
		parts := []string{}
		if len(visibility.AcademicUnitRootIDs) > 0 {
			parts = append(parts, `(scope_type='academic_unit' AND EXISTS (
				WITH RECURSIVE descendants AS (
					SELECT id FROM academic_units WHERE id=ANY(?)
					UNION ALL SELECT child.id FROM academic_units child JOIN descendants parent ON child.parent_id=parent.id
				) SELECT 1 FROM descendants WHERE id=scope_id
			))`)
			args = append(args, pq.Array(idStrings(visibility.AcademicUnitRootIDs)))
			parts = append(parts, `(scope_type='class' AND EXISTS (
				WITH RECURSIVE descendants AS (
					SELECT id FROM academic_units WHERE id=ANY(?)
					UNION ALL SELECT child.id FROM academic_units child JOIN descendants parent ON child.parent_id=parent.id
				) SELECT 1 FROM classes c JOIN programme_levels pl ON pl.id=c.programme_level_id
				JOIN programmes p ON p.id=pl.programme_id WHERE c.id=scope_id AND p.academic_unit_id IN (SELECT id FROM descendants)
			))`)
			args = append(args, pq.Array(idStrings(visibility.AcademicUnitRootIDs)))
		}
		if len(visibility.ClassIDs) > 0 {
			parts = append(parts, `(scope_type='class' AND scope_id=ANY(?))`)
			args = append(args, pq.Array(idStrings(visibility.ClassIDs)))
		}
		conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
	}
	if exactID.IsValid() {
		conditions = append(conditions, "id=?")
		args = append(args, exactID.String())
	}
	if options.Purpose != "" {
		conditions = append(conditions, "purpose=?")
		args = append(args, string(options.Purpose))
	}
	if options.State != "" {
		conditions = append(conditions, "state=?")
		args = append(args, string(options.State))
	}
	if options.TargetEmail != "" {
		conditions = append(conditions, "target_email=?")
		args = append(args, options.TargetEmail)
	}
	if options.TargetID != "" {
		conditions = append(conditions, "(class_id=? OR academic_unit_id=? OR role_id=? OR scope_id=?)")
		args = append(args, options.TargetID, options.TargetID, options.TargetID, options.TargetID)
	}
	if !options.CreatedAfter.IsZero() {
		conditions = append(conditions, "created_at>=?")
		args = append(args, model.TimeUTC(options.CreatedAfter))
	}
	if !options.CreatedBefore.IsZero() {
		conditions = append(conditions, "created_at<?")
		args = append(args, model.TimeUTC(options.CreatedBefore))
	}
	if !options.BeforeCreatedAt.IsZero() {
		conditions = append(conditions, "(created_at<? OR (created_at=? AND id<?))")
		before := model.TimeUTC(options.BeforeCreatedAt)
		args = append(args, before, before, options.BeforeID.String())
	}
	if len(conditions) == 0 {
		conditions = append(conditions, "TRUE")
	}
	query += strings.Join(conditions, " AND ") + " ORDER BY created_at DESC,id DESC LIMIT ?"
	args = append(args, options.Limit+1)
	rows := []invitationRow{}
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Invitations: %w", err)
	}
	return rows, nil
}

func (s SQLInvitationStore) administrationRecord(ctx context.Context, row invitationRow) (*store.InvitationAdministrationRecord, error) {
	invitation, err := row.model()
	if err != nil {
		return nil, err
	}
	var delivery mailDeliveryRow
	err = s.GetMaster().Get(ctx, &delivery, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries
		WHERE target_invitation_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, invitation.ID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return &store.InvitationAdministrationRecord{Invitation: invitation}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get Invitation delivery summary: %w", err)
	}
	current, err := delivery.model()
	if err != nil {
		return nil, err
	}
	return &store.InvitationAdministrationRecord{Invitation: invitation, Delivery: invitationDeliverySummary(current)}, nil
}

func invitationDeliverySummary(delivery *model.MailDelivery) *store.InvitationDeliverySummary {
	if delivery == nil {
		return nil
	}
	return &store.InvitationDeliverySummary{TemplateKey: delivery.TemplateKey, State: delivery.State,
		MaskedRecipient: delivery.MaskedRecipient, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt,
		Deadline: delivery.Deadline, AcceptedAt: delivery.AcceptedAt, PublicFailureCode: delivery.PublicFailureCode}
}

func (s SQLInvitationStore) Resend(ctx context.Context, input *store.InvitationResend) (*store.InvitationAdministrationRecord, error) {
	if err := validateInvitationResend(input); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("invitation", "delivery_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "Invitation resend", func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationAdministrationRecord, error) {
		return resendInvitation(ctx, tx, input, payloadKeyID)
	})
}

func (s SQLInvitationStore) ResendIdempotently(ctx context.Context, input *store.InvitationResend, command *store.CommandIdempotency) (*store.InvitationAdministrationCommandResult, error) {
	if err := validateInvitationResend(input); err != nil || command == nil {
		return nil, store.NewErrInvalidInput("invitation", "idempotent_resend", err)
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("invitation", "delivery_payload", err)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent Invitation resend", idempotentMutation[*store.InvitationAdministrationCommandResult]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationAdministrationCommandResult, error) {
			value, executeErr := resendInvitation(ctx, tx, input, payloadKeyID)
			return &store.InvitationAdministrationCommandResult{Record: value}, executeErr
		},
		encode: encodeInvitationAdministrationOutcome, decode: decodeInvitationAdministrationOutcome,
		hydrateReplay: hydrateInvitationAdministrationOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationAdministrationCommandResult, originalAuditID string) error {
			if value.Duplicate {
				return nil
			}
			return completeInvitationReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Record.Invitation, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func resendInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.InvitationResend, payloadKeyID string) (*store.InvitationAdministrationRecord, error) {
	if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
		return nil, err
	}
	current, err := lockInvitation(ctx, tx, input.ID)
	if err != nil {
		return nil, err
	}
	if current.Revision != input.ExpectedRevision || current.State != model.InvitationPending {
		return nil, store.NewErrConflict("invitation", "invitation_revision", nil)
	}
	at, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = requireInvitationManagementAuthority(ctx, tx, input.ActorUserID, current, at); err != nil {
		return nil, err
	}
	if err = current.Resend(input.ClaimHash, invitationTransitionAt(at, current)); err != nil {
		return nil, store.NewErrConflict("invitation", "invitation_lifecycle", err)
	}
	occurrence, delivery, job, err := invitationMailAt(input.Occurrence, input.Delivery, input.DeliveryJob, at, current.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if err = updateInvitationLifecycle(ctx, tx, current, true); err != nil {
		return nil, err
	}
	if err = suppressInvitationCredentialMail(ctx, tx, current.ID, model.MailDeliveryObsoleteCode, at); err != nil {
		return nil, err
	}
	if err = insertInvitationMail(ctx, tx, occurrence, delivery, job, payloadKeyID); err != nil {
		return nil, err
	}
	if err = completeInvitationLifecycleAudit(ctx, tx, input.AuditEventID, input.AuditAt, current.Auditable()); err != nil {
		return nil, err
	}
	return &store.InvitationAdministrationRecord{Invitation: current, Delivery: invitationDeliverySummary(delivery)}, nil
}

func (s SQLInvitationStore) Revoke(ctx context.Context, input *store.InvitationRevocation) (*store.InvitationAdministrationRecord, error) {
	if input == nil || !input.ID.IsValid() || input.ExpectedRevision < 1 || !input.ActorUserID.IsValid() ||
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return nil, store.NewErrInvalidInput("invitation", "revocation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "Invitation revocation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationAdministrationRecord, error) {
		return revokeInvitation(ctx, tx, input)
	})
}

func (s SQLInvitationStore) RevokeIdempotently(ctx context.Context, input *store.InvitationRevocation, command *store.CommandIdempotency) (*store.InvitationAdministrationCommandResult, error) {
	if input == nil || !input.ID.IsValid() || input.ExpectedRevision < 1 || !input.ActorUserID.IsValid() ||
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() || command == nil {
		return nil, store.NewErrInvalidInput("invitation", "idempotent_revocation", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent Invitation revocation", idempotentMutation[*store.InvitationAdministrationCommandResult]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationAdministrationCommandResult, error) {
			value, executeErr := revokeInvitation(ctx, tx, input)
			return &store.InvitationAdministrationCommandResult{Record: value}, executeErr
		},
		encode: encodeInvitationAdministrationOutcome, decode: decodeInvitationAdministrationOutcome,
		hydrateReplay: hydrateInvitationAdministrationOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.InvitationAdministrationCommandResult, originalAuditID string) error {
			if value.Duplicate {
				return nil
			}
			return completeInvitationReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Record.Invitation, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func revokeInvitation(ctx context.Context, tx *sqlxTxWrapper, input *store.InvitationRevocation) (*store.InvitationAdministrationRecord, error) {
	current, err := lockInvitation(ctx, tx, input.ID)
	if err != nil {
		return nil, err
	}
	if current.Revision != input.ExpectedRevision || current.State != model.InvitationPending {
		return nil, store.NewErrConflict("invitation", "invitation_revision", nil)
	}
	at, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = requireInvitationManagementAuthority(ctx, tx, input.ActorUserID, current, at); err != nil {
		return nil, err
	}
	var accepted bool
	if err = tx.Get(ctx, &accepted, `SELECT EXISTS(SELECT 1 FROM mail_deliveries WHERE target_invitation_id=?
			AND template_key IN (?,?,?,?) AND state='accepted')`, current.ID.String(),
		string(model.MailTemplateAccessStudentClassInvitation), string(model.MailTemplateAccessTeacherAcademicUnitInvitation),
		string(model.MailTemplateAccessAcademicUnitRoleInvitation), string(model.MailTemplateAccessInstitutionRoleInvitation)); err != nil {
		return nil, fmt.Errorf("inspect accepted Invitation delivery: %w", err)
	}
	if err = current.Revoke(invitationTransitionAt(at, current)); err != nil {
		return nil, store.NewErrConflict("invitation", "invitation_lifecycle", err)
	}
	if err = updateInvitationLifecycle(ctx, tx, current, false); err != nil {
		return nil, err
	}
	if err = suppressInvitationCredentialMail(ctx, tx, current.ID, model.MailDeliveryObsoleteCode, at); err != nil {
		return nil, err
	}
	var summary *store.InvitationDeliverySummary
	if accepted {
		if err = validateInvitationRevocationNotice(input.RevocationNotice, current, input.ActorUserID); err != nil {
			return nil, err
		}
		payloadKeyID, payloadErr := mailPayloadKeyID(input.RevocationNotice.Delivery.EncryptedPayload)
		if payloadErr != nil {
			return nil, store.NewErrInvalidInput("invitation", "revocation_payload", payloadErr)
		}
		if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return nil, err
		}
		deadline := at.Add(24 * time.Hour)
		occurrence, delivery, job, mailErr := invitationMailAt(input.RevocationNotice.Occurrence, input.RevocationNotice.Delivery, input.RevocationNotice.Job, at, deadline)
		if mailErr != nil {
			return nil, mailErr
		}
		if err = insertInvitationMail(ctx, tx, occurrence, delivery, job, payloadKeyID); err != nil {
			return nil, err
		}
		summary = invitationDeliverySummary(delivery)
	}
	if err = completeInvitationLifecycleAudit(ctx, tx, input.AuditEventID, input.AuditAt, current.Auditable()); err != nil {
		return nil, err
	}
	return &store.InvitationAdministrationRecord{Invitation: current, Delivery: summary}, nil
}

func encodeInvitationAdministrationOutcome(result *store.InvitationAdministrationCommandResult) ([]byte, error) {
	if result == nil || result.Record == nil || result.Record.Invitation == nil || !result.Record.Invitation.ID.IsValid() {
		return nil, store.NewErrInvalidInput("invitation", "command_outcome", nil)
	}
	return encodeOutcome(invitationCommandOutcome{Kind: invitationOutcomeKindInvitation,
		InvitationID: result.Record.Invitation.ID.String(), Delivery: (*invitationDeliveryDocument)(result.Record.Delivery)})
}

func decodeInvitationAdministrationOutcome(data []byte) (*store.InvitationAdministrationCommandResult, error) {
	var outcome invitationCommandOutcome
	if err := decodeOutcome(data, &outcome); err != nil {
		return nil, err
	}
	if outcome.Kind == invitationOutcomeKindDuplicate {
		return &store.InvitationAdministrationCommandResult{Duplicate: true}, nil
	}
	id, err := model.ParseInvitationID(outcome.InvitationID)
	if err != nil || outcome.Kind != invitationOutcomeKindInvitation {
		return nil, invalidPersistedState("command_outcome", "invitation_administration", err)
	}
	return &store.InvitationAdministrationCommandResult{Record: &store.InvitationAdministrationRecord{
		Invitation: &model.Invitation{ID: id}, Delivery: (*store.InvitationDeliverySummary)(outcome.Delivery)}}, nil
}

func hydrateInvitationAdministrationOutcome(ctx context.Context, tx *sqlxTxWrapper, result *store.InvitationAdministrationCommandResult) (*store.InvitationAdministrationCommandResult, error) {
	if result == nil || result.Duplicate {
		return result, nil
	}
	invitation, err := getInvitationInTransaction(ctx, tx, result.Record.Invitation.ID)
	if err != nil {
		return nil, err
	}
	result.Record.Invitation = invitation
	return result, nil
}

func (s SQLInvitationStore) Replace(ctx context.Context, input *store.InvitationReplacement) (*store.InvitationAdministrationRecord, error) {
	if err := validateInvitationReplacement(input); err != nil {
		return nil, err
	}
	payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
	if err != nil {
		return nil, store.NewErrInvalidInput("invitation", "replacement_payload", err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "Invitation replacement", func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationAdministrationRecord, error) {
		if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return nil, err
		}
		current, err := lockInvitation(ctx, tx, input.CurrentID)
		if err != nil {
			return nil, err
		}
		if current.Revision != input.ExpectedRevision || current.State != model.InvitationPending {
			return nil, store.NewErrConflict("invitation", "invitation_revision", nil)
		}
		at, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		if err = requireInvitationManagementAuthority(ctx, tx, input.ActorUserID, current, at); err != nil {
			return nil, err
		}
		candidate := *input.Replacement
		candidate.RoleActions = append([]string(nil), input.Replacement.RoleActions...)
		candidate.CreatedAt, candidate.UpdatedAt, candidate.ExpiresAt = at, at, at.Add(input.Lifetime)
		candidate.Revision = 1
		if err = candidate.Validate(); err != nil {
			return nil, store.NewErrInvalidInput("invitation", "replacement", err)
		}
		if current.HasSamePackage(&candidate) {
			return nil, store.NewErrConflict("invitation", "invitation_no_changes", nil)
		}
		if err = validateReplacementPackage(ctx, tx, &candidate, at); err != nil {
			return nil, err
		}
		if err = requireReplacementAuthority(ctx, tx, &candidate, at); err != nil {
			return nil, err
		}
		if err = current.Supersede(invitationTransitionAt(at, current)); err != nil {
			return nil, store.NewErrConflict("invitation", "invitation_lifecycle", err)
		}
		if err = updateInvitationLifecycle(ctx, tx, current, false); err != nil {
			return nil, err
		}
		if err = suppressInvitationCredentialMail(ctx, tx, current.ID, model.MailDeliveryObsoleteCode, at); err != nil {
			return nil, err
		}
		occurrence, delivery, job, err := invitationMailAt(input.Occurrence, input.Delivery, input.DeliveryJob, at, candidate.ExpiresAt)
		if err != nil {
			return nil, err
		}
		if err = insertInvitation(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		if err = insertInvitationMail(ctx, tx, occurrence, delivery, job, payloadKeyID); err != nil {
			return nil, err
		}
		if err = completeInvitationLifecycleAudit(ctx, tx, input.CurrentAuditEventID, input.AuditAt, current.Auditable()); err != nil {
			return nil, err
		}
		if err = completeInvitationLifecycleAudit(ctx, tx, input.ReplacementAuditEventID, input.AuditAt, candidate.Auditable()); err != nil {
			return nil, err
		}
		return &store.InvitationAdministrationRecord{Invitation: &candidate, Delivery: invitationDeliverySummary(delivery)}, nil
	})
}

func lockInvitation(ctx context.Context, tx *sqlxTxWrapper, id model.InvitationID) (*model.Invitation, error) {
	var row invitationRow
	if err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE id=? FOR UPDATE`, id.String()); err != nil {
		return nil, translateError("invitation", id.String(), err)
	}
	return row.model()
}

func getInvitationInTransaction(ctx context.Context, tx *sqlxTxWrapper, id model.InvitationID) (*model.Invitation, error) {
	var row invitationRow
	if err := tx.Get(ctx, &row, `SELECT `+invitationColumns+` FROM invitations WHERE id=?`, id.String()); err != nil {
		return nil, translateError("invitation", id.String(), err)
	}
	return row.model()
}

func updateInvitationLifecycle(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, updateClaim bool) error {
	query := `UPDATE invitations SET state=?,updated_at=?,revision=?`
	args := []any{invitation.State, invitation.UpdatedAt, invitation.Revision}
	if updateClaim {
		query += `,claim_hash=?`
		args = append(args, invitation.ClaimHash)
	}
	query += ` WHERE id=? AND revision=? AND state='pending'`
	args = append(args, invitation.ID.String(), invitation.Revision-1)
	result, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update Invitation lifecycle: %w", translateError("invitation", invitation.ID.String(), err))
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return store.NewErrConflict("invitation", "invitation_revision", err)
	}
	return nil
}

func completeInvitationLifecycleAudit(ctx context.Context, tx *sqlxTxWrapper, id model.AuditEventID, at time.Time, value any) error {
	encoded, err := model.EncodeAuditData(value)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, id, model.AuditStatusSuccess, "", encoded, at); err != nil {
		return fmt.Errorf("complete Invitation lifecycle audit: %w", err)
	}
	return nil
}

func invitationMailAt(occurrence *model.MailOccurrence, source *model.MailDelivery, sourceJob *model.Job, at, deadline time.Time) (*model.MailOccurrence, *model.MailDelivery, *model.Job, error) {
	if occurrence == nil || source == nil || sourceJob == nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail", nil)
	}
	copyOccurrence := *occurrence
	copyOccurrence.CreatedAt = model.TimeUTC(at)
	delivery := source.Clone()
	delivery.CreatedAt, delivery.UpdatedAt, delivery.MessageDate, delivery.Deadline = model.TimeUTC(at), model.TimeUTC(at), model.TimeUTC(at), model.TimeUTC(deadline)
	job := *sourceJob
	job.CreatedAt, job.UpdatedAt, job.AvailableAt = model.TimeUTC(at), model.TimeUTC(at), model.TimeUTC(at)
	if copyOccurrence.Validate() != nil || delivery.Validate() != nil || job.Validate() != nil {
		return nil, nil, nil, store.NewErrInvalidInput("invitation", "mail_lifecycle", nil)
	}
	return &copyOccurrence, delivery, &job, nil
}

func validateInvitationResend(input *store.InvitationResend) error {
	if input == nil || !input.ID.IsValid() || input.ExpectedRevision < 1 || !model.IsValidTokenHash(input.ClaimHash) ||
		!input.ActorUserID.IsValid() || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() ||
		input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil {
		return store.NewErrInvalidInput("invitation", "resend", nil)
	}
	if input.Occurrence.Kind != model.MailOccurrenceInvitation || input.Occurrence.ActorUserID != input.ActorUserID ||
		input.Delivery.TargetInvitationID != input.ID || input.Delivery.TargetUserID.IsValid() ||
		input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		input.Delivery.TemplateKey != input.Occurrence.TemplateKey || input.DeliveryJob.Type != model.JobTypeMailDeliverCredential ||
		input.Delivery.State != model.MailDeliveryQueued || input.DeliveryJob.Status != model.JobStatusQueued {
		return store.NewErrInvalidInput("invitation", "resend_mail", nil)
	}
	return nil
}

func validateInvitationRevocationNotice(prepared *store.PreparedMail, invitation *model.Invitation, actor model.UserID) error {
	if prepared == nil || prepared.Occurrence == nil || prepared.Delivery == nil || prepared.Job == nil ||
		prepared.Occurrence.Kind != model.MailOccurrenceInvitation || prepared.Occurrence.TemplateKey != model.MailTemplateAccessInvitationRevoked ||
		prepared.Occurrence.ActorUserID != actor || prepared.Delivery.TargetInvitationID != invitation.ID ||
		prepared.Delivery.TargetUserID.IsValid() || prepared.Delivery.TemplateKey != model.MailTemplateAccessInvitationRevoked ||
		prepared.Delivery.OccurrenceID != prepared.Occurrence.ID || prepared.Delivery.JobID != prepared.Job.ID ||
		prepared.Job.Type != model.JobTypeMailDeliver ||
		(prepared.Delivery.State != model.MailDeliveryQueued && prepared.Delivery.State != model.MailDeliverySuppressed) {
		return store.NewErrInvalidInput("invitation", "revocation_notice", nil)
	}
	return nil
}

func validateInvitationReplacement(input *store.InvitationReplacement) error {
	if input == nil || !input.CurrentID.IsValid() || input.ExpectedRevision < 1 || input.Replacement == nil ||
		input.Lifetime != model.InvitationLifetime || !input.ActorUserID.IsValid() ||
		input.Replacement.InviterUserID != input.ActorUserID || input.Replacement.ID == input.CurrentID ||
		!input.CurrentAuditEventID.IsValid() || !input.ReplacementAuditEventID.IsValid() ||
		input.CurrentAuditEventID == input.ReplacementAuditEventID || input.AuditAt.IsZero() ||
		input.Occurrence == nil || input.Delivery == nil || input.DeliveryJob == nil ||
		input.Occurrence.Kind != model.MailOccurrenceInvitation || input.Occurrence.ActorUserID != input.ActorUserID ||
		input.Delivery.TargetInvitationID != input.Replacement.ID || input.Delivery.TargetUserID.IsValid() ||
		input.Delivery.OccurrenceID != input.Occurrence.ID || input.Delivery.JobID != input.DeliveryJob.ID ||
		input.Delivery.TemplateKey != input.Occurrence.TemplateKey || input.DeliveryJob.Type != model.JobTypeMailDeliverCredential ||
		input.Delivery.State != model.MailDeliveryQueued || input.DeliveryJob.Status != model.JobStatusQueued {
		return store.NewErrInvalidInput("invitation", "replacement", nil)
	}
	return nil
}

func validateReplacementPackage(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, at time.Time) error {
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		if err := requireInvitationPolicy(ctx, tx); err != nil {
			return err
		}
		return validateStudentClassInvitationPackage(ctx, tx, invitation, at)
	case model.InvitationPurposeTeacherAcademicUnit:
		if err := requireInvitationPolicy(ctx, tx); err != nil {
			return err
		}
		return validateTeacherAcademicUnitInvitationPackage(ctx, tx, invitation, at)
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		if err := requireExistingUserInvitationPolicy(ctx, tx); err != nil {
			return err
		}
		return validateScopedRoleInvitationPackage(ctx, tx, invitation, at)
	default:
		return store.NewErrInvalidInput("invitation", "replacement_purpose", nil)
	}
}

func requireReplacementAuthority(ctx context.Context, tx *sqlxTxWrapper, invitation *model.Invitation, at time.Time) error {
	switch invitation.Purpose {
	case model.InvitationPurposeStudentClass:
		return requireStudentClassInvitationAuthority(ctx, tx, invitation.InviterUserID, invitation.ClassID, at)
	case model.InvitationPurposeTeacherAcademicUnit:
		return requireTeacherAcademicUnitInvitationAuthority(ctx, tx, invitation, at)
	case model.InvitationPurposeAcademicUnitRole, model.InvitationPurposeInstitutionRole:
		return requireScopedRoleInvitationAuthority(ctx, tx, invitation, at)
	default:
		return store.NewErrInvalidInput("invitation", "replacement_purpose", nil)
	}
}

func requireInvitationManagementAuthority(ctx context.Context, tx *sqlxTxWrapper, actor model.UserID, invitation *model.Invitation, at time.Time) error {
	if !actor.IsValid() || invitation == nil {
		return store.NewErrInvalidInput("invitation", "management_authority", nil)
	}
	var active bool
	if err := tx.Get(ctx, &active, `SELECT true FROM users WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, actor.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("invitation", "invitation_authority", nil)
		}
		return fmt.Errorf("lock Invitation manager: %w", err)
	}
	var allowed bool
	var err error
	if invitation.ScopeType == model.RoleScopeAcademicUnit || invitation.ScopeType == model.RoleScopeClass {
		if err = lockAcademicUnitHierarchy(ctx, tx); err != nil {
			return err
		}
	}
	switch invitation.ScopeType {
	case model.RoleScopeInstitution:
		err = tx.Get(ctx, &allowed, `SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
			WHERE rb.user_id=? AND rb.archived_at IS NULL AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?)
			AND rb.scope_type='institution' AND rb.scope_id=? AND ?=ANY(r.permissions) LIMIT 1 FOR SHARE OF rb,r`,
			actor.String(), at, at, invitation.ScopeID, string(model.ActionInvitationManage))
	case model.RoleScopeAcademicUnit:
		err = tx.Get(ctx, &allowed, `WITH RECURSIVE ancestors AS (
			SELECT id,parent_id FROM academic_units WHERE id=? AND archived_at IS NULL
			UNION ALL SELECT au.id,au.parent_id FROM academic_units au JOIN ancestors child ON au.id=child.parent_id WHERE au.archived_at IS NULL)
			SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
			WHERE rb.user_id=? AND rb.archived_at IS NULL AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?)
			AND ?=ANY(r.permissions) AND (rb.scope_type='institution' OR (rb.scope_type='academic_unit' AND rb.scope_id IN (SELECT id FROM ancestors)))
			LIMIT 1 FOR SHARE OF rb,r`, invitation.ScopeID, actor.String(), at, at, string(model.ActionInvitationManage))
	case model.RoleScopeClass:
		err = tx.Get(ctx, &allowed, `WITH RECURSIVE class_unit AS (
			SELECT p.academic_unit_id FROM classes c JOIN programme_levels pl ON pl.id=c.programme_level_id
			JOIN programmes p ON p.id=pl.programme_id WHERE c.id=?), ancestors AS (
			SELECT academic_unit_id id FROM class_unit UNION ALL SELECT au.parent_id FROM academic_units au JOIN ancestors child ON au.id=child.id
			WHERE au.parent_id IS NOT NULL AND au.archived_at IS NULL)
			SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
			WHERE rb.user_id=? AND rb.archived_at IS NULL AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?)
			AND ?=ANY(r.permissions) AND (rb.scope_type='institution' OR (rb.scope_type='class' AND rb.scope_id=?)
			OR (rb.scope_type='academic_unit' AND rb.scope_id IN (SELECT id FROM ancestors))) LIMIT 1 FOR SHARE OF rb,r`,
			invitation.ScopeID, actor.String(), at, at, string(model.ActionInvitationManage), invitation.ScopeID)
	default:
		return store.NewErrInvalidInput("invitation", "scope", nil)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.NewErrConflict("invitation", "invitation_authority", nil)
	}
	if err != nil {
		return fmt.Errorf("lock Invitation management authority: %w", err)
	}
	return nil
}

func (s SQLInvitationStore) Maintain(ctx context.Context, limit int) (*store.InvitationMaintenanceResult, error) {
	if limit < 1 || limit > 500 {
		return nil, store.NewErrInvalidInput("invitation", "maintenance_limit", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "invitation maintenance", func(ctx context.Context, tx *sqlxTxWrapper) (*store.InvitationMaintenanceResult, error) {
		now, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		rows := make([]invitationRow, 0, limit)
		if err = tx.Select(ctx, &rows, `SELECT `+invitationColumns+` FROM invitations
			WHERE state='pending' AND (expires_at<=? OR (intended_end_at IS NOT NULL AND intended_end_at<=?))
			ORDER BY LEAST(expires_at,COALESCE(intended_end_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT ?`, now, now, limit); err != nil {
			return nil, fmt.Errorf("lock elapsed Invitations: %w", err)
		}
		for index := range rows {
			invitation, modelErr := rows[index].model()
			if modelErr != nil {
				return nil, modelErr
			}
			if modelErr = invitation.Expire(invitationTransitionAt(now, invitation)); modelErr != nil {
				return nil, invalidPersistedState("invitation", "expiry", modelErr)
			}
			if err = updateExpiredInvitation(ctx, tx, invitation); err != nil {
				return nil, err
			}
			if err = suppressInvitationCredentialMail(ctx, tx, invitation.ID, model.MailDeliveryObsoleteCode, now); err != nil {
				return nil, err
			}
		}
		cutoff := now.Add(-invitationTerminalRetention)
		purged, err := tx.Exec(ctx, `WITH candidates AS (
			SELECT id FROM invitations WHERE state<>'pending' AND updated_at<=?
			  AND NOT EXISTS (SELECT 1 FROM browser_authentication_transactions bat WHERE bat.invitation_id=invitations.id)
			 ORDER BY updated_at,id FOR UPDATE SKIP LOCKED LIMIT ?)
			DELETE FROM invitations i USING candidates c WHERE i.id=c.id`, cutoff, limit)
		if err != nil {
			return nil, fmt.Errorf("purge Invitations: %w", err)
		}
		purgedCount, err := purged.RowsAffected()
		if err != nil {
			return nil, err
		}
		var more bool
		if err = tx.Get(ctx, &more, `SELECT EXISTS(SELECT 1 FROM invitations WHERE
			(state='pending' AND (expires_at<=? OR (intended_end_at IS NOT NULL AND intended_end_at<=?))) OR
			(state<>'pending' AND updated_at<=? AND NOT EXISTS (
				SELECT 1 FROM browser_authentication_transactions bat WHERE bat.invitation_id=invitations.id)))`, now, now, cutoff); err != nil {
			return nil, fmt.Errorf("inspect Invitation maintenance continuation: %w", err)
		}
		return &store.InvitationMaintenanceResult{Expired: len(rows), Purged: int(purgedCount), More: more}, nil
	})
}

func (row invitationRow) model() (*model.Invitation, error) {
	invitation := &model.Invitation{
		ID: model.InvitationID(row.ID), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		Revision: row.Revision, Purpose: row.Purpose, State: row.State, TargetEmail: row.TargetEmail,
		ClassID: model.ClassID(row.ClassID.String), AcademicPeriodID: model.AcademicPeriodID(row.AcademicPeriodID.String),
		AcademicUnitID: model.AcademicUnitID(row.AcademicUnitID.String), RoleID: model.RoleID(row.RoleID.String),
		RoleActions:      append([]string(nil), row.RoleActions...),
		IntendedStartsAt: model.TimeUTC(row.IntendedStartsAt), IntendedEndsAt: optionalTime(row.IntendedEndsAt),
		Suggestions:   model.InvitationProfileSuggestions{Username: row.SuggestedUsername, DisplayName: row.SuggestedDisplayName, FirstName: row.SuggestedFirstName, LastName: row.SuggestedLastName, Locale: row.SuggestedLocale, Timezone: row.SuggestedTimezone},
		InviterUserID: model.UserID(row.InviterUserID), ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		ClaimHash: row.ClaimHash, ExpiresAt: model.TimeUTC(row.ExpiresAt), AcceptedAt: optionalTime(row.AcceptedAt),
		AcceptedUserID: model.UserID(row.AcceptedUserID.String), AcceptedAffiliationID: model.AffiliationID(row.AcceptedAffiliationID.String), AcceptedClassMemberID: model.ClassMemberID(row.AcceptedClassMemberID.String),
		AcceptedAcademicUnitMemberID: model.AcademicUnitMemberID(row.AcceptedAcademicUnitMemberID.String), AcceptedRoleBindingID: model.RoleBindingID(row.AcceptedRoleBindingID.String),
	}
	if err := invitation.Validate(); err != nil {
		return nil, invalidPersistedState("invitation", "value", err)
	}
	return invitation, nil
}
