// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package ordertest

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// recordingT captures failures instead of failing the enclosing test.
type recordingT struct {
	failed bool
}

func (r *recordingT) Errorf(string, ...any) { r.failed = true }

func (r *recordingT) FailNow() {
	r.failed = true
	panic(r)
}

func run(recorded []string, want ...string) (failed bool) {
	r := &recordingT{}
	defer func() {
		if recovered := recover(); recovered != nil && recovered != r {
			panic(recovered)
		}
		failed = r.failed
	}()
	RequireInOrder(r, recorded, want...)
	return r.failed
}

func TestRequireInOrder(t *testing.T) {
	t.Parallel()

	recorded := []string{"lookup", "audit", "lookup", "commit", "effect"}
	for _, test := range []struct {
		want   []string
		failed bool
	}{
		{want: []string{"audit", "commit", "effect"}},
		{want: []string{"commit"}},
		{want: nil},
		{want: []string{"commit", "audit"}, failed: true},
		{want: []string{"audit", "missing"}, failed: true},
		{want: []string{"effect", "effect"}, failed: true},
	} {
		t.Run(fmt.Sprint(test.want), func(t *testing.T) {
			assert.Equal(t, test.failed, run(recorded, test.want...))
		})
	}
}
