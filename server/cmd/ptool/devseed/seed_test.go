// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func unitRunner(t *testing.T, transport transportFunc) *runner {
	t.Helper()
	return &runner{ctx: context.Background(), options: Options{StateDir: t.TempDir()}, state: &journal{RunID: strings.Repeat("a", 32), Server: "http://127.0.0.1:8065", Accounts: map[string]*account{}, Operations: map[string]*operation{}}, client: &http.Client{Transport: transport}, out: io.Discard}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestLoopbackOrigins(t *testing.T) {
	for _, value := range []string{"https://127.0.0.1:8065", "http://127.0.0.1:8065@evil.example", "http://localhost.evil:8065", "http://localhost:8065/path", "http://localhost:8065?target=x", "http://localhost:8065#x", "http://localhost", "http://192.168.1.1:8065", "http://127.0.0.1:99999", "http://[::]:8065"} {
		_, err := loopbackOrigin(value)
		assert.Error(t, err, "accepted %s", value)
	}
	for _, value := range []string{"http://127.0.0.1:8065", "http://localhost:8065", "http://[::1]:8065/"} {
		_, err := loopbackOrigin(value)
		assert.NoError(t, err, "rejected %s", value)
	}
}

func TestContentSeedIsReproducibleAndProfilesExercisePagination(t *testing.T) {
	first, second := people(42, 150), people(42, 150)
	require.Equal(t, first, second, "content seed does not control the generated people")
	require.NotEqual(t, people(43, 150), people(42, 150), "content seed does not control the generated people")
	require.Greater(t, sizeFor("desktop").Students/2, 50, "fixtures do not exceed page boundaries")
	require.Greater(t, sizeFor("large").Students/2, 200, "fixtures do not exceed page boundaries")
	r := unitRunner(t, nil)
	a, err := r.account("a", "a", "a@northbridge.example")
	require.NoError(t, err)
	b, err := r.account("b", "b", "b@northbridge.example")
	require.NoError(t, err)
	require.NotEqual(t, b.Password, a.Password, "credentials are not independently generated")
}

func TestUnknownOutcomeKeepsExactRequestAndIdempotencyKey(t *testing.T) {
	var bodies, keys []string
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(raw))
		keys = append(keys, req.Header.Get("Idempotency-Key"))
		if len(bodies) == 1 {
			return nil, errors.New("lost response")
		}
		return response(201, `{"id":"created"}`), nil
	})
	_, err := r.step("create", "token", "POST", "/api/v1/exams", object{"expected_revision": 1}, nil, nil)
	require.Error(t, err, "expected transport failure")
	// Recover from disk, not just the in-memory state of the failed call.
	raw, err := os.ReadFile(filepath.Join(r.options.StateDir, "journal.json"))
	require.NoError(t, err)
	err = json.Unmarshal(raw, &r.state)
	require.NoError(t, err)
	_, err = r.step("create", "token", "POST", "/api/v1/exams", object{"expected_revision": 2}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, bodies[1], bodies[0], "unknown outcome changed request semantics")
	require.NotEmpty(t, keys[0], "unknown outcome changed request semantics")
	require.Equal(t, keys[1], keys[0], "unknown outcome changed request semantics")
	_, err = r.step("create", "token", "POST", "/api/v1/example", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, bodies, 2, "completed operation was sent again")
}

func TestUnknownNonIdempotentCreateReconcilesWithoutDuplicate(t *testing.T) {
	mutations := 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == "GET" {
			return response(200, `[{"name":"science","id":"existing"}]`), nil
		}
		mutations++
		return nil, errors.New("lost response")
	})
	_, err := r.create("unit", "/api/v1/academic-units", "token", object{"name": "science"})
	require.Error(t, err, "expected unknown outcome")
	value, err := r.create("unit", "/api/v1/academic-units", "token", object{"name": "science"})
	require.NoError(t, err)
	require.Equal(t, "existing", str(value, "id"))
	require.Equal(t, 1, mutations)
}

func TestOldUnknownOutcomeRequiresAnAuthoritativeRead(t *testing.T) {
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		require.FailNow(t, "expired outcome caused another request")
		return nil, nil
	})
	r.state.Operations["old"] = &operation{PreparedAt: time.Now().Add(-2 * time.Hour), Method: "POST", Path: "/api/v1/exams", Body: json.RawMessage(`{}`)}
	_, err := r.step("old", "token", "POST", "/api/v1/exams", nil, nil, nil)
	require.Error(t, err, "old unknown outcome was retried")
	value, err := r.step("old", "token", "POST", "/api/v1/exams", nil, nil, func() (json.RawMessage, bool, error) {
		return json.RawMessage(`{"id":"already-created"}`), true, nil
	})
	require.NoError(t, err)
	require.Equal(t, "already-created", str(value, "id"))
}

