// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/app/apperror"
)

func TestPublicFailuresConcealDenialAndAbsence(t *testing.T) {
	t.Parallel()
	want := apperror.Concealment{Unavailable: "exam.unavailable", Concealed: []apperror.Code{"exam.not_found"}}
	if !reflect.DeepEqual(publicFailures, want) {
		t.Fatalf("publicFailures = %#v, want %#v", publicFailures, want)
	}
	for _, code := range append([]apperror.Code{"authorization.denied"}, want.Concealed...) {
		cause := apperror.New(code)
		if err := publicFailures.Conceal(cause); !apperror.Is(err, "resource.not_found") || !errors.Is(err, cause) {
			t.Fatalf("Conceal(%s) = %v, want wrapped resource.not_found", code, err)
		}
	}
	safe := apperror.Invalid("request.invalid", "field")
	if err := publicFailures.Conceal(safe); !apperror.Is(err, "request.invalid") || !errors.Is(err, safe) {
		t.Fatalf("Conceal(request.invalid) = %v, want unchanged", err)
	}
	dependency := errors.New("dependency failed")
	if err := publicFailures.Conceal(dependency); !apperror.Is(err, want.Unavailable) || !errors.Is(err, dependency) {
		t.Fatalf("Conceal(dependency) = %v, want wrapped %s", err, want.Unavailable)
	}
}
