// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type mailRekeyTargetRow struct {
	Kind             string    `db:"kind"`
	ID               string    `db:"id"`
	KeyID            string    `db:"payload_key_id"`
	EncryptedPayload jsonValue `db:"encrypted_payload"`
}

type mailRekeyCompletionRow struct {
	Type           string        `db:"type"`
	Status         string        `db:"status"`
	CommandVersion int           `db:"command_version"`
	Command        jsonValue     `db:"command"`
	ResultVersion  sql.NullInt64 `db:"result_version"`
	Result         jsonValue     `db:"result"`
}

func (s SQLMailStore) InspectKeyState(ctx context.Context) (*store.MailKeyState, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "mail key-state inspection", func(ctx context.Context, tx *sqlxTxWrapper) (*store.MailKeyState, error) {
		var fence struct {
			RequiredPrimaryKeyID sql.NullString `db:"required_primary_key_id"`
			ActiveJobID          sql.NullString `db:"active_rekey_job_id"`
			JobType              string         `db:"job_type"`
			JobStatus            string         `db:"job_status"`
			JobCommandVersion    int            `db:"job_command_version"`
			JobCommand           jsonValue      `db:"job_command"`
			JobResultVersion     sql.NullInt64  `db:"job_result_version"`
			JobResult            jsonValue      `db:"job_result"`
		}
		if err := tx.Get(ctx, &fence, `SELECT mail_key_state.required_primary_key_id, mail_key_state.active_rekey_job_id,
			COALESCE(jobs.type, '') AS job_type, COALESCE(jobs.status, '') AS job_status,
			COALESCE(jobs.command_version, 0) AS job_command_version, jobs.command AS job_command,
			jobs.result_version AS job_result_version, jobs.result AS job_result
			FROM mail_key_state LEFT JOIN jobs ON jobs.id = mail_key_state.active_rekey_job_id
			WHERE mail_key_state.singleton = TRUE FOR SHARE OF mail_key_state`); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, invalidPersistedState("mail_rekey", "key_state", errors.New("mail primary-key fence is missing"))
			}
			return nil, fmt.Errorf("inspect mail primary-key fence: %w", err)
		}
		rows := make([]struct {
			KeyID            string `db:"key_id"`
			ActiveReferences int64  `db:"active_references"`
		}, 0, 9)
		if err := tx.Select(ctx, &rows, `SELECT key_id, active_references FROM mail_payload_keys ORDER BY key_id LIMIT 10`); err != nil {
			return nil, fmt.Errorf("inspect active mail payload keys: %w", err)
		}
		if len(rows) > 9 {
			return nil, invalidPersistedState("mail_rekey", "key_usage", errors.New("active mail payload key ring exceeds its bound"))
		}
		state := &store.MailKeyState{RequiredPrimaryKeyID: fence.RequiredPrimaryKeyID.String, Active: make([]store.MailPayloadKeyUsage, 0, len(rows))}
		if state.RequiredPrimaryKeyID != "" && !validMailPayloadKeyID(state.RequiredPrimaryKeyID) {
			return nil, invalidPersistedState("mail_rekey", "required_primary_key_id", errors.New("required primary key identity is invalid"))
		}
		for _, row := range rows {
			if !validMailPayloadKeyID(row.KeyID) || row.ActiveReferences <= 0 {
				return nil, invalidPersistedState("mail_rekey", "key_usage", errors.New("active payload-key usage is invalid"))
			}
			state.Active = append(state.Active, store.MailPayloadKeyUsage{KeyID: row.KeyID, ActiveReferences: row.ActiveReferences})
		}
		completion := mailRekeyCompletionRow{Type: fence.JobType, Status: fence.JobStatus,
			CommandVersion: fence.JobCommandVersion, Command: fence.JobCommand,
			ResultVersion: fence.JobResultVersion, Result: fence.JobResult}
		if fence.ActiveJobID.Valid && state.RequiredPrimaryKeyID != "" && completedMailRekeyAllowsPromotion(completion, state.RequiredPrimaryKeyID) {
			state.PrimaryPromotionAllowed = true
			for _, usage := range state.Active {
				if usage.KeyID != state.RequiredPrimaryKeyID {
					state.PrimaryPromotionAllowed = false
					break
				}
			}
		}
		return state, nil
	})
}

