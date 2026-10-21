// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestEventValidateForPublish(t *testing.T) {
	t.Parallel()

	unitID := model.NewID()
	valid := &Event{
		Event:  "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: Resource{
			Type: model.ResourceAcademicUnit,
			ID:   unitID,
		},
	}
	require.NoError(t, valid.ValidateForPublish())

	userTargeted := &Event{
		Event:  "user.notification",
		UserID: model.NewID(),
	}
	require.NoError(t, userTargeted.ValidateForPublish())

	require.Error(t, (&Event{Event: "x"}).ValidateForPublish(), "expected missing target error")
	require.Error(t, (&Event{
		Event: "x", UserID: model.NewID(), Data: json.RawMessage(`{`),
	}).ValidateForPublish(), "expected invalid data error")
}

func TestSubscriptionIsValid(t *testing.T) {
	t.Parallel()

	subscription := Subscription{
		Action: model.ActionInstitutionManage,
		Resource: Resource{
			Type: model.ResourceInstitution,
			ID:   model.NewID(),
		},
	}
	require.True(t, subscription.IsValid(), "expected valid subscription")
	require.NotEmpty(t, subscription.Key(), "expected subscription key")
	require.False(t, (Subscription{}).IsValid(), "empty subscription should be invalid")
}

func TestEventResourceWireShapeRemainsSnakeCase(t *testing.T) {
	t.Parallel()

	event := &Event{
		Event:  "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: Resource{
			Type: model.ResourceAcademicUnit,
			ID:   model.NewID(),
		},
	}
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	var wire struct {
		Resource map[string]json.RawMessage `json:"resource"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Len(t, wire.Resource, 2)
	require.NotNil(t, wire.Resource["type"])
	require.NotNil(t, wire.Resource["id"])
}
