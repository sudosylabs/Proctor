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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// mfaRekeyMaximumAttempts is the attempt budget StartRekey requires of the
// queued Job, matching the handler descriptor.
const mfaRekeyMaximumAttempts = 10

type mfaRekeyCompletionRow struct {
	Type    string    `db:"type"`
	Status  string    `db:"status"`
	Command jsonValue `db:"command"`
	Result  jsonValue `db:"result"`
}

// mfaRekeyCommandDocument and mfaRekeyResultDocument read the Job documents
// the rekey handler writes, strictly, to decide whether a completed rekey
// proves its primary.
type mfaRekeyCommandDocument struct {
	PrimaryKeyID  string `json:"primary_key_id"`
	RetiringKeyID string `json:"retiring_key_id"`
}

type mfaRekeyResultDocument struct {
	PrimaryKeyID         string `json:"primary_key_id"`
	RetiringKeyID        string `json:"retiring_key_id"`
	Processed            int64  `json:"processed"`
	Resealed             int64  `json:"resealed"`
	NonPrimaryReferences int64  `json:"non_primary_references"`
	RetiringReferences   int64  `json:"retiring_references"`
	RetirementSafe       bool   `json:"retirement_safe"`
}

// requireMFASecretPrimary takes a shared lock on the MFA primary-key fence for
// the caller's transaction and refuses a secret sealed with another key once
// the fence is set. StartRekey takes the exclusive lock, so every credential
// write is ordered wholly before or wholly after the fence moves.
func requireMFASecretPrimary(ctx context.Context, tx sqlxExecutor, keyID string) error {
	var requiredPrimary sql.NullString
	if err := tx.Get(ctx, &requiredPrimary, `SELECT required_primary_key_id FROM mfa_key_state WHERE singleton = TRUE FOR SHARE`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalidPersistedState("mfa_rekey", "key_state", errors.New("MFA primary-key fence is missing"))
		}
		return fmt.Errorf("lock MFA primary-key fence: %w", err)
	}
	if requiredPrimary.Valid && requiredPrimary.String != keyID {
		return store.NewErrConflict("mfa_credential", "stale_primary_key", nil)
	}
	return nil
}

func (s SQLMFAStore) InspectKeyState(ctx context.Context) (*store.MFAKeyState, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "MFA key-state inspection", func(ctx context.Context, tx *sqlxTxWrapper) (*store.MFAKeyState, error) {
		var fence struct {
			RequiredPrimaryKeyID sql.NullString `db:"required_primary_key_id"`
			ActiveJobID          sql.NullString `db:"active_rekey_job_id"`
		}
		if err := tx.Get(ctx, &fence, `SELECT required_primary_key_id, active_rekey_job_id FROM mfa_key_state WHERE singleton = TRUE FOR SHARE`); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, invalidPersistedState("mfa_rekey", "key_state", errors.New("MFA primary-key fence is missing"))
			}
			return nil, fmt.Errorf("inspect MFA primary-key fence: %w", err)
		}
		state := &store.MFAKeyState{RequiredPrimaryKeyID: fence.RequiredPrimaryKeyID.String, ActiveRekeyJobID: model.JobID(fence.ActiveJobID.String)}
		if state.RequiredPrimaryKeyID != "" && !validMFAKeyID(state.RequiredPrimaryKeyID) {
			return nil, invalidPersistedState("mfa_rekey", "required_primary_key_id", errors.New("required primary key identity is invalid"))
		}
		usage, err := listMFASecretKeyUsage(ctx, tx)
		if err != nil {
			return nil, err
		}
		state.Active = usage
		return state, nil
	})
}

