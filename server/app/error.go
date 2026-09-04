// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"errors"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
)

// Error is a transport-neutral application failure. It carries a stable
// machine-readable code, explicitly safe public fields, and an optional
// wrapped cause. It contains no HTTP status, protocol envelope, localization
// state, request ID, or other transport metadata; each transport maps codes
// through its own exhaustive table.
type Error struct {
	code   string
	fields map[string]string
	cause  error
}

// NewError constructs an application failure with the given stable code.
// Codes use lowercase dotted domain names such as
// "academic_unit.not_found" or "class.enrollment_conflict".
func NewError(code string) *Error {
	if code == "" {
		panic("app: error code is empty")
	}
	return &Error{code: code}
}

// Code returns the stable machine-readable failure code.
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

// Fields returns a copy of the explicitly safe public fields.
func (e *Error) Fields() map[string]string {
	if e == nil {
		return nil
	}
	return cloneFields(e.fields)
}

// WithField attaches one explicitly safe public field and returns the
// receiver for fluent construction.
func (e *Error) WithField(key, value string) *Error {
	if e == nil {
		return nil
	}
	if e.fields == nil {
		e.fields = make(map[string]string, 1)
	}
	e.fields[key] = value
	return e
}

// WithFields attaches explicitly safe public fields and returns the
// receiver for fluent construction. Values are copied.
func (e *Error) WithFields(fields map[string]string) *Error {
	if e == nil {
		return nil
	}
	if len(fields) == 0 {
		return e
	}
	if e.fields == nil {
		e.fields = make(map[string]string, len(fields))
	}
	for key, value := range fields {
		e.fields[key] = value
	}
	return e
}

// Wrap attaches an internal cause and returns the receiver for fluent
// construction. Causes are operator-only and must never be serialized to an
// untrusted client.
func (e *Error) Wrap(err error) *Error {
	if e != nil {
		e.cause = err
	}
	return e
}

// Error implements the error interface for operator-facing diagnostics.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	var builder strings.Builder
	builder.WriteString(e.code)
	if e.cause != nil {
		builder.WriteString(": ")
		builder.WriteString(e.cause.Error())
	}
	return builder.String()
}

// Unwrap returns the wrapped cause, if any.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is reports whether err is or wraps an application error with the given code.
func Is(err error, code string) bool {
	got, ok := As(err)
	return ok && got.Code() == code
}

// As finds the first *Error in err's chain.
func As(err error) (*Error, bool) {
	var applicationError *Error
	if errors.As(err, &applicationError) {
		return applicationError, true
	}
	return nil, false
}

// domainInvalid maps a domain ValidationError into a stable application code
// while preserving safe field context for transport mapping.
func domainInvalid(code string, err error) error {
	out := NewError(code)
	var validation *model.ValidationError
	if errors.As(err, &validation) {
		if fields := validation.SafeFields(); len(fields) > 0 {
			out = out.WithFields(fields)
		}
	}
	return out.Wrap(err)
}

func cloneFields(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
