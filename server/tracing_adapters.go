// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
	"github.com/sudosylabs/proctor/server/tracing"
)

// httpTracing adapts the tracing module to the HTTP transport's port.
type httpTracing struct{ tracing *tracing.Module }

var _ httpapi.Tracing = httpTracing{}

func (t httpTracing) StartRequest(ctx context.Context, request httpapi.TracedRequest) (context.Context, httpapi.RequestSpan) {
	return t.tracing.StartHTTPRequest(ctx, request.Header, request.Method, request.ContinueParent, request.RequestID)
}

// storeTracing adapts the tracing module to the store timing layer's port.
type storeTracing struct{ tracing *tracing.Module }

var _ timerlayer.Tracer = storeTracing{}

func (t storeTracing) StartOperation(ctx context.Context, operation timerlayer.Operation) (context.Context, func(timerlayer.Outcome)) {
	ctx, finish := t.tracing.StartStoreOperation(ctx, operation.String())
	return ctx, func(outcome timerlayer.Outcome) { finish(outcome != timerlayer.OutcomeSuccess) }
}

// jobTracing adapts the tracing module to the Job engine's port.
type jobTracing struct{ tracing *tracing.Module }

var _ job.Tracer = jobTracing{}

func (t jobTracing) StartExecution(ctx context.Context, jobType model.JobType) (context.Context, func(job.ExecutionOutcome)) {
	ctx, finish := t.tracing.StartJobExecution(ctx, string(jobType))
	return ctx, func(outcome job.ExecutionOutcome) {
		finish(string(outcome), outcome != job.ExecutionSucceeded && outcome != job.ExecutionCanceled &&
			outcome != job.ExecutionRelinquished && outcome != job.ExecutionShutdown)
	}
}

// logCorrelationFields adds the request ID and the current trace and span IDs
// to every context-carrying log record. They are server-assigned or
// trusted-proxy correlation identifiers, never request content.
func logCorrelationFields(ctx context.Context) []logging.Field {
	var fields []logging.Field
	if requestID := httpapi.RequestID(ctx); requestID != "" {
		fields = append(fields, logging.String("request_id", requestID))
	}
	if traceID, spanID, ok := tracing.Correlation(ctx); ok {
		fields = append(fields, logging.String("trace_id", traceID), logging.String("span_id", spanID))
	}
	return fields
}