func (s SQLMFAStore) StartRekey(ctx context.Context, input *store.MFARekeyStart) (*store.MFARekeyOperation, error) {
	if err := validateMFARekeyStart(input); err != nil {
		return nil, err
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin,
		rawSQLTransactionPolicy[*store.MFARekeyOperation](true, func(_ *store.MFARekeyOperation, err error) error {
			return fmt.Errorf("commit MFA rekey start: %w", err)
		}), func(ctx context.Context, tx *sqlxTxWrapper) (*store.MFARekeyOperation, error) {
			var state struct {
				RequiredPrimaryKeyID sql.NullString `db:"required_primary_key_id"`
				ActiveJobID          sql.NullString `db:"active_rekey_job_id"`
			}
			if err := tx.Get(ctx, &state, `SELECT required_primary_key_id, active_rekey_job_id FROM mfa_key_state WHERE singleton = TRUE FOR UPDATE`); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, invalidPersistedState("mfa_rekey", "key_state", errors.New("MFA primary-key fence is missing"))
				}
				return nil, fmt.Errorf("lock MFA key state: %w", err)
			}
			var priorCompletion *mfaRekeyCompletionRow
			if state.ActiveJobID.Valid {
				var completion mfaRekeyCompletionRow
				if err := tx.Get(ctx, &completion, `SELECT type, status, command, result FROM jobs WHERE id = ?`, state.ActiveJobID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("read active MFA rekey job: %w", err)
				}
				priorCompletion = &completion
				if completion.Status == string(model.JobStatusQueued) || completion.Status == string(model.JobStatusRunning) || completion.Status == string(model.JobStatusCancelRequested) {
					return nil, store.NewErrConflict("mfa_rekey", "active_operation", nil)
				}
			}
			// The fence moves to a new primary only after the rekey that
			// installed the current one proved no live secret needs another key.
			if state.RequiredPrimaryKeyID.Valid && state.RequiredPrimaryKeyID.String != input.PrimaryKeyID {
				if priorCompletion == nil || !completedMFARekeyProves(*priorCompletion, state.RequiredPrimaryKeyID.String) {
					return nil, store.NewErrConflict("mfa_rekey", "primary_promotion_unproven", nil)
				}
				var nonPrimary int64
				if err := tx.Get(ctx, &nonPrimary, `SELECT COUNT(*) FROM mfa_credentials WHERE archived_at IS NULL AND encryption_key_id <> ?`, state.RequiredPrimaryKeyID.String); err != nil {
					return nil, fmt.Errorf("verify completed MFA rekey references: %w", err)
				}
				if nonPrimary != 0 {
					return nil, store.NewErrConflict("mfa_rekey", "primary_promotion_unproven", nil)
				}
			}
			if _, err := insertQueuedJob(ctx, tx, input.Job, false); err != nil {
				return nil, fmt.Errorf("insert MFA rekey job: %w", translateError("job", input.Job.ID.String(), err))
			}
			if _, err := tx.Exec(ctx, `UPDATE mfa_key_state SET required_primary_key_id = ?, active_rekey_job_id = ?, updated_at = GREATEST(updated_at, ?) WHERE singleton = TRUE`,
				input.PrimaryKeyID, input.Job.ID.String(), input.Job.CreatedAt); err != nil {
				return nil, fmt.Errorf("fence MFA primary key: %w", err)
			}
			data, err := model.EncodeAuditData(map[string]any{"operation": "start_rekey", "job_id": input.Job.ID.String(),
				"primary_key_id": input.PrimaryKeyID, "retiring_key_id": input.RetiringKeyID})
			if err != nil {
				return nil, store.NewErrInvalidInput("mfa_rekey", "audit", nil).Wrap(err)
			}
			if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
				return nil, fmt.Errorf("complete MFA rekey start audit: %w", err)
			}
			return &store.MFARekeyOperation{JobID: input.Job.ID, PrimaryKeyID: input.PrimaryKeyID,
				RetiringKeyID: input.RetiringKeyID, CreatedAt: input.Job.CreatedAt}, nil
		})
}

func completedMFARekeyProves(job mfaRekeyCompletionRow, requiredPrimaryKeyID string) bool {
	if !validMFAKeyID(requiredPrimaryKeyID) || job.Type != string(model.JobTypeMFARekey) ||
		job.Status != string(model.JobStatusSucceeded) || len(job.Result) == 0 {
		return false
	}
	var command mfaRekeyCommandDocument
	if decodeStrictRekeyDocument(json.RawMessage(job.Command), &command) != nil || command.PrimaryKeyID != requiredPrimaryKeyID ||
		!validMFAKeyID(command.RetiringKeyID) || command.RetiringKeyID == command.PrimaryKeyID {
		return false
	}
	var proof mfaRekeyResultDocument
	return decodeStrictRekeyDocument(json.RawMessage(job.Result), &proof) == nil && proof.PrimaryKeyID == command.PrimaryKeyID &&
		proof.RetiringKeyID == command.RetiringKeyID && proof.Processed >= 0 && proof.Resealed >= 0 &&
		proof.Resealed <= proof.Processed && proof.NonPrimaryReferences == 0 && proof.RetiringReferences == 0 &&
		proof.RetirementSafe
}

