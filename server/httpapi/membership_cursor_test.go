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

	"github.com/sudosylabs/proctor/server/model"
)

func TestMembershipCursorStrictEnvelope(t *testing.T) {
	t.Parallel()
	at := int64(0) // history requires an explicit zero; missing and null are invalid
	cursor := membershipPageCursor{ScopeType: model.ResourceClass, ScopeID: model.NewId(), ActiveAt: &at, AfterUserID: model.NewId(), AfterID: model.NewId()}
	encoded, err := encodeOpaqueCursor(cursor, membershipPageCursorSpec())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeOpaqueCursor(encoded, membershipPageCursorSpec())
	if err != nil || decoded.ActiveAt == nil || *decoded.ActiveAt != 0 {
		t.Fatalf("history cursor = %#v, %v", decoded, err)
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	reject := func(name string, body []byte) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			if _, err := decodeOpaqueCursor(base64.RawURLEncoding.EncodeToString(body), membershipPageCursorSpec()); err == nil {
				t.Fatalf("invalid cursor accepted: %q", body)
			}
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
		if _, err := decodeOpaqueCursor(token, membershipPageCursorSpec()); err == nil {
			t.Fatalf("noncanonical encoding accepted: %q", token)
		}
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
	if _, err := decodeOpaqueCursor(alias, membershipPageCursorSpec()); err == nil {
		t.Fatal("nonzero Base64 pad bits accepted")
	}
}

func TestMembershipCursorScopeAndFilterBinding(t *testing.T) {
	t.Parallel()
	scope := model.Resource{Type: model.ResourceClass, ID: model.NewId()}
	instant := int64(1000)
	cursor := membershipPageCursor{ScopeType: scope.Type, ScopeID: scope.ID, ActiveAt: &instant, AfterUserID: model.NewId(), AfterID: model.NewId()}
	encoded, err := encodeOpaqueCursor(cursor, membershipPageCursorSpec())
	if err != nil {
		t.Fatal(err)
	}
	request := func(suffix string) string { return "/members?cursor=" + url.QueryEscape(encoded) + suffix }
	query, after, err := membershipPageQuery(httptest.NewRequest("GET", request(""), nil), scope)
	if err != nil || query.ActiveAt == nil || *query.ActiveAt != instant || after.AfterID != cursor.AfterID {
		t.Fatalf("continuation = %#v %#v %v", query, after, err)
	}
	for _, suffix := range []string{"&active_at=2000", "&history=true"} {
		if _, _, err := membershipPageQuery(httptest.NewRequest("GET", request(suffix), nil), scope); err == nil {
			t.Fatalf("conflicting filter accepted: %s", suffix)
		}
	}
	for _, other := range []model.Resource{{Type: scope.Type, ID: model.NewId()}, {Type: model.ResourceAcademicUnit, ID: scope.ID}} {
		if _, _, err := membershipPageQuery(httptest.NewRequest("GET", request(""), nil), other); err == nil {
			t.Fatalf("cross-scope continuation accepted: %#v", other)
		}
	}
	instant = 0
	encoded, err = encodeOpaqueCursor(cursor, membershipPageCursorSpec())
	if err != nil {
		t.Fatal(err)
	}
	query, _, err = membershipPageQuery(httptest.NewRequest("GET", request(""), nil), scope)
	if err != nil || query.History == nil || !*query.History || query.ActiveAt != nil {
		t.Fatalf("history continuation = %#v, %v", query, err)
	}
}