func (s SQLMailStore) StartRekey(ctx context.Context, input *store.MailRekeyStart) (*store.MailRekeyOperation, error) {
	if err := validateMailRekeyStart(input); err != nil {
		return nil, err
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin,
		rawSQLTransactionPolicy[*store.MailRekeyOperation](true, func(_ *store.MailRekeyOperation, err error) error {
			return fmt.Errorf("commit mail rekey start: %w", err)
		}), func(ctx context.Context, tx *sqlxTxWrapper) (*store.MailRekeyOperation, error) {
			if _, err := tx.Exec(ctx, `INSERT INTO mail_key_state(singleton, required_primary_key_id, active_rekey_job_id, updated_at)
				VALUES (TRUE, NULL, NULL, ?) ON CONFLICT(singleton) DO NOTHING`, input.Job.CreatedAt); err != nil {
				return nil, fmt.Errorf("initialize mail key state: %w", err)
			}
			var state struct {
				RequiredPrimaryKeyID sql.NullString `db:"required_primary_key_id"`
				ActiveJobID          sql.NullString `db:"active_rekey_job_id"`
			}
			if err := tx.Get(ctx, &state, `SELECT required_primary_key_id, active_rekey_job_id FROM mail_key_state WHERE singleton = TRUE FOR UPDATE`); err != nil {
				return nil, fmt.Errorf("lock mail key state: %w", err)
			}
			var priorCompletion *mailRekeyCompletionRow
			if state.ActiveJobID.Valid {
				var completion mailRekeyCompletionRow
				if err := tx.Get(ctx, &completion, `SELECT type, status, command_version, command, result_version, result FROM jobs WHERE id = ?`, state.ActiveJobID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("read active mail rekey job: %w", err)
				}
				priorCompletion = &completion
				if completion.Status == string(model.JobStatusQueued) || completion.Status == string(model.JobStatusRunning) || completion.Status == string(model.JobStatusCancelRequested) {
					return nil, store.NewErrConflict("mail_rekey", "active_operation", nil)
				}
			}
			if state.RequiredPrimaryKeyID.Valid && state.RequiredPrimaryKeyID.String != input.PrimaryKeyID &&
				(priorCompletion == nil || !completedMailRekeyAllowsPromotion(*priorCompletion, state.RequiredPrimaryKeyID.String)) {
				return nil, store.NewErrConflict("mail_rekey", "primary_promotion_unproven", nil)
			}
			if state.RequiredPrimaryKeyID.Valid && state.RequiredPrimaryKeyID.String != input.PrimaryKeyID {
				var nonPrimaryReferences int64
				if err := tx.Get(ctx, &nonPrimaryReferences, `SELECT COALESCE(SUM(active_references) FILTER (WHERE key_id <> ?), 0)::bigint FROM mail_payload_keys`, state.RequiredPrimaryKeyID.String); err != nil {
					return nil, fmt.Errorf("verify completed mail rekey references: %w", err)
				}
				if nonPrimaryReferences != 0 {
					return nil, store.NewErrConflict("mail_rekey", "primary_promotion_unproven", nil)
				}
			}
			if _, err := insertQueuedJob(ctx, tx, input.Job, false); err != nil {
				return nil, fmt.Errorf("insert mail rekey job: %w", translateError("job", input.Job.ID.String(), err))
			}
			if _, err := tx.Exec(ctx, `UPDATE mail_key_state SET required_primary_key_id = ?, active_rekey_job_id = ?, updated_at = ? WHERE singleton = TRUE`,
				input.PrimaryKeyID, input.Job.ID.String(), input.Job.CreatedAt); err != nil {
				return nil, fmt.Errorf("fence mail primary key: %w", err)
			}
			if err := completeMailRekeyStartAudit(ctx, tx, input); err != nil {
				return nil, err
			}
			return &store.MailRekeyOperation{JobID: input.Job.ID, PrimaryKeyID: input.PrimaryKeyID,
				RetiringKeyID: input.RetiringKeyID, CreatedAt: input.Job.CreatedAt}, nil
		})
}

