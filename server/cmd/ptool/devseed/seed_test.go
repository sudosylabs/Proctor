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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
		if _, err := loopbackOrigin(value); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
	for _, value := range []string{"http://127.0.0.1:8065", "http://localhost:8065", "http://[::1]:8065/"} {
		if _, err := loopbackOrigin(value); err != nil {
			t.Errorf("rejected %s: %v", value, err)
		}
	}
}

func TestContentSeedIsReproducibleAndProfilesExercisePagination(t *testing.T) {
	if !reflect.DeepEqual(people(42, 150), people(42, 150)) || reflect.DeepEqual(people(42, 150), people(43, 150)) {
		t.Fatal("content seed does not control the generated people")
	}
	if sizeFor("desktop").Students/2 <= 50 || sizeFor("large").Students/2 <= 200 {
		t.Fatal("fixtures do not exceed page boundaries")
	}
	r := unitRunner(t, nil)
	a, err := r.account("a", "a", "a@northbridge.example")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.account("b", "b", "b@northbridge.example")
	if err != nil {
		t.Fatal(err)
	}
	if a.Password == b.Password {
		t.Fatal("credentials are not independently generated")
	}
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
	if _, err := r.step("create", "token", "POST", "/api/v1/exams", object{"expected_revision": 1}, nil, nil); err == nil {
		t.Fatal("expected transport failure")
	}
	// Recover from disk, not just the in-memory state of the failed call.
	raw, err := os.ReadFile(filepath.Join(r.options.StateDir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &r.state); err != nil {
		t.Fatal(err)
	}
	if _, err = r.step("create", "token", "POST", "/api/v1/exams", object{"expected_revision": 2}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if bodies[0] != bodies[1] || keys[0] == "" || keys[0] != keys[1] {
		t.Fatal("unknown outcome changed request semantics")
	}
	if _, err = r.step("create", "token", "POST", "/api/v1/example", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatal("completed operation was sent again")
	}
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
	if _, err := r.create("unit", "/api/v1/academic-units", "token", object{"name": "science"}); err == nil {
		t.Fatal("expected unknown outcome")
	}
	value, err := r.create("unit", "/api/v1/academic-units", "token", object{"name": "science"})
	if err != nil || str(value, "id") != "existing" || mutations != 1 {
		t.Fatalf("reconciliation failed: %v", err)
	}
}

func TestOldUnknownOutcomeRequiresAnAuthoritativeRead(t *testing.T) {
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		t.Fatal("expired outcome caused another request")
		return nil, nil
	})
	r.state.Operations["old"] = &operation{PreparedAt: time.Now().Add(-2 * time.Hour), Method: "POST", Path: "/api/v1/exams", Body: json.RawMessage(`{}`)}
	if _, err := r.step("old", "token", "POST", "/api/v1/exams", nil, nil, nil); err == nil {
		t.Fatal("old unknown outcome was retried")
	}
	value, err := r.step("old", "token", "POST", "/api/v1/exams", nil, nil, func() (json.RawMessage, bool, error) {
		return json.RawMessage(`{"id":"already-created"}`), true, nil
	})
	if err != nil || str(value, "id") != "already-created" {
		t.Fatalf("authoritative recovery failed: %v", err)
	}
}

func TestCorruptJournalIsRejectedBeforeServerAccess(t *testing.T) {
	for _, corrupt := range []string{"account", "operation"} {
		t.Run(corrupt, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "seed")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			state := &journal{Version: schemaVersion, RunID: strings.Repeat("a", 32), Server: "http://127.0.0.1:8065", Mailpit: "http://127.0.0.1:18025", Profile: "small", Seed: 42,
				StartedAt: time.Now(), ScheduleAt: time.Now(), Accounts: map[string]*account{}, Operations: map[string]*operation{}, Records: map[string]record{}}
			if corrupt == "account" {
				state.Accounts["broken"] = nil
			} else {
				state.Operations["broken"] = nil
			}
			if err := writeJSON(filepath.Join(directory, "journal.json"), state); err != nil {
				t.Fatal(err)
			}
			err := Run(context.Background(), Options{ServerURL: state.Server, MailpitURL: state.Mailpit, Profile: state.Profile, Seed: state.Seed, StateDir: directory, EnvironmentFile: "unused"}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "invalid seed journal "+corrupt) {
				t.Fatalf("corrupt journal was not rejected: %v", err)
			}
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
		if req.URL.Query().Get("cursor") != "second-page" || req.URL.Query().Get("limit") != "50" {
			t.Fatal("paging changed the query")
		}
		return response(200, `{"items":[{"id":"second"}]}`), nil
	})
	items, err := r.list("/api/v1/classes/example/members?limit=50", "token")
	if err != nil || len(items) != 2 || calls != 2 || str(items[1], "id") != "second" {
		t.Fatalf("pagination failed: %v", err)
	}
}

func TestClientWithholdsSecretsAndRefusesRedirects(t *testing.T) {
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		return response(400, `{"detail":"private-secret"}`), nil
	})
	_, err := r.get("/api/v1/bootstrap", "secret")
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal("response content escaped")
	}
	client := localHTTPClient()
	defer client.CloseIdleConnections()
	if !errors.Is(client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("redirects are enabled")
	}
}

