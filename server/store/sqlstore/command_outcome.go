// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type commandOutcomeRow struct {
	FingerprintVersion int            `db:"fingerprint_version"`
	Fingerprint        []byte         `db:"fingerprint"`
	OutcomeVersion     int            `db:"outcome_version"`
	Outcome            jsonValue      `db:"outcome"`
	OriginalAuditID    sql.NullString `db:"original_audit_event_id"`
	BatchGroupDigest   []byte         `db:"batch_group_digest"`
	DuplicateOfDigest  []byte         `db:"duplicate_of_key_digest"`
}

type SQLCommandOutcomeStore struct{ *SQLStore }

func newSQLCommandOutcomeStore(sqlStore *SQLStore) store.CommandOutcomeStore {
	return &SQLCommandOutcomeStore{SQLStore: sqlStore}
}

func (s SQLCommandOutcomeStore) Has(ctx context.Context, command *store.CommandIdempotency) (bool, error) {
	if command == nil || !command.UserID.IsValid() || command.Operation == "" || command.KeyDigest == ([sha256.Size]byte{}) ||
		command.Retention <= 0 || command.Wait <= 0 {
		return false, store.NewErrInvalidInput("command_outcome", "lookup", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "retain command outcome", func(ctx context.Context, tx *sqlxTxWrapper) (bool, error) {
		lockInput := append([]byte(command.UserID.String()+"\x00"+command.Operation+"\x00"), command.KeyDigest[:]...)
		lockDigest := sha256.Sum256(lockInput)
		lockID := int64(binary.BigEndian.Uint64(lockDigest[:8]))
		lockCtx, cancel := context.WithTimeout(ctx, command.Wait)
		defer cancel()
		var locked bool
		if err := tx.Get(lockCtx, &locked, `SELECT true FROM pg_advisory_xact_lock(?)`, lockID); err != nil {
			if errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
				return false, &store.ErrIdempotencyInProgress{}
			}
			return false, fmt.Errorf("lock command outcome lookup: %w", err)
		}
		var found bool
		err := tx.Get(ctx, &found, `UPDATE command_outcomes
			SET expires_at=GREATEST(expires_at, CURRENT_TIMESTAMP + (? * interval '1 millisecond'))
			WHERE user_id=? AND operation=? AND key_digest=?
			RETURNING true`, command.Retention.Milliseconds(), command.UserID.String(), command.Operation, command.KeyDigest[:])
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("retain command outcome: %w", err)
		}
		return found, nil
	})
}

func (s SQLCommandOutcomeStore) DeleteExpired(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 500 {
		return 0, store.NewErrInvalidInput("command_outcome", "limit", limit)
	}
	result, err := s.GetMaster().Exec(ctx, `WITH expired AS (
		SELECT ctid FROM command_outcomes
		WHERE expires_at <= CURRENT_TIMESTAMP
		ORDER BY expires_at, user_id, operation
		FOR UPDATE SKIP LOCKED LIMIT ?
	) DELETE FROM command_outcomes AS outcomes USING expired
	WHERE outcomes.ctid = expired.ctid`, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired command outcomes: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted command outcomes: %w", err)
	}
	return deleted, nil
}

type idempotentMutation[T any] struct {
	command           *store.CommandIdempotency
	auditEventID      string
	execute           func(context.Context, *sqlxTxWrapper) (T, error)
	encode            func(T) ([]byte, error)
	decode            func(int, []byte) (T, error)
	hydrateReplay     func(context.Context, *sqlxTxWrapper, T) (T, error)
	completeReplay    func(context.Context, *sqlxTxWrapper, T, string) error
	completeDuplicate func(context.Context, *sqlxTxWrapper, T, string) error
	freshAuditEventID func(T) (string, error)
	onboardingOutcome func(T) (onboardingImportCommandResult, error)
}

type idempotentResult[T any] struct {
	Value    T
	Replayed bool
}

