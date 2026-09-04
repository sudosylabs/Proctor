// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"errors"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/store"
)

const (
	postgresUniqueViolation      = "23505"
	postgresForeignKeyViolation  = "23503"
	postgresUndefinedTable       = "42P01"
	postgresSerializationFailure = "40001"
	postgresDeadlockDetected     = "40P01"
)

// IsTransientError reports whether err is an explicitly approved PostgreSQL
// concurrency failure. Domain errors are terminal even when they wrap a
// driver error, and the original error is never translated or replaced.
func IsTransientError(err error) bool {
	if err == nil || isDomainError(err) {
		return false
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	switch string(pqErr.Code) {
	case postgresSerializationFailure, postgresDeadlockDetected:
		return true
	default:
		return false
	}
}

func isDomainError(err error) bool {
	var invalid *store.ErrInvalidInput
	var notFound *store.ErrNotFound
	var conflict *store.ErrConflict
	var reference *store.ErrReference
	return errors.As(err, &invalid) || errors.As(err, &notFound) ||
		errors.As(err, &conflict) || errors.As(err, &reference)
}

func translateError(resource, id string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.NewErrNotFound(resource, id).Wrap(err)
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch string(pqErr.Code) {
		case postgresUniqueViolation:
			return store.NewErrConflict(resource, pqErr.Constraint, err)
		case postgresForeignKeyViolation:
			return store.NewErrReference(resource, pqErr.Constraint, err)
		}
	}
	return err
}