func completeMailRekeyStartAudit(ctx context.Context, tx *sqlxTxWrapper, input *store.MailRekeyStart) error {
	data, err := model.EncodeAuditData(map[string]any{"operation": "start_rekey", "job_id": input.Job.ID.String(),
		"primary_key_id": input.PrimaryKeyID, "retiring_key_id": input.RetiringKeyID})
	if err != nil {
		return store.NewErrInvalidInput("mail_rekey", "audit", nil).Wrap(err)
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
		return fmt.Errorf("complete mail rekey start audit: %w", err)
	}
	return nil
}

func completedMailRekeyAllowsPromotion(job mailRekeyCompletionRow, requiredPrimaryKeyID string) bool {
	if !validMailPayloadKeyID(requiredPrimaryKeyID) || job.Type != string(model.JobTypeMailRekey) ||
		job.Status != string(model.JobStatusSucceeded) || job.CommandVersion != 1 || !job.ResultVersion.Valid ||
		job.ResultVersion.Int64 != 1 {
		return false
	}
	var command struct {
		PrimaryKeyID  string `json:"primary_key_id"`
		RetiringKeyID string `json:"retiring_key_id"`
	}
	if decodeStrictMailRekeyCommand(json.RawMessage(job.Command), &command) != nil || command.PrimaryKeyID != requiredPrimaryKeyID ||
		!validMailPayloadKeyID(command.RetiringKeyID) || command.RetiringKeyID == command.PrimaryKeyID {
		return false
	}
	var proof struct {
		PrimaryKeyID         string `json:"primary_key_id"`
		RetiringKeyID        string `json:"retiring_key_id"`
		Processed            int64  `json:"processed"`
		Reencrypted          int64  `json:"reencrypted"`
		NonPrimaryReferences int64  `json:"non_primary_references"`
		RetiringReferences   int64  `json:"retiring_references"`
		RetirementSafe       bool   `json:"retirement_safe"`
	}
	return decodeStrictMailRekeyCommand(json.RawMessage(job.Result), &proof) == nil && proof.PrimaryKeyID == command.PrimaryKeyID &&
		proof.RetiringKeyID == command.RetiringKeyID && proof.Processed >= 0 && proof.Reencrypted >= 0 &&
		proof.Reencrypted <= proof.Processed && proof.NonPrimaryReferences == 0 && proof.RetiringReferences == 0 &&
		proof.RetirementSafe
}

func validateMailRekeyStart(input *store.MailRekeyStart) error {
	if input == nil || input.Job == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || !validMailPayloadKeyID(input.PrimaryKeyID) ||
		!validMailPayloadKeyID(input.RetiringKeyID) || input.PrimaryKeyID == input.RetiringKeyID ||
		input.Job.Validate() != nil || input.Job.Type != model.JobTypeMailRekey || input.Job.Status != model.JobStatusQueued ||
		input.Job.CommandVersion != 1 || input.Job.MaximumAttempts != 10 || input.Job.AttemptCount != 0 ||
		input.Job.DedupePolicy != model.JobDedupeActive || input.Job.Revision != 1 || input.Job.StartedAt.Valid ||
		input.Job.CompletedAt.Valid || len(input.Job.Checkpoint) != 0 || len(input.Job.Result) != 0 || input.Job.Progress != nil ||
		input.Job.WorkReserved != 0 {
		return store.NewErrInvalidInput("mail_rekey", "value", nil)
	}
	var command struct {
		PrimaryKeyID  string `json:"primary_key_id"`
		RetiringKeyID string `json:"retiring_key_id"`
	}
	if err := decodeStrictMailRekeyCommand(input.Job.Command, &command); err != nil || command.PrimaryKeyID != input.PrimaryKeyID ||
		command.RetiringKeyID != input.RetiringKeyID {
		return store.NewErrInvalidInput("mail_rekey", "command", err)
	}
	return nil
}

