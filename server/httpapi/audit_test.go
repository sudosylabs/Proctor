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

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuditResourceResponseWireShapeRemainsSnakeCase(t *testing.T) {
	t.Parallel()

	response := auditEventResponseFromModel(&model.AuditEvent{
		Resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()},
	})
	encoded, err := json.Marshal(response)
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
