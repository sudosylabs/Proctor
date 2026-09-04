// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

// ClassEnrollment reports the membership created by an enrollment or transfer
// and the prior membership closed by the same transaction, when one existed.
type ClassEnrollment struct {
	Membership *ClassMember `json:"membership"`
	Previous   *ClassMember `json:"previous,omitempty"`
}

func (e *ClassEnrollment) Auditable() map[string]any {
	result := map[string]any{}
	if e != nil && e.Membership != nil {
		result["membership"] = e.Membership.Auditable()
	}
	if e != nil && e.Previous != nil {
		result["previous"] = e.Previous.Auditable()
	}
	return result
}
