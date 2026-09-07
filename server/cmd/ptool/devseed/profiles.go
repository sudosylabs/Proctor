// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// picturePNG makes fictional artwork locally, without external photos or fonts.
func picturePNG(seed int64, alias string) ([]byte, error) {
	digest := sha256.Sum256(fmt.Appendf(nil, "%d/%s", seed, alias))
	canvas := image.NewRGBA(image.Rect(0, 0, 256, 256))
	background := color.RGBA{R: 238, G: 235, B: 226, A: 255}
	foreground := color.RGBA{R: 40 + digest[0]/2, G: 40 + digest[1]/2, B: 40 + digest[2]/2, A: 255}
	accent := color.RGBA{R: 120 + digest[3]/2, G: 90 + digest[4]/2, B: 60 + digest[5]/2, A: 255}
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			pixel := background
			if (x-128)*(x-128)+(y-104)*(y-104) < 68*68 {
				pixel = foreground
			}
			if y > 170 && x > 32 && x < 224 || x > 184 && y > 24 && y < 72 {
				pixel = accent
			}
			canvas.SetRGBA(x, y, pixel)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func (r *runner) profilePicture(alias string) error {
	a := r.state.Accounts[alias]
	key := "picture/" + alias
	path := "/api/v1/users/" + a.UserID + "/profile-picture"
	token := r.state.Accounts["administrator"].Token
	if r.state.Operations[key] == nil {
		_, headers, err := r.request(r.state.Server, "GET", path, token, "", nil, nil)
		if err != nil {
			return err
		}
		tag := headers.Get("ETag")
		if !pictureETag(tag) {
			return errors.New("profile picture did not supply a strong checksum ETag")
		}
		content, err := picturePNG(r.state.Seed, alias)
		if err != nil {
			return err
		}
		r.state.Operations[key] = &operation{PreparedAt: time.Now().UTC(), Method: "PUT", Path: path, Body: json.RawMessage("null"), Content: content, IfMatch: tag}
		if err = r.save(); err != nil {
			return err
		}
	}
	// Normalization belongs to the server, so the client cannot derive the
	// resulting WebP checksum from its PNG. Never adopt an intervening picture
	// or replace it with a new ETag after an ambiguous upload outcome.
	_, err := r.step(key, token, "PUT", path, nil, nil, func() (json.RawMessage, bool, error) {
		_, headers, err := r.request(r.state.Server, "GET", path, token, "", nil, nil)
		if err != nil {
			return nil, false, err
		}
		if headers.Get("ETag") != r.state.Operations[key].IfMatch {
			return nil, false, fmt.Errorf("picture for %s changed after upload preparation; inspect the saved operation before continuing", alias)
		}
		return nil, false, nil
	})
	if err != nil {
		return err
	}
	a.SeededCustomPicture = true
	return r.remember(key, a.UserID, "Custom profile picture — "+alias, path)
}

func pictureETag(tag string) bool {
	if len(tag) != 66 || tag[0] != '"' || tag[65] != '"' || strings.ToLower(tag) != tag {
		return false
	}
	_, err := hex.DecodeString(tag[1:65])
	return err == nil
}

func (r *runner) disableAccount(alias string, a *account) error {
	token := r.state.Accounts["administrator"].Token
	path := "/api/v1/users/" + a.UserID
	_, err := r.step("disable/"+alias, token, http.MethodPost, path+"/disable", object{}, nil, func() (json.RawMessage, bool, error) {
		raw, err := r.administrativeUser(a.UserID)
		return raw, number(decode(raw), "disabled_at") > 0, err
	})
	if err != nil {
		return err
	}
	// These are dedicated fixtures; never use their revoked credentials again.
	a.SeededDisabled, a.Token, a.RefreshToken = true, "", ""
	if item, ok := r.state.Records["user/"+alias]; ok {
		item.URL = "/api/v1/users?" + url.Values{"include_disabled": {"true"}, "q": {a.Username}}.Encode()
		r.state.Records["user/"+alias] = item
	}
	return r.save()
}

// Disabled Users are deliberately hidden from exact profile reads. The
// administrative directory has its own username/ID pagination protocol.
func (r *runner) administrativeUser(id string) (json.RawMessage, error) {
	query := url.Values{"include_disabled": {"true"}, "limit": {"200"}}
	for page := 0; page < 100; page++ {
		items, err := r.list("/api/v1/users?"+query.Encode(), r.state.Accounts["administrator"].Token)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if str(item, "id") == id {
				return json.Marshal(item)
			}
		}
		if len(items) < 200 {
			return nil, errors.New("fixture User is missing from the administrative directory")
		}
		last := items[len(items)-1]
		username, lastID := str(last, "username"), str(last, "id")
		if username == "" || lastID == "" || lastID == query.Get("after_id") {
			return nil, errors.New("invalid administrative directory cursor")
		}
		query.Set("after_username", username)
		query.Set("after_id", lastID)
	}
	return nil, errors.New("administrative directory exceeded the seed page bound")
}
