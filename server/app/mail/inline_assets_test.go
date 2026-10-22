// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"bytes"
	"os"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

const testLockupCID = "proctor-lockup-9867b48826bf314b6729f73941d053922d7baa7f5120894241ce197c219b8d66@proctor"

func TestInlineAssetsRejectMissingOrChangedReleasedBytes(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../templates/proctor-lockup-25d-v1.png")
	require.NoError(t, err)
	for _, test := range []struct {
		name  string
		files fstest.MapFS
	}{
		{name: "missing", files: fstest.MapFS{}},
		{name: "changed", files: fstest.MapFS{"proctor-lockup-25d-v1.png": {Data: append(bytes.Clone(data), 0)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewInlineAssets(test.files)
			require.Error(t, err, "accepted an unavailable immutable image")
		})
	}
}

func TestInlineAssetsResolveOnlyReleasedImages(t *testing.T) {
	t.Parallel()
	assets, err := NewInlineAssets(os.DirFS("../../templates"))
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		html string
		want int
		fail bool
	}{
		{name: "released", html: `<img alt="Proctor" src="cid:` + testLockupCID + `">`, want: 1},
		{name: "duplicate reference", html: `<img src="cid:` + testLockupCID + `"><img src='cid:` + testLockupCID + `'>`, want: 1},
		{name: "escaped text is not an image", html: `&lt;img src="https://private.example/image"&gt;`},
		{name: "relative image", html: `<img src="proctor-lockup.png">`, fail: true},
		{name: "no image", html: `<p>Plain message</p>`},
		{name: "unknown cid", html: `<img src="cid:unknown">`, fail: true},
		{name: "external image", html: `<img src="https://private.example/image">`, fail: true},
		{name: "unversioned image", html: `<img src="other.png">`, fail: true},
		{name: "missing source", html: `<img alt="Proctor">`, fail: true},
		{name: "browser uses first source", html: `<img src="cid:` + testLockupCID + `" src="https://private.example/image">`, want: 1},
		{name: "external source alternative", html: `<img src="cid:` + testLockupCID + `" srcset="https://private.example/image 2x">`, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			images, err := assets.ForHTML(test.html)
			require.Equal(t, test.fail, err != nil, "ForHTML() returned %d images, %v", len(images), err)
			require.Len(t, images, test.want, "ForHTML() returned %d images, %v", len(images), err)
			if err != nil {
				require.NotContains(t, err.Error(), "private.example", "error exposed an image source")
			}
		})
	}
}

func TestInlineAssetsKeepFrozenReferencesStable(t *testing.T) {
	t.Parallel()
	assets, err := NewInlineAssets(os.DirFS("../../templates"))
	require.NoError(t, err)
	frozenHTML := `<img src="cid:` + testLockupCID + `">`
	first, err := assets.ForHTML(frozenHTML)
	require.NoError(t, err, "initial resolution")
	require.Len(t, first, 1, "initial resolution")
	original := bytes.Clone(first[0].Data)
	first[0].Data[0] ^= 0xff
	// A later release may add artwork without redirecting an existing CID.
	assets.byID["future-release@proctor"] = InlineAsset{Filename: "future.png", ContentID: "future-release@proctor", Data: []byte("future")}
	second, err := assets.ForHTML(frozenHTML)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.True(t, bytes.Equal(second[0].Data, original))
	require.Equal(t, testLockupCID, second[0].ContentID)
}
