// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"errors"
	"testing"
)

func TestStoreErrorsPreserveClassificationAndCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("driver detail")
	notFound := NewErrNotFound("institution", "id").Wrap(cause)
	if !IsNotFound(notFound) || !errors.Is(notFound, cause) {
		t.Fatalf("not found classification or cause was lost: %v", notFound)
	}

	conflict := NewErrConflict("institution", "institutions_singleton_key", cause)
	if !IsConflict(conflict) || !errors.Is(conflict, cause) {
		t.Fatalf("conflict classification or cause was lost: %v", conflict)
	}
}
