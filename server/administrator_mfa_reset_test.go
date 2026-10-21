// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
)

func TestAdministratorMFAResetOnlyAvailableOnInertServer(t *testing.T) {
	t.Parallel()
	command := AdministratorMFAResetCommand{InstitutionID: "institution", UserID: "user"}
	for _, state := range []nodeState{nodeInert, nodeStarting, nodeRunning, nodeStopping, nodeClosed} {
		fake := &administratorRecoveryRuntimeFake{mfaResult: &app.AdministratorMFAResetResult{RecoveryGeneration: 3, ReenrollmentRequired: true}}
		node := &Server{state: state, components: runtimeComponents{administratorRecovery: fake}}
		result, err := node.ResetAdministratorMFA(context.Background(), command)
		if state == nodeInert {
			require.NoError(t, err, "inert result=%#v error=%v command=%#v", result, err, fake.mfaCommand)
			require.NotNil(t, result, "inert result=%#v error=%v command=%#v", result, err, fake.mfaCommand)
			require.True(t, result.ReenrollmentRequired, "inert result=%#v error=%v command=%#v", result, err, fake.mfaCommand)
			require.Equal(t, int64(3), result.RecoveryGeneration, "inert result=%#v error=%v command=%#v", result, err, fake.mfaCommand)
			require.Equal(t, command.InstitutionID, fake.mfaCommand.InstitutionID, "inert result=%#v error=%v command=%#v", result, err, fake.mfaCommand)
			require.Equal(t, command.UserID, fake.mfaCommand.UserID, "inert result=%#v error=%v command=%#v", result, err, fake.mfaCommand)
		} else {
			require.Error(t, err, "state=%v accepted offline reset", state)
			require.Nil(t, result, "state=%v accepted offline reset", state)
			require.Empty(t, fake.mfaCommand.UserID, "state=%v accepted offline reset", state)
		}
	}
}
