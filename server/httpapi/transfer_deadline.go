// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"net/http"
	"time"
)

// minimumTransferBytesPerSecond is the slowest sustained client throughput a
// declared upload or download route admits. The server-wide read and write
// timeouts bound ordinary JSON requests; a bounded binary transfer instead
// receives that timeout plus the time its declared size takes at this rate,
// so a slow but legitimate connection can finish a large Resource or
// Workspace transfer.
const minimumTransferBytesPerSecond = 64 << 10

// transferDeadlines sizes connection deadlines for authenticated streaming
// upload and binary download routes. The zero value leaves the server-wide
// deadlines unchanged.
type transferDeadlines struct {
	readTimeout  time.Duration
	writeTimeout time.Duration
	now          func() time.Time
}

func newTransferDeadlines(readTimeout, writeTimeout time.Duration) transferDeadlines {
	return transferDeadlines{readTimeout: readTimeout, writeTimeout: writeTimeout, now: time.Now}
}

// transferAllowance returns the time a transfer of size bytes may take at the
// minimum admitted throughput.
func transferAllowance(size int64) time.Duration {
	if size <= 0 {
		return 0
	}
	seconds := size / minimumTransferBytesPerSecond
	if size%minimumTransferBytesPerSecond != 0 {
		seconds++
	}
	return time.Duration(seconds) * time.Second
}

// uploadTransferBytes is the body size an upload's deadline is sized for: the
// declared Content-Length when the client sent one within the route maximum,
// otherwise the route maximum. A client cannot earn the time of a maximal
// upload by declaring a small one, because the server reads no more than the
// declared length.
func uploadTransferBytes(contentLength, maxBodyBytes int64) int64 {
	if contentLength >= 0 && contentLength < maxBodyBytes {
		return contentLength
	}
	return maxBodyBytes
}

// extendForUpload extends the read deadline for the request body, sized by
// uploadTransferBytes, and the write deadline for the response that follows.
func (d transferDeadlines) extendForUpload(writer http.ResponseWriter, contentLength, maxBodyBytes int64) error {
	allowance := transferAllowance(uploadTransferBytes(contentLength, maxBodyBytes))
	controller := http.NewResponseController(writer)
	var result error
	if d.readTimeout > 0 {
		result = errors.Join(result, ignoreUnsupportedDeadline(controller.SetReadDeadline(d.now().Add(d.readTimeout+allowance))))
	}
	if d.writeTimeout > 0 {
		result = errors.Join(result, ignoreUnsupportedDeadline(controller.SetWriteDeadline(d.now().Add(d.writeTimeout+allowance))))
	}
	return result
}

// extendForDownload extends the write deadline for a response body of
// exactly contentLength bytes, measured from the start of the body copy.
func (d transferDeadlines) extendForDownload(writer http.ResponseWriter, contentLength int64) error {
	if d.writeTimeout <= 0 {
		return nil
	}
	controller := http.NewResponseController(writer)
	return ignoreUnsupportedDeadline(controller.SetWriteDeadline(d.now().Add(d.writeTimeout + transferAllowance(contentLength))))
}

func ignoreUnsupportedDeadline(err error) error {
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}
