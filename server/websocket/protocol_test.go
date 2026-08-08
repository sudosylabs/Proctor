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
		Resource: Resource{
			Type: model.ResourceAcademicUnit,
			ID:   unitID,
		},
	}
	if err := valid.ValidateForPublish(); err != nil {
		t.Fatalf("valid event: %v", err)
	}

	userTargeted := &Event{
		Event:  "user.notification",
		UserID: model.NewId(),
	}
	if err := userTargeted.ValidateForPublish(); err != nil {
		t.Fatalf("user-targeted event: %v", err)
	}

	if err := (&Event{Event: "x"}).ValidateForPublish(); err == nil {
		t.Fatal("expected missing target error")
	}
	if err := (&Event{
		Event: "x", UserID: model.NewId(), Data: json.RawMessage(`{`),
	}).ValidateForPublish(); err == nil {
		t.Fatal("expected invalid data error")
	}
}

func TestSubscriptionIsValid(t *testing.T) {
	t.Parallel()

	subscription := Subscription{
		Action: model.ActionInstitutionManage,
		Resource: Resource{
			Type: model.ResourceInstitution,
			ID:   model.NewId(),
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

func TestEventResourceWireShapeRemainsSnakeCase(t *testing.T) {
	t.Parallel()

	event := &Event{
		Event:  "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: Resource{
			Type: model.ResourceAcademicUnit,
			ID:   model.NewId(),
		},
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Resource map[string]json.RawMessage `json:"resource"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Resource) != 2 || wire.Resource["type"] == nil || wire.Resource["id"] == nil {
		t.Fatalf("resource wire shape = %s", encoded)
	}
}
