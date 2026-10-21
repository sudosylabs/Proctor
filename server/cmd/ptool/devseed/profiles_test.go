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

	"github.com/stretchr/testify/require"
)

func TestPicturePNG(t *testing.T) {
	t.Parallel()
	first, err := picturePNG(42, "candidate-001")
	require.NoError(t, err)
	again, err := picturePNG(42, "candidate-001")
	require.NoError(t, err)
	other, err := picturePNG(42, "candidate-003")
	require.NoError(t, err)
	require.True(t, bytes.Equal(first, again), "picture identity is not deterministic and varied")
	require.False(t, bytes.Equal(first, other), "picture identity is not deterministic and varied")
	decoded, err := png.Decode(bytes.NewReader(first))
	require.NoError(t, err)
	require.Equal(t, 256, decoded.Bounds().Dx())
	require.Equal(t, 256, decoded.Bounds().Dy())
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
				require.NoError(t, err)
				require.Equal(t, "image/png", req.Header.Get("Content-Type"), "upload lost its raw protocol or original precondition")
				require.Equal(t, `"`+strings.Repeat("a", 64)+`"`, req.Header.Get("If-Match"), "upload lost its raw protocol or original precondition")
				require.Empty(t, req.Header.Get("Idempotency-Key"), "upload lost its raw protocol or original precondition")
				_, err = png.Decode(bytes.NewReader(raw))
				require.NoError(t, err, "upload is not raw PNG")
				if puts == 1 {
					original = raw
					return nil, errors.New("lost response")
				}
				require.True(t, bytes.Equal(raw, original), "retry regenerated the saved picture")
				return response(200, `{"id":"user"}`), nil
			})
			r.state.Records = map[string]record{}
			r.state.Accounts["administrator"] = &account{Token: "token"}
			r.state.Accounts["candidate-001"] = &account{UserID: "user"}
			require.Error(t, r.profilePicture("candidate-001"), "expected response loss")
			raw, err := os.ReadFile(filepath.Join(r.options.StateDir, "journal.json"))
			require.NoError(t, err)
			err = json.Unmarshal(raw, &r.state)
			require.NoError(t, err)
			r.state.Seed = 999 // A retry must use the persisted bytes.
			if changed {
				tag = `"` + strings.Repeat("b", 64) + `"`
			}
			err = r.profilePicture("candidate-001")
			if changed {
				require.Error(t, err, "ambiguous picture was overwritten")
				require.Contains(t, err.Error(), "changed after upload preparation", "ambiguous picture was overwritten")
				require.Equal(t, 1, puts, "ambiguous picture was overwritten")
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, puts)
			require.True(t, r.state.Accounts["candidate-001"].SeededCustomPicture)
			err = r.profilePicture("candidate-001")
			require.NoError(t, err)
			require.Equal(t, 2, puts)
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
			return response(200, `[{"id":"user","disabled_at":"2026-01-02T03:04:05.123Z"}]`), nil
		}
		return response(200, `[{"id":"user"}]`), nil
	})
	r.state.Accounts["administrator"] = &account{Token: "token"}
	a := &account{UserID: "user", Token: "old-access", RefreshToken: "old-refresh"}
	r.state.Records = map[string]record{"user/disabled-student": {ID: "user", URL: "/api/v1/users/user"}}
	require.Error(t, r.disableAccount("disabled-student", a), "expected response loss")
	require.NoError(t, r.disableAccount("disabled-student", a))
	require.Equal(t, 1, writes, "disablement failed to reconcile and discard revoked credentials")
	require.True(t, a.SeededDisabled, "disablement failed to reconcile and discard revoked credentials")
	require.Empty(t, a.Token, "disablement failed to reconcile and discard revoked credentials")
	require.Empty(t, a.RefreshToken, "disablement failed to reconcile and discard revoked credentials")
	require.Contains(t, r.state.Records["user/disabled-student"].URL, "include_disabled=true", "disabled manifest record does not point to the administrative directory")
	disabled = false
	require.NoError(t, r.disableAccount("disabled-student", a))
	require.Equal(t, 1, writes)
}

func TestAdministrativeUserFindsDisabledAccountBeyondFirstPage(t *testing.T) {
	t.Parallel()
	calls := 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "true", req.URL.Query().Get("include_disabled"), "administrative scope was lost")
		require.Equal(t, "200", req.URL.Query().Get("limit"), "administrative scope was lost")
		if calls == 1 {
			items := make([]object, 200)
			for i := range items {
				items[i] = object{"id": fmt.Sprintf("user-%03d", i), "username": fmt.Sprintf("candidate-%03d", i)}
			}
			items[199]["username"] = "candidate-é+199"
			raw, err := json.Marshal(items)
			require.NoError(t, err)
			return response(200, string(raw)), nil
		}
		require.Equal(t, "user-199", req.URL.Query().Get("after_id"), "directory cursor was not preserved")
		require.Equal(t, "candidate-é+199", req.URL.Query().Get("after_username"), "directory cursor was not preserved")
		return response(200, `[{"id":"target","username":"renamed-user","disabled_at":"2026-01-02T03:04:05.123Z"}]`), nil
	})
	r.state.Accounts["administrator"] = &account{Token: "token"}
	raw, err := r.administrativeUser("target")
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, "2026-01-02T03:04:05.123Z", str(decode(raw), "disabled_at"))
}