func (s SQLMailStore) ListRekeyTargets(ctx context.Context, request *store.MailRekeyTargetPageRequest) (*store.MailRekeyTargetPage, error) {
	if request == nil || !request.JobID.IsValid() || !validMailPayloadKeyID(request.PrimaryKeyID) || request.Limit < 1 || request.Limit > 500 ||
		(request.AfterKind == "") != (request.AfterID == "") ||
		(request.AfterKind != "" && (!validMailRekeyKind(request.AfterKind) || !model.IsValidId(request.AfterID))) {
		return nil, store.NewErrInvalidInput("mail_rekey", "page", nil)
	}
	rows := make([]mailRekeyTargetRow, 0, request.Limit+1)
	err := s.GetMaster().Select(ctx, &rows, `
		WITH targets AS (
			SELECT 'delivery'::text AS kind, id, payload_key_id, encrypted_payload
			FROM mail_deliveries
			WHERE encrypted_payload IS NOT NULL AND payload_key_id <> ?
			UNION ALL
			SELECT 'fanout_bundle'::text AS kind, id, payload_key_id, encrypted_payload
			FROM mail_fanout_bundles
			WHERE encrypted_payload IS NOT NULL AND payload_key_id <> ?
		)
		SELECT targets.kind, targets.id, targets.payload_key_id, targets.encrypted_payload
		FROM targets CROSS JOIN mail_key_state
		WHERE mail_key_state.singleton = TRUE AND mail_key_state.active_rekey_job_id = ?
			AND mail_key_state.required_primary_key_id = ?
			AND (targets.kind, targets.id) > (?, ?)
		ORDER BY targets.kind, targets.id
		LIMIT ?`, request.PrimaryKeyID, request.PrimaryKeyID, request.JobID.String(), request.PrimaryKeyID,
		string(request.AfterKind), request.AfterID, request.Limit+1)
	if err != nil {
		return nil, fmt.Errorf("list mail rekey targets: %w", err)
	}
	more := len(rows) > request.Limit
	if more {
		rows = rows[:request.Limit]
	}
	targets := make([]store.MailRekeyTarget, 0, len(rows))
	for _, row := range rows {
		kind := store.MailRekeyTargetKind(row.Kind)
		if !validMailRekeyKind(kind) || !model.IsValidId(row.ID) || !validMailPayloadKeyID(row.KeyID) || len(row.EncryptedPayload) == 0 {
			return nil, invalidPersistedState("mail_rekey", "target", errors.New("mail rekey target row is invalid"))
		}
		targets = append(targets, store.MailRekeyTarget{Kind: kind, ID: row.ID, KeyID: row.KeyID,
			EncryptedPayload: append(json.RawMessage(nil), row.EncryptedPayload...)})
	}
	return &store.MailRekeyTargetPage{Targets: targets, More: more}, nil
}

