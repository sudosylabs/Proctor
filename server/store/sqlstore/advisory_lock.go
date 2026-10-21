// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/sudosylabs/proctor/server/store"
)

// Every named advisory lock hashes its text key with hashtextextended into
// PostgreSQL's 64-bit advisory key space and is held until the transaction
// ends. Keys are "proctor:<kebab-case topic>" followed by ":"-separated parts,
// so no two topics can produce the same key text. Callers acquire locks in the
// order their operation documents; this file names keys, it does not order
// them. Command idempotency fences use their own digest-derived bigint keys
// (lockCommandIdempotency).
const (
	advisoryLockSQL       = `SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`
	advisoryLockSharedSQL = `SELECT pg_advisory_xact_lock_shared(hashtextextended(?, 0))`
)

// advisoryLockKey names the advisory lock for topic and its identifying parts.
func advisoryLockKey(topic string, parts ...string) string {
	return "proctor:" + strings.Join(append([]string{topic}, parts...), ":")
}

// lockAdvisory blocks until the transaction holds the exclusive advisory lock
// named by key.
func lockAdvisory(ctx context.Context, executor sqlxExecutor, key string) error {
	_, err := executor.Exec(ctx, advisoryLockSQL, key)
	return err
}

// lockAdvisoryShared blocks until the transaction holds the shared advisory
// lock named by key.
func lockAdvisoryShared(ctx context.Context, executor sqlxExecutor, key string) error {
	_, err := executor.Exec(ctx, advisoryLockSharedSQL, key)
	return err
}

// lockCommandIdempotency holds the command's idempotency fence for the rest of
// the transaction. The key is derived from the actor, operation, and key
// digest; a wait longer than command.Wait reports the command as still in
// progress.
func lockCommandIdempotency(ctx context.Context, tx *sqlxTxWrapper, command *store.CommandIdempotency) error {
	lockInput := append([]byte(command.UserID.String()+"\x00"+command.Operation+"\x00"), command.KeyDigest[:]...)
	lockDigest := sha256.Sum256(lockInput)
	lockID := int64(binary.BigEndian.Uint64(lockDigest[:8])) //nolint:gosec // reinterpreting digest bits as a signed key is intended
	lockCtx, cancel := context.WithTimeout(ctx, command.Wait)
	defer cancel()
	var locked bool
	if err := tx.Get(lockCtx, &locked, `SELECT true FROM pg_advisory_xact_lock(?)`, lockID); err != nil {
		if errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
			return &store.ErrIdempotencyInProgress{}
		}
		return fmt.Errorf("lock command idempotency fence: %w", err)
	}
	return nil
}
