// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Options select one isolated development installation and a versioned dataset.
// EnvironmentFile is the generated local environment containing bootstrap proof;
// no credential is accepted on the command line or written to ordinary output.
type Options struct {
	ServerURL       string
	MailpitURL      string
	StateDir        string
	EnvironmentFile string
	Profile         string
	Seed            int64
	RefreshSittings bool
}

type runner struct {
	ctx     context.Context
	options Options
	state   *journal
	client  *http.Client
	out     io.Writer
	tokens  map[string]*account
}

// Run seeds a pristine installation or resumes its own private journal. A
// completed replay verifies records without changing fixture content.
func Run(ctx context.Context, options Options, out io.Writer) error {
	server, err := loopbackOrigin(options.ServerURL)
	if err != nil {
		return err
	}
	mailpit, err := loopbackOrigin(options.MailpitURL)
	if err != nil {
		return err
	}
	if options.Profile != "small" && options.Profile != "desktop" && options.Profile != "large" {
		return errors.New("profile must be small, desktop, or large")
	}
	if options.Seed < 0 {
		return errors.New("seed must be nonnegative")
	}
	if options.StateDir == "" || options.EnvironmentFile == "" {
		return errors.New("state directory and environment file are required")
	}
	if err = os.MkdirAll(options.StateDir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(options.StateDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("seed directory must be private (mode 0700) and not a symlink")
	}
	unlock, err := lockState(filepath.Join(options.StateDir, "lock"))
	if err != nil {
		return err
	}
	defer unlock()
	r := &runner{ctx: ctx, options: options, client: localHTTPClient(), out: out}
	defer r.client.CloseIdleConnections()
	path := filepath.Join(options.StateDir, "journal.json")
	if err = privateFile(path); err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return statErr
		}
		if info.Size() > 32<<20 {
			return errors.New("seed journal exceeds 32 MiB")
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if len(raw) > 32<<20 {
			return errors.New("seed journal exceeds 32 MiB")
		}
		if err = json.Unmarshal(raw, &r.state); err != nil || r.state == nil {
			return errors.New("invalid seed journal")
		}
		if r.state.Version != schemaVersion || r.state.Server != server || r.state.Mailpit != mailpit || r.state.Profile != options.Profile || r.state.Seed != options.Seed || len(r.state.RunID) != 32 || r.state.Accounts == nil || r.state.Operations == nil || r.state.Records == nil {
			return errors.New("seed journal version, origin, or dataset does not match; use its original options or a separate pristine development installation")
		}
		if err = r.state.validate(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		for _, old := range []string{"fixture.json", "credentials.json", "in-progress.json"} {
			if _, legacyErr := os.Lstat(filepath.Join(options.StateDir, old)); !errors.Is(legacyErr, os.ErrNotExist) {
				return errors.New("legacy or incomplete seed state found; preserve it and use a separate pristine development installation")
			}
		}
		r.state = &journal{Version: schemaVersion, Server: server, Mailpit: mailpit, Profile: options.Profile, Seed: options.Seed, StartedAt: time.Now().UTC(), Accounts: map[string]*account{}, Operations: map[string]*operation{}, Records: map[string]record{}}
		r.state.RunID, err = randomHex(16)
		if err != nil {
			return err
		}
		r.state.ScheduleAt = r.state.StartedAt
		// Check readiness and ownership before leaving a new journal behind.
		status, statusErr := r.get("/api/v1/bootstrap", "")
		if statusErr != nil {
			return fmt.Errorf("server must be running: %w", statusErr)
		}
		if decode(status)["initialized"] != false {
			return errors.New("seed refuses an initialized installation without its matching journal")
		}
		if err = r.save(); err != nil {
			return err
		}
	}
	if err = r.bootstrap(); err != nil {
		return err
	}
	if options.RefreshSittings {
		if !r.state.Ready {
			return errors.New("finish the pending seed run without --refresh-sittings first")
		}
		r.state.Generation++
		next := time.Now().UTC()
		if !next.After(r.state.ScheduleAt.Add(time.Second)) {
			next = r.state.ScheduleAt.Add(time.Second)
		}
		r.state.ScheduleAt = next
		r.state.Ready = false
		if err = r.save(); err != nil {
			return err
		}
	}
	if !r.state.Ready {
		if err = r.preflight(); err != nil {
			return err
		}
		fmt.Fprintln(out, "Creating or reconciling the synthetic development dataset…")
		if err = r.populate(); err != nil {
			return err
		}
		if err = r.verify(); err != nil {
			return err
		}
		r.state.Ready = true
		if err = r.save(); err != nil {
			return err
		}
	} else if err = r.verify(); err != nil {
		return err
	}
	return r.report()
}

func (r *runner) bootstrap() error {
	raw, err := r.get("/api/v1/discovery", "")
	if err != nil {
		return err
	}
	if str(decode(raw), "canonical_origin") != r.state.Server {
		return errors.New("server canonical origin differs from seed origin")
	}
	admin, err := r.account("administrator", "northbridge-admin", "administrator@northbridge.example")
	if err != nil {
		return err
	}
	raw, err = r.get("/api/v1/bootstrap", "")
	if err != nil {
		return err
	}
	initialized, valid := decode(raw)["initialized"].(bool)
	if !valid {
		return errors.New("invalid bootstrap status")
	}
	if !initialized {
		if r.state.InstitutionID != "" {
			return errors.New("the recorded installation has been replaced; refusing to recreate it")
		}
		var env []byte
		env, err = os.ReadFile(r.options.EnvironmentFile)
		if err != nil {
			return errors.New("cannot read generated development environment; run make dev-state")
		}
		secret := ""
		for _, line := range strings.Split(string(env), "\n") {
			if value, ok := strings.CutPrefix(line, "PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET="); ok {
				secret = value
			}
		}
		if len(secret) < 32 || len(secret) > 512 {
			return errors.New("generated bootstrap secret is missing or invalid")
		}
		body := object{"institution": object{"name": "northbridge-university", "display_name": "Northbridge University", "description": "Synthetic development fixture " + r.state.RunID}, "administrator": object{"username": admin.Username, "email": admin.Email, "display_name": "Northbridge Administrator", "first_name": "Morgan", "last_name": "Reed", "locale": "en", "timezone": "Europe/London"}, "password": admin.Password, "bootstrap_secret": secret}
		if _, err = r.step("bootstrap", "", "POST", "/api/v1/bootstrap", body, nil, nil); err != nil {
			return err
		}
	}
	// Login proof plus the private marker reconciles a lost bootstrap response.
	if err = r.login(admin); err != nil {
		return err
	}
	raw, err = r.get("/api/v1/discovery", "")
	if err != nil {
		return err
	}
	discovery := decode(raw)
	if str(discovery, "canonical_origin") != r.state.Server {
		return errors.New("server canonical origin differs from seed origin")
	}
	id := str(nested(discovery, "institution"), "id")
	if id == "" || (r.state.InstitutionID != "" && r.state.InstitutionID != id) {
		return errors.New("seed installation identity mismatch")
	}
	raw, err = r.get("/api/v1/institution", admin.Token)
	if err != nil {
		return err
	}
	if r.state.InstitutionID == "" && str(decode(raw), "description") != "Synthetic development fixture "+r.state.RunID {
		return errors.New("installation does not carry this seed's bootstrap marker")
	}
	r.state.InstitutionID = id
	return r.remember("user/administrator", admin.UserID, "Northbridge Administrator", "/api/v1/users/"+admin.UserID)
}

func (r *runner) preflight() error {
	raw, err := r.get("/api/v1/users/me/mfa", r.state.Accounts["administrator"].Token)
	if err != nil {
		return err
	}
	if decode(raw)["service_enabled"] != true {
		return errors.New("MFA fixtures require local MFA to be enabled; use the generated make run-server environment")
	}
	if _, _, err = r.request(r.state.Mailpit, "GET", "/api/v1/messages?limit=1", "", "", nil, nil); err != nil {
		return fmt.Errorf("local Mailpit must be running: %w", err)
	}
	return nil
}

func (r *runner) account(key, username, email string) (*account, error) {
	if saved := r.state.Accounts[key]; saved != nil {
		return saved, nil
	}
	password, err := randomHex(18)
	if err != nil {
		return nil, err
	}
	a := &account{Username: username, Email: email, Password: "Dev-" + password + "!"}
	r.state.Accounts[key] = a
	return a, r.save()
}

func (r *runner) login(a *account) error {
	if a.Token != "" {
		raw, err := r.get("/api/v1/users/me", a.Token)
		if err == nil {
			id := str(decode(raw), "id")
			if id == "" || (a.UserID != "" && id != a.UserID) {
				return errors.New("account identity mismatch")
			}
			a.UserID = id
			return nil
		}
		var response *responseError
		if !errors.As(err, &response) || response.Status != 401 {
			return err
		}
	}
	var raw json.RawMessage
	var err error
	if a.RefreshToken != "" {
		raw, _, err = r.send(r.state.Server, "POST", "/api/v1/auth/refresh", a.RefreshToken, "", nil, nil, "")
		var response *responseError
		if err != nil && (!errors.As(err, &response) || response.Status != http.StatusUnauthorized) {
			return fmt.Errorf("development session refresh failed: %w", err)
		}
	}
	if len(raw) == 0 {
		body, _ := json.Marshal(object{"login_id": a.Username, "password": a.Password, "client_type": "cli", "device_id": "dev-seed-" + r.state.RunID, "device_name": "Proctor development seed"})
		raw, _, err = r.request(r.state.Server, "POST", "/api/v1/auth/login", "", "", body, nil)
		if err != nil {
			return fmt.Errorf("development account login failed: %w", err)
		}
	}
	a.Token = str(nested(decode(raw), "tokens"), "access_token")
	a.RefreshToken = str(nested(decode(raw), "tokens"), "refresh_token")
	if a.Token == "" || a.RefreshToken == "" {
		return errors.New("authentication response did not contain both session tokens")
	}
	raw, err = r.get("/api/v1/users/me", a.Token)
	if err != nil {
		return err
	}
	id := str(decode(raw), "id")
	if id == "" || (a.UserID != "" && a.UserID != id) {
		return errors.New("development account identity mismatch")
	}
	a.UserID = id
	return r.save()
}

type record struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"api_path"`
}

func (r *runner) remember(key, id, label, path string) error {
	if id == "" {
		return fmt.Errorf("seed step %s is missing an identifier", key)
	}
	item := record{ID: id, Label: label, URL: path}
	if existing, ok := r.state.Records[key]; ok && existing == item {
		return nil
	}
	r.state.Records[key] = item
	return r.save()
}

func (r *runner) verify() error {
	token := r.state.Accounts["administrator"].Token
	for key, item := range r.state.Records {
		var err error
		if a := r.state.Accounts[strings.TrimPrefix(key, "user/")]; strings.HasPrefix(key, "user/") && a != nil && a.SeededDisabled {
			_, err = r.administrativeUser(item.ID)
		} else {
			_, err = r.get(item.URL, token)
		}
		if err != nil {
			return fmt.Errorf("fixture verification %s failed; no record was repaired: %w", key, err)
		}
	}
	return nil
}

func (r *runner) report() error {
	manifest := object{"schema_version": schemaVersion, "server_url": r.state.Server, "profile": r.state.Profile, "seed": r.state.Seed, "institution_id": r.state.InstitutionID, "created_at": r.state.StartedAt, "sitting_generation": r.state.Generation, "records": r.state.Records}
	users := map[string]object{}
	credentials := map[string]object{}
	for key, a := range r.state.Accounts {
		users[key] = object{"id": a.UserID, "username": a.Username, "email": a.Email, "mfa_enabled": a.MFASecret != "", "seeded_disabled": a.SeededDisabled, "seeded_custom_picture": a.SeededCustomPicture}
		credentials[key] = object{"username": a.Username, "password": a.Password, "mfa_secret": a.MFASecret, "recovery_codes": a.RecoveryCodes}
	}
	manifest["accounts"] = users
	if err := writeJSON(filepath.Join(r.options.StateDir, "fixture.json"), manifest); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(r.options.StateDir, "credentials.json"), object{"schema_version": schemaVersion, "server_url": r.state.Server, "accounts": credentials}); err != nil {
		return err
	}
	fmt.Fprintf(r.out, "Development fixture verified: %d accounts, %d records (%s).\nFixture manifest: %s\nPrivate credentials: %s\n", len(users), len(r.state.Records), r.state.Profile, filepath.Join(r.options.StateDir, "fixture.json"), filepath.Join(r.options.StateDir, "credentials.json"))
	return nil
}
