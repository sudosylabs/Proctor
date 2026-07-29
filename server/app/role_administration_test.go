// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestValidatePatchedPermissionsPreservesButDoesNotIntroduceUnknownActions(
	t *testing.T,
) {
	t.Parallel()

	current := []string{string(model.ActionClassView), "future.permission"}
	if appErr := validatePatchedPermissions("test", current, nil); appErr != nil {
		t.Fatalf("display-only patch rejected existing unknown permission: %v", appErr)
	}
	preserved := []string{string(model.ActionClassMembersView), "future.permission"}
	if appErr := validatePatchedPermissions("test", current, &preserved); appErr != nil {
		t.Fatalf("preserved unknown permission rejected: %v", appErr)
	}
	introduced := []string{"another.future_permission"}
	if appErr := validatePatchedPermissions("test", current, &introduced); appErr == nil {
		t.Fatal("new unknown permission was accepted")
	}
}
