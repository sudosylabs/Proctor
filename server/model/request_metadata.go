// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

// RequestMetadata is transport-neutral, bounded context for security auditing.
// IPAddress is the direct peer address; forwarded headers are intentionally not
// trusted until deployment configuration defines trusted proxies.
type RequestMetadata struct {
	RequestID string
	IPAddress string
	UserAgent string
}
