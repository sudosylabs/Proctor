// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type operationRequest struct {
	context        context.Context
	principal      model.Principal
	metadata       model.RequestMetadata
	params         Params
	request        *http.Request
	idempotencyKey string
}

type operationResult struct {
	status    int
	body      any
	noContent bool
	problem   *Problem
	headers   http.Header
}

func jsonResult(status int, body any) operationResult {
	return operationResult{status: status, body: body}
}

func noContentResult() operationResult {
	return operationResult{status: http.StatusNoContent, noContent: true}
}

func statusResult(status int) operationResult {
	return operationResult{status: status, noContent: true}
}

func problemResult(problem Problem) operationResult {
	return operationResult{status: problem.Status, problem: &problem}
}

func (result operationResult) withHeaders(headers http.Header) operationResult {
	result.headers = headers.Clone()
	return result
}

type operation func(operationRequest) (operationResult, error)

type protocolResult struct {
	kind          RouteProtocolKind
	status        int
	location      string
	body          io.ReadCloser
	contentLength int64
	jsonBody      any
	headers       http.Header
}

func redirectProtocolResult(location string) protocolResult {
	return protocolResult{kind: RouteProtocolRedirect, status: http.StatusSeeOther, location: location}
}

func binaryDownloadProtocolResult(body io.ReadCloser, contentLength int64) protocolResult {
	return protocolResult{kind: RouteProtocolBinaryDownload, status: http.StatusOK, body: body, contentLength: contentLength}
}

func notModifiedProtocolResult(contentLength int64) protocolResult {
	return protocolResult{kind: RouteProtocolBinaryDownload, status: http.StatusNotModified, contentLength: contentLength}
}

func streamingUploadProtocolResult(status int, body any) protocolResult {
	return protocolResult{kind: RouteProtocolStreamingUpload, status: status, jsonBody: body}
}

func (result protocolResult) withHeaders(headers http.Header) protocolResult {
	result.headers = headers.Clone()
	return result
}

func (a *API) upgradeOperationHandler(
	definition routeDefinition,
	errorPolicy routeErrorPolicy,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, hasPrincipal := Principal(request.Context())
		if authRequiresPrincipal(definition.auth) && !hasPrincipal {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, authenticationRequiredError())
			return
		}
		params, ok := RequestParams(request.Context())
		if !ok {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, invalidRequestError("route_params", nil))
			return
		}
		err := definition.upgradeOperation(writer, operationRequest{
			context: request.Context(), principal: principal,
			metadata: RequestMetadata(request.Context()), params: params,
			request: request,
		})
		if err == nil {
			return
		}
		cause, headers := responseErrorParts(err)
		if validateResponseHeaders(headers) != nil || !routeErrorAllowed(errorPolicy, cause) {
			a.logInvalidRouteError(request, cause)
			WriteProblem(writer, internalProblem(request))
			return
		}
		applyResponseHeaders(writer, headers)
		writeApplicationError(writer, request, a.logger, cause)
	})
}

type headerOnlyResponseWriter struct {
	header http.Header
}

func (writer headerOnlyResponseWriter) Header() http.Header { return writer.header }

func (headerOnlyResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("response header operation cannot write a body")
}

func (headerOnlyResponseWriter) WriteHeader(int) {
	panic("response header capture cannot write a status")
}

func captureResponseHeaders(operation func(http.ResponseWriter)) http.Header {
	headers := make(http.Header)
	if operation != nil {
		operation(headerOnlyResponseWriter{header: headers})
	}
	return headers
}

func combineResponseHeaders(groups ...http.Header) http.Header {
	combined := make(http.Header)
	for _, headers := range groups {
		for key, values := range headers {
			for _, value := range values {
				combined.Add(key, value)
			}
		}
	}
	return combined
}

type operationResponseError struct {
	err     error
	headers http.Header
}

func (failure *operationResponseError) Error() string { return failure.err.Error() }
func (failure *operationResponseError) Unwrap() error { return failure.err }

func errorWithHeaders(err error, headers http.Header) error {
	if err == nil {
		return nil
	}
	return &operationResponseError{err: err, headers: headers.Clone()}
}

func responseErrorParts(err error) (error, http.Header) {
	var responseError *operationResponseError
	if errors.As(err, &responseError) {
		return responseError.err, responseError.headers.Clone()
	}
	return err, nil
}

func validateResponseHeaders(headers http.Header) error {
	for key, values := range headers {
		if key == "" || http.CanonicalHeaderKey(key) == "" || strings.ContainsAny(key, "\r\n") {
			return fmt.Errorf("response header name %q is invalid", key)
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("response header %q contains an invalid value", key)
			}
		}
	}
	return nil
}

