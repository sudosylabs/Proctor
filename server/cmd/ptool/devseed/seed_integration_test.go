//go:build integration

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
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/testlib"
)

// This test creates and drops only its own randomly named database. All fixture
// data is created over HTTP against the real composition graph, including real
// PostgreSQL, file-content processing, Jobs, and SMTP delivery to local Mailpit.
func TestDevelopmentSeedIntegration(t *testing.T) {
	dsn := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	mailpit := os.Getenv("PROCTOR_TEST_MAILPIT_HTTP_URL")
	smtp := os.Getenv("PROCTOR_TEST_MAIL_SMTP_ADDRESS")
	if dsn == "" || mailpit == "" || smtp == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL, PROCTOR_TEST_MAILPIT_HTTP_URL, and PROCTOR_TEST_MAIL_SMTP_ADDRESS are required")
	}
	control, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	suffix, err := randomHex(8)
	if err != nil {
		t.Fatal(err)
	}
	database := "ptool_seed_" + suffix
	if _, err = control.Exec(`CREATE DATABASE ` + database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := control.Exec(`DROP DATABASE ` + database + ` WITH (FORCE)`); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + database
	settings := config.Default().Database
	settings.DataSource = u.String()
	migrator, err := sqlstore.NewMigrator(context.Background(), sqlstore.SettingsFromConfig(settings))
	if err != nil {
		t.Fatal(err)
	}
	if err = migrator.Up(); err != nil {
		t.Fatal(err)
	}
	if err = migrator.Close(); err != nil {
		t.Fatal(err)
	}
	persistence, err := sqlstore.New(context.Background(), sqlstore.SettingsFromConfig(settings))
	if err != nil {
		t.Fatal(err)
	}
	var helper *testlib.Helper
	var loseResponse atomic.Bool
	var losePictureRequest atomic.Bool
	var loseDisableResponse atomic.Bool
	var writes atomic.Int64
	api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			writes.Add(1)
		}
		if req.Method == "PUT" && strings.HasSuffix(req.URL.Path, "/profile-picture") && losePictureRequest.CompareAndSwap(true, false) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/programmes") && loseResponse.CompareAndSwap(true, false) {
			recorder := httptest.NewRecorder()
			helper.Handler().ServeHTTP(recorder, req)
			if recorder.Code != 201 {
				t.Errorf("fault target failed before response loss: HTTP %d", recorder.Code)
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		recorder := httptest.NewRecorder()
		helper.Handler().ServeHTTP(recorder, req)
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/disable") && loseDisableResponse.CompareAndSwap(true, false) {
			if recorder.Code != http.StatusOK {
				t.Errorf("disablement fault target failed: HTTP %d", recorder.Code)
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		if recorder.Code >= 400 {
			var problem struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(recorder.Body.Bytes(), &problem)
			t.Logf("%s %s -> %d %s", req.Method, req.URL.Path, recorder.Code, problem.Code)
		}
		for key, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	origin := "http://" + api.Listener.Addr().String()
	helper = testlib.Setup(t, testlib.WithStore(persistence), testlib.WithConfiguredMailer(), testlib.WithConfig(func(cfg *config.Config) {
		cfg.Server.ListenAddress = "127.0.0.1:0"
		cfg.Server.PublicURL = origin
		cfg.Authentication.MFA.Enabled = true
		cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts = 10000
		cfg.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
		cfg.Mail.Enabled = true
		cfg.Mail.SMTP.Address = smtp
		cfg.Mail.FromAddress = "seed@northbridge.example"
	}))
	api.Start()
	t.Cleanup(api.Close)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- helper.Server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for !helper.Server.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("server readiness timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	dir := filepath.Join(t.TempDir(), "seed")
	env := filepath.Join(t.TempDir(), "environment")
	if err = os.WriteFile(env, []byte("PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET="+testlib.BootstrapSecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := os.Getenv("PROCTOR_TEST_SEED_PROFILE")
	if profile == "" {
		profile = "small"
	}
	options := Options{ServerURL: origin, MailpitURL: mailpit, StateDir: dir, EnvironmentFile: env, Profile: profile, Seed: 42}
	var output bytes.Buffer
	loseResponse.Store(true)
	if err = Run(ctx, options, &output); err == nil {
		t.Fatal("expected interrupted creation")
	}
	if loseResponse.Load() {
		t.Fatalf("seed stopped before the injected failure: %v", err)
	}
	loseDisableResponse.Store(true)
	if err = Run(ctx, options, &output); err == nil || loseDisableResponse.Load() {
		t.Fatalf("seed did not reach the lost disablement response: %v", err)
	}
	losePictureRequest.Store(true)
	if err = Run(ctx, options, &output); err == nil || losePictureRequest.Load() {
		t.Fatalf("seed did not reach the interrupted picture upload: %v", err)
	}
	if err = Run(ctx, options, &output); err != nil {
		t.Fatal(err)
	}
	readJournal := func() *journal {
		raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
		if err != nil {
			t.Fatal(err)
		}
		var state journal
		if err = json.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}
		return &state
	}
	state := readJournal()
	if len(state.Accounts) != sizeFor(profile).Students+sizeFor(profile).Teachers+4 {
		t.Fatal("incomplete account population")
	}
	foreign := options
	foreign.StateDir = filepath.Join(t.TempDir(), "unrelated-seed")
	foreignBefore := writes.Load()
	if err = Run(ctx, foreign, io.Discard); err == nil || !strings.Contains(err.Error(), "initialized installation") {
		t.Fatalf("another journal was allowed to adopt this installation: %v", err)
	}
	if writes.Load() != foreignBefore {
		t.Fatal("foreign journal mutated the initialized installation")
	}
	r := &runner{ctx: ctx, options: options, state: state, client: localHTTPClient(), out: io.Discard}
	defer r.client.CloseIdleConnections()
	// A lost or expired local access credential must recover through ordinary
	// server authentication, including when a caller retains the old token.
	var sessionsBefore, sessionsAfter int
	if err = persistence.GetMaster().Get(ctx, &sessionsBefore, "SELECT count(*) FROM sessions WHERE user_id = ?", state.Accounts["administrator"].UserID); err != nil {
		t.Fatal(err)
	}
	state.Accounts["administrator"].Token = "expired-fixture-token"
	if _, err = r.get("/api/v1/institution", "expired-fixture-token"); err != nil {
		t.Fatalf("could not renew an invalidated fixture credential: %v", err)
	}
	if err = persistence.GetMaster().Get(ctx, &sessionsAfter, "SELECT count(*) FROM sessions WHERE user_id = ?", state.Accounts["administrator"].UserID); err != nil || sessionsBefore != sessionsAfter {
		t.Fatalf("access renewal accumulated another session: %d -> %d, %v", sessionsBefore, sessionsAfter, err)
	}
	admin := state.Accounts["administrator"].Token
	for _, alias := range []string{"disabled-student", "disabled-teacher"} {
		a := state.Accounts[alias]
		raw, readErr := r.administrativeUser(a.UserID)
		if readErr != nil || number(decode(raw), "disabled_at") == 0 || !a.SeededDisabled {
			t.Fatalf("disabled fixture %s missing: %v", alias, readErr)
		}
		loginBody, _ := json.Marshal(object{"login_id": a.Username, "password": a.Password, "client_type": "cli", "device_id": "disabled-fixture-test", "device_name": "Seed test"})
		_, _, loginErr := r.request(origin, "POST", "/api/v1/auth/login", "", "", loginBody, nil)
		var rejected *responseError
		if !errors.As(loginErr, &rejected) || rejected.Status != http.StatusUnauthorized {
			t.Fatalf("disabled account %s was not rejected: %v", alias, loginErr)
		}
	}
	var customPictures int
	if err = persistence.GetMaster().Get(ctx, &customPictures, "SELECT count(*) FROM users WHERE custom_profile_picture_file_id IS NOT NULL"); err != nil || customPictures != 6 {
		t.Fatalf("custom/default picture mixture incorrect: %d, %v", customPictures, err)
	}
	for _, alias := range []string{"candidate-001", "candidate-002", "teacher-02"} {
		for _, size := range []string{"128", "256", "512"} {
			path := "/api/v1/users/" + state.Accounts[alias].UserID + "/profile-picture?size=" + size
			content, headers, readErr := r.request(origin, "GET", path, admin, "", nil, nil)
			if readErr != nil || headers.Get("Content-Type") != "image/webp" || !pictureETag(headers.Get("ETag")) || len(content) < 12 || string(content[8:12]) != "WEBP" {
				t.Fatalf("profile picture %s/%s unavailable: %v", alias, size, readErr)
			}
		}
	}
	for key, expected := range map[string]string{
		"exam/03/file/schema.sql":                 "CREATE TABLE loans",
		"exam/03/resource/Report parameters.json": "2030-03-15",
		"exam/04/file/analysis/summary.py":        "def summarize(rows)",
		"exam/04/resource/Data dictionary.json":   "degrees Celsius",
	} {
		raw, readErr := r.get(state.Records[key].URL, admin)
		if readErr != nil || !bytes.Contains(raw, []byte(expected)) {
			t.Fatalf("exercise material %s missing: %v", key, readErr)
		}
	}
	programmes, err := r.list("/api/v1/academic-units/"+state.Records["unit/0"].ID+"/programmes", admin)
	if err != nil || len(programmes) != 2 {
		t.Fatalf("duplicate or missing programmes after recovery: %d, %v", len(programmes), err)
	}
	for _, key := range []string{"candidate-001", "academic-reader"} {
		raw, err := r.get("/api/v1/users/me/mfa", state.Accounts[key].Token)
		if err != nil || decode(raw)["enabled"] != true {
			t.Fatalf("MFA fixture %s missing: %v", key, err)
		}
	}
	raw, err := r.get(state.Records["exam/01/file/src/main.py"].URL, admin)
	if err != nil || !bytes.Contains(raw, []byte("unique_records")) {
		t.Fatalf("starter bytes unavailable: %v", err)
	}
	raw, err = r.get(state.Records["exam/01/file/notes/design-decisions-and-test-observations-for-the-final-implementation.md"].URL, admin)
	if err != nil || len(raw) != 0 {
		t.Fatalf("empty file unavailable: %v", err)
	}
	classID := state.Records["class/0"].ID
	members, err := r.list("/api/v1/classes/"+classID+"/members?limit=50", admin)
	if err != nil || len(members) != sizeFor(profile).Students/2 {
		t.Fatalf("roster paging: got %d, %v", len(members), err)
	}
	// Changing a seeded display name must survive a successful seed replay.
	unitID := state.Records["unit/0"].ID
	body, _ := json.Marshal(object{"display_name": "Developer's edited unit"})
	if _, _, err = r.request(origin, "PATCH", "/api/v1/academic-units/"+unitID, admin, "", body, nil); err != nil {
		t.Fatal(err)
	}
	enabledUserPath := "/api/v1/users/" + state.Accounts["disabled-student"].UserID
	if _, _, err = r.request(origin, "POST", enabledUserPath+"/enable", admin, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	picturePath := "/api/v1/users/" + state.Accounts["candidate-003"].UserID + "/profile-picture"
	_, pictureHeaders, err := r.request(origin, "GET", picturePath, admin, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.requestWithMatch(origin, "DELETE", picturePath, admin, "", nil, nil, pictureHeaders.Get("ETag")); err != nil {
		t.Fatal(err)
	}
	_, defaultHeaders, err := r.request(origin, "GET", picturePath, admin, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := writes.Load()
	if err = Run(ctx, options, &output); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != before {
		t.Fatal("completed replay mutated server state")
	}
	raw, err = r.get("/api/v1/academic-units/"+unitID, admin)
	if err != nil || str(decode(raw), "display_name") != "Developer's edited unit" {
		t.Fatal("replay overwrote developer edits")
	}
	// The limited account cannot create an Exam, even in its own Unit.
	body, _ = json.Marshal(object{"academic_unit_id": unitID, "title": "Should be denied"})
	_, _, err = r.request(origin, "POST", "/api/v1/exams", state.Accounts["academic-reader"].Token, "seed-denied", body, nil)
	var denied *responseError
	if !errors.As(err, &denied) || denied.Status != 403 {
		t.Fatalf("limited role did not deny exam creation: %v", err)
	}
	options.RefreshSittings = true
	if err = Run(ctx, options, &output); err != nil {
		t.Fatal(err)
	}
	if readJournal().Generation != 1 {
		t.Fatal("fresh sitting generation was not recorded")
	}
	raw, err = r.get(enabledUserPath, admin)
	if err != nil || number(decode(raw), "disabled_at") != 0 {
		t.Fatalf("replay or refresh disabled a developer-enabled User: %v", err)
	}
	_, refreshedHeaders, err := r.request(origin, "GET", picturePath, admin, "", nil, nil)
	if err != nil || refreshedHeaders.Get("ETag") != defaultHeaders.Get("ETag") {
		t.Fatalf("replay or refresh restored a removed custom picture: %v", err)
	}
	for _, a := range state.Accounts {
		if strings.Contains(output.String(), a.Password) || a.MFASecret != "" && strings.Contains(output.String(), a.MFASecret) {
			t.Fatal("credential leaked into command output")
		}
	}
	var attempts int
	if err = persistence.GetMaster().Get(ctx, &attempts, "SELECT count(*) FROM exam_attempts"); err != nil || attempts != 0 {
		t.Fatalf("unexpected live exam fixture: %d, %v", attempts, err)
	}
	t.Logf("Verified %s fixture: %d accounts; resume, read-only replay, file bytes, roles, MFA, and fresh Sittings", profile, len(state.Accounts))
}
