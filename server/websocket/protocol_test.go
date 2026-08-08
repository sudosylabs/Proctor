// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"encoding/json"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestEventValidateForPublish(t *testing.T) {
	t.Parallel()

	unitID := model.NewId()
	valid := &Event{
		Event:  "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			Id:   unitID,
		},
	}
	if err := valid.ValidateForPublish(); err != nil {
		t.Fatalf("valid event: %v", err)
	}

	userTargeted := &Event{
		Event:  "user.notification",
		UserId: model.NewId(),
	}
	if err := userTargeted.ValidateForPublish(); err != nil {
		t.Fatalf("user-targeted event: %v", err)
	}

	if err := (&Event{Event: "x"}).ValidateForPublish(); err == nil {
		t.Fatal("expected missing target error")
	}
	if err := (&Event{
		Event: "x", UserId: model.NewId(), Data: json.RawMessage(`{`),
	}).ValidateForPublish(); err == nil {
		t.Fatal("expected invalid data error")
	}
}

func TestSubscriptionIsValid(t *testing.T) {
	t.Parallel()

	subscription := Subscription{
		Action: model.ActionInstitutionManage,
		Resource: model.Resource{
			Type: model.ResourceInstitution,
			Id:   model.NewId(),
		},
	}
	if !subscription.IsValid() {
		t.Fatal("expected valid subscription")
	}
	if subscription.Key() == "" {
		t.Fatal("expected subscription key")
	}
	if (Subscription{}).IsValid() {
		t.Fatal("empty subscription should be invalid")
	}
}
