// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// RequestMetadata is transport-neutral, bounded context for security auditing.
// IPAddress is the direct peer address; forwarded headers are intentionally not
// trusted until deployment configuration defines trusted proxies.
type RequestMetadata struct {
	RequestId string
	IPAddress string
	UserAgent string
}
