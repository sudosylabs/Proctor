// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"database/sql"
	"errors"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/store"
)

const (
	postgresUniqueViolation     = "23505"
	postgresForeignKeyViolation = "23503"
	postgresUndefinedTable      = "42P01"
)

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
