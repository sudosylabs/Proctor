// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"testing"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

func TestCatalogDeclaresTheCompleteCoreDurableWorkGraph(t *testing.T) {
	t.Parallel()
	catalog := NewCatalog(CatalogDependencies{})
	_, err := jobengine.NewRegistry(catalog.Descriptors)
	require.NoError(t, err)
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
		model.JobTypeExamExportBuild:               true,
		model.JobTypeExamExportCleanup:             true,
		model.JobTypeRetentionExpire:               true,
		model.JobTypeRetentionReconcile:            true,
		model.JobTypeRetentionPurge:                true,
		model.JobTypeRetentionNotices:              true,
		model.JobTypeCleanup:                       true,
	}
	require.Len(t, catalog.Descriptors, len(wantTypes), "descriptor count")
	for _, descriptor := range catalog.Descriptors {
		require.True(t, wantTypes[descriptor.Type], "unexpected descriptor %q", descriptor.Type)
	}
	wantRecurrences := map[string]bool{
		"profile-picture-default-reconciliation": true,
		"file-purge-expired-content":             true,
		"job-history-cleanup":                    true,
		"command-outcome-cleanup":                true,
		"mail-cleanup":                           true,
		"invitation-maintenance":                 true,
		"exam-sitting-lifecycle-recovery":        true,
		"exam-export-cleanup":                    true,
		"retention-expire":                       true,
		"retention-reconcile":                    true,
		"retention-purge":                        true,
		"retention-notices":                      true,
	}
	require.Len(t, catalog.Recurrences, len(wantRecurrences), "recurrence count")
	for _, recurrence := range catalog.Recurrences {
		require.True(t, wantRecurrences[recurrence.Name], "unexpected recurrence %q", recurrence.Name)
	}
}
