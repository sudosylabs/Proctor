// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

func withMiddleware(next http.Handler, logger Logger) http.Handler {
	handler := next
	handler = recoverPanics(handler, logger)
	handler = logRequests(handler, logger)
	handler = securityHeaders(handler)
	handler = attachRequestMetadata(handler)
	handler = assignRequestID(handler)
	return handler
}

func attachRequestMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ipAddress := request.RemoteAddr
		if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
			ipAddress = host
		}
		metadata := model.RequestMetadata{
			RequestID: RequestID(request.Context()),
			IPAddress: ipAddress,
			UserAgent: request.UserAgent(),
		}
		ctx := context.WithValue(request.Context(), requestMetadataContextKey{}, metadata)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

type requestMetadataContextKey struct{}

func RequestMetadata(ctx context.Context) model.RequestMetadata {
	metadata, _ := ctx.Value(requestMetadataContextKey{}).(model.RequestMetadata)
	return metadata
}

func assignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := requestIDFromHeader(request.Header.Get(RequestIDHeader))
		writer.Header().Set(RequestIDHeader, requestID)
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
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

func limitRequestBody(next http.Handler, maxBodyBytes int64) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Body != nil {
			request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
		}
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