func applyResponseHeaders(writer http.ResponseWriter, headers http.Header) {
	for key, values := range headers {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
}

func (a *API) operationHandler(
	definition routeDefinition,
	errorPolicy routeErrorPolicy,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, hasPrincipal := Principal(request.Context())
		if authRequiresPrincipal(definition.auth) && !hasPrincipal {
			writeRouteApplicationError(
				writer, request, a.logger, errorPolicy,
				authenticationRequiredError(),
			)
			return
		}
		params, ok := RequestParams(request.Context())
		if !ok {
			writeRouteApplicationError(
				writer, request, a.logger, errorPolicy,
				invalidRequestError("route_params", nil),
			)
			return
		}
		result, err := definition.operation(operationRequest{
			context: request.Context(), principal: principal,
			metadata: RequestMetadata(request.Context()), params: params,
			request: request, idempotencyKey: idempotencyKeyFromContext(request.Context()),
		})
		if err != nil {
			cause, headers := responseErrorParts(err)
			if failure, ok := cause.(applicationFailure); ok && failure.Code() == "idempotency.in_progress" {
				if headers == nil {
					headers = make(http.Header)
				}
				headers.Set("Retry-After", "1")
			}
			if validateResponseHeaders(headers) != nil || !routeErrorAllowed(errorPolicy, cause) {
				a.logInvalidRouteError(request, cause)
				WriteProblem(writer, internalProblem(request))
				return
			}
			applyResponseHeaders(writer, headers)
			writeApplicationError(writer, request, a.logger, cause)
			return
		}
		if err := validateOperationResult(result, errorPolicy); err != nil {
			a.logger.ErrorContext(request.Context(), "route returned an invalid operation result", logErr(err))
			WriteProblem(writer, internalProblem(request))
			return
		}
		applyResponseHeaders(writer, result.headers)
		if result.problem != nil {
			WriteProblem(writer, *result.problem)
			return
		}
		if result.noContent {
			if result.status == http.StatusNoContent {
				writer.Header().Set("Cache-Control", "no-store")
			}
			writer.WriteHeader(result.status)
			return
		}
		writeJSON(writer, result.status, result.body)
	})
}

func validateOperationResult(result operationResult, errorPolicy routeErrorPolicy) error {
	if err := validateResponseHeaders(result.headers); err != nil {
		return err
	}
	if result.problem != nil {
		if result.status < 400 || result.status > 599 || result.problem.Status != result.status ||
			result.body != nil || result.noContent || result.problem.Code == "" {
			return errors.New("problem result shape is invalid")
		}
		if _, allowed := errorPolicy[result.problem.Code]; !allowed {
			return fmt.Errorf("problem code %q is undeclared", result.problem.Code)
		}
		return nil
	}
	if result.status < 200 || result.status > 299 {
		return fmt.Errorf("success status %d is invalid", result.status)
	}
	if result.noContent && result.body != nil {
		return errors.New("empty result contains a body")
	}
	return nil
}

func (a *API) protocolOperationHandler(
	definition routeDefinition,
	errorPolicy routeErrorPolicy,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, hasPrincipal := Principal(request.Context())
		if authRequiresPrincipal(definition.auth) && !hasPrincipal {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, authenticationRequiredError())
			return
		}
		params, ok := RequestParams(request.Context())
		if !ok {
			writeRouteApplicationError(writer, request, a.logger, errorPolicy, invalidRequestError("route_params", nil))
			return
		}
		result, err := definition.protocolOperation(operationRequest{
			context: request.Context(), principal: principal,
			metadata: RequestMetadata(request.Context()), params: params,
			request: request, idempotencyKey: idempotencyKeyFromContext(request.Context()),
		})
		if result.body != nil {
			defer func() {
				if err := result.body.Close(); err != nil {
					a.logger.ErrorContext(request.Context(), "close protocol response body", logErr(err))
				}
			}()
		}
		if err != nil {
			cause, headers := responseErrorParts(err)
			if failure, ok := cause.(applicationFailure); ok && failure.Code() == "idempotency.in_progress" {
				if headers == nil {
					headers = make(http.Header)
				}
				headers.Set("Retry-After", "1")
			}
			if validateResponseHeaders(headers) != nil || !routeErrorAllowed(errorPolicy, cause) {
				a.logInvalidRouteError(request, cause)
				WriteProblem(writer, internalProblem(request))
				return
			}
			applyResponseHeaders(writer, headers)
			writeApplicationError(writer, request, a.logger, cause)
			return
		}
		if err := validateProtocolResult(definition.protocolKind, result); err != nil {
			a.logger.ErrorContext(request.Context(), "route returned an invalid protocol result", logErr(err))
			WriteProblem(writer, internalProblem(request))
			return
		}
		applyResponseHeaders(writer, result.headers)
		switch result.kind {
		case RouteProtocolRedirect:
			http.Redirect(writer, request, result.location, result.status)
		case RouteProtocolBinaryDownload:
			if result.status == http.StatusNotModified {
				writer.Header().Set("Content-Length", strconv.FormatInt(result.contentLength, 10))
				writer.WriteHeader(result.status)
				return
			}
			writer.Header().Set("Content-Length", strconv.FormatInt(result.contentLength, 10))
			writer.WriteHeader(result.status)
			written, copyErr := io.CopyN(writer, result.body, result.contentLength)
			if copyErr != nil || written != result.contentLength {
				a.logger.ErrorContext(request.Context(), "write bounded protocol response", logInt64("written", written), logErr(copyErr))
			}
		case RouteProtocolStreamingUpload:
			writeJSON(writer, result.status, result.jsonBody)
		}
	})
}