func TestCorruptJournalIsRejectedBeforeServerAccess(t *testing.T) {
	for _, corrupt := range []string{"account", "operation"} {
		t.Run(corrupt, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "seed")
			require.NoError(t, os.Mkdir(directory, 0700))
			state := &journal{Version: schemaVersion, RunID: strings.Repeat("a", 32), Server: "http://127.0.0.1:8065", Mailpit: "http://127.0.0.1:18025", Profile: "small", Seed: 42,
				StartedAt: time.Now(), ScheduleAt: time.Now(), Accounts: map[string]*account{}, Operations: map[string]*operation{}, Records: map[string]record{}}
			if corrupt == "account" {
				state.Accounts["broken"] = nil
			} else {
				state.Operations["broken"] = nil
			}
			require.NoError(t, writeJSON(filepath.Join(directory, "journal.json"), state))
			err := Run(context.Background(), Options{ServerURL: state.Server, MailpitURL: state.Mailpit, Profile: state.Profile, Seed: state.Seed, StateDir: directory, EnvironmentFile: "unused"}, io.Discard)
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid seed journal "+corrupt)
		})
	}
}

func TestCollectionPagingKeepsEveryItem(t *testing.T) {
	calls := 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, `{"items":[{"id":"first"}],"next_cursor":"second-page"}`), nil
		}
		require.Equal(t, "second-page", req.URL.Query().Get("cursor"), "paging changed the query")
		require.Equal(t, "50", req.URL.Query().Get("limit"), "paging changed the query")
		return response(200, `{"items":[{"id":"second"}]}`), nil
	})
	items, err := r.list("/api/v1/classes/example/members?limit=50", "token")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, 2, calls)
	require.Equal(t, "second", str(items[1], "id"))
}

func TestClientWithholdsSecretsAndRefusesRedirects(t *testing.T) {
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		return response(400, `{"detail":"private-secret"}`), nil
	})
	_, err := r.get("/api/v1/bootstrap", "secret")
	require.Error(t, err, "response content escaped")
	require.NotContains(t, err.Error(), "private-secret", "response content escaped")
	client := localHTTPClient()
	defer client.CloseIdleConnections()
	require.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse, "redirects are enabled")
}

func TestStateFilesArePrivateAndConcurrentWriterIsRejected(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "journal.json")
	require.NoError(t, writeJSON(file, object{"secret": "private"}))
	info, _ := os.Stat(file)
	require.Equal(t, uint32(0600), uint32(info.Mode().Perm()), "journal is not private")
	unlock, err := lockState(filepath.Join(directory, "lock"))
	require.NoError(t, err)
	defer unlock()
	if release, lockErr := lockState(filepath.Join(directory, "lock")); lockErr == nil {
		release()
		require.FailNow(t, "second writer acquired state")
	}
	link := filepath.Join(directory, "credentials.json")
	err = os.Symlink(file, link)
	require.NoError(t, err)
	err = writeJSON(link, object{})
	require.Error(t, err, "followed state symlink")
}

func TestForeignPaginationCannotReceiveCredentials(t *testing.T) {
	calls := 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		calls++
		resp := response(200, `[]`)
		resp.Header.Set("Link", `<http://evil.example/items>; rel="next"`)
		return resp, nil
	})
	_, err := r.list("/api/v1/users", "private-token")
	require.Error(t, err, "foreign pagination was followed")
	require.Equal(t, 1, calls, "foreign pagination was followed")
}

func TestTOTPMatchesRFC6238SHA1Vector(t *testing.T) {
	value, err := totp("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0))
	require.NoError(t, err, "TOTP")
	require.Equal(t, "287082", value, "TOTP")
}

func TestUploadsKeepMetadataAndContentIncludingEmptyRecovery(t *testing.T) {
	for _, content := range [][]byte{[]byte("# Workspace\n"), {}} {
		calls := 0
		r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
			calls++
			reader, err := req.MultipartReader()
			require.NoError(t, err)
			part, err := reader.NextPart()
			require.NoError(t, err)
			metadata, err := io.ReadAll(part)
			require.NoError(t, err)
			require.Equal(t, int64(len(content)), number(decode(metadata), "size"), "wrong declared size")
			part, err = reader.NextPart()
			require.NoError(t, err)
			actual, err := io.ReadAll(part)
			require.NoError(t, err, "uploaded bytes differ")
			require.True(t, bytes.Equal(actual, content), "uploaded bytes differ")
			if calls == 1 {
				return nil, errors.New("lost response")
			}
			return response(201, `{"id":"created"}`), nil
		})
		_, _ = r.step("upload", "token", "POST", "/api/v1/exams/example/draft/starter-workspace/files", uploadMetadata(content, "text/plain"), content, nil)
		raw, err := os.ReadFile(filepath.Join(r.options.StateDir, "journal.json"))
		require.NoError(t, err)
		err = json.Unmarshal(raw, &r.state)
		require.NoError(t, err)
		_, err = r.step("upload", "token", "POST", "unused", nil, nil, nil)
		require.NoError(t, err)
	}
}

