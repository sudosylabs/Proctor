// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"strings"
)

type VFSLocal struct {
	Root string `json:"Root"`
}

type VFSS3 struct {
	Endpoint     string `json:"Endpoint"`
	AccessKey    string `json:"AccessKey"`
	SecretKey    string `json:"SecretKey"`
	SessionToken string `json:"SessionToken"`
	Bucket       string `json:"Bucket"`
	Prefix       string `json:"Prefix"`
	Region       string `json:"Region"`
	Secure       bool   `json:"Secure"`
}

type VFS struct {
	Backend string   `json:"Backend"`
	Local   VFSLocal `json:"Local"`
	S3      VFSS3    `json:"S3"`
}

func validateVFS(vfsConfig VFS, add func(string, string)) {
	switch vfsConfig.Backend {
	case "local":
		if vfsConfig.Local.Root == "" || strings.ContainsRune(vfsConfig.Local.Root, '\x00') {
			add("vfs.local.root", "must be a non-empty filesystem path")
		}
	case "s3":
		if vfsConfig.S3.Endpoint == "" ||
			strings.ContainsAny(vfsConfig.S3.Endpoint, "\x00\r\n") {
			add("vfs.s3.endpoint", "is required and must not contain control characters")
		}
		if vfsConfig.S3.Bucket == "" || strings.ContainsAny(vfsConfig.S3.Bucket, "\x00\r\n/") {
			add("vfs.s3.bucket", "is required and must be a bucket name")
		}
		if strings.ContainsAny(vfsConfig.S3.Prefix, "\x00\r\n") {
			add("vfs.s3.prefix", "contains invalid characters")
		}
	default:
		add("vfs.backend", "must be local or s3")
	}
}
