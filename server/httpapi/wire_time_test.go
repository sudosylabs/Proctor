// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func encodeWireJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	for _, legacy := range []string{"create_at", "update_at", "delete_at"} {
		_, present := decoded[legacy]
		require.False(t, present, "response carries retired member %q: %s", legacy, encoded)
	}
	return decoded
}

func TestWireTimestampsAreRFC3339UTCStrings(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 8, 1, 9, 30, 0, 123_456_789, time.FixedZone("WAT", 3600))
	class := &model.Class{
		ID: model.NewClassID(), ProgrammeLevelID: model.NewProgrammeLevelID(), AcademicPeriodID: model.NewAcademicPeriodID(),
		CreatedAt: created, UpdatedAt: created.Add(1500 * time.Millisecond), Name: "class-a", DisplayName: "Class A",
	}
	active := encodeWireJSON(t, classResponseFromModel(class))
	require.Equal(t, "2026-08-01T08:30:00.123456Z", active["created_at"])
	require.Equal(t, "2026-08-01T08:30:01.623456Z", active["updated_at"])
	_, present := active["archived_at"]
	require.False(t, present, "active Class must omit archived_at: %#v", active)
	class.ArchivedAt = model.OptionalTimeFrom(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	require.Equal(t, "2026-09-01T00:00:00Z", encodeWireJSON(t, classResponseFromModel(class))["archived_at"])

	token := &model.PersonalAccessToken{
		ID: model.NewPersonalAccessTokenID(), UserID: model.NewUserID(), CreatedAt: time.UnixMilli(1_786_000_000_250).UTC(),
		UpdatedAt: time.UnixMilli(1_786_000_000_250).UTC(), ExpiresAt: time.UnixMilli(1_788_000_000_000).UTC(),
		LastUsedAt: model.OptionalTimeFrom(time.UnixMilli(1_787_000_000_001).UTC()),
	}
	pat := encodeWireJSON(t, personalAccessTokenResponseFromModel(token))
	require.Equal(t, "2026-08-06T07:06:40.25Z", pat["created_at"])
	require.Equal(t, "2026-08-29T10:40:00Z", pat["expires_at"])
	require.Equal(t, "2026-08-17T20:53:20.001Z", pat["last_used_at"])
	for _, absent := range []string{"archived_at", "disabled_at", "revoked_at"} {
		_, present := pat[absent]
		require.False(t, present, "absent %s must be omitted, not null or zero: %#v", absent, pat)
	}
}

func TestWireTimestampRequestsAcceptAnyRFC3339OffsetAndRejectOtherSpellings(t *testing.T) {
	t.Parallel()
	millis, err := wireTimeMillis("start_at", "2026-08-01T10:30:00.123456+02:00")
	require.NoError(t, err, "offset instant")
	require.Equal(t, time.Date(2026, 8, 1, 8, 30, 0, 123_000_000, time.UTC).UnixMilli(), millis, "offset instant")
	millis, err = wireTimeMillis("end_at", "")
	require.NoError(t, err, "absent instant")
	require.Equal(t, int64(0), millis, "absent instant")
	for _, invalid := range []string{"1786000000000", "2026-08-01", "2026-08-01 08:30:00Z", "0001-01-01T00:00:00Z"} {
		_, err := wireTimeMillis("start_at", invalid)
		require.Error(t, err, "%q was accepted as a wire timestamp", invalid)
	}
}

func TestCreateRequestsRejectServerOwnedFields(t *testing.T) {
	t.Parallel()
	targets := []any{
		&createAcademicUnitRequest{}, &createProgrammeRequest{}, &createProgrammeLevelRequest{},
		&createAcademicPeriodRequest{}, &createClassRequest{}, &createAcademicUnitMemberRequest{},
		&enrollClassMemberRequest{}, &createAffiliationRequest{},
	}
	for _, target := range targets {
		for _, member := range []string{`"id":"client-owned"`, `"created_at":"2026-08-01T00:00:00Z"`, `"create_at":1`, `"delete_at":0`} {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("{"+member+"}"))
			request.Header.Set("Content-Type", "application/json")
			fresh := reflect.New(reflect.TypeOf(target).Elem()).Interface()
			require.Error(t, decodeRequestJSON(request, fresh), "%T accepted server-owned member %s", target, member)
		}
	}
}

// Route scope is the only authority for a created child's parent, so create
// requests do not declare parent identifiers they would silently ignore.
func TestCreateRequestsRejectRouteOwnedParentFields(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		target  any
		members []string
	}{
		"academic_unit":        {&createAcademicUnitRequest{}, []string{"institution_id", "parent_id"}},
		"programme":            {&createProgrammeRequest{}, []string{"academic_unit_id"}},
		"programme_level":      {&createProgrammeLevelRequest{}, []string{"programme_id"}},
		"class":                {&createClassRequest{}, []string{"programme_level_id"}},
		"academic_unit_member": {&createAcademicUnitMemberRequest{}, []string{"academic_unit_id", "end_at"}},
		"class_member":         {&enrollClassMemberRequest{}, []string{"class_id", "academic_period_id"}},
		"affiliation":          {&createAffiliationRequest{}, []string{"user_id"}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, member := range test.members {
				request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
					strings.NewReader(`{"`+member+`":"route-owned"}`))
				request.Header.Set("Content-Type", "application/json")
				fresh := reflect.New(reflect.TypeOf(test.target).Elem()).Interface()
				require.Error(t, decodeRequestJSON(request, fresh), "%T accepted route-owned member %s", test.target, member)
			}
		})
	}
}
