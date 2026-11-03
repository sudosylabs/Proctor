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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func insertExamSittingMailFanout(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingMailFanout,
	sitting *model.ExamSitting, priorClassID model.ClassID, actorID model.UserID, disabledEligibilityRevision int64,
) error {
	if err := validateExamSittingMailFanout(input, sitting, priorClassID, actorID); err != nil {
		return err
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return err
	}
	if err = supersedeActiveSittingMailFanouts(ctx, tx, sitting.ID, databaseNow); err != nil {
		return err
	}
	occurrence := *input.Occurrence
	occurrence.CreatedAt = databaseNow
	job := *input.ExpansionJob
	job.CreatedAt, job.UpdatedAt, job.AvailableAt = databaseNow, databaseNow, databaseNow
	deadline := databaseNow.Add(input.DeliveryLifetime)
	var bundleID any
	var completedAt any
	terminalReason := ""
	if input.Bundle == nil {
		if job.Status != model.JobStatusCanceled || !job.CompletedAt.Valid || disabledEligibilityRevision < 1 {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
		job.CompletedAt = model.OptionalTimeFrom(databaseNow)
		completedAt = databaseNow
		terminalReason = "suppressed_disabled"
	} else {
		if disabledEligibilityRevision != 0 || input.Bundle.Validate() != nil || input.Bundle.ID != occurrence.ID || job.Status != model.JobStatusQueued {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
		if bundleID, err = insertExamSittingMailFanoutBundle(ctx, tx, input.Bundle, databaseNow); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mail_occurrences(id,kind,template_key,actor_user_id,created_at) VALUES(?,?,?,?,?)`,
		occurrence.ID.String(), occurrence.Kind, occurrence.TemplateKey, occurrence.ActorUserID.String(), databaseNow); err != nil {
		return fmt.Errorf("insert Sitting mail occurrence: %w", translateError("mail_occurrence", occurrence.ID.String(), err))
	}
	if err = insertPreparedMailJob(ctx, tx, &job); err != nil {
		return fmt.Errorf("insert Sitting mail expansion Job: %w", translateError("job", job.ID.String(), err))
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_sitting_mail_fanouts
		(occurrence_id,bundle_id,exam_sitting_id,sitting_revision,prior_class_id,change_kind,created_at,deadline,completed_at,terminal_reason)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, occurrence.ID.String(), bundleID, sitting.ID.String(), sitting.Revision, nullableID(priorClassID.String()),
		input.ChangeKind, databaseNow, deadline, completedAt, terminalReason); err != nil {
		return fmt.Errorf("insert Sitting mail fan-out: %w", translateError("exam_sitting_mail_fanout", occurrence.ID.String(), err))
	}
	if terminalReason == "suppressed_disabled" {
		return recordDisabledExamSittingMailConvergence(ctx, tx, sitting, actorID, disabledEligibilityRevision)
	}
	result, updateErr := tx.Exec(ctx, `UPDATE exam_sittings SET mail_reconciliation_actor_user_id=? WHERE id=? AND revision=?`,
		actorID.String(), sitting.ID.String(), sitting.Revision)
	if updateErr != nil {
		return fmt.Errorf("record Sitting mail reconciliation actor: %w", updateErr)
	}
	affected, affectedErr := result.RowsAffected()
	if affectedErr != nil || affected != 1 {
		return store.NewErrConflict("exam_sitting", "exam_sitting_revision", affectedErr)
	}
	return nil
}

// validateExamSittingMailFanout checks that the prepared fan-out matches the
// Sitting's change kind, template, actor and expansion Job contract.
func validateExamSittingMailFanout(input *store.ExamSittingMailFanout, sitting *model.ExamSitting, priorClassID model.ClassID,
	actorID model.UserID,
) error {
	if input == nil {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	if sitting == nil || sitting.Validate() != nil || input.Occurrence == nil || input.ExpansionJob == nil ||
		input.DeliveryLifetime <= 0 || input.DeliveryLifetime > 72*time.Hour {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	wantKey := model.MailTemplateExamSittingScheduled
	switch input.ChangeKind {
	case store.ExamSittingMailScheduled:
		if sitting.Revision != 1 || sitting.State != model.ExamSittingScheduled {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	case store.ExamSittingMailRescheduled:
		wantKey = model.MailTemplateExamSittingRescheduled
		if sitting.State != model.ExamSittingScheduled || !priorClassID.IsValid() {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	case store.ExamSittingMailCanceled:
		wantKey = model.MailTemplateExamSittingCanceled
		if sitting.State != model.ExamSittingCanceled || !priorClassID.IsValid() {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	case store.ExamSittingMailReconciled:
		if sitting.State != model.ExamSittingScheduled || !priorClassID.IsValid() {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	default:
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	if input.Occurrence.Validate() != nil || input.Occurrence.Kind != model.MailOccurrenceSittingSchedule ||
		input.Occurrence.TemplateKey != wantKey || input.Occurrence.ActorUserID != actorID || input.ExpansionJob.Validate() != nil ||
		input.ExpansionJob.Type != model.JobTypeMailExpandSitting || input.ExpansionJob.DedupePolicy != model.JobDedupePermanent ||
		input.ExpansionJob.MaximumAttempts != model.MailMaximumAttempts {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	command, err := model.DecodeSittingMailExpansionCommand(input.ExpansionJob.Command)
	wantDedupe, keyErr := model.SittingMailExpansionDedupeKey(input.Occurrence.ID)
	if err != nil || keyErr != nil || command.OccurrenceID != input.Occurrence.ID || input.ExpansionJob.DedupeKey != wantDedupe {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", err)
	}
	return nil
}

// insertExamSittingMailFanoutBundle stores the encrypted fan-out bundle under
// its primary payload key and references that key, returning the bundle ID.
func insertExamSittingMailFanoutBundle(ctx context.Context, tx *sqlxTxWrapper, bundle *model.MailFanoutBundle,
	databaseNow time.Time,
) (any, error) {
	payloadKeyID, payloadErr := mailPayloadKeyID(bundle.EncryptedPayload)
	if payloadErr != nil {
		return nil, store.NewErrInvalidInput("mail_fanout_bundle", "encrypted_payload", payloadErr)
	}
	if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_fanout_bundles(id,payload_key_id,encrypted_payload,created_at,revision) VALUES(?,?,?,?,1)`,
		bundle.ID.String(), payloadKeyID, bundle.EncryptedPayload, databaseNow); err != nil {
		return nil, fmt.Errorf("insert Sitting mail fan-out bundle: %w", translateError("mail_fanout_bundle", bundle.ID.String(), err))
	}
	if err := incrementMailPayloadKeyReference(ctx, tx, payloadKeyID); err != nil {
		return nil, err
	}
	return bundle.ID.String(), nil
}

// recordDisabledExamSittingMailConvergence records the reconciliation actor
// and the Sitting, audience and eligibility revisions a disabled-mail
// suppression converged at.
func recordDisabledExamSittingMailConvergence(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting,
	actorID model.UserID, disabledEligibilityRevision int64,
) error {
	var audienceRevision int64
	if updateErr := tx.Get(ctx, &audienceRevision, `SELECT mail_audience_revision FROM classes WHERE id=? FOR SHARE`,
		sitting.ClassID.String()); updateErr != nil {
		return fmt.Errorf("lock disabled Sitting mail audience: %w", updateErr)
	}
	result, updateErr := tx.Exec(ctx, `UPDATE exam_sittings SET mail_reconciliation_actor_user_id=?,
		mail_disabled_suppressed_revision=?,mail_disabled_suppressed_audience_revision=?,
		mail_disabled_suppressed_eligibility_revision=? WHERE id=? AND revision=?`,
		actorID.String(), sitting.Revision, audienceRevision, disabledEligibilityRevision, sitting.ID.String(), sitting.Revision)
	if updateErr != nil {
		return fmt.Errorf("record disabled Sitting mail convergence: %w", updateErr)
	}
	affected, affectedErr := result.RowsAffected()
	if affectedErr != nil || affected != 1 {
		return store.NewErrConflict("exam_sitting", "exam_sitting_revision", affectedErr)
	}
	return nil
}

// lockDisabledExamSittingMailChronology establishes the canonical ordering for
// a disabled-mail terminal fan-out: active Manager User, installation mail
// eligibility singleton, then (at the caller) Class and hierarchy. The
// captured revision is persisted only after those later authority checks.
func lockDisabledExamSittingMailChronology(ctx context.Context, tx *sqlxTxWrapper, actorID model.UserID,
	input *store.ExamSittingMailFanout,
) (int64, error) {
	if input == nil || input.Bundle != nil {
		return 0, nil
	}
	var activeActorID string
	if err := tx.Get(ctx, &activeActorID, `SELECT id FROM users
		WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, actorID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, store.NewErrConflict("exam_sitting_mail", "actor_unavailable", nil)
		}
		return 0, fmt.Errorf("lock disabled Sitting mail actor: %w", err)
	}
	return currentUserMailEligibilityRevision(ctx, tx)
}

func supersedeActiveSittingMailFanouts(ctx context.Context, tx *sqlxTxWrapper, sittingID model.ExamSittingID, at time.Time) error {
	var rows []struct {
		OccurrenceID string         `db:"occurrence_id"`
		BundleID     sql.NullString `db:"bundle_id"`
		PayloadKeyID sql.NullString `db:"payload_key_id"`
		JobID        string         `db:"job_id"`
	}
	if err := tx.Select(ctx, &rows, `SELECT f.occurrence_id,f.bundle_id,b.payload_key_id,j.id job_id
		FROM exam_sitting_mail_fanouts f
		LEFT JOIN mail_fanout_bundles b ON b.id=f.bundle_id
		JOIN jobs j ON j.type='mail.expand_sitting' AND j.dedupe_key='sitting-mail:'||f.occurrence_id
		WHERE f.exam_sitting_id=? AND f.completed_at IS NULL ORDER BY f.occurrence_id FOR UPDATE OF f,j`, sittingID.String()); err != nil {
		return fmt.Errorf("lock superseded Sitting mail fan-outs: %w", err)
	}
	for _, row := range rows {
		jobID := model.JobID(row.JobID)
		job, err := getJob(ctx, tx, jobID, true)
		if err != nil {
			return err
		}
		if job.Status == model.JobStatusQueued || job.Status == model.JobStatusRunning {
			transitionAt := model.TimeUTC(at)
			if transitionAt.Before(job.UpdatedAt) {
				transitionAt = job.UpdatedAt
			}
			updated, cancelErr := job.RequestCancellation(transitionAt)
			if cancelErr != nil {
				return invalidPersistedState("job", "sitting_mail_supersession", cancelErr)
			}
			if err = updateJob(ctx, tx, updated); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_sitting_mail_fanouts SET bundle_id=NULL,completed_at=GREATEST(created_at,?),terminal_reason='superseded'
			WHERE occurrence_id=? AND completed_at IS NULL`, model.TimeUTC(at), row.OccurrenceID); err != nil {
			return fmt.Errorf("complete superseded Sitting mail fan-out: %w", err)
		}
		if row.BundleID.Valid {
			if _, err = tx.Exec(ctx, `DELETE FROM mail_fanout_bundles WHERE id=?`, row.BundleID.String); err != nil {
				return fmt.Errorf("destroy superseded Sitting mail bundle: %w", err)
			}
			if !row.PayloadKeyID.Valid {
				return invalidPersistedState("mail_fanout_bundle", "payload_key_id", errors.New("missing payload key"))
			}
			if err = decrementMailPayloadKeyReference(ctx, tx, row.PayloadKeyID.String); err != nil {
				return err
			}
		}
	}
	return nil
}
