// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package model

import "slices"

// NativeSecuritySummary excludes detector, condition, source-instance and
// occurrence identities. Health is live coverage, never evidence of misconduct.
type NativeSecuritySummary struct {
	RetainedConditionRecords int64                       `json:"retained_condition_records"`
	LiveCoverageAvailable    bool                        `json:"live_coverage_available"`
	Sources                  []NativeSourceHealthSummary `json:"sources"`
}
type NativeSourceHealthSummary struct {
	SourceID   NativeSourceID `json:"source_id"`
	Health     string         `json:"health"`
	Permission string         `json:"permission"`
	Complete   bool           `json:"complete"`
}

func (s NativeSecuritySummary) Validate() error {
	if s.RetainedConditionRecords < 0 || s.RetainedConditionRecords > DeliveryAttemptRecordLimit || s.Sources == nil || len(s.Sources) > 11 || !s.LiveCoverageAvailable && len(s.Sources) > 0 {
		return ErrNativeDeliveryInvalid
	}
	previous := -1
	for _, source := range s.Sources {
		order := slices.Index(NativeSources(), source.SourceID)
		if order <= previous || !slices.Contains([]string{"starting", "healthy", "degraded", "failed", "recovering", "stopped"}, source.Health) || !slices.Contains([]string{"denied", "granted", "not_determined", "restricted", "unavailable"}, source.Permission) {
			return ErrNativeDeliveryInvalid
		}
		previous = order
	}
	return nil
}
func (s NativeSecuritySummary) Clone() NativeSecuritySummary {
	s.Sources = append([]NativeSourceHealthSummary{}, s.Sources...)
	return s
}
