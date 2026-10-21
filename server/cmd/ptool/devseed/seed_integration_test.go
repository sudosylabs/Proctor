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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NotEmpty(t, dsn, "PROCTOR_TEST_DATABASE_URL, PROCTOR_TEST_MAILPIT_HTTP_URL, and PROCTOR_TEST_MAIL_SMTP_ADDRESS are required")
	require.NotEmpty(t, mailpit, "PROCTOR_TEST_DATABASE_URL, PROCTOR_TEST_MAILPIT_HTTP_URL, and PROCTOR_TEST_MAIL_SMTP_ADDRESS are required")
	require.NotEmpty(t, smtp, "PROCTOR_TEST_DATABASE_URL, PROCTOR_TEST_MAILPIT_HTTP_URL, and PROCTOR_TEST_MAIL_SMTP_ADDRESS are required")
	control, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = control.Close() })
	suffix, err := randomHex(8)
	require.NoError(t, err)
	database := "ptool_seed_" + suffix
	_, err = control.Exec(`CREATE DATABASE ` + database)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := control.Exec(`DROP DATABASE ` + database + ` WITH (FORCE)`)
		assert.NoError(t, err)
	})
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + database
	settings := config.Default().Database
	settings.DataSource = u.String()
	migrator, err := sqlstore.NewMigrator(context.Background(), sqlstore.SettingsFromConfig(settings))
	require.NoError(t, err)
	err = migrator.Up()
	require.NoError(t, err)
	err = migrator.Close()
	require.NoError(t, err)
	persistence, err := sqlstore.New(context.Background(), sqlstore.SettingsFromConfig(settings))
	require.NoError(t, err)
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
			assert.Equal(t, 201, recorder.Code, "fault target failed before response loss: HTTP")
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		recorder := httptest.NewRecorder()
		helper.Handler().ServeHTTP(recorder, req)
		if req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/disable") && loseDisableResponse.CompareAndSwap(true, false) {
			assert.Equal(t, http.StatusOK, recorder.Code, "disablement fault target failed: HTTP")
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
		assert.NoError(t, (<-done))
	})
	deadline := time.Now().Add(10 * time.Second)
	for !helper.Server.Ready() {
		require.False(t, time.Now().After(deadline), "server readiness timed out")
		time.Sleep(10 * time.Millisecond)
	}
	dir := filepath.Join(t.TempDir(), "seed")
	env := filepath.Join(t.TempDir(), "environment")
	err = os.WriteFile(env, []byte("PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET="+testlib.BootstrapSecret+"\n"), 0600)
	require.NoError(t, err)
	profile := os.Getenv("PROCTOR_TEST_SEED_PROFILE")
	if profile == "" {
		profile = "small"
	}
	options := Options{ServerURL: origin, MailpitURL: mailpit, StateDir: dir, EnvironmentFile: env, Profile: profile, Seed: 42}
	var output bytes.Buffer
	loseResponse.Store(true)
	err = Run(ctx, options, &output)
	require.Error(t, err, "expected interrupted creation")
	require.False(t, loseResponse.Load(), "seed stopped before the injected failure: %v", err)
	loseDisableResponse.Store(true)
	err = Run(ctx, options, &output)
	require.Error(t, err)
	require.False(t, loseDisableResponse.Load())
	losePictureRequest.Store(true)
	err = Run(ctx, options, &output)
	require.Error(t, err)
	require.False(t, losePictureRequest.Load())
	err = Run(ctx, options, &output)
	require.NoError(t, err)
	readJournal := func() *journal {
		raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
		require.NoError(t, err)
		var state journal
		err = json.Unmarshal(raw, &state)
		require.NoError(t, err)
		return &state
	}
	state := readJournal()
	require.Len(t, state.Accounts, sizeFor(profile).Students+sizeFor(profile).Teachers+4, "incomplete account population")
	foreign := options
	foreign.StateDir = filepath.Join(t.TempDir(), "unrelated-seed")
	foreignBefore := writes.Load()
	err = Run(ctx, foreign, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "initialized installation")
	require.Equal(t, foreignBefore, writes.Load(), "foreign journal mutated the initialized installation")
	r := &runner{ctx: ctx, options: options, state: state, client: localHTTPClient(), out: io.Discard}
	defer r.client.CloseIdleConnections()
	// A lost or expired local access credential must recover through ordinary
	// server authentication, including when a caller retains the old token.
	var sessionsBefore, sessionsAfter int
	err = persistence.GetMaster().Get(ctx, &sessionsBefore, "SELECT count(*) FROM sessions WHERE user_id = ?", state.Accounts["administrator"].UserID)
	require.NoError(t, err)
	state.Accounts["administrator"].Token = "expired-fixture-token"
	_, err = r.get("/api/v1/institution", "expired-fixture-token")
	require.NoError(t, err)
	err = persistence.GetMaster().Get(ctx, &sessionsAfter, "SELECT count(*) FROM sessions WHERE user_id = ?", state.Accounts["administrator"].UserID)
	require.NoError(t, err, "access renewal accumulated another session")
	require.Equal(t, sessionsAfter, sessionsBefore, "access renewal accumulated another session")
	admin := state.Accounts["administrator"].Token
	for _, alias := range []string{"disabled-student", "disabled-teacher"} {
		a := state.Accounts[alias]
		raw, readErr := r.administrativeUser(a.UserID)
		require.NoError(t, readErr, "disabled fixture %s missing", alias)
		require.NotEmpty(t, str(decode(raw), "disabled_at"), "disabled fixture %s missing", alias)
		require.True(t, a.SeededDisabled, "disabled fixture %s missing", alias)
		loginBody, _ := json.Marshal(object{"login_id": a.Username, "password": a.Password, "client_type": "cli", "device_id": "disabled-fixture-test", "device_name": "Seed test"})
		_, _, loginErr := r.request(origin, "POST", "/api/v1/auth/login", "", "", loginBody, nil)
		var rejected *responseError
		require.ErrorAs(t, loginErr, &rejected, "disabled account %s was not rejected", alias)
		require.Equal(t, http.StatusUnauthorized, rejected.Status, "disabled account %s was not rejected", alias)
	}
	var customPictures int
	err = persistence.GetMaster().Get(ctx, &customPictures, "SELECT count(*) FROM users WHERE custom_profile_picture_file_id IS NOT NULL")
	require.NoError(t, err, "custom/default picture mixture incorrect")
	require.Equal(t, 6, customPictures, "custom/default picture mixture incorrect")
	for _, alias := range []string{"candidate-001", "candidate-002", "teacher-02"} {
		for _, size := range []string{"128", "256", "512"} {
			path := "/api/v1/users/" + state.Accounts[alias].UserID + "/profile-picture?size=" + size
			content, headers, readErr := r.request(origin, "GET", path, admin, "", nil, nil)
			require.NoError(t, readErr, "profile picture %s/%s unavailable", alias, size)
			require.Equal(t, "image/webp", headers.Get("Content-Type"), "profile picture %s/%s unavailable", alias, size)
			require.True(t, pictureETag(headers.Get("ETag")), "profile picture %s/%s unavailable", alias, size)
			require.GreaterOrEqual(t, len(content), 12, "profile picture %s/%s unavailable", alias, size)
			require.Equal(t, "WEBP", string(content[8:12]), "profile picture %s/%s unavailable", alias, size)
		}
	}
	for key, expected := range map[string]string{
		"exam/03/file/schema.sql":                 "CREATE TABLE loans",
		"exam/03/resource/Report parameters.json": "2030-03-15",
		"exam/04/file/analysis/summary.py":        "def summarize(rows)",
		"exam/04/resource/Data dictionary.json":   "degrees Celsius",
	} {
		raw, readErr := r.get(state.Records[key].URL, admin)
		require.NoError(t, readErr, "exercise material %s missing", key)
		require.True(t, bytes.Contains(raw, []byte(expected)), "exercise material %s missing", key)
	}
	programmes, err := r.list("/api/v1/academic-units/"+state.Records["unit/0"].ID+"/programmes", admin)
	require.NoError(t, err, "duplicate or missing programmes after recovery")
	require.Len(t, programmes, 2, "duplicate or missing programmes after recovery")
	for _, key := range []string{"candidate-001", "academic-reader"} {
		raw, err := r.get("/api/v1/users/me/mfa", state.Accounts[key].Token)
		require.NoError(t, err, "MFA fixture %s missing", key)
		require.Equal(t, true, decode(raw)["enabled"], "MFA fixture %s missing", key)
	}
	raw, err := r.get(state.Records["exam/01/file/src/main.py"].URL, admin)
	require.NoError(t, err)
	require.True(t, bytes.Contains(raw, []byte("unique_records")))
	raw, err = r.get(state.Records["exam/01/file/notes/design-decisions-and-test-observations-for-the-final-implementation.md"].URL, admin)
	require.NoError(t, err)
	require.Empty(t, raw)
	classID := state.Records["class/0"].ID
	members, err := r.list("/api/v1/classes/"+classID+"/members?limit=50", admin)
	require.NoError(t, err, "roster paging")
	require.Len(t, members, sizeFor(profile).Students/2, "roster paging")
	// Changing a seeded display name must survive a successful seed replay.
	unitID := state.Records["unit/0"].ID
	body, _ := json.Marshal(object{"display_name": "Developer's edited unit"})
	_, _, err = r.request(origin, "PATCH", "/api/v1/academic-units/"+unitID, admin, "", body, nil)
	require.NoError(t, err)
	enabledUserPath := "/api/v1/users/" + state.Accounts["disabled-student"].UserID
	_, _, err = r.request(origin, "POST", enabledUserPath+"/enable", admin, "", nil, nil)
	require.NoError(t, err)
	picturePath := "/api/v1/users/" + state.Accounts["candidate-003"].UserID + "/profile-picture"
	_, pictureHeaders, err := r.request(origin, "GET", picturePath, admin, "", nil, nil)
	require.NoError(t, err)
	_, _, err = r.requestWithMatch(origin, "DELETE", picturePath, admin, "", nil, nil, pictureHeaders.Get("ETag"))
	require.NoError(t, err)
	_, defaultHeaders, err := r.request(origin, "GET", picturePath, admin, "", nil, nil)
	require.NoError(t, err)
	before := writes.Load()
	err = Run(ctx, options, &output)
	require.NoError(t, err)
	require.Equal(t, before, writes.Load(), "completed replay mutated server state")
	raw, err = r.get("/api/v1/academic-units/"+unitID, admin)
	require.NoError(t, err, "replay overwrote developer edits")
	require.Equal(t, "Developer's edited unit", str(decode(raw), "display_name"), "replay overwrote developer edits")
	// The limited account cannot create an Exam, even in its own Unit.
	body, _ = json.Marshal(object{"academic_unit_id": unitID, "title": "Should be denied"})
	_, _, err = r.request(origin, "POST", "/api/v1/exams", state.Accounts["academic-reader"].Token, "seed-denied", body, nil)
	var denied *responseError
	require.ErrorAs(t, err, &denied)
	require.Equal(t, 403, denied.Status)
	options.RefreshSittings = true
	err = Run(ctx, options, &output)
	require.NoError(t, err)
	require.Equal(t, 1, readJournal().Generation, "fresh sitting generation was not recorded")
	raw, err = r.get(enabledUserPath, admin)
	require.NoError(t, err)
	require.Empty(t, str(decode(raw), "disabled_at"))
	_, refreshedHeaders, err := r.request(origin, "GET", picturePath, admin, "", nil, nil)
	require.NoError(t, err)
	require.Equal(t, defaultHeaders.Get("ETag"), refreshedHeaders.Get("ETag"))
	for _, a := range state.Accounts {
		require.NotContains(t, output.String(), a.Password, "credential leaked into command output")
		require.False(t, a.MFASecret != "" && strings.Contains(output.String(), a.MFASecret), "credential leaked into command output")
	}
	var attempts int
	err = persistence.GetMaster().Get(ctx, &attempts, "SELECT count(*) FROM exam_attempts")
	require.NoError(t, err, "unexpected live exam fixture")
	require.Equal(t, 0, attempts, "unexpected live exam fixture")
	t.Logf("Verified %s fixture: %d accounts; resume, read-only replay, file bytes, roles, MFA, and fresh Sittings", profile, len(state.Accounts))
}
