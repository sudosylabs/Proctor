// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- RFC 6238 interoperability requires the server's HMAC-SHA-1 profile.
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// totp implements the authenticator's RFC 6238 SHA-1/6-digit/30-second profile.
// Secrets come only from the real enrollment endpoint, never from dataset RNG.
func totp(secret string, at time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) == 0 {
		return "", errors.New("invalid authenticator enrollment secret")
	}
	var counter [8]byte
	seconds := at.Unix()
	if seconds < 0 {
		return "", errors.New("authenticator time precedes the Unix epoch")
	}
	binary.BigEndian.PutUint64(counter[:], uint64(seconds/30))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000), nil
}

func (r *runner) enrollMFA(key string, a *account) error {
	if op := r.state.Operations["mfa/"+key]; op != nil && op.Done {
		return nil
	}
	if err := r.login(a); err != nil {
		return err
	}
	raw, err := r.get("/api/v1/users/me/mfa", a.Token)
	if err != nil {
		return err
	}
	status := decode(raw)
	if status["service_enabled"] != true {
		return errors.New("MFA fixtures require local MFA to be enabled; use the generated make run-server environment")
	}
	if status["recently_authenticated"] != true {
		a.Token = ""
		a.RefreshToken = ""
		if err = r.login(a); err != nil {
			return err
		}
	}
	if status["enabled"] == true {
		if a.MFASecret == "" {
			return errors.New("MFA was enabled outside this seed; refusing to replace it")
		}
		// An activation response may be lost after commit. The saved enrollment
		// secret still works; one-time recovery codes cannot be recovered by read.
		r.state.Operations["mfa/"+key] = &operation{Done: true, Result: json.RawMessage(`{}`)}
		return r.save()
	}
	// A pending enrollment can be replaced through the public setup operation.
	// Persist the returned secret before activation so its unknown outcome is safe.
	raw, _, err = r.request(r.state.Server, "POST", "/api/v1/users/me/mfa/setup", a.Token, "", json.RawMessage(`{}`), nil)
	if err != nil {
		return err
	}
	a.MFASecret = str(decode(raw), "secret")
	if err = r.save(); err != nil {
		return err
	}
	code, err := totp(a.MFASecret, time.Now())
	if err != nil {
		return err
	}
	body, _ := json.Marshal(object{"code": code})
	raw, _, err = r.request(r.state.Server, "POST", "/api/v1/users/me/mfa/activate", a.Token, "", body, nil)
	if err != nil {
		return err
	}
	var result struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return errors.New("invalid MFA activation response")
	}
	a.RecoveryCodes = result.RecoveryCodes
	r.state.Operations["mfa/"+key] = &operation{Done: true, Result: json.RawMessage(`{}`)}
	return r.save()
}
