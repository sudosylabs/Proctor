// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const schemaVersion = 3

type account struct {
	Username            string   `json:"username"`
	Email               string   `json:"email"`
	Password            string   `json:"password"`
	UserID              string   `json:"user_id,omitempty"`
	Token               string   `json:"access_token,omitempty"`
	RefreshToken        string   `json:"refresh_token,omitempty"`
	MFASecret           string   `json:"mfa_secret,omitempty"`
	RecoveryCodes       []string `json:"recovery_codes,omitempty"`
	SeededDisabled      bool     `json:"seeded_disabled,omitempty"`
	SeededCustomPicture bool     `json:"seeded_custom_picture,omitempty"`
}

type operation struct {
	PreparedAt time.Time       `json:"prepared_at"`
	Method     string          `json:"method"`
	Path       string          `json:"path"`
	Body       json.RawMessage `json:"body,omitempty"`
	Content    []byte          `json:"content"`
	IfMatch    string          `json:"if_match,omitempty"`
	Done       bool            `json:"done"`
	Result     json.RawMessage `json:"result,omitempty"`
}

type journal struct {
	Version       int                   `json:"schema_version"`
	Server        string                `json:"server_url"`
	Mailpit       string                `json:"mailpit_url"`
	Profile       string                `json:"profile"`
	Seed          int64                 `json:"seed"`
	RunID         string                `json:"run_id"`
	StartedAt     time.Time             `json:"started_at"`
	InstitutionID string                `json:"institution_id,omitempty"`
	Ready         bool                  `json:"ready"`
	Accounts      map[string]*account   `json:"accounts"`
	Operations    map[string]*operation `json:"operations"`
	Records       map[string]record     `json:"records"`
	Generation    int                   `json:"sitting_generation"`
	ScheduleAt    time.Time             `json:"schedule_at"`
}

func (j *journal) validate() error {
	if j.StartedAt.IsZero() || j.ScheduleAt.IsZero() || j.Generation < 0 {
		return errors.New("invalid seed journal schedule")
	}
	for _, a := range j.Accounts {
		if a == nil || a.Username == "" || a.Email == "" || a.Password == "" {
			return errors.New("invalid seed journal account")
		}
	}
	for _, op := range j.Operations {
		if op == nil || op.Done && !json.Valid(op.Result) || !op.Done && (op.PreparedAt.IsZero() || op.Method == "" || op.Path == "" || !json.Valid(op.Body)) {
			return errors.New("invalid seed journal operation")
		}
		if op.IfMatch != "" && (!pictureETag(op.IfMatch) || op.Method != "PUT" || !strings.HasSuffix(op.Path, "/profile-picture") || len(op.Content) == 0 || string(op.Body) != "null") {
			return errors.New("invalid seed journal picture upload")
		}
	}
	return nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func privateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("seed state must be a private regular file (mode 0600)")
	}
	return nil
}

func writeJSON(path string, value any) error {
	if err := privateFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(raw, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (r *runner) save() error {
	return writeJSON(filepath.Join(r.options.StateDir, "journal.json"), r.state)
}

// reconcile resolves an unknown write outcome by reading an authoritative
// natural key. A missing result allows an exact saved request to be retried.
type reconcile func() (json.RawMessage, bool, error)

func (r *runner) step(key, token, method, path string, body object, content []byte, resolve reconcile) (object, error) {
	op, exists := r.state.Operations[key]
	if exists && op.Done {
		return decode(op.Result), nil
	}
	if !exists {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		op = &operation{PreparedAt: time.Now().UTC(), Method: method, Path: path, Body: raw, Content: content}
		r.state.Operations[key] = op
		if err := r.save(); err != nil {
			return nil, err
		}
	} else if resolve != nil {
		raw, found, err := resolve()
		if err != nil {
			return nil, fmt.Errorf("reconcile %s: %w", key, err)
		}
		if found {
			op.Done, op.Result = true, raw
			return decode(raw), r.save()
		}
	}
	// Server command outcomes are retained for 24 hours. Use a deliberately
	// shorter retry window when an authoritative read cannot prove completion;
	// never turn an expired key into another blind create.
	if exists && (op.PreparedAt.IsZero() || time.Since(op.PreparedAt) > time.Hour) {
		return nil, fmt.Errorf("seed step %s has an unresolved outcome older than the one-hour automatic retry window; inspect it before continuing", key)
	}
	digest := sha256.Sum256([]byte(r.state.RunID + "/" + key))
	idempotencyKey := ""
	// These seeded operations require command idempotency. Older administration
	// and invitation routes reject that header and recover through natural keys.
	if op.Path == "/api/v1/exams" || strings.HasPrefix(op.Path, "/api/v1/exams/") || op.Path == "/api/v1/users/me/settings" {
		idempotencyKey = "dev-seed-" + hex.EncodeToString(digest[:16])
	}
	requestBody := op.Body
	if op.IfMatch != "" {
		requestBody = nil // A journaled, revision-fenced raw picture upload.
	}
	raw, _, err := r.requestWithMatch(r.state.Server, op.Method, op.Path, token, idempotencyKey, requestBody, op.Content, op.IfMatch)
	if err != nil {
		return nil, fmt.Errorf("seed step %s: %w", key, err)
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("seed step %s returned invalid JSON", key)
	}
	op.Done, op.Result = true, raw
	return decode(raw), r.save()
}

func (r *runner) find(path, token, field, value string) reconcile {
	return r.findWhere(path, token, func(item object) bool { return str(item, field) == value })
}

func (r *runner) findWhere(path, token string, match func(object) bool) reconcile {
	return func() (json.RawMessage, bool, error) {
		items, err := r.list(path, token)
		if err != nil {
			return nil, false, err
		}
		var found object
		for _, item := range items {
			if !match(item) {
				continue
			}
			if found != nil {
				return nil, false, errors.New("ambiguous fixture record; no mutation performed")
			}
			found = item
		}
		if found == nil {
			return nil, false, nil
		}
		raw, err := json.Marshal(found)
		return raw, true, err
	}
}

func (r *runner) create(key, path, token string, body object) (object, error) {
	return r.step(key, token, "POST", path, body, nil, r.find(path, token, "name", str(body, "name")))
}
