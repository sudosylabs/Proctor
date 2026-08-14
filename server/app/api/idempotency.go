// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"regexp"

	application "github.com/sudosylabs/proctor/server/app"
)

const idempotencyHeader = "Idempotency-Key"

var validIdempotencyKey = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,128}$`)

type idempotencyKeyContextKey struct{}

func idempotencyKeyFromContext(ctx context.Context) string {
	value, _ := ctx.Value(idempotencyKeyContextKey{}).(string)
	return value
}

func (a *API) withIdempotency(next http.Handler, requirement IdempotencyRequirement) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		values, supplied := request.Header[http.CanonicalHeaderKey(idempotencyHeader)]
		if requirement == IdempotencyNone {
			if supplied {
				writeApplicationError(writer, request, a.logger, application.NewError("idempotency.not_supported"))
				return
			}
			next.ServeHTTP(writer, request)
			return
		}
		if !supplied {
			if requirement == IdempotencyRequired {
				writeApplicationError(writer, request, a.logger, application.NewError("idempotency.key_required"))
				return
			}
			next.ServeHTTP(writer, request)
			return
		}
		if len(values) != 1 || !validIdempotencyKey.MatchString(values[0]) {
			writeApplicationError(writer, request, a.logger, application.NewError("idempotency.invalid_key"))
			return
		}
		ctx := context.WithValue(request.Context(), idempotencyKeyContextKey{}, values[0])
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}