func runIdempotentMutation[T any](ctx context.Context, sqlStore *SQLStore, operation string, mutation idempotentMutation[T]) (*idempotentResult[T], error) {
	command := mutation.command
	if command == nil || !command.UserID.IsValid() || command.Operation == "" ||
		command.FingerprintVersion <= 0 || command.OutcomeVersion <= 0 ||
		command.Retention <= 0 || command.Wait <= 0 || mutation.execute == nil ||
		mutation.encode == nil || mutation.decode == nil ||
		(command.OnboardingImportID.IsValid() != (command.OnboardingImportRowNumber > 0)) ||
		!validCommandAuthorization(command.Authorization) || !validCommandBatch(command.Batch, command.KeyDigest) ||
		(mutation.freshAuditEventID == nil &&
			(mutation.completeReplay == nil || !model.IsValidId(mutation.auditEventID))) {
		return nil, store.NewErrInvalidInput("command_outcome", "mutation", nil)
	}
	return runSQLTransaction(ctx, sqlStore.GetMaster().Begin, operation, func(ctx context.Context, tx *sqlxTxWrapper) (*idempotentResult[T], error) {
		lockInput := append([]byte(command.UserID.String()+"\x00"+command.Operation+"\x00"), command.KeyDigest[:]...)
		lockDigest := sha256.Sum256(lockInput)
		lockID := int64(binary.BigEndian.Uint64(lockDigest[:8]))
		lockCtx, cancel := context.WithTimeout(ctx, command.Wait)
		defer cancel()
		var locked bool
		if err := tx.Get(lockCtx, &locked, `SELECT true FROM pg_advisory_xact_lock(?)`, lockID); err != nil {
			if errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
				return nil, &store.ErrIdempotencyInProgress{}
			}
			return nil, fmt.Errorf("lock idempotent command: %w", err)
		}
		if command.Operation == "user.enabled_state.v1" || command.Operation == "role_binding.end.v1" {
			if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
				return nil, err
			}
		}
		if err := lockOnboardingImportCommand(ctx, tx, command); err != nil {
			return nil, err
		}
		if err := lockCommandAuthorization(ctx, tx, command.Authorization); err != nil {
			return nil, err
		}

		var row commandOutcomeRow
		err := tx.Get(ctx, &row, `SELECT fingerprint_version, fingerprint, outcome_version, outcome, original_audit_event_id,
			batch_group_digest, duplicate_of_key_digest
			FROM command_outcomes WHERE user_id = ? AND operation = ? AND key_digest = ?`,
			command.UserID.String(), command.Operation, command.KeyDigest[:])
		switch {
		case err == nil:
			if row.FingerprintVersion != command.FingerprintVersion || !bytes.Equal(row.Fingerprint, command.Fingerprint[:]) {
				return nil, &store.ErrIdempotencyConflict{}
			}
			if command.Batch != nil && !bytes.Equal(row.BatchGroupDigest, command.Batch.GroupDigest[:]) ||
				command.Batch == nil && len(row.BatchGroupDigest) != 0 {
				return nil, &store.ErrIdempotencyConflict{}
			}
			value, decodeErr := mutation.decode(row.OutcomeVersion, []byte(row.Outcome))
			if decodeErr != nil {
				return nil, fmt.Errorf("decode idempotent command outcome: %w", decodeErr)
			}
			if mutation.hydrateReplay != nil {
				value, decodeErr = mutation.hydrateReplay(ctx, tx, value)
				if decodeErr != nil {
					return nil, fmt.Errorf("hydrate idempotent command outcome: %w", decodeErr)
				}
			}
			duplicate := len(row.DuplicateOfDigest) == sha256.Size
			if duplicate {
				if mutation.completeDuplicate != nil {
					err = mutation.completeDuplicate(ctx, tx, value, row.OriginalAuditID.String)
				} else {
					err = completeCommandBatchDuplicateAudit(ctx, tx, mutation.auditEventID, row.OriginalAuditID.String)
				}
				if err != nil {
					return nil, err
				}
			} else if mutation.completeReplay != nil && row.OriginalAuditID.Valid &&
				mutation.auditEventID != row.OriginalAuditID.String {
				if completeErr := mutation.completeReplay(ctx, tx, value, row.OriginalAuditID.String); completeErr != nil {
					return nil, completeErr
				}
			}
			if err = completeOnboardingImportCommand(ctx, tx, command, value, mutation.onboardingOutcome); err != nil {
				return nil, err
			}
			if command.Batch != nil {
				command.Batch.Duplicate = duplicate
			}
			return &idempotentResult[T]{Value: value, Replayed: true}, nil
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("find idempotent command outcome: %w", err)
		}

		if command.Batch != nil && command.Batch.DuplicateOfKeyDigest != ([sha256.Size]byte{}) {
			var canonical commandOutcomeRow
			if err = tx.Get(ctx, &canonical, `SELECT fingerprint_version, fingerprint, outcome_version, outcome, original_audit_event_id,
				batch_group_digest, duplicate_of_key_digest FROM command_outcomes
				WHERE user_id=? AND operation=? AND key_digest=?`, command.UserID.String(), command.Operation,
				command.Batch.DuplicateOfKeyDigest[:]); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, store.NewErrConflict("command_outcome", "batch_canonical_missing", nil)
				}
				return nil, fmt.Errorf("find canonical batch outcome: %w", err)
			}
			if len(canonical.DuplicateOfDigest) != 0 || !bytes.Equal(canonical.BatchGroupDigest, command.Batch.GroupDigest[:]) {
				return nil, &store.ErrIdempotencyConflict{}
			}
			value, decodeErr := mutation.decode(canonical.OutcomeVersion, []byte(canonical.Outcome))
			if decodeErr != nil {
				return nil, fmt.Errorf("decode canonical batch outcome: %w", decodeErr)
			}
			if mutation.hydrateReplay != nil {
				value, decodeErr = mutation.hydrateReplay(ctx, tx, value)
				if decodeErr != nil {
					return nil, fmt.Errorf("hydrate canonical batch outcome: %w", decodeErr)
				}
			}
			if mutation.completeDuplicate != nil {
				err = mutation.completeDuplicate(ctx, tx, value, "")
			} else {
				err = completeCommandBatchDuplicateAudit(ctx, tx, mutation.auditEventID, "")
			}
			if err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO command_outcomes (
				user_id, operation, key_digest, fingerprint_version, fingerprint, outcome_version, outcome,
				batch_group_digest, duplicate_of_key_digest, original_audit_event_id, created_at, expires_at
			) VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + (? * interval '1 millisecond'))`,
				command.UserID.String(), command.Operation, command.KeyDigest[:], command.FingerprintVersion, command.Fingerprint[:],
				canonical.OutcomeVersion, canonical.Outcome, command.Batch.GroupDigest[:], command.Batch.DuplicateOfKeyDigest[:],
				mutation.auditEventID, command.Retention.Milliseconds()); err != nil {
				return nil, fmt.Errorf("persist duplicate batch outcome: %w", err)
			}
			command.Batch.Duplicate = true
			return &idempotentResult[T]{Value: value}, nil
		}

		value, err := mutation.execute(ctx, tx)
		if err != nil {
			return nil, err
		}
		encoded, err := mutation.encode(value)
		if err != nil {
			return nil, fmt.Errorf("encode idempotent command outcome: %w", err)
		}
		if len(encoded) == 0 || len(encoded) > store.CommandOutcomeMaxBytes || !json.Valid(encoded) {
			return nil, store.NewErrInvalidInput("command_outcome", "outcome", len(encoded))
		}
		originalAuditID := mutation.auditEventID
		if mutation.freshAuditEventID != nil {
			originalAuditID, err = mutation.freshAuditEventID(value)
			if err != nil {
				return nil, fmt.Errorf("resolve idempotent command audit: %w", err)
			}
		}
		var persistedAuditID any
		switch {
		case originalAuditID == "":
		case model.IsValidId(originalAuditID):
			persistedAuditID = originalAuditID
		default:
			return nil, store.NewErrInvalidInput("command_outcome", "audit_event_id", nil)
		}
		var batchGroup any
		if command.Batch != nil {
			batchGroup = command.Batch.GroupDigest[:]
		}
		if _, err = tx.Exec(ctx, `INSERT INTO command_outcomes (
			user_id, operation, key_digest, fingerprint_version, fingerprint,
			outcome_version, outcome, batch_group_digest, original_audit_event_id, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + (? * interval '1 millisecond'))`,
			command.UserID.String(), command.Operation, command.KeyDigest[:], command.FingerprintVersion,
			command.Fingerprint[:], command.OutcomeVersion, encoded, batchGroup, persistedAuditID,
			command.Retention.Milliseconds()); err != nil {
			return nil, fmt.Errorf("persist idempotent command outcome: %w", err)
		}
		if err = completeOnboardingImportCommand(ctx, tx, command, value, mutation.onboardingOutcome); err != nil {
			return nil, err
		}
		return &idempotentResult[T]{Value: value}, nil
	})
}

func replayIdempotentMutation[T any](ctx context.Context, sqlStore *SQLStore, operation string, mutation idempotentMutation[T]) (*idempotentResult[T], error) {
	var zero T
	mutation.execute = func(context.Context, *sqlxTxWrapper) (T, error) {
		return zero, store.NewErrNotFound("command_outcome", "idempotency_key")
	}
	mutation.encode = func(T) ([]byte, error) { return nil, nil }
	return runIdempotentMutation(ctx, sqlStore, operation, mutation)
}

func encodeCommandOutcome(value any) ([]byte, error) { return json.Marshal(value) }

func decodeCommandOutcome(data []byte, target any) error { return json.Unmarshal(data, target) }

func validCommandBatch(batch *store.CommandBatch, keyDigest [sha256.Size]byte) bool {
	if batch == nil {
		return true
	}
	if batch.GroupDigest == ([sha256.Size]byte{}) || batch.DuplicateOfKeyDigest == keyDigest {
		return false
	}
	return true
}

func completeCommandBatchDuplicateAudit(ctx context.Context, tx *sqlxTxWrapper, auditID, originalAuditID string) error {
	if !model.IsValidId(auditID) {
		return store.NewErrInvalidInput("command_outcome", "duplicate_audit_event_id", nil)
	}
	data := map[string]any{"batch_duplicate": true}
	if model.IsValidId(originalAuditID) {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	at, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusFail, "onboarding_batch.duplicate", encoded, at.UnixMilli()); err != nil {
		return fmt.Errorf("complete batch duplicate audit: %w", err)
	}
	return nil
}

func completeAdministrativeNoOpAudit(ctx context.Context, tx *sqlxTxWrapper, auditID string, at int64, idField, id string) error {
	data, err := model.EncodeAuditData(map[string]any{idField: id, "no_op": true})
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", data, at); err != nil {
		return fmt.Errorf("complete administrative no-op audit: %w", err)
	}
	return nil
}

func completeAdministrativeReplayAudit(ctx context.Context, tx *sqlxTxWrapper, auditID string, at int64, idField, id string, noOp bool, originalAuditID string) error {
	data := map[string]any{idField: id, "idempotency_replayed": true, "original_audit_event_id": originalAuditID}
	if noOp {
		data["no_op"] = true
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, at); err != nil {
		return fmt.Errorf("complete administrative replay audit: %w", err)
	}
	return nil
}
