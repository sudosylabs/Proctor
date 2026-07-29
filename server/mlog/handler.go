// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mlog

import (
	"context"
	"log/slog"
)

type fanoutHandler []slog.Handler

func (h fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var firstErr error
	for _, handler := range h {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h fanoutHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	handlers := make(fanoutHandler, 0, len(h))
	for _, handler := range h {
		handlers = append(handlers, handler.WithAttrs(attributes))
	}
	return handlers
}

func (h fanoutHandler) WithGroup(name string) slog.Handler {
	handlers := make(fanoutHandler, 0, len(h))
	for _, handler := range h {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return handlers
}

type dynamicHandler struct {
	core       *core
	operations []handlerOperation
}

type handlerOperation struct {
	group      string
	attributes []slog.Attr
}

func (h *dynamicHandler) Enabled(ctx context.Context, level slog.Level) bool {
	h.core.mu.RLock()
	defer h.core.mu.RUnlock()
	return !h.core.closed && h.core.handler.Enabled(ctx, level)
}

func (h *dynamicHandler) Handle(ctx context.Context, record slog.Record) error {
	h.core.mu.RLock()
	defer h.core.mu.RUnlock()
	if h.core.closed {
		return nil
	}
	handler := h.decorated(h.core.handler)
	return handler.Handle(ctx, record)
}

func (h *dynamicHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	cloned := &dynamicHandler{
		core:       h.core,
		operations: append([]handlerOperation(nil), h.operations...),
	}
	cloned.operations = append(cloned.operations, handlerOperation{
		attributes: append([]slog.Attr(nil), attributes...),
	})
	return cloned
}

func (h *dynamicHandler) WithGroup(name string) slog.Handler {
	cloned := &dynamicHandler{
		core:       h.core,
		operations: append([]handlerOperation(nil), h.operations...),
	}
	cloned.operations = append(cloned.operations, handlerOperation{group: name})
	return cloned
}

func (h *dynamicHandler) decorated(handler slog.Handler) slog.Handler {
	for _, operation := range h.operations {
		if operation.group != "" {
			handler = handler.WithGroup(operation.group)
		} else {
			handler = handler.WithAttrs(operation.attributes)
		}
	}
	return handler
}
