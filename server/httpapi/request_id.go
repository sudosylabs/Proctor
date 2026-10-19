// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"crypto/rand"
	"strings"
)

const RequestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

func newRequestID() string {
	return rand.Text()
}

// requestIDFromTrustedProxy accepts a request ID supplied by a trusted
// reverse proxy when it is short and uses only URL-safe characters; any
// other value is replaced by a server-generated ID.
func requestIDFromTrustedProxy(value string) string {
	if value == "" || len(value) > 128 {
		return newRequestID()
	}
	for _, character := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.", character) {
			return newRequestID()
		}
	}
	return value
}
