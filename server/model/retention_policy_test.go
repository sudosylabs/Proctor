// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInitialRetentionPolicyDoesNotEnableExpiryOrDeletion(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 6, 12, 30, 0, 123456789, time.FixedZone("institution", 2*60*60))
	storedAt := at.UTC().Truncate(time.Microsecond)
	institutionID := NewInstitutionID()
	policy := NewInitialRetentionPolicy(institutionID, at)
	require.NoError(t, policy.Validate())
	require.Equal(t, institutionID, policy.InstitutionID)
	require.Equal(t, int64(1), policy.Revision)
	require.Equal(t, RetentionPolicySettings{}, policy.RetentionPolicySettings)
	require.True(t, policy.CreatedAt.Equal(storedAt))
	require.True(t, policy.UpdatedAt.Equal(storedAt))
	require.Same(t, time.UTC, policy.CreatedAt.Location())
	require.Same(t, time.UTC, policy.UpdatedAt.Location())
	want := map[string]any{"institution_id": institutionID.String(), "revision": int64(1),
		"submission_retention_days": 0, "integrity_retention_days": 0, "browser_activity_retention_days": 0, "security_operational_retention_days": 0, "audit_retention_days": 0,
		"export_retention_days": 0, "deletion_grace_days": 0, "candidate_notices": false, "automatic_deletion_enabled": false}
	require.Equal(t, want, policy.Auditable())
	projection := policy.Auditable()
	projection["automatic_deletion_enabled"] = true
	require.Equal(t, false, policy.Auditable()["automatic_deletion_enabled"], "mutating a returned projection enabled deletion")
}

func TestRetentionPolicySettingsAcceptOnlyBoundedNonNegativeDays(t *testing.T) {
	t.Parallel()
	for _, field := range []struct {
		name    string
		maximum int
		set     func(*RetentionPolicySettings, int)
	}{
		{"submission", 36500, func(s *RetentionPolicySettings, days int) { s.SubmissionRetentionDays = days }},
		{"integrity", 36500, func(s *RetentionPolicySettings, days int) { s.IntegrityRetentionDays = days }},
		{"audit", 36500, func(s *RetentionPolicySettings, days int) { s.AuditRetentionDays = days }},
		{"export", 7, func(s *RetentionPolicySettings, days int) { s.ExportRetentionDays = days }},
		{"grace", 36500, func(s *RetentionPolicySettings, days int) { s.DeletionGraceDays = days }},
	} {
		t.Run(field.name, func(t *testing.T) {
			for _, days := range []int{0, 1, 7, 8, 36500, -1, 36501, math.MaxInt} {
				settings := RetentionPolicySettings{}
				field.set(&settings, days)
				err := settings.Validate()
				require.Equal(t, days >= 0 && days <= field.maximum, err == nil, "%s retention %d days: %v", field.name, days, err)
			}
		})
	}
}

func TestRetentionPolicyReplacementPreservesNoOpAndZeroSemantics(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	policy := NewInitialRetentionPolicy(NewInstitutionID(), at)
	settings := RetentionPolicySettings{SubmissionRetentionDays: 365, IntegrityRetentionDays: 90, AuditRetentionDays: 730, ExportRetentionDays: 7, DeletionGraceDays: 30}
	require.NoError(t, policy.Replace(1, settings, at.Add(time.Hour)))
	require.Equal(t, int64(2), policy.Revision)
	require.Equal(t, settings, policy.RetentionPolicySettings)
	require.True(t, policy.CreatedAt.Equal(at))
	require.True(t, policy.UpdatedAt.Equal(at.Add(time.Hour)))
	before := *policy
	require.NoError(t, policy.Replace(2, settings, at.Add(2*time.Hour)), "no-op changed policy: %#v", policy)
	require.Equal(t, before, *policy, "no-op changed policy: %#v", policy)
	require.NoError(t, policy.Replace(2, RetentionPolicySettings{}, at.Add(3*time.Hour)))
	require.Equal(t, int64(3), policy.Revision)
	require.Equal(t, RetentionPolicySettings{}, policy.RetentionPolicySettings)
	require.Equal(t, false, policy.Auditable()["automatic_deletion_enabled"])
	clone := policy.Clone()
	clone.SubmissionRetentionDays = 10
	require.Equal(t, 0, policy.SubmissionRetentionDays, "Clone shared mutable policy values")
}

func TestRetentionPolicyRejectedReplacementNeverMutatesRecord(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		prepare  func(*RetentionPolicy)
		revision int64
		settings RetentionPolicySettings
		at       time.Time
		conflict bool
	}{
		{name: "stale revision", revision: 2, at: at.Add(time.Hour), conflict: true},
		{name: "negative duration", revision: 1, settings: RetentionPolicySettings{ExportRetentionDays: -1}, at: at.Add(time.Hour)},
		{name: "unbounded duration", revision: 1, settings: RetentionPolicySettings{AuditRetentionDays: 36501}, at: at.Add(time.Hour)},
		{name: "export beyond ceiling", revision: 1, settings: RetentionPolicySettings{ExportRetentionDays: 8}, at: at.Add(time.Hour)},
		{name: "zero instant", revision: 1},
		{name: "backwards instant", revision: 1, settings: RetentionPolicySettings{DeletionGraceDays: 1}, at: at.Add(-time.Second)},
		{name: "invalid existing record", prepare: func(p *RetentionPolicy) { p.InstitutionID = "" }, revision: 1, at: at.Add(time.Hour)},
		{name: "revision overflow", prepare: func(p *RetentionPolicy) { p.Revision = math.MaxInt64 }, revision: math.MaxInt64,
			settings: RetentionPolicySettings{SubmissionRetentionDays: 1}, at: at.Add(time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := NewInitialRetentionPolicy(NewInstitutionID(), at)
			if test.prepare != nil {
				test.prepare(policy)
			}
			before := *policy
			err := policy.Replace(test.revision, test.settings, test.at)
			require.Error(t, err, "rejected replacement mutated policy or returned wrong error: %#v", policy)
			require.Equal(t, test.conflict, errors.Is(err, ErrRetentionPolicyRevisionConflict), "rejected replacement mutated policy or returned wrong error: %#v", policy)
			require.Equal(t, before, *policy, "rejected replacement mutated policy or returned wrong error: %#v", policy)
		})
	}
	var missing *RetentionPolicy
	require.Error(t, missing.Validate(), "nil policy did not fail closed")
	require.Error(t, missing.Replace(1, RetentionPolicySettings{}, at), "nil policy did not fail closed")
	require.Nil(t, missing.Clone(), "nil policy did not fail closed")
}

func TestRetentionPolicyActionsRequireInteractiveAdministration(t *testing.T) {
	t.Parallel()
	for _, action := range []Action{ActionRetentionPolicyView, ActionRetentionPolicyManage} {
		definition, ok := DefinitionForAction(action)
		require.True(t, ok)
		require.Equal(t, ResourceInstitution, definition.ResourceType)
		require.True(t, definition.InheritInstitutionScope)
		require.False(t, definition.InheritAcademicUnitScopes)
		require.True(t, definition.PersonalAccessTokenForbidden)
		require.False(t, IsPersonalAccessTokenAction(string(action)))
	}
	require.False(t, IsGrantableAction(string(ActionRetentionPolicyManage)), "retention management escaped the protected system-administrator Role")
	require.True(t, IsSystemAdministratorAction(string(ActionRetentionPolicyManage)), "retention management escaped the protected system-administrator Role")
}
