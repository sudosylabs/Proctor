// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"strings"

	"golang.org/x/net/html"
)

// InlineAsset is one Proctor-owned PNG resolved from an immutable Content-ID.
// Data is a caller-owned copy. MIME construction belongs to the transport.
type InlineAsset struct {
	Filename  string
	ContentID string
	Data      []byte
}

// InlineAssets retains released image bytes so frozen deliveries and fan-out
// bundles resolve the same artwork after later template releases.
type InlineAssets struct {
	byID map[string]InlineAsset
}

// NewInlineAssets loads and verifies the closed catalog of released images.
// Released filenames, digests, and bytes must never be changed or removed.
func NewInlineAssets(files fs.FS) (*InlineAssets, error) {
	if files == nil {
		return nil, errors.New("mail inline asset files are unavailable")
	}
	catalog := []struct {
		filename string
		digest   string
		width    int
		height   int
	}{
		{filename: "proctor-lockup-25d-v1.png", digest: "9867b48826bf314b6729f73941d053922d7baa7f5120894241ce197c219b8d66", width: 600, height: 118},
	}
	assets := &InlineAssets{byID: make(map[string]InlineAsset, len(catalog))}
	for _, entry := range catalog {
		data, err := fs.ReadFile(files, entry.filename)
		if err != nil {
			return nil, fmt.Errorf("read mail inline asset %q: %w", entry.filename, err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != entry.digest {
			return nil, fmt.Errorf("mail inline asset %q has changed", entry.filename)
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != entry.width || config.Height != entry.height {
			return nil, fmt.Errorf("mail inline asset %q has invalid PNG dimensions", entry.filename)
		}
		id := "proctor-lockup-" + entry.digest + "@proctor"
		assets.byID[id] = InlineAsset{Filename: entry.filename, ContentID: id, Data: data}
	}
	return assets, nil
}

// ForHTML resolves each referenced image once, in document order. Unknown
// images fail closed without exposing their source in errors. The old relative
// logo remains tolerated only for already-frozen messages; it is not rewritten
// or substituted with new artwork.
func (a *InlineAssets) ForHTML(body string) ([]InlineAsset, error) {
	if a == nil {
		return nil, errors.New("mail inline assets are unavailable")
	}
	var result []InlineAsset
	seen := make(map[string]bool)
	tokens := html.NewTokenizer(strings.NewReader(body))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			if err := tokens.Err(); !errors.Is(err, io.EOF) {
				return nil, errors.New("mail image markup is invalid")
			}
			return result, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if token.Data != "img" {
				continue
			}
			var source string
			count := 0
			for _, attr := range token.Attr {
				if attr.Key == "srcset" {
					return nil, errors.New("mail image alternatives are not permitted")
				}
				if attr.Key == "src" {
					source = attr.Val
					count++
				}
			}
			if count != 1 {
				return nil, errors.New("mail image source is invalid")
			}
			if source == "proctor-lockup.png" {
				continue
			}
			id, ok := strings.CutPrefix(source, "cid:")
			asset, exists := a.byID[id]
			if !ok || !exists {
				return nil, errors.New("mail image is not a released inline asset")
			}
			if !seen[id] {
				asset.Data = bytes.Clone(asset.Data)
				result = append(result, asset)
				seen[id] = true
			}
		}
	}
}
