// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAccessPolicyChangedEventContainsOnlyRevision(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	event, err := NewAccessPolicyChangedEvent(institutionID, 7)
	require.NoError(t, err)
	require.Equal(t, "access_policy_changed", event.Name, "event = %#v payload", event)
	require.Equal(t, model.ActionAccessPolicyView, event.Action, "event = %#v payload", event)
	require.Equal(t, model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, event.Resource, "event = %#v payload", event)
	require.Equal(t, `{"revision":7}`, string(event.Data), "event = %#v payload", event)
	require.NoError(t, event.ValidateForPublish())
	_, err = NewAccessPolicyChangedEvent(institutionID, 0)
	require.Error(t, err, "accepted zero policy revision")
}
