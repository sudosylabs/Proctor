// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestCandidateActivityAttemptAccessMembershipFence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		attemptState      model.ExamAttemptState
		currentMembership bool
		establishedAccess bool
		want              store.CandidateExamAccessState
	}{
		{name: "ready requires current membership", attemptState: model.ExamAttemptReady, want: store.CandidateExamAccessNotEligible},
		{name: "suspended requires current membership", attemptState: model.ExamAttemptSuspended, want: store.CandidateExamAccessNotEligible},
		{name: "active reconnect requires current membership", attemptState: model.ExamAttemptActive, want: store.CandidateExamAccessNotEligible},
		{name: "active established access survives membership end", attemptState: model.ExamAttemptActive, establishedAccess: true, want: store.CandidateExamAccessResumable},
		{name: "suspended member awaits reallow", attemptState: model.ExamAttemptSuspended, currentMembership: true, want: store.CandidateExamAccessAwaitReallow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := candidateActivityAttemptAccess(test.attemptState, model.ExamSittingOpen,
				test.currentMembership, test.establishedAccess, false)
			if got != test.want {
				t.Fatalf("candidateActivityAttemptAccess() = %q, want %q", got, test.want)
			}
		})
	}
}
