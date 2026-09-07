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
			if err != nil || result == nil || !result.ReenrollmentRequired || result.RecoveryGeneration != 3 ||
				fake.mfaCommand.InstitutionID != command.InstitutionID || fake.mfaCommand.UserID != command.UserID {
				t.Fatalf("inert result=%#v error=%v command=%#v", result, err, fake.mfaCommand)
			}
		} else if err == nil || result != nil || fake.mfaCommand.UserID != "" {
			t.Fatalf("state=%v accepted offline reset result=%#v error=%v", state, result, err)
		}
	}
}
