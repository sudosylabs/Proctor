// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

func withMiddleware(next http.Handler, logger *mlog.Logger, maxBodyBytes int64) http.Handler {
	handler := limitRequestBody(next, maxBodyBytes)
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
			RequestId: RequestID(request.Context()),
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

func recoverPanics(next http.Handler, logger *mlog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(
					request.Context(),
					"panic recovered",
					mlog.String("request_id", RequestID(request.Context())),
					mlog.Any("panic", recovered),
					mlog.String("stack", string(debug.Stack())),
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

func logRequests(next http.Handler, logger *mlog.Logger) http.Handler {
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
			mlog.String("request_id", RequestID(request.Context())),
			mlog.String("method", request.Method),
			mlog.String("path", request.URL.Path),
			mlog.Int("status", recorder.status),
			mlog.Int("bytes", recorder.bytes),
			mlog.Int64("duration_ms", time.Since(started).Milliseconds()),
		)
	})
}
