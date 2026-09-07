//go:build !darwin && !linux

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import "errors"

func lockState(string) (func(), error) {
	return nil, errors.New("development seeding currently supports macOS and Linux")
}
