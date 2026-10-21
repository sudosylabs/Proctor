// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"fmt"
	"strings"
)

// ValidationError is a transport-neutral domain validation failure. It carries
// a stable machine code and an explicitly safe field name, but no HTTP status,
// localization state, or request correlation.
//
// It is the one error type the model constructs inline, always through
// invalidModelError. Field is empty when the failure concerns the whole value,
// such as a nil receiver, a refused transition, or a malformed document. A
// non-empty Field reaches clients as a safe Problem Details field, so naming
// one is a deliberate public change. Conditions that callers match with
// errors.Is are package-level sentinels instead.
type ValidationError struct {
	Code    string
	Field   string
	Reason  string
	Details string
	Model   string

	cause error
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString(e.Code)
	if e.Reason != "" {
		b.WriteString(": ")
		b.WriteString(e.Reason)
	}
	if e.Details != "" {
		b.WriteString(" (")
		b.WriteString(e.Details)
		b.WriteString(")")
	}
	if e.cause != nil {
		b.WriteString(": ")
		b.WriteString(e.cause.Error())
	}
	return b.String()
}

// Unwrap returns the underlying failure that a wrapping validation reported.
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// ErrorCode returns the stable machine-readable validation code.
func (e *ValidationError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

// SafeFields returns explicitly client-safe structured context.
func (e *ValidationError) SafeFields() map[string]string {
	if e == nil || e.Field == "" {
		return nil
	}
	return map[string]string{"field": e.Field}
}

// invalidModelError builds every inline model failure. where names the
// reporting function for call-site readability and is not serialized.
func invalidModelError(where, modelName, field, reason, details string) *ValidationError {
	_ = where
	code := fmt.Sprintf("model.%s.is_valid.%s.app_error", modelName, field)
	if field == "" {
		code = fmt.Sprintf("model.%s.is_valid.app_error", modelName)
	}
	return &ValidationError{
		Code:    code,
		Field:   field,
		Reason:  reason,
		Details: details,
		Model:   modelName,
	}
}

// wrap records cause as the underlying failure. A whole-value failure adopts
// the field of a wrapped ValidationError, so wrapping never hides the safe
// field the inner validation reported.
func (e *ValidationError) wrap(cause error) *ValidationError {
	e.cause = cause
	var inner *ValidationError
	if e.Field == "" && errors.As(cause, &inner) && inner.Field != "" {
		e.Field = inner.Field
	}
	return e
}
