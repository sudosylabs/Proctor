// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package apperror owns Proctor's single transport-neutral application error
// type. The parent application and its child modules share it so every
// expected public failure carries the same stable code, safe fields, and
// operator-only cause regardless of which module produced it. StoreTable
// translates typed Store failures and Concealment presents a protected use
// case's failures, so no module writes its own translator or presenter.
//
// The package depends only on the standard library. It contains no HTTP
// status, protocol envelope, localization state, or request metadata; each
// transport maps codes through its own exhaustive table.
package apperror

import (
	"errors"
	"strings"
)

// Code is a stable machine-readable application failure code. Codes are
// lowercase dotted `<domain>[.<subdomain>].<condition>` names whose segments
// are snake_case, such as "exam.sitting.not_found". Every code is listed in
// the HTTP error catalog; constant codes are verified against it.
type Code string

// Error is a transport-neutral application failure. It carries a stable code,
// explicitly safe public fields, and an optional wrapped cause.
type Error struct {
	code   Code
	fields map[string]string
	cause  error
}

// New constructs an application failure with the given stable code.
func New(code Code) *Error {
	if code == "" {
		panic("apperror: error code is empty")
	}
	return &Error{code: code}
}

// Invalid constructs a failure for one rejected input and records the safe
// input name in the conventional "field" public field.
func Invalid(code Code, field string) *Error {
	return New(code).WithField("field", field)
}

// Code returns the stable machine-readable failure code.
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return string(e.code)
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

// WithFields attaches explicitly safe public fields and returns the receiver
// for fluent construction. Values are copied.
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
	builder.WriteString(string(e.code))
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
// Matching never mints a code, so it accepts any string-typed code value.
func Is[C ~string](err error, code C) bool {
	got, ok := As(err)
	return ok && string(got.code) == string(code)
}

// As finds the first *Error in err's chain.
func As(err error) (*Error, bool) {
	var applicationError *Error
	if errors.As(err, &applicationError) {
		return applicationError, true
	}
	return nil, false
}

// CodeOf returns the code of the first *Error in err's chain, or fallback
// when err carries no application error.
func CodeOf(err error, fallback Code) string {
	if got, ok := As(err); ok {
		return got.Code()
	}
	return string(fallback)
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
