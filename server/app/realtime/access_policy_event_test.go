// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAccessPolicyChangedEventContainsOnlyRevision(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	event, err := NewAccessPolicyChangedEvent(institutionID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != "access_policy_changed" || event.Action != model.ActionAccessPolicyView ||
		event.Resource != (model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}) ||
		string(event.Data) != `{"revision":7}` {
		t.Fatalf("event = %#v payload=%s", event, event.Data)
	}
	if err := event.ValidateForPublish(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAccessPolicyChangedEvent(institutionID, 0); err == nil {
		t.Fatal("accepted zero policy revision")
	}
}
