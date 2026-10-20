// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
)

// applicationFailure is the transport-facing view of a transport-neutral
// application error such as *apperror.Error (Code + Fields). The interface keeps
// HTTP mapping duck-typed over any error exposing a stable code and safe
// fields.
type applicationFailure interface {
	error
	Code() string
	Fields() map[string]string
}

// applicationErrorMapping is the HTTP presentation for one public error code.
// The Problem Details type is derived from the code. The table itself is
// generated from error_catalog.yaml into error_mapping.go.
type applicationErrorMapping struct {
	status int
}

// ApplicationErrorStatuses returns a copy of the registered application-code
// to HTTP status mapping. Tests use it to prove every registered code produces
// its declared status.
func ApplicationErrorStatuses() map[string]int {
	cloned := make(map[string]int, len(applicationErrorMappings))
	for code, mapping := range applicationErrorMappings {
		cloned[code] = mapping.status
	}
	return cloned
}

func problemFromApplicationFailure(request *http.Request, failure applicationFailure) Problem {
	mapping, ok := applicationErrorMappings[failure.Code()]
	if !ok {
		return internalProblem(request)
	}

	status := mapping.status
	if status < 400 || status > 599 {
		return internalProblem(request)
	}
	title, detail := localizedProblemPresentation(request, status)
	return newProblem(request, failure.Code(), status, title, detail, failure.Fields())
}

func newProblem(
	request *http.Request,
	code string,
	status int,
	title, detail string,
	fields map[string]string,
) Problem {
	return Problem{
		Type:      "https://proctor.sudosylabs.com/problems/" + code,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  request.URL.Path,
		Code:      code,
		RequestID: RequestID(request.Context()),
		Fields:    fields,
	}
}
