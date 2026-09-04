// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"testing"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

func TestCatalogDeclaresTheCompleteCoreDurableWorkGraph(t *testing.T) {
	t.Parallel()
	catalog := NewCatalog(CatalogDependencies{})
	if _, err := jobengine.NewRegistry(catalog.Descriptors); err != nil {
		t.Fatalf("registry: %v", err)
	}
	wantTypes := map[model.JobType]bool{
		model.JobTypeProfilePictureGenerateDefault: true,
		model.JobTypeProfilePictureReconcile:       true,
		model.JobTypeFilePurgeExpiredContent:       true,
		model.JobTypeCommandOutcomeCleanup:         true,
		model.JobTypeMailDeliver:                   true,
		model.JobTypeMailDeliverCredential:         true,
		model.JobTypeMailExpandSitting:             true,
		model.JobTypeMailCleanup:                   true,
		model.JobTypeMailRekey:                     true,
		model.JobTypeInvitationMaintenance:         true,
		model.JobTypeExamSittingLifecycle:          true,
		model.JobTypeExamSittingSealing:            true,
		model.JobTypeExamSittingLifecycleRecovery:  true,
		model.JobTypeCleanup:                       true,
	}
	if len(catalog.Descriptors) != len(wantTypes) {
		t.Fatalf("descriptor count = %d, want %d", len(catalog.Descriptors), len(wantTypes))
	}
	for _, descriptor := range catalog.Descriptors {
		if !wantTypes[descriptor.Type] {
			t.Fatalf("unexpected descriptor %q", descriptor.Type)
		}
	}
	wantRecurrences := map[string]bool{
		"profile-picture-default-reconciliation": true,
		"file-purge-expired-content":             true,
		"job-history-cleanup":                    true,
		"command-outcome-cleanup":                true,
		"mail-cleanup":                           true,
		"invitation-maintenance":                 true,
		"exam-sitting-lifecycle-recovery":        true,
	}
	if len(catalog.Recurrences) != len(wantRecurrences) {
		t.Fatalf("recurrence count = %d, want %d", len(catalog.Recurrences), len(wantRecurrences))
	}
	for _, recurrence := range catalog.Recurrences {
		if !wantRecurrences[recurrence.Name] {
			t.Fatalf("unexpected recurrence %q", recurrence.Name)
		}
	}
}
