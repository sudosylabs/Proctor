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
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersistedStateErrorsRemainInternalAndSafe(t *testing.T) {
	t.Parallel()

	_, err := parsePersistedID("affiliation", "user_id", "raw-invalid-id", model.ParseUserID)
	var persisted *persistedStateError
	if !errors.As(err, &persisted) {
		t.Fatalf("error = %v, want persisted-state error", err)
	}
	var invalidInput *store.ErrInvalidInput
	if errors.As(err, &invalidInput) {
		t.Fatalf("persisted corruption was classified as caller input: %v", err)
	}
	if strings.Contains(err.Error(), "raw-invalid-id") {
		t.Fatalf("safe error contains raw value: %v", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("persisted-state error discarded its cause")
	}
}

func TestParseNullablePersistedIDDistinguishesAbsentAndMalformed(t *testing.T) {
	t.Parallel()

	absent, err := parseNullablePersistedID("session", "replaced_by_id", sql.NullString{}, model.ParseSessionCredentialID)
	if err != nil || !absent.IsZero() {
		t.Fatalf("absent ID = %q, %v", absent, err)
	}
	presentID := model.NewSessionCredentialID()
	present, err := parseNullablePersistedID("session", "replaced_by_id", sql.NullString{String: presentID.String(), Valid: true}, model.ParseSessionCredentialID)
	if err != nil || present != presentID {
		t.Fatalf("present ID = %q, %v; want %q", present, err, presentID)
	}
	_, err = parseNullablePersistedID("session", "replaced_by_id", sql.NullString{Valid: true}, model.ParseSessionCredentialID)
	var persisted *persistedStateError
	if !errors.As(err, &persisted) || persisted.Field != "replaced_by_id" {
		t.Fatalf("empty present ID error = %v", err)
	}
}
