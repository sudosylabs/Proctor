// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPublicFailuresConcealDenialAndAbsence(t *testing.T) {
	t.Parallel()
	want := apperror.Concealment{Unavailable: "exam.attempt.unavailable", Concealed: []apperror.Code{"exam.attempt.not_found", "exam.attempt.continuity_invalid"}}
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

// TestBrowserSourceConflictsKeepTheirMeaning covers every conflict the Browser
// source Store mints, so the desktop can tell a spent source budget from other
// Browser conflicts instead of receiving the generic Attempt conflict.
func TestBrowserSourceConflictsKeepTheirMeaning(t *testing.T) {
	t.Parallel()
	for constraint, want := range map[string]string{
		"browser_source_limit":       "exam.browser.source_budget_exhausted",
		"browser_source_conflict":    "exam.attempt.browser_activity_conflict",
		"browser_source_current":     "exam.attempt.browser_activity_conflict",
		"browser_source_predecessor": "exam.attempt.browser_activity_conflict",
		"browser_source_policy":      "exam.attempt.browser_activity_conflict",
		"browser_source_fence":       "exam.attempt.browser_activity_conflict",
		"browser_policy_disabled":    "exam.attempt.browser_activity_conflict",
		"browser_policy_revision":    "exam.attempt.browser_activity_conflict",
	} {
		t.Run(constraint, func(t *testing.T) {
			t.Parallel()
			err := storeError(storeErrors, store.NewErrConflict("browser_activity", constraint, nil))
			require.Equal(t, want, apperror.CodeOf(err, ""))
		})
	}
}
