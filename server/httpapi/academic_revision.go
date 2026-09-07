// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import "strconv"

func validateAcademicRevision(value Optional[int64]) error {
	if value.IsNull() {
		return invalidRequestError("expected_revision", nil)
	}
	if revision := value.ValuePointer(); revision != nil && *revision <= 0 {
		return invalidRequestError("expected_revision", nil)
	}
	return nil
}

func (request operationRequest) academicRevision() (*int64, error) {
	values, present := request.request.URL.Query()["expected_revision"]
	if !present {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, invalidRequestError("expected_revision", nil)
	}
	revision, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || revision <= 0 {
		return nil, invalidRequestError("expected_revision", err)
	}
	return &revision, nil
}
