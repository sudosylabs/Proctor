// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"regexp"
	"time"
)

// Validate millisecond precision lexically: time.Parse truncates fractional
// digits beyond nanoseconds, which must not hide a nonzero submillisecond tail.
var securityInstantPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,3}0*)?(Z|[+-]00:00)$`)

// securityJSONInstant preserves timestamp strings in hashed agreement documents.
// The spelling is immutable value state, so copying a domain record is safe.
// Domain logic continues to compare time.Time values; changing that value causes
// serialization to use its new spelling rather than stale received bytes.
type securityJSONInstant struct {
	time.Time
	spelling string
}

func (v securityJSONInstant) MarshalJSON() ([]byte, error) {
	if v.spelling != "" {
		original, err := time.Parse(time.RFC3339Nano, v.spelling)
		if err == nil && original.Equal(v.Time) {
			return json.Marshal(v.spelling)
		}
	}
	return v.Time.MarshalJSON()
}

func (v *securityJSONInstant) UnmarshalJSON(raw []byte) error {
	var spelling string
	if err := json.Unmarshal(raw, &spelling); err != nil {
		return err
	}
	if !securityInstantPattern.MatchString(spelling) {
		return ErrSecurityPolicyInvalid
	}
	value, err := time.Parse(time.RFC3339Nano, spelling)
	if err != nil {
		return err
	}
	if !securityInstant(value) {
		return ErrSecurityPolicyInvalid
	}
	*v = securityJSONInstant{Time: value, spelling: spelling}
	return nil
}

func optionalSecurityJSONInstant(value *time.Time, spelling string) *securityJSONInstant {
	if value == nil {
		return nil
	}
	return &securityJSONInstant{Time: *value, spelling: spelling}
}
