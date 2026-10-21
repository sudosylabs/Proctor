// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package review

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
)

func TestPublicFailuresConcealDenialAndAbsence(t *testing.T) {
	t.Parallel()
	want := apperror.Concealment{Unavailable: "exam.integrity_review.unavailable", Concealed: []apperror.Code{"exam.integrity_review.not_found"}}
	require.Equal(t, want, publicFailures, "publicFailures")
	for _, code := range append([]apperror.Code{"authorization.denied"}, want.Concealed...) {
		cause := apperror.New(code)
		err := publicFailures.Conceal(cause)
		require.True(t, apperror.Is(err, "resource.not_found"), "Conceal(%s)", code)
		require.ErrorIs(t, err, cause, "Conceal(%s)", code)
	}
	safe := apperror.Invalid("request.invalid", "field")
	err := publicFailures.Conceal(safe)
	require.True(t, apperror.Is(err, "request.invalid"), "Conceal(request.invalid)")
	require.ErrorIs(t, err, safe, "Conceal(request.invalid)")
	dependency := errors.New("dependency failed")
	err = publicFailures.Conceal(dependency)
	require.True(t, apperror.Is(err, want.Unavailable), "Conceal(dependency) = %v, want wrapped %s", err, want.Unavailable)
	require.ErrorIs(t, err, dependency, "Conceal(dependency) = %v, want wrapped %s", err, want.Unavailable)
}
