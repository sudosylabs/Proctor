// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDevSeedHelpAndArgumentValidation(t *testing.T) {
	var out bytes.Buffer
	if err := Execute(context.Background(), []string{"dev", "seed", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"--profile", "--seed", "--state-dir", "--refresh-sittings"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("missing %s", name)
		}
	}
	if err := Execute(context.Background(), []string{"dev", "seed", "extra"}, &out, &out); err == nil {
		t.Fatal("accepted positional arguments")
	}
}
