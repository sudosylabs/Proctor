// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuditResourceResponseWireShapeRemainsSnakeCase(t *testing.T) {
	t.Parallel()

	response := auditEventResponseFromModel(&model.AuditEvent{
		Resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewID()},
	})
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	var wire struct {
		Resource map[string]json.RawMessage `json:"resource"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Len(t, wire.Resource, 2)
	require.NotNil(t, wire.Resource["type"])
	require.NotNil(t, wire.Resource["id"])
}
