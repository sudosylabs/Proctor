// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"fmt"
	"strings"
)

// ValidationError is a transport-neutral domain validation failure. It carries
// a stable machine code and an explicitly safe field name, but no HTTP status,
// localization state, or request correlation.
type ValidationError struct {
	Code    string
	Field   string
	Reason  string
	Details string
	Model   string
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
	return b.String()
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

func invalidModelError(where, modelName, field, reason, details string) *ValidationError {
	_ = where // retained for call-site readability; not serialized
	return &ValidationError{
		Code:    fmt.Sprintf("model.%s.is_valid.%s.app_error", modelName, field),
		Field:   field,
		Reason:  reason,
		Details: details,
		Model:   modelName,
	}
}
