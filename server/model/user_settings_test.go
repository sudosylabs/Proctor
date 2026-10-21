// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewUserSettingsDocumentCreatesCanonicalExactSource(t *testing.T) {
	at := time.Date(2026, time.August, 15, 12, 0, 0, 123456789, time.FixedZone("test", 3600))
	document, err := NewUserSettingsDocument(NewUserID(), NewUserSettingsRevision(), at)
	require.NoError(t, err)
	require.Equal(t, UserSettingsInitialSource, document.Source, "document source/version")
	require.Equal(t, UserSettingsFormatVersion1, document.FormatVersion, "document source/version")
	want := time.Date(2026, time.August, 15, 11, 0, 0, 123456000, time.UTC)
	require.Equal(t, want, document.CreatedAt, "document times")
	require.Equal(t, want, document.UpdatedAt, "document times")
	require.True(t, document.Revision.IsValid(), "document revision = %q, want valid opaque revision", document.Revision)
}

func TestUserSettingsDocumentValidationIsSourceSafe(t *testing.T) {
	document, err := NewUserSettingsDocument(NewUserID(), NewUserSettingsRevision(), time.Now())
	require.NoError(t, err)
	document.Source = `{"private.future":"do not expose"}`

	encoded, err := json.Marshal(document)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
	require.NotContains(t, string(encoded), "do not expose")

	document.Source = strings.Repeat("x", UserSettingsSourceMaxBytes+1)
	err = document.Validate()
	require.Error(t, err, "Validate() accepted oversized source")
	require.NotContains(t, err.Error(), "xxxxx")
}

func TestUserSettingsDocumentAcceptsBoundedOpaqueFutureFormat(t *testing.T) {
	at := TimeUTC(time.Now())
	const source = "future-format(source => is.not.version.one.jsonc);\n"
	document := &UserSettingsDocument{
		UserID: NewUserID(), Source: source, FormatVersion: 2,
		Revision: NewUserSettingsRevision(), CreatedAt: at, UpdatedAt: at,
	}
	require.NoError(t, document.Validate())
	clone := document.Clone()
	require.Equal(t, source, clone.Source)
	require.Equal(t, 2, clone.FormatVersion)
}

func TestUserSettingsDocumentRejectsInvalidDurableState(t *testing.T) {
	at := TimeUTC(time.Now())
	valid := UserSettingsDocument{
		UserID:        NewUserID(),
		Source:        UserSettingsInitialSource,
		FormatVersion: UserSettingsFormatVersion1,
		Revision:      NewUserSettingsRevision(),
		CreatedAt:     at,
		UpdatedAt:     at,
	}

	tests := []struct {
		name   string
		mutate func(*UserSettingsDocument)
	}{
		{name: "user", mutate: func(value *UserSettingsDocument) { value.UserID = "invalid" }},
		{name: "format", mutate: func(value *UserSettingsDocument) { value.FormatVersion = 0 }},
		{name: "revision", mutate: func(value *UserSettingsDocument) { value.Revision = "invalid" }},
		{name: "created", mutate: func(value *UserSettingsDocument) { value.CreatedAt = time.Time{} }},
		{name: "updated", mutate: func(value *UserSettingsDocument) { value.UpdatedAt = value.CreatedAt.Add(-time.Second) }},
		{name: "source", mutate: func(value *UserSettingsDocument) { value.Source = strings.Repeat("x", UserSettingsSourceMaxBytes+1) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			require.Error(t, candidate.Validate(), "Validate() accepted invalid durable state")
		})
	}
}
