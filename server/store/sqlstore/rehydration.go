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
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
)

// persistedStateError reports that authoritative data could not be safely
// reconstructed as a domain model. It deliberately carries only structural
// context; raw persisted values are retained only in the wrapped cause.
type persistedStateError struct {
	Entity string
	Field  string
	cause  error
}

func parseNullablePersistedID[T any](
	entity string,
	field string,
	raw sql.NullString,
	parse func(string) (T, error),
) (T, error) {
	if !raw.Valid {
		var zero T
		return zero, nil
	}
	return parsePersistedID(entity, field, raw.String, parse)
}

func (e *persistedStateError) Error() string {
	return fmt.Sprintf("sqlstore: invalid persisted state: entity=%s field=%s", e.Entity, e.Field)
}

func (e *persistedStateError) Unwrap() error {
	return e.cause
}

func invalidPersistedState(entity, field string, cause error) error {
	return &persistedStateError{Entity: entity, Field: field, cause: cause}
}

func parsePersistedID[T any](
	entity string,
	field string,
	raw string,
	parse func(string) (T, error),
) (T, error) {
	value, err := parse(raw)
	if err != nil {
		var zero T
		return zero, invalidPersistedState(entity, field, err)
	}
	return value, nil
}

func validatePersistedModel(entity string, value interface{ Validate() error }) error {
	if err := value.Validate(); err != nil {
		field := "value"
		var validation *model.ValidationError
		if errors.As(err, &validation) && validation.Field != "" {
			field = validation.Field
		}
		return invalidPersistedState(entity, field, err)
	}
	return nil
}
