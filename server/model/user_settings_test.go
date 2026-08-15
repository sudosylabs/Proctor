// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewUserSettingsDocumentCreatesCanonicalExactSource(t *testing.T) {
	at := time.Date(2026, time.August, 15, 12, 0, 0, 123456789, time.FixedZone("test", 3600))
	document, err := NewUserSettingsDocument(NewUserID(), NewUserSettingsRevision(), at)
	if err != nil {
		t.Fatalf("NewUserSettingsDocument() error = %v", err)
	}
	if document.Source != UserSettingsInitialSource || document.FormatVersion != UserSettingsFormatVersion1 {
		t.Fatalf("document source/version = %q/%d", document.Source, document.FormatVersion)
	}
	if document.CreatedAt.Location() != time.UTC || !document.CreatedAt.Equal(at) || !document.UpdatedAt.Equal(at) {
		t.Fatalf("document times = %v/%v, want UTC %v", document.CreatedAt, document.UpdatedAt, at)
	}
	if !document.Revision.IsValid() {
		t.Fatalf("document revision = %q, want valid opaque revision", document.Revision)
	}
}

func TestUserSettingsDocumentValidationIsSourceSafe(t *testing.T) {
	document, err := NewUserSettingsDocument(NewUserID(), NewUserSettingsRevision(), time.Now())
	if err != nil {
		t.Fatalf("NewUserSettingsDocument() error = %v", err)
	}
	document.Source = `{"private.future":"do not expose"}`

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "do not expose") {
		t.Fatalf("JSON exposed source: %s", encoded)
	}

	document.Source = strings.Repeat("x", UserSettingsSourceMaxBytes+1)
	err = document.Validate()
	if err == nil {
		t.Fatal("Validate() accepted oversized source")
	}
	if strings.Contains(err.Error(), "xxxxx") {
		t.Fatalf("validation error exposed source: %v", err)
	}
}

func TestUserSettingsDocumentAcceptsBoundedOpaqueFutureFormat(t *testing.T) {
	at := TimeUTC(time.Now())
	const source = "future-format(source => is.not.version.one.jsonc);\n"
	document := &UserSettingsDocument{
		UserID: NewUserID(), Source: source, FormatVersion: 2,
		Revision: NewUserSettingsRevision(), CreatedAt: at, UpdatedAt: at,
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() rejected bounded opaque future format: %v", err)
	}
	if clone := document.Clone(); clone.Source != source || clone.FormatVersion != 2 {
		t.Fatalf("Clone() = %#v", clone)
	}
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
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid durable state")
			}
		})
	}
}
