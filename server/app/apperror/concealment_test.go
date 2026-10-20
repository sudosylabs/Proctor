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
		if err := present(nil); err != nil {
			t.Fatalf("%s(nil) = %v", name, err)
		}
		err := present(cause)
		if !apperror.Is(err, "widget.unavailable") || !errors.Is(err, cause) {
			t.Fatalf("%s(cause) = %v, want wrapped widget.unavailable", name, err)
		}
	}
}

func TestConcealmentHidesOnlyDenialAndDeclaredCodes(t *testing.T) {
	t.Parallel()

	for _, code := range []apperror.Code{"authorization.denied", "widget.not_found"} {
		failure := apperror.New(code)
		if err := testConcealment.Present(failure); !apperror.Is(err, code) || !errors.Is(err, failure) {
			t.Fatalf("Present(%s) = %v, want unchanged", code, err)
		}
		err := testConcealment.Conceal(failure)
		if !apperror.Is(err, "resource.not_found") || !errors.Is(err, failure) {
			t.Fatalf("Conceal(%s) = %v, want wrapped resource.not_found", code, err)
		}
	}
	visible := apperror.New("widget.conflict")
	if err := testConcealment.Conceal(visible); !apperror.Is(err, "widget.conflict") || !errors.Is(err, visible) {
		t.Fatalf("Conceal(widget.conflict) = %v, want unchanged", err)
	}
}
