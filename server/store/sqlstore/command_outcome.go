// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
}

type SQLCommandOutcomeStore struct{ *SQLStore }

func newSQLCommandOutcomeStore(sqlStore *SQLStore) store.CommandOutcomeStore {
	return &SQLCommandOutcomeStore{SQLStore: sqlStore}
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
	freshAuditEventID func(T) (string, error)
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

		var row commandOutcomeRow
		err := tx.Get(ctx, &row, `SELECT fingerprint_version, fingerprint, outcome_version, outcome, original_audit_event_id
			FROM command_outcomes WHERE user_id = ? AND operation = ? AND key_digest = ?`,
			command.UserID.String(), command.Operation, command.KeyDigest[:])
		switch {
		case err == nil:
			if row.FingerprintVersion != command.FingerprintVersion || !bytes.Equal(row.Fingerprint, command.Fingerprint[:]) {
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
			if mutation.completeReplay != nil && row.OriginalAuditID.Valid &&
				mutation.auditEventID != row.OriginalAuditID.String {
				if completeErr := mutation.completeReplay(ctx, tx, value, row.OriginalAuditID.String); completeErr != nil {
					return nil, completeErr
				}
			}
			return &idempotentResult[T]{Value: value, Replayed: true}, nil
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("find idempotent command outcome: %w", err)
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
		if _, err = tx.Exec(ctx, `INSERT INTO command_outcomes (
			user_id, operation, key_digest, fingerprint_version, fingerprint,
			outcome_version, outcome, original_audit_event_id, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP + (? * interval '1 millisecond'))`,
			command.UserID.String(), command.Operation, command.KeyDigest[:], command.FingerprintVersion,
			command.Fingerprint[:], command.OutcomeVersion, encoded, persistedAuditID,
			command.Retention.Milliseconds()); err != nil {
			return nil, fmt.Errorf("persist idempotent command outcome: %w", err)
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
