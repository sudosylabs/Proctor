// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package externalauth

import (
	"errors"
	"io"
	"net/http"
	"time"
)

var ErrResponseTooLarge = errors.New(
	"external authentication response exceeds configured limit",
)

func NewHTTPClient(timeout time.Duration, maximumResponseBytes int64) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout: timeout,
		Transport: &boundedRoundTripper{
			base: transport, maximumResponseBytes: maximumResponseBytes,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type boundedRoundTripper struct {
	base                 http.RoundTripper
	maximumResponseBytes int64
}

func (t *boundedRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &boundedReadCloser{
		source: response.Body,
		reader: &io.LimitedReader{
			R: response.Body,
			N: t.maximumResponseBytes + 1,
		},
		maximum: t.maximumResponseBytes,
	}
	return response, nil
}

type boundedReadCloser struct {
	source  io.ReadCloser
	reader  *io.LimitedReader
	read    int64
	maximum int64
}

func (r *boundedReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.read += int64(count)
	if r.read > r.maximum {
		return count, ErrResponseTooLarge
	}
	return count, err
}

func (r *boundedReadCloser) Close() error {
	return r.source.Close()
}
