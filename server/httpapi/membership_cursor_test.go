// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMembershipCursorStrictEnvelope(t *testing.T) {
	t.Parallel()
	at := int64(0) // history requires an explicit zero; missing and null are invalid
	cursor := membershipPageCursor{ScopeType: model.ResourceClass, ScopeID: model.NewID(), ActiveAt: &at, AfterUserID: model.NewID(), AfterID: model.NewID()}
	encoded, err := encodeOpaqueCursor(cursor, membershipPageCursorSpec())
	require.NoError(t, err)
	decoded, err := decodeOpaqueCursor(encoded, membershipPageCursorSpec())
	require.NoError(t, err, "history cursor = %#v", decoded)
	require.NotNil(t, decoded.ActiveAt, "history cursor = %#v", decoded)
	require.Equal(t, int64(0), *decoded.ActiveAt, "history cursor = %#v", decoded)
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	reject := func(name string, body []byte) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			_, decodeErr := decodeOpaqueCursor(base64.RawURLEncoding.EncodeToString(body), membershipPageCursorSpec())
			require.Error(t, decodeErr, "invalid cursor accepted: %q", body)
		})
	}
	for key := range fields {
		clone := make(map[string]json.RawMessage, len(fields))
		for key, value := range fields {
			clone[key] = value
		}
		delete(clone, key)
		incomplete, _ := json.Marshal(clone)
		reject("missing/"+key, incomplete)
		clone[key] = json.RawMessage("null")
		nullField, _ := json.Marshal(clone)
		reject("null/"+key, nullField)
		reject("case-alias/"+key, []byte(strings.Replace(string(body), `"`+key+`":`, `"`+strings.ToUpper(key)+`":`, 1)))
		duplicate := append([]byte(nil), body[:len(body)-1]...)
		duplicate = append(duplicate, []byte(`,"`+key+`":`+string(fields[key])+`}`)...)
		reject("duplicate/"+key, duplicate)
	}
	reject("null-object", []byte("null"))
	reject("array", []byte("[]"))
	reject("unknown", append(append([]byte(nil), body[:len(body)-1]...), []byte(`,"unexpected":1}`)...))
	reject("trailing", append(append([]byte(nil), body...), []byte(" {}")...))
	reject("invalid-utf8", []byte(strings.Replace(string(body), string(model.ResourceClass), "cl"+string([]byte{0xff})+"ss", 1)))
	for _, token := range []string{encoded + "=", encoded[:4] + "\n" + encoded[4:], encoded[:4] + "\r\n" + encoded[4:], strings.Repeat("a", 1025)} {
		_, err = decodeOpaqueCursor(token, membershipPageCursorSpec())
		require.Error(t, err, "noncanonical encoding accepted: %q", token)
	}
	// A nonzero unused Base64 bit is another representation of identical bytes.
	paddedBody := append([]byte(nil), body...)
	for len(paddedBody)%3 == 0 {
		paddedBody = append(paddedBody, ' ')
	}
	canonical := base64.RawURLEncoding.EncodeToString(paddedBody)
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, canonical[len(canonical)-1])
	alias := canonical[:len(canonical)-1] + string(alphabet[last+1])
	_, err = decodeOpaqueCursor(alias, membershipPageCursorSpec())
	require.Error(t, err, "nonzero Base64 pad bits accepted")
}

func TestMembershipCursorScopeAndFilterBinding(t *testing.T) {
	t.Parallel()
	scope := model.Resource{Type: model.ResourceClass, ID: model.NewID()}
	instant := int64(1000)
	cursor := membershipPageCursor{ScopeType: scope.Type, ScopeID: scope.ID, ActiveAt: &instant, AfterUserID: model.NewID(), AfterID: model.NewID()}
	encoded, err := encodeOpaqueCursor(cursor, membershipPageCursorSpec())
	require.NoError(t, err)
	request := func(suffix string) string { return "/members?cursor=" + url.QueryEscape(encoded) + suffix }
	query, after, err := membershipPageQuery(httptest.NewRequest("GET", request(""), nil), scope)
	require.NoError(t, err, "continuation = %#v %#v", query, after)
	require.NotNil(t, query.ActiveAt, "continuation = %#v %#v", query, after)
	require.Equal(t, instant, *query.ActiveAt, "continuation = %#v %#v", query, after)
	require.Equal(t, cursor.AfterID, after.AfterID, "continuation = %#v %#v", query, after)
	for _, suffix := range []string{"&active_at=1970-01-01T00:00:02Z", "&history=true"} {
		_, _, err = membershipPageQuery(httptest.NewRequestWithContext(t.Context(), "GET", request(suffix), nil), scope)
		require.Error(t, err, "conflicting filter accepted: %s", suffix)
	}
	for _, other := range []model.Resource{{Type: scope.Type, ID: model.NewID()}, {Type: model.ResourceAcademicUnit, ID: scope.ID}} {
		_, _, err = membershipPageQuery(httptest.NewRequestWithContext(t.Context(), "GET", request(""), nil), other)
		require.Error(t, err, "cross-scope continuation accepted: %#v", other)
	}
	instant = 0
	encoded, err = encodeOpaqueCursor(cursor, membershipPageCursorSpec())
	require.NoError(t, err)
	query, _, err = membershipPageQuery(httptest.NewRequest("GET", request(""), nil), scope)
	require.NoError(t, err, "history continuation = %#v", query)
	require.NotNil(t, query.History, "history continuation = %#v", query)
	require.True(t, *query.History, "history continuation = %#v", query)
	require.Nil(t, query.ActiveAt, "history continuation = %#v", query)
}
