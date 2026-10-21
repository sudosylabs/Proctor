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

	"github.com/stretchr/testify/require"
)

func TestWholeValueValidationErrorHasNoSafeField(t *testing.T) {
	t.Parallel()

	err := invalidModelError("Class.Archive", "class", "", "class is already archived", "")
	require.Equal(t, "model.class.is_valid.app_error", err.Code, "whole-value failure")
	require.Nil(t, err.SafeFields(), "whole-value failure")
	require.Equal(t, "model.class.is_valid.app_error: class is already archived", err.Error())
}

func TestWrappingValidationErrorKeepsCauseAndInnerField(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("origin is invalid")
	outer := invalidModelError("NewBrowserPolicy", "browser_policy", "", "browser policy rule 0 origin", "").wrap(sentinel)
	require.ErrorIs(t, outer, sentinel, "wrapped sentinel")
	require.Nil(t, outer.SafeFields(), "wrapped sentinel")
	inner := invalidModelError("ExamPolicySet.Validate", "exam_policy", "schema_version", "is unsupported", "")
	wrapped := invalidModelError("Exam.Validate", "exam", "", "policy", "").wrap(inner)
	require.Equal(t, "schema_version", wrapped.SafeFields()["field"])
	var found *ValidationError
	require.ErrorAs(t, error(wrapped), &found, "wrapping did not expose the outer failure with its cause")
	require.Same(t, wrapped, found, "wrapping did not expose the outer failure with its cause")
	require.ErrorIs(t, wrapped, inner, "wrapping did not expose the outer failure with its cause")
}