func validateMFARekeyStart(input *store.MFARekeyStart) error {
	if input == nil || input.Job == nil || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() || !validMFAKeyID(input.PrimaryKeyID) ||
		!validMFAKeyID(input.RetiringKeyID) || input.PrimaryKeyID == input.RetiringKeyID ||
		input.Job.Validate() != nil || input.Job.Type != model.JobTypeMFARekey || input.Job.Status != model.JobStatusQueued ||
		input.Job.MaximumAttempts != mfaRekeyMaximumAttempts || input.Job.AttemptCount != 0 ||
		input.Job.DedupePolicy != model.JobDedupeActive || input.Job.Revision != 1 || input.Job.StartedAt.Valid ||
		input.Job.CompletedAt.Valid || len(input.Job.Checkpoint) != 0 || len(input.Job.Result) != 0 || input.Job.Progress != nil ||
		input.Job.WorkReserved != 0 {
		return store.NewErrInvalidInput("mfa_rekey", "value", nil)
	}
	var command mfaRekeyCommandDocument
	if err := decodeStrictRekeyDocument(input.Job.Command, &command); err != nil || command.PrimaryKeyID != input.PrimaryKeyID ||
		command.RetiringKeyID != input.RetiringKeyID {
		return store.NewErrInvalidInput("mfa_rekey", "command", err)
	}
	return nil
}