func TestRunRejectsInvalidOptionsBeforeCreatingState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "absent")
	var out bytes.Buffer
	err := Run(context.Background(), Options{ServerURL: "http://remote.example:8065", MailpitURL: "http://localhost:18025", StateDir: directory}, &out)
	require.Error(t, err, "accepted foreign origin")
	_, err = os.Stat(directory)
	require.ErrorIs(t, err, os.ErrNotExist, "invalid invocation created state")
}

func TestCanonicalOriginMismatchStopsBeforeBootstrap(t *testing.T) {
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method, "origin mismatch continued to account creation")
		require.Equal(t, "/api/v1/discovery", req.URL.Path, "origin mismatch continued to account creation")
		return response(200, `{"canonical_origin":"http://localhost:8065"}`), nil
	})
	err := r.bootstrap()
	require.Error(t, err)
	require.Contains(t, err.Error(), "canonical origin")
	require.Empty(t, r.state.Accounts, "origin mismatch created an account")
}

func TestLongRunRenewsExpiredCredentialsAndRetainsRequestSemantics(t *testing.T) {
	logins, rejected, completed := 0, 0, 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/auth/login":
			logins++
			return response(200, `{"tokens":{"access_token":"renewed","refresh_token":"refresh"}}`), nil
		case "/api/v1/users/me":
			if req.Header.Get("Authorization") != "Bearer renewed" {
				return response(401, `{}`), nil
			}
			return response(200, `{"id":"fixture-user"}`), nil
		case "/api/v1/exams":
			raw, _ := io.ReadAll(req.Body)
			require.Equal(t, `{"title":"Example"}`, string(raw), "credential renewal changed mutation semantics")
			require.Equal(t, "saved-key", req.Header.Get("Idempotency-Key"), "credential renewal changed mutation semantics")
			if req.Header.Get("Authorization") == "Bearer expired" {
				rejected++
				return response(401, `{}`), nil
			}
			require.Equal(t, "Bearer renewed", req.Header.Get("Authorization"), "missing renewed credential")
			completed++
			return response(201, `{"id":"example"}`), nil
		default:
			require.FailNow(t, fmt.Sprintf("unexpected request path: %s", req.URL.Path))
			return nil, nil
		}
	})
	r.state.Accounts["teacher"] = &account{Username: "teacher", Password: "fixture-password", Token: "expired", UserID: "fixture-user"}
	for range 2 {
		_, _, err := r.request(r.state.Server, "POST", "/api/v1/exams", "expired", "saved-key", json.RawMessage(`{"title":"Example"}`), nil)
		require.NoError(t, err)
	}
	require.Equal(t, 1, logins, "expired token aliases did not follow the renewed session")
	require.Equal(t, 1, rejected, "expired token aliases did not follow the renewed session")
	require.Equal(t, 2, completed, "expired token aliases did not follow the renewed session")
	require.Equal(t, "renewed", r.state.Accounts["teacher"].Token, "expired token aliases did not follow the renewed session")
}

func TestExpiredAccessRotatesTheSavedSession(t *testing.T) {
	refreshes := 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/v1/users/me":
			if req.Header.Get("Authorization") == "Bearer expired" {
				return response(401, `{}`), nil
			}
			return response(200, `{"id":"fixture-user"}`), nil
		case "/api/v1/auth/refresh":
			refreshes++
			require.Equal(t, "Bearer previous-refresh", req.Header.Get("Authorization"), "refresh did not use the saved credential")
			return response(200, `{"tokens":{"access_token":"renewed","refresh_token":"rotated-refresh"}}`), nil
		default:
			require.FailNow(t, "renewal attempted a new password login")
			return nil, nil
		}
	})
	a := &account{Username: "teacher", Password: "fixture-password", UserID: "fixture-user", Token: "expired", RefreshToken: "previous-refresh"}
	r.state.Accounts["teacher"] = a
	require.NoError(t, r.login(a))
	require.Equal(t, 1, refreshes, "session rotation did not save both replacement credentials")
	require.Equal(t, "renewed", a.Token, "session rotation did not save both replacement credentials")
	require.Equal(t, "rotated-refresh", a.RefreshToken, "session rotation did not save both replacement credentials")
}
