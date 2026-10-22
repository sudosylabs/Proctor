// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package apperror_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
)

var testConcealment = apperror.Concealment{
	Unavailable: "widget.unavailable",
	Concealed:   []apperror.Code{"widget.not_found"},
}

func TestConcealmentPresentsUnexpectedFailuresAsUnavailable(t *testing.T) {
	t.Parallel()

	cause := errors.New("disk on fire")
	for name, present := range map[string]func(error) error{
		"present": testConcealment.Present, "conceal": testConcealment.Conceal,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, present(nil))
			err := present(cause)
			require.True(t, apperror.Is(err, "widget.unavailable"))
			require.ErrorIs(t, err, cause)
		})
	}
}

func TestConcealmentHidesOnlyDenialAndDeclaredCodes(t *testing.T) {
	t.Parallel()

	for _, code := range []apperror.Code{"authorization.denied", "widget.not_found"} {
		failure := apperror.New(code)
		err := testConcealment.Present(failure)
		require.True(t, apperror.Is(err, code), "Present(%s)", code)
		require.ErrorIs(t, err, failure, "Present(%s)", code)
		err = testConcealment.Conceal(failure)
		require.True(t, apperror.Is(err, "resource.not_found"), "Conceal(%s)", code)
		require.ErrorIs(t, err, failure, "Conceal(%s)", code)
	}
	visible := apperror.New("widget.conflict")
	err := testConcealment.Conceal(visible)
	require.True(t, apperror.Is(err, "widget.conflict"), "Conceal(widget.conflict)")
	require.ErrorIs(t, err, visible, "Conceal(widget.conflict)")
}
