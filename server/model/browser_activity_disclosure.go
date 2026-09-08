// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

// BrowserActivityDisclosure explains collection. It confers no history access
// and is neither consent nor a correction acknowledgement. Zero days denotes
// no configured automatic expiry; holds and completion gates still apply.
type BrowserActivityDisclosure struct {
	NoticeID                     string `json:"notice_id"`
	Audience                     string `json:"audience"`
	RetentionPolicyRevision      int64  `json:"retention_policy_revision"`
	BrowserActivityRetentionDays int    `json:"browser_activity_retention_days"`
	RetentionAnchor              string `json:"retention_anchor"`
	MayCreateIntegrityEvidence   bool   `json:"may_create_integrity_evidence"`
}

func NewBrowserActivityDisclosure(policy *RetentionPolicy, mayCreateEvidence bool) (BrowserActivityDisclosure, error) {
	if policy == nil || policy.Validate() != nil {
		return BrowserActivityDisclosure{}, errRetentionPolicyInvalid
	}
	return BrowserActivityDisclosure{NoticeID: "integrated_browser_activity", Audience: "exam_managers_with_browser_activity_permission", RetentionPolicyRevision: policy.Revision, BrowserActivityRetentionDays: policy.BrowserActivityRetentionDays, RetentionAnchor: "eligible_closed_sitting_records_completion", MayCreateIntegrityEvidence: mayCreateEvidence}, nil
}

func (value BrowserActivityDisclosure) Validate() error {
	if value.NoticeID != "integrated_browser_activity" || value.Audience != "exam_managers_with_browser_activity_permission" || value.RetentionPolicyRevision < 1 || !securitySafeInt(value.RetentionPolicyRevision) || value.BrowserActivityRetentionDays < 0 || value.BrowserActivityRetentionDays > 36500 || value.RetentionAnchor != "eligible_closed_sitting_records_completion" {
		return errRetentionPolicyInvalid
	}
	return nil
}
