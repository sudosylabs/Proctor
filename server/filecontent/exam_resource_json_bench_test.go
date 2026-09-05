// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"strings"
	"testing"
)

// These bounded fixtures measure the complete JSON validation path, including
// its separate UTF-8 pass. They exclude upload spooling and VFS storage.
func BenchmarkValidateExamResourceJSON(b *testing.B) {
	const targetBytes = 1 << 20
	const row = `{"key":123,"value":"abc"},`
	for _, fixture := range []struct {
		name string
		body string
	}{
		{"object_rows", "[" + strings.TrimSuffix(strings.Repeat(row, (targetBytes-2)/len(row)), ",") + "]"},
		{"string", `"` + strings.Repeat("a", targetBytes-2) + `"`},
		{"multibyte_string", `"` + strings.Repeat("😀", (targetBytes-2)/4) + `"`},
		{"numbers", "[" + strings.Repeat("0,", (targetBytes-3)/2) + "0]"},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.body)))
			for b.Loop() {
				if err := validateExamResourceJSON(strings.NewReader(fixture.body)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
