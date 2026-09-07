// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPicturePNG(t *testing.T) {
	t.Parallel()
	first, err := picturePNG(42, "candidate-001")
	if err != nil {
		t.Fatal(err)
	}
	again, err := picturePNG(42, "candidate-001")
	if err != nil {
		t.Fatal(err)
	}
	other, err := picturePNG(42, "candidate-003")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) || bytes.Equal(first, other) {
		t.Fatal("picture identity is not deterministic and varied")
	}
	decoded, err := png.Decode(bytes.NewReader(first))
	if err != nil || decoded.Bounds().Dx() != 256 || decoded.Bounds().Dy() != 256 {
		t.Fatalf("invalid upload image: %v", err)
	}
}

func TestProfilePictureRetryPreservesBytesAndETag(t *testing.T) {
	t.Parallel()
	for _, changed := range []bool{false, true} {
		name := "unchanged picture"
		if changed {
			name = "changed picture"
		}
		t.Run(name, func(t *testing.T) {
			tag := `"` + strings.Repeat("a", 64) + `"`
			puts := 0
			var original []byte
			r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == "GET" {
					resp := response(200, "normalized image")
					resp.Header.Set("ETag", tag)
					return resp, nil
				}
				puts++
				raw, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if req.Header.Get("Content-Type") != "image/png" || req.Header.Get("If-Match") != `"`+strings.Repeat("a", 64)+`"` || req.Header.Get("Idempotency-Key") != "" {
					t.Fatal("upload lost its raw protocol or original precondition")
				}
				if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
					t.Fatal("upload is not raw PNG")
				}
				if puts == 1 {
					original = raw
					return nil, errors.New("lost response")
				}
				if !bytes.Equal(raw, original) {
					t.Fatal("retry regenerated the saved picture")
				}
				return response(200, `{"id":"user"}`), nil
			})
			r.state.Records = map[string]record{}
			r.state.Accounts["administrator"] = &account{Token: "token"}
			r.state.Accounts["candidate-001"] = &account{UserID: "user"}
			if err := r.profilePicture("candidate-001"); err == nil {
				t.Fatal("expected response loss")
			}
			raw, err := os.ReadFile(filepath.Join(r.options.StateDir, "journal.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &r.state); err != nil {
				t.Fatal(err)
			}
			r.state.Seed = 999 // A retry must use the persisted bytes.
			if changed {
				tag = `"` + strings.Repeat("b", 64) + `"`
			}
			err = r.profilePicture("candidate-001")
			if changed {
				if err == nil || !strings.Contains(err.Error(), "changed after upload preparation") || puts != 1 {
					t.Fatalf("ambiguous picture was overwritten: %v, writes=%d", err, puts)
				}
				return
			}
			if err != nil || puts != 2 || !r.state.Accounts["candidate-001"].SeededCustomPicture {
				t.Fatalf("upload recovery failed: %v", err)
			}
			if err = r.profilePicture("candidate-001"); err != nil || puts != 2 {
				t.Fatalf("completed upload repeated: %v", err)
			}
		})
	}
}

func TestDisableAccountReconcilesLostResponseAndPreservesLaterEnablement(t *testing.T) {
	t.Parallel()
	disabled, writes := false, 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == "POST" {
			writes++
			disabled = true
			return nil, errors.New("lost response")
		}
		if disabled {
			return response(200, `[{"id":"user","disabled_at":123}]`), nil
		}
		return response(200, `[{"id":"user"}]`), nil
	})
	r.state.Accounts["administrator"] = &account{Token: "token"}
	a := &account{UserID: "user", Token: "old-access", RefreshToken: "old-refresh"}
	r.state.Records = map[string]record{"user/disabled-student": {ID: "user", URL: "/api/v1/users/user"}}
	if err := r.disableAccount("disabled-student", a); err == nil {
		t.Fatal("expected response loss")
	}
	if err := r.disableAccount("disabled-student", a); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || !a.SeededDisabled || a.Token != "" || a.RefreshToken != "" {
		t.Fatal("disablement failed to reconcile and discard revoked credentials")
	}
	if !strings.Contains(r.state.Records["user/disabled-student"].URL, "include_disabled=true") {
		t.Fatal("disabled manifest record does not point to the administrative directory")
	}
	disabled = false
	if err := r.disableAccount("disabled-student", a); err != nil || writes != 1 {
		t.Fatalf("replay disabled a developer-enabled account: %v", err)
	}
}

func TestAdministrativeUserFindsDisabledAccountBeyondFirstPage(t *testing.T) {
	t.Parallel()
	calls := 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Query().Get("include_disabled") != "true" || req.URL.Query().Get("limit") != "200" {
			t.Fatal("administrative scope was lost")
		}
		if calls == 1 {
			items := make([]object, 200)
			for i := range items {
				items[i] = object{"id": fmt.Sprintf("user-%03d", i), "username": fmt.Sprintf("candidate-%03d", i)}
			}
			items[199]["username"] = "candidate-é+199"
			raw, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			return response(200, string(raw)), nil
		}
		if req.URL.Query().Get("after_id") != "user-199" || req.URL.Query().Get("after_username") != "candidate-é+199" {
			t.Fatal("directory cursor was not preserved")
		}
		return response(200, `[{"id":"target","username":"renamed-user","disabled_at":123}]`), nil
	})
	r.state.Accounts["administrator"] = &account{Token: "token"}
	raw, err := r.administrativeUser("target")
	if err != nil || calls != 2 || number(decode(raw), "disabled_at") != 123 {
		t.Fatalf("paged lookup failed: %v", err)
	}
}
