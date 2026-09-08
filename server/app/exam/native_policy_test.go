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
	"github.com/sudosylabs/proctor/server/model"
	"reflect"
	"testing"
)

func nativeObservePolicy(t *testing.T) model.NativeSecurityPolicy {
	t.Helper()
	policy := model.DefaultNativeSecurityPolicy()
	if err := json.Unmarshal([]byte(`{"id":"interactive_session","mode":"observe"}`), &policy.Families[1]); err != nil {
		t.Fatal(err)
	}
	return policy
}
func TestConfigureDraftNativePolicyOrderAndFailures(t *testing.T) {
	for _, test := range []struct {
		name                     string
		replay, archived, denied bool
		revision                 int64
		want                     string
	}{
		{name: "success", revision: 1}, {name: "replay", replay: true, revision: 1}, {name: "archived", archived: true, revision: 1, want: "exam.archived"}, {name: "stale", revision: 2, want: "exam.draft.revision_conflict"}, {name: "authorization denied", denied: true, revision: 1, want: "denied"},
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
			view, err := fixture.service.ConfigureDraftNativePolicy(context.Background(), fixture.call, ConfigureDraftNativePolicyCommand{ExamID: fixture.examID, ExpectedDraftRevision: test.revision, NativePolicy: policy, IdempotencyKey: "native-key"})
			if test.want != "" {
				if err == nil || err.Error() != test.want {
					t.Fatalf("error = %v, want %s", err, test.want)
				}
				if fixture.effects.updatedRevision != 0 {
					t.Fatal("effect published after failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.replay {
				if fixture.effects.updatedRevision != 0 {
					t.Fatal("replay republished")
				}
				return
			}
			if view.Draft.Policy.Native.Families[1].Mode() != model.NativeModeObserve || view.Draft.Revision != 2 || fixture.authorizer.action != model.ActionExamManage {
				t.Fatal("native selection was not authorized and saved")
			}
			if fixture.persistence.idempotency.Operation != "exam.draft.native_policy.configure.v1" || len(fixture.auditor.value) != 3 {
				t.Fatal("wrong idempotency or unsafe audit")
			}
			want := []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_native_policy", "effect.updated"}
			if !reflect.DeepEqual(*fixture.order, want) {
				t.Fatalf("order = %v", *fixture.order)
			}
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
		_, err := fixture.service.ConfigureDraftNativePolicy(context.Background(), fixture.call, ConfigureDraftNativePolicyCommand{ExamID: fixture.examID, ExpectedDraftRevision: 1, NativePolicy: policy, IdempotencyKey: "native-key"})
		if err == nil || fixture.persistence.nativePolicyUpdate != nil || fixture.effects.updatedRevision != 0 {
			t.Fatal("invalid or no-op mutation reached persistence")
		}
	}
}

func TestConfigureBrowserPolicyUsesInstallationPin(t *testing.T) {
	for _, origin := range []string{"http://institution.example", "http://unrelated.example", "http://institution.example:8080"} {
		t.Run(origin, func(t *testing.T) {
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			policy, err := model.NewBrowserPolicy(true, "start", []model.BrowserPolicyRule{{RuleID: "start", Origin: origin, PathPrefix: "/", HostMatch: model.BrowserPolicyHostExact, BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationRecord, InstitutionHTTPException: true}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := fixture.service.ConfigureDraftBrowserPolicy(context.Background(), fixture.call, ConfigureDraftBrowserPolicyCommand{ExamID: fixture.examID, ExpectedDraftRevision: 1, Policy: policy, IdempotencyKey: "browser-pin"})
			if origin == "http://institution.example" {
				if err != nil || !result.Draft.BrowserPolicy.Enabled {
					t.Fatalf("matching exception: %#v, %v", result, err)
				}
			} else if err == nil || fixture.effects.updatedRevision != 0 || len(*fixture.order) != 0 {
				t.Fatalf("foreign exception reached application effects: %v, %v", err, *fixture.order)
			}
		})
	}
}
