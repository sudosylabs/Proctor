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
	postgresCheckViolation       = "23514"
	postgresNotNullViolation     = "23502"
	postgresStringDataTruncation = "22001"
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

// translateIntegrityError translates the integrity violations of every
// statement the executors run, so a write that never calls translateError
// still reports a Store error. The violated table names the resource; an
// operation that calls translateError with its own resource re-translates the
// wrapped driver error. Absence and other failures pass through unchanged.
func translateIntegrityError(err error) error {
	var pqErr *pq.Error
	if err == nil || !errors.As(err, &pqErr) || isDomainError(err) {
		return err
	}
	switch string(pqErr.Code) {
	case postgresUniqueViolation, postgresForeignKeyViolation, postgresCheckViolation,
		postgresNotNullViolation, postgresStringDataTruncation:
		return translateError(pqErr.Table, "", err)
	default:
		return err
	}
}

// translateError maps driver failures to store errors. Integrity violations
// that reject the written values (CHECK, NOT NULL, and over-length strings)
// become constraint-violation ErrInvalidInput naming the constraint or column,
// never the value.
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
		case postgresCheckViolation:
			return store.NewErrConstraintViolation(resource, pqErr.Constraint).Wrap(err)
		case postgresNotNullViolation:
			return store.NewErrConstraintViolation(resource, pqErr.Column).Wrap(err)
		case postgresStringDataTruncation:
			return store.NewErrConstraintViolation(resource, pqErr.Column).Wrap(err)
		}
	}
	return err
}
