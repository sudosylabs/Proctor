// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"testing"
)

func TestWholeValueValidationErrorHasNoSafeField(t *testing.T) {
	t.Parallel()

	err := invalidModelError("Class.Archive", "class", "", "class is already archived", "")
	if err.Code != "model.class.is_valid.app_error" || err.SafeFields() != nil {
		t.Fatalf("whole-value failure = %q %v", err.Code, err.SafeFields())
	}
	if got := err.Error(); got != "model.class.is_valid.app_error: class is already archived" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestWrappingValidationErrorKeepsCauseAndInnerField(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("origin is invalid")
	outer := invalidModelError("NewBrowserPolicy", "browser_policy", "", "browser policy rule 0 origin", "").wrap(sentinel)
	if !errors.Is(outer, sentinel) || outer.SafeFields() != nil {
		t.Fatalf("wrapped sentinel = %v %v", outer, outer.SafeFields())
	}
	inner := invalidModelError("ExamPolicySet.Validate", "exam_policy", "schema_version", "is unsupported", "")
	wrapped := invalidModelError("Exam.Validate", "exam", "", "policy", "").wrap(inner)
	if fields := wrapped.SafeFields(); fields["field"] != "schema_version" {
		t.Fatalf("wrapping hid the inner field: %v", fields)
	}
	var found *ValidationError
	if !errors.As(error(wrapped), &found) || found != wrapped || !errors.Is(wrapped, inner) {
		t.Fatal("wrapping did not expose the outer failure with its cause")
	}
}
