// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
)

func BenchmarkPasswordHasherDefault(b *testing.B) {
	settings := config.Default().Authentication.Password
	policy := PasswordPolicy{
		MinimumLength: settings.MinimumLength, MaximumLength: settings.MaximumLength,
		ArgonMemoryKiB: settings.ArgonMemoryKiB, ArgonIterations: settings.ArgonIterations,
		ArgonParallelism: settings.ArgonParallelism, ArgonSaltBytes: settings.ArgonSaltBytes,
		ArgonKeyBytes: settings.ArgonKeyBytes, MaximumConcurrentOperations: settings.MaximumConcurrentOperations,
	}
	hasher, err := newPasswordHasher(policy, nil)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	password := "correct horse battery staple"
	encoded, err := hasher.Hash(ctx, password)
	if err != nil {
		b.Fatal(err)
	}
	for _, operation := range []string{"Hash", "Verify", "VerifyDummy"} {
		b.Run(operation, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				switch operation {
				case "Hash":
					_, err = hasher.Hash(ctx, password)
				case "Verify":
					err = hasher.Verify(ctx, encoded, password)
				case "VerifyDummy":
					err = hasher.VerifyDummy(ctx, password)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
