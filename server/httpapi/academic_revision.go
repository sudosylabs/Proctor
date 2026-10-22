// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

func validateAcademicRevision(value Optional[int64]) error {
	if value.IsNull() {
		return invalidRequestError(nil)
	}
	if revision := value.ValuePointer(); revision != nil && *revision <= 0 {
		return invalidRequestError(nil)
	}
	return nil
}

// archiveAcademicRequest is the optional revision fence of an academic
// archive. Like every other fence it travels in the JSON body; a DELETE
// without a body archives unconditionally.
type archiveAcademicRequest struct {
	ExpectedRevision Optional[int64] `json:"expected_revision"`
}

func (r operationRequest) academicRevision() (*int64, error) {
	if r.request.ContentLength == 0 && r.request.Header.Get("Content-Type") == "" {
		return nil, nil
	}
	var body archiveAcademicRequest
	if err := r.decodeJSON(&body); err != nil {
		return nil, err
	}
	if err := validateAcademicRevision(body.ExpectedRevision); err != nil {
		return nil, err
	}
	return body.ExpectedRevision.ValuePointer(), nil
}