func (s SQLMFAStore) ListRekeyTargets(ctx context.Context, request *store.MFARekeyTargetPageRequest) (*store.MFARekeyTargetPage, error) {
	if request == nil || !request.JobID.IsValid() || !validMFAKeyID(request.PrimaryKeyID) || request.Limit < 1 || request.Limit > 500 ||
		(!request.AfterCredentialID.IsZero() && !request.AfterCredentialID.IsValid()) {
		return nil, store.NewErrInvalidInput("mfa_rekey", "page", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "MFA rekey target page", func(ctx context.Context, tx *sqlxTxWrapper) (*store.MFARekeyTargetPage, error) {
		if err := requireMFARekeyFence(ctx, tx, request.JobID, request.PrimaryKeyID); err != nil {
			return nil, err
		}
		rows := make([]struct {
			ID              string `db:"id"`
			UserID          string `db:"user_id"`
			KeyID           string `db:"encryption_key_id"`
			EncryptedSecret string `db:"encrypted_secret"`
		}, 0, request.Limit+1)
		if err := tx.Select(ctx, &rows, `SELECT id, user_id, encryption_key_id, encrypted_secret FROM mfa_credentials
			WHERE archived_at IS NULL AND encryption_key_id <> ? AND id > ? ORDER BY id LIMIT ?`,
			request.PrimaryKeyID, request.AfterCredentialID.String(), request.Limit+1); err != nil {
			return nil, fmt.Errorf("list MFA rekey targets: %w", err)
		}
		page := &store.MFARekeyTargetPage{More: len(rows) > request.Limit}
		rows = rows[:min(len(rows), request.Limit)]
		page.Targets = make([]store.MFARekeyTarget, 0, len(rows))
		for _, row := range rows {
			target := store.MFARekeyTarget{CredentialID: model.MFACredentialID(row.ID), UserID: model.UserID(row.UserID),
				KeyID: strings.TrimSpace(row.KeyID), EncryptedSecret: row.EncryptedSecret}
			if !target.CredentialID.IsValid() || !target.UserID.IsValid() || !validMFAKeyID(target.KeyID) || target.EncryptedSecret == "" {
				return nil, invalidPersistedState("mfa_rekey", "target", errors.New("MFA rekey target row is invalid"))
			}
			page.Targets = append(page.Targets, target)
		}
		return page, nil
	})
}

func (s SQLMFAStore) ReplaceRekeyTarget(ctx context.Context, input *store.MFARekeyReplacement) (bool, error) {
	if input == nil || !input.JobID.IsValid() || !input.CredentialID.IsValid() ||
		!validMFAKeyID(input.ExpectedKeyID) || !validMFAKeyID(input.PrimaryKeyID) ||
		input.ExpectedKeyID == input.PrimaryKeyID || input.EncryptedSecret == "" || len(input.EncryptedSecret) > 4096 {
		return false, store.NewErrInvalidInput("mfa_rekey", "replacement", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin,
		rawSQLTransactionPolicy[bool](true, func(_ bool, err error) error { return fmt.Errorf("commit MFA rekey target: %w", err) }),
		func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
			if err := requireMFARekeyFence(ctx, tx, input.JobID, input.PrimaryKeyID); err != nil {
				return false, err
			}
			// The key identity is the compare-and-swap fence: a credential a
			// User archived, replaced, or another pass re-sealed is skipped.
			result, err := tx.Exec(ctx, `UPDATE mfa_credentials SET encrypted_secret = ?, encryption_key_id = ?
				WHERE id = ? AND encryption_key_id = ? AND archived_at IS NULL`,
				input.EncryptedSecret, input.PrimaryKeyID, input.CredentialID.String(), input.ExpectedKeyID)
			if err != nil {
				return false, fmt.Errorf("replace MFA rekey target: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil || affected > 1 {
				return false, invalidPersistedState("mfa_rekey", "replacement", err)
			}
			return affected == 1, nil
		})
}

func (s SQLMFAStore) ProveRekey(ctx context.Context, request *store.MFARekeyProofRequest) (*store.MFARekeyProof, error) {
	if request == nil || !request.JobID.IsValid() || !validMFAKeyID(request.PrimaryKeyID) ||
		!validMFAKeyID(request.RetiringKeyID) || request.PrimaryKeyID == request.RetiringKeyID {
		return nil, store.NewErrInvalidInput("mfa_rekey", "proof", nil)
	}
	var proof struct {
		Fenced               bool  `db:"fenced"`
		NonPrimaryReferences int64 `db:"non_primary_references"`
		RetiringReferences   int64 `db:"retiring_references"`
	}
	err := s.GetMaster().Get(ctx, &proof, `
		SELECT
			EXISTS (SELECT 1 FROM mfa_key_state WHERE singleton = TRUE AND active_rekey_job_id = ? AND required_primary_key_id = ?) AS fenced,
			COUNT(*) FILTER (WHERE encryption_key_id <> ?) AS non_primary_references,
			COUNT(*) FILTER (WHERE encryption_key_id = ?) AS retiring_references
		FROM mfa_credentials WHERE archived_at IS NULL`, request.JobID.String(), request.PrimaryKeyID, request.PrimaryKeyID, request.RetiringKeyID)
	if err != nil {
		return nil, fmt.Errorf("prove MFA rekey: %w", err)
	}
	if !proof.Fenced {
		return nil, store.NewErrConflict("mfa_rekey", "stale_fence", nil)
	}
	return &store.MFARekeyProof{PrimaryKeyID: request.PrimaryKeyID, RetiringKeyID: request.RetiringKeyID,
		NonPrimaryReferences: proof.NonPrimaryReferences, RetiringReferences: proof.RetiringReferences,
		RetirementSafe: proof.RetiringReferences == 0}, nil
}

// requireMFARekeyFence refuses a rekey Job that no longer holds the fence.
func requireMFARekeyFence(ctx context.Context, tx *sqlxTxWrapper, jobID model.JobID, primaryKeyID string) error {
	var fenced bool
	if err := tx.Get(ctx, &fenced, `SELECT EXISTS (SELECT 1 FROM mfa_key_state WHERE singleton = TRUE AND active_rekey_job_id = ? AND required_primary_key_id = ? FOR SHARE)`,
		jobID.String(), primaryKeyID); err != nil {
		return fmt.Errorf("verify MFA rekey fence: %w", err)
	}
	if !fenced {
		return store.NewErrConflict("mfa_rekey", "stale_fence", nil)
	}
	return nil
}

// validMFAKeyID accepts a secretseal key identity: 32 lowercase hex digits.
func validMFAKeyID(value string) bool {
	return validMailPayloadKeyID(value)
}