func validateProtocolResult(expected RouteProtocolKind, result protocolResult) error {
	if result.kind != expected {
		return fmt.Errorf("protocol result kind %q does not match route kind %q", result.kind, expected)
	}
	if err := validateResponseHeaders(result.headers); err != nil {
		return err
	}
	if result.headers.Get("Location") != "" || result.headers.Get("Content-Length") != "" {
		return errors.New("protocol-owned headers cannot be supplied by an operation")
	}
	switch result.kind {
	case RouteProtocolRedirect:
		if result.status != http.StatusSeeOther || result.location == "" || strings.ContainsAny(result.location, "\r\n") ||
			result.body != nil || result.contentLength != 0 || result.jsonBody != nil {
			return errors.New("redirect protocol result is invalid")
		}
		if _, err := url.Parse(result.location); err != nil {
			return fmt.Errorf("redirect location: %w", err)
		}
	case RouteProtocolBinaryDownload:
		if result.location != "" || result.jsonBody != nil {
			return errors.New("binary protocol result contains incompatible fields")
		}
		if result.status == http.StatusNotModified {
			if result.body != nil || result.contentLength < 0 {
				return errors.New("not-modified binary result contains a body")
			}
		} else if result.status != http.StatusOK || result.body == nil || result.contentLength < 0 {
			return errors.New("binary protocol result is invalid")
		}
	case RouteProtocolStreamingUpload:
		if result.status < 200 || result.status > 299 || result.location != "" || result.body != nil || result.contentLength != 0 {
			return errors.New("streaming upload protocol result is invalid")
		}
	default:
		return fmt.Errorf("protocol result kind %q is unsupported", result.kind)
	}
	return nil
}

func writeRouteApplicationError(
	writer http.ResponseWriter,
	request *http.Request,
	logger Logger,
	policy routeErrorPolicy,
	err error,
) {
	if !routeErrorAllowed(policy, err) {
		logger.ErrorContext(
			request.Context(),
			"route returned an undeclared application error",
			logString("request_id", RequestID(request.Context())),
			logString("error_id", applicationErrorCode(err)),
			logErr(err),
		)
		WriteProblem(writer, internalProblem(request))
		return
	}
	writeApplicationError(writer, request, logger, err)
}

func routeErrorAllowed(policy routeErrorPolicy, err error) bool {
	if policy == nil {
		return true
	}
	_, allowed := policy[applicationErrorCode(err)]
	return allowed
}

func (a *API) logInvalidRouteError(request *http.Request, err error) {
	a.logger.ErrorContext(
		request.Context(),
		"route returned an invalid application error response",
		logString("request_id", RequestID(request.Context())),
		logString("error_id", applicationErrorCode(err)),
		logErr(err),
	)
}

func authRequiresPrincipal(requirement AuthRequirement) bool {
	switch requirement {
	case AuthPrincipalRequired,
		AuthSessionRequired,
		AuthStrongSessionRequired,
		AuthRecentSessionRequired,
		AuthStrongRecentSessionRequired:
		return true
	default:
		return false
	}
}

func (request operationRequest) invocation() application.Invocation {
	return application.NewInvocation(request.principal, request.metadata)
}

func (request operationRequest) decodeJSON(target any, where string) error {
	if err := decodeRequestJSON(request.request, target); err != nil {
		return invalidRequestError(where, err)
	}
	return nil
}

func (request operationRequest) queryLimit() (int, error) {
	value := request.request.URL.Query().Get("limit")
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 200 {
		return 0, invalidRequestError("limit", err)
	}
	return limit, nil
}