func (s SQLMailStore) ReplaceRekeyTarget(ctx context.Context, input *store.MailRekeyReplacement) (bool, error) {
	if input == nil || !input.JobID.IsValid() || !validMailRekeyKind(input.Kind) || !model.IsValidId(input.ID) ||
		!validMailPayloadKeyID(input.ExpectedKeyID) || !validMailPayloadKeyID(input.PrimaryKeyID) ||
		input.ExpectedKeyID == input.PrimaryKeyID || len(input.EncryptedPayload) == 0 || !json.Valid(input.EncryptedPayload) {
		return false, store.NewErrInvalidInput("mail_rekey", "replacement", nil)
	}
	keyID, err := mailPayloadKeyID(input.EncryptedPayload)
	if err != nil || keyID != input.PrimaryKeyID {
		return false, store.NewErrInvalidInput("mail_rekey", "encrypted_payload", err)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin,
		rawSQLTransactionPolicy[bool](true, func(_ bool, err error) error { return fmt.Errorf("commit mail rekey target: %w", err) }),
		func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
			var fenced bool
			if err := tx.Get(ctx, &fenced, `SELECT EXISTS (SELECT 1 FROM mail_key_state WHERE singleton = TRUE AND active_rekey_job_id = ? AND required_primary_key_id = ?)`, input.JobID.String(), input.PrimaryKeyID); err != nil {
				return false, fmt.Errorf("verify mail rekey fence: %w", err)
			}
			if !fenced {
				return false, store.NewErrConflict("mail_rekey", "stale_fence", nil)
			}
			table := "mail_deliveries"
			if input.Kind == store.MailRekeyTargetFanoutBundle {
				table = "mail_fanout_bundles"
			}
			var current sql.NullString
			if err := tx.Get(ctx, &current, `SELECT payload_key_id FROM `+table+` WHERE id = ? AND encrypted_payload IS NOT NULL FOR UPDATE`, input.ID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return false, nil
				}
				return false, fmt.Errorf("lock mail rekey target: %w", err)
			}
			if !current.Valid || current.String == input.PrimaryKeyID || current.String != input.ExpectedKeyID {
				return false, nil
			}
			result, err := tx.Exec(ctx, `UPDATE `+table+` SET payload_key_id = ?, encrypted_payload = ? WHERE id = ? AND payload_key_id = ? AND encrypted_payload IS NOT NULL`,
				input.PrimaryKeyID, input.EncryptedPayload, input.ID, input.ExpectedKeyID)
			if err != nil {
				return false, fmt.Errorf("replace mail rekey target: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				return false, invalidPersistedState("mail_rekey", "replacement", err)
			}
			if err = decrementMailPayloadKeyReference(ctx, tx, input.ExpectedKeyID); err != nil {
				return false, err
			}
			if err = incrementMailPayloadKeyReference(ctx, tx, input.PrimaryKeyID); err != nil {
				return false, err
			}
			return true, nil
		})
}

func (s SQLMailStore) ProveRekey(ctx context.Context, request *store.MailRekeyProofRequest) (*store.MailRekeyProof, error) {
	if request == nil || !request.JobID.IsValid() || !validMailPayloadKeyID(request.PrimaryKeyID) ||
		!validMailPayloadKeyID(request.RetiringKeyID) || request.PrimaryKeyID == request.RetiringKeyID {
		return nil, store.NewErrInvalidInput("mail_rekey", "proof", nil)
	}
	var proof struct {
		Fenced               bool  `db:"fenced"`
		NonPrimaryReferences int64 `db:"non_primary_references"`
		RetiringReferences   int64 `db:"retiring_references"`
	}
	err := s.GetMaster().Get(ctx, &proof, `
		SELECT
			EXISTS (SELECT 1 FROM mail_key_state WHERE singleton = TRUE AND active_rekey_job_id = ? AND required_primary_key_id = ?) AS fenced,
			COALESCE(SUM(active_references) FILTER (WHERE key_id <> ?), 0)::bigint AS non_primary_references,
			COALESCE(SUM(active_references) FILTER (WHERE key_id = ?), 0)::bigint AS retiring_references
		FROM mail_payload_keys`, request.JobID.String(), request.PrimaryKeyID, request.PrimaryKeyID, request.RetiringKeyID)
	if err != nil {
		return nil, fmt.Errorf("prove mail rekey: %w", err)
	}
	if !proof.Fenced {
		return nil, store.NewErrConflict("mail_rekey", "stale_fence", nil)
	}
	return &store.MailRekeyProof{PrimaryKeyID: request.PrimaryKeyID, RetiringKeyID: request.RetiringKeyID,
		NonPrimaryReferences: proof.NonPrimaryReferences, RetiringReferences: proof.RetiringReferences,
		RetirementSafe: proof.RetiringReferences == 0}, nil
}

func validMailPayloadKeyID(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validMailRekeyKind(kind store.MailRekeyTargetKind) bool {
	return kind == store.MailRekeyTargetDelivery || kind == store.MailRekeyTargetFanoutBundle
}

func decodeStrictMailRekeyCommand(document json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("mail rekey command contains trailing data")
	}
	return nil
}
