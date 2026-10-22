// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func nativeObservePolicy(t *testing.T) model.NativeSecurityPolicy {
	t.Helper()
	policy := model.DefaultNativeSecurityPolicy()
	require.NoError(t, json.Unmarshal([]byte(`{"id":"interactive_session","mode":"observe"}`), &policy.Families[1]))
	return policy
}
func TestConfigureDraftNativePolicyOrderAndFailures(t *testing.T) {
	for _, test := range []struct {
		name                     string
		replay, archived, denied bool
		revision                 int64
		want                     string
	}{
		{name: "success", revision: 1}, {name: "replay", replay: true, revision: 1}, {name: "archived", archived: true, revision: 1, want: "exam.archived"}, {name: "stale", revision: 2, want: "exam.draft.revision_conflict"}, {name: "authorization denied", denied: true, revision: 1, want: "exam.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.persistence.replayed = test.replay
			fixture.persistence.archived = test.archived
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			if test.denied {
				fixture.authorizer.err = errors.New("denied")
			}
			policy := nativeObservePolicy(t)
			view, err := fixture.service.ConfigureExamDraftNativePolicy(context.Background(), fixture.invocation, ConfigureDraftNativePolicyCommand{ExamID: fixture.examID, ExpectedDraftRevision: test.revision, NativePolicy: policy, IdempotencyKey: "native-key"})
			if test.want != "" {
				require.Error(t, err, "error = %v, want %s", err, test.want)
				require.False(t, !apperror.Is(err, test.want) && err.Error() != test.want, "error = %v, want %s", err, test.want)
				require.Equal(t, int64(0), fixture.effects.updatedRevision, "effect published after failure")
				return
			}
			require.NoError(t, err)
			if test.replay {
				require.Equal(t, int64(0), fixture.effects.updatedRevision, "replay republished")
				return
			}
			require.Equal(t, model.NativeModeObserve, view.Draft.Policy.Native.Families[1].Mode(), "native selection was not authorized and saved")
			require.Equal(t, int64(2), view.Draft.Revision, "native selection was not authorized and saved")
			require.Equal(t, model.ActionExamManage, fixture.authorizer.action, "native selection was not authorized and saved")
			require.Equal(t, "exam.draft.native_policy.configure.v1", fixture.persistence.idempotency.Operation, "wrong idempotency or unsafe audit")
			require.Len(t, fixture.auditor.value, 3, "wrong idempotency or unsafe audit")
			// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
			// begins before the durable write, and the write commits before its effect.
			require.Equal(t, []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_native_policy", "effect.updated"}, *fixture.order)
		})
	}
}
func TestConfigureDraftNativePolicyNoOpAndMalformed(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		fixture := newAuthoringFixture(t)
		fixture.persistence.actorIsManager = true
		fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
		policy := model.DefaultNativeSecurityPolicy()
		if invalid {
			policy.BaselineID = "disabled"
		}
		_, err := fixture.service.ConfigureExamDraftNativePolicy(context.Background(), fixture.invocation, ConfigureDraftNativePolicyCommand{ExamID: fixture.examID, ExpectedDraftRevision: 1, NativePolicy: policy, IdempotencyKey: "native-key"})
		require.Error(t, err, "invalid or no-op mutation reached persistence")
		require.Nil(t, fixture.persistence.nativePolicyUpdate, "invalid or no-op mutation reached persistence")
		require.Equal(t, int64(0), fixture.effects.updatedRevision, "invalid or no-op mutation reached persistence")
	}
}

func TestConfigureBrowserPolicyUsesInstallationPin(t *testing.T) {
	for _, origin := range []string{"http://institution.example", "http://unrelated.example", "http://institution.example:8080"} {
		t.Run(origin, func(t *testing.T) {
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: origin, PathPrefix: "/", HostMatch: model.BrowserPolicyHostExact, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord, InstitutionHTTPException: true}})
			require.NoError(t, err)
			result, err := fixture.service.ConfigureExamDraftBrowserPolicy(context.Background(), fixture.invocation, ConfigureDraftBrowserPolicyCommand{ExamID: fixture.examID, ExpectedDraftRevision: 1, Policy: policy, IdempotencyKey: "browser-pin"})
			if origin == "http://institution.example" {
				require.NoError(t, err, "matching exception: %#v", result)
				require.True(t, result.Draft.BrowserPolicy.Enabled, "matching exception: %#v", result)
			} else {
				require.Error(t, err, "foreign exception reached application effects")
				require.Zero(t, fixture.effects.updatedRevision, "foreign exception reached application effects")
				require.Empty(t, *fixture.order, "foreign exception reached application effects")
			}
		})
	}
}
