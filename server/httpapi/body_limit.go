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
)

func limitRequestBody(next http.Handler, maxBodyBytes int64) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength > maxBodyBytes {
			writeRequestTooLarge(writer, request)
			return
		}
		if request.Body != nil {
			request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
		}
		next.ServeHTTP(writer, request)
	})
}

// requestBodyTooLarge reports whether err records reading past the route's
// request body limit, through any application or decoding wrapper.
func requestBodyTooLarge(err error) bool {
	var limit *http.MaxBytesError
	return errors.As(err, &limit)
}

func writeRequestTooLarge(writer http.ResponseWriter, request *http.Request) {
	title, detail := localizedProblemPresentation(request, http.StatusRequestEntityTooLarge)
	// A partially read body cannot be resumed, so the connection is closed
	// rather than draining an oversized remainder.
	writer.Header().Set("Connection", "close")
	WriteProblem(writer, Problem{
		Type:      problemType(requestTooLargeCode),
		Title:     title,
		Status:    http.StatusRequestEntityTooLarge,
		Detail:    detail,
		Instance:  request.URL.Path,
		Code:      requestTooLargeCode,
		RequestID: RequestID(request.Context()),
	})
}