func TestStateFilesArePrivateAndConcurrentWriterIsRejected(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "journal.json")
	if err := writeJSON(file, object{"secret": "private"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("journal is not private")
	}
	unlock, err := lockState(filepath.Join(directory, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if release, lockErr := lockState(filepath.Join(directory, "lock")); lockErr == nil {
		release()
		t.Fatal("second writer acquired state")
	}
	link := filepath.Join(directory, "credentials.json")
	if err = os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if err = writeJSON(link, object{}); err == nil {
		t.Fatal("followed state symlink")
	}
}

func TestForeignPaginationCannotReceiveCredentials(t *testing.T) {
	calls := 0
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		calls++
		resp := response(200, `[]`)
		resp.Header.Set("Link", `<http://evil.example/items>; rel="next"`)
		return resp, nil
	})
	if _, err := r.list("/api/v1/users", "private-token"); err == nil || calls != 1 {
		t.Fatal("foreign pagination was followed")
	}
}

func TestTOTPMatchesRFC6238SHA1Vector(t *testing.T) {
	value, err := totp("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0))
	if err != nil || value != "287082" {
		t.Fatalf("TOTP = %s, %v", value, err)
	}
}

func TestUploadsKeepMetadataAndContentIncludingEmptyRecovery(t *testing.T) {
	for _, content := range [][]byte{[]byte("# Workspace\n"), {}} {
		calls := 0
		r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
			calls++
			reader, err := req.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			if number(decode(metadata), "size") != int64(len(content)) {
				t.Fatal("wrong declared size")
			}
			part, err = reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(part)
			if err != nil || !bytes.Equal(actual, content) {
				t.Fatal("uploaded bytes differ")
			}
			if calls == 1 {
				return nil, errors.New("lost response")
			}
			return response(201, `{"id":"created"}`), nil
		})
		_, _ = r.step("upload", "token", "POST", "/api/v1/exams/example/draft/starter-workspace/files", uploadMetadata(content, "text/plain"), content, nil)
		raw, err := os.ReadFile(filepath.Join(r.options.StateDir, "journal.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &r.state); err != nil {
			t.Fatal(err)
		}
		if _, err = r.step("upload", "token", "POST", "unused", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunRejectsInvalidOptionsBeforeCreatingState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "absent")
	var out bytes.Buffer
	err := Run(context.Background(), Options{ServerURL: "http://remote.example:8065", MailpitURL: "http://localhost:18025", StateDir: directory}, &out)
	if err == nil {
		t.Fatal("accepted foreign origin")
	}
	if _, err = os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid invocation created state")
	}
}

func TestCanonicalOriginMismatchStopsBeforeBootstrap(t *testing.T) {
	r := unitRunner(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/v1/discovery" {
			t.Fatal("origin mismatch continued to account creation")
		}
		return response(200, `{"canonical_origin":"http://localhost:8065"}`), nil
	})
	if err := r.bootstrap(); err == nil || !strings.Contains(err.Error(), "canonical origin") {
		t.Fatalf("origin mismatch was not rejected: %v", err)
	}
	if len(r.state.Accounts) != 0 {
		t.Fatal("origin mismatch created an account")
	}
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
			if string(raw) != `{"title":"Example"}` || req.Header.Get("Idempotency-Key") != "saved-key" {
				t.Fatal("credential renewal changed mutation semantics")
			}
			if req.Header.Get("Authorization") == "Bearer expired" {
				rejected++
				return response(401, `{}`), nil
			}
			if req.Header.Get("Authorization") != "Bearer renewed" {
				t.Fatal("missing renewed credential")
			}
			completed++
			return response(201, `{"id":"example"}`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.Path)
			return nil, nil
		}
	})
	r.state.Accounts["teacher"] = &account{Username: "teacher", Password: "fixture-password", Token: "expired", UserID: "fixture-user"}
	for range 2 {
		if _, _, err := r.request(r.state.Server, "POST", "/api/v1/exams", "expired", "saved-key", json.RawMessage(`{"title":"Example"}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	if logins != 1 || rejected != 1 || completed != 2 || r.state.Accounts["teacher"].Token != "renewed" {
		t.Fatal("expired token aliases did not follow the renewed session")
	}
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
			if req.Header.Get("Authorization") != "Bearer previous-refresh" {
				t.Fatal("refresh did not use the saved credential")
			}
			return response(200, `{"tokens":{"access_token":"renewed","refresh_token":"rotated-refresh"}}`), nil
		default:
			t.Fatal("renewal attempted a new password login")
			return nil, nil
		}
	})
	a := &account{Username: "teacher", Password: "fixture-password", UserID: "fixture-user", Token: "expired", RefreshToken: "previous-refresh"}
	r.state.Accounts["teacher"] = a
	if err := r.login(a); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || a.Token != "renewed" || a.RefreshToken != "rotated-refresh" {
		t.Fatal("session rotation did not save both replacement credentials")
	}
}
