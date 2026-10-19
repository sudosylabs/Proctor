// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// middlewarePolicy carries the deployment-owned inputs of the common
// middleware chain.
type middlewarePolicy struct {
	trustedProxies trustedProxies
}

func withMiddleware(next http.Handler, logger Logger, policy middlewarePolicy) http.Handler {
	handler := next
	handler = recoverPanics(handler, logger)
	handler = logRequests(handler, logger)
	handler = securityHeaders(handler)
	handler = attachRequestIdentity(handler, policy.trustedProxies)
	return handler
}

// attachRequestIdentity assigns the request ID and the request metadata
// recorded by audit. The server generates every request ID unless the
// connection peer is a trusted proxy that supplied a well-formed one, so a
// client cannot choose the audit correlation of its own requests.
func attachRequestIdentity(next http.Handler, proxies trustedProxies) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		peer := proxies.peer(request)
		requestID := newRequestID()
		if peer.trusted {
			requestID = requestIDFromTrustedProxy(request.Header.Get(RequestIDHeader))
		}
		writer.Header().Set(RequestIDHeader, requestID)
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		metadata := model.RequestMetadata{
			RequestID: requestID,
			IPAddress: proxies.clientAddress(request, peer),
			UserAgent: request.UserAgent(),
		}
		ctx = context.WithValue(ctx, requestMetadataContextKey{}, metadata)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

type requestMetadataContextKey struct{}

func RequestMetadata(ctx context.Context) model.RequestMetadata {
	metadata, _ := ctx.Value(requestMetadataContextKey{}).(model.RequestMetadata)
	return metadata
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}

func recoverPanics(next http.Handler, logger Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(
					request.Context(),
					"panic recovered",
					logString("request_id", RequestID(request.Context())),
					logAny("panic", recovered),
					logString("stack", string(debug.Stack())),
				)
				WriteProblem(writer, internalProblem(request))
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(data)
	r.bytes += written
	return written, err
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	connection, buffered, err := hijacker.Hijack()
	if err == nil && r.status == 0 {
		// The WebSocket upgrader writes the handshake after hijacking, so the
		// recorder must track the protocol switch without calling WriteHeader.
		r.status = http.StatusSwitchingProtocols
	}
	return connection, buffered, err
}

func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type requestBodyRecorder struct {
	io.ReadCloser
	bytes int64
}

func (r *requestBodyRecorder) Read(buffer []byte) (int, error) {
	read, err := r.ReadCloser.Read(buffer)
	r.bytes += int64(read)
	return read, err
}

type requestMetricsObservation struct {
	metrics      Metrics
	route        string
	method       string
	started      time.Time
	response     *responseRecorder
	requestBody  *requestBodyRecorder
	httpFinished func()
}

func beginRequestMetrics(writer http.ResponseWriter, request *http.Request, metrics Metrics, route, method string) (*responseRecorder, *requestMetricsObservation) {
	response := &responseRecorder{ResponseWriter: writer}
	body := &requestBodyRecorder{ReadCloser: request.Body}
	if request.Body != nil {
		request.Body = body
	}
	return response, &requestMetricsObservation{
		metrics: metrics, route: route, method: method, started: time.Now(),
		response: response, requestBody: body, httpFinished: metrics.HTTPStarted(),
	}
}

func (o *requestMetricsObservation) finish(status int) {
	if status == 0 {
		status = o.response.status
	}
	if status == 0 {
		status = http.StatusOK
	}
	o.metrics.ObserveHTTPRequest(o.route, o.method, status, time.Since(o.started))
	o.metrics.ObserveHTTPPayload(o.route, o.method, o.requestBody.bytes, int64(o.response.bytes))
	o.httpFinished()
}

func logRequests(next http.Handler, logger Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: writer}
		next.ServeHTTP(recorder, request)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		logger.InfoContext(
			request.Context(),
			"http request",
			logString("request_id", RequestID(request.Context())),
			logString("method", request.Method),
			logString("path", request.URL.Path),
			logInt("status", recorder.status),
			logInt("bytes", recorder.bytes),
			logInt64("duration_ms", time.Since(started).Milliseconds()),
		)
	})
}

func observeRequestMetrics(next http.Handler, metrics Metrics, route, method string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder, observation := beginRequestMetrics(writer, request, metrics, route, method)
		defer func() {
			recovered := recover()
			status := 0
			if recovered != nil {
				status = http.StatusInternalServerError
			}
			observation.finish(status)
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(recorder, request)
	})
}
