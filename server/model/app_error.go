// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/utils.go. Proctor keeps the
// translation-id and wrapping flow while integrating with Problem Details.

package model

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const maxErrorLength = 1024

// TranslateFunc resolves a stable translation ID with optional template data.
// It intentionally matches Mattermost's translation function shape so a
// complete localization service can be wired without changing model APIs.
type TranslateFunc func(translationID string, args ...any) string

var (
	translateFunc     TranslateFunc
	translateFuncOnce sync.Once
)

// AppErrorInit installs the process-wide default model error translator.
// Initialization is intentionally one-shot and must happen during server
// construction, before requests are served.
func AppErrorInit(translator TranslateFunc) {
	if translator == nil {
		return
	}
	translateFuncOnce.Do(func() {
		translateFunc = translator
	})
}

// AppError is the error contract shared by models, application services, and
// transports. Id is both a stable machine code and a translation ID.
//
// DetailedError and wrapped errors are operator-facing and must never be
// serialized to an untrusted client. HTTP transports map AppError to their
// public error envelope instead of encoding it directly.
type AppError struct {
	Id              string `json:"id"`
	Message         string `json:"message"`
	DetailedError   string `json:"-"`
	RequestId       string `json:"request_id,omitempty"`
	StatusCode      int    `json:"status_code,omitempty"`
	Where           string `json:"-"`
	SkipTranslation bool   `json:"-"`

	params     map[string]any
	safeFields map[string]string
	wrapped    error
}

// NewAppError constructs and immediately translates an application error with
// the default translator, when one has been initialized.
func NewAppError(where, id string, params map[string]any, details string, status int) *AppError {
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	appErr := &AppError{
		Id:            id,
		Message:       id,
		DetailedError: details,
		StatusCode:    status,
		Where:         where,
		params:        cloneAnyMap(params),
	}
	appErr.Translate(translateFunc)
	return appErr
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}

	var builder strings.Builder
	if e.Where != "" {
		builder.WriteString(e.Where)
		builder.WriteString(": ")
	}
	builder.WriteString(e.Message)
	if e.DetailedError != "" {
		builder.WriteString(", ")
		builder.WriteString(e.DetailedError)
	}
	if e.wrapped != nil {
		builder.WriteString(", ")
		builder.WriteString(e.wrapped.Error())
	}

	result := builder.String()
	if len(result) > maxErrorLength {
		return result[:maxErrorLength] + "..."
	}
	return result
}

// Translate updates the public message while preserving the stable Id.
func (e *AppError) Translate(translator TranslateFunc) {
	if e == nil || e.SkipTranslation {
		return
	}
	if translator == nil {
		e.Message = e.Id
		return
	}
	if e.params == nil {
		e.Message = translator(e.Id)
		return
	}
	e.Message = translator(e.Id, cloneAnyMap(e.params))
}

// SystemMessage returns a translated message without mutating the error.
func (e *AppError) SystemMessage(translator TranslateFunc) string {
	if e == nil {
		return ""
	}
	if translator == nil {
		return e.Id
	}
	if e.params == nil {
		return translator(e.Id)
	}
	return translator(e.Id, cloneAnyMap(e.params))
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.wrapped
}

// Wrap attaches an internal cause and returns the receiver for fluent error
// construction.
func (e *AppError) Wrap(err error) *AppError {
	if e != nil {
		e.wrapped = err
	}
	return e
}

// WipeDetailed removes all operator-only detail before an error crosses a
// trust boundary.
func (e *AppError) WipeDetailed() {
	if e == nil {
		return
	}
	e.wrapped = nil
	e.DetailedError = ""
}

// WithSafeFields attaches fields that are explicitly safe for public error
// responses. Translation parameters are not public fields by default.
func (e *AppError) WithSafeFields(fields map[string]string) *AppError {
	if e != nil {
		e.safeFields = cloneStringMap(fields)
	}
	return e
}

func (e *AppError) HTTPStatus() int {
	if e == nil || e.StatusCode < 400 || e.StatusCode > 599 {
		return http.StatusInternalServerError
	}
	return e.StatusCode
}

func (e *AppError) ErrorCode() string {
	if e == nil || e.Id == "" {
		return "internal"
	}
	return e.Id
}

func (e *AppError) ClientMessage() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *AppError) SafeFields() map[string]string {
	if e == nil {
		return nil
	}
	return cloneStringMap(e.safeFields)
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func invalidModelError(where, modelName, field, reason, details string) *AppError {
	id := fmt.Sprintf("model.%s.is_valid.%s.app_error", modelName, field)
	return NewAppError(
		where,
		id,
		map[string]any{"Field": field, "Reason": reason},
		details,
		http.StatusBadRequest,
	).WithSafeFields(map[string]string{"field": field})
}
