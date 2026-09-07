// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

// checkAcademicRevision compares a client's optional snapshot only after the
// application has authorized and loaded the resource. Store CAS still protects
// changes between this comparison and commit.
func checkAcademicRevision(expected *int64, current int64, conflictCode string) error {
	if expected == nil {
		return nil
	}
	if *expected <= 0 {
		return NewError("request.invalid").WithField("field", "expected_revision")
	}
	if *expected != current {
		return NewError(conflictCode)
	}
	return nil
}
