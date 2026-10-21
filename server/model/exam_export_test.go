// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExamExportBoundsAndAvailability(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	valid := ExamExport{ID: NewExamExportID(), Scope: RetentionHoldScope{ExamID: NewExamID(), SittingID: NewExamSittingID()}, RequesterUserID: NewUserID(), Categories: []RetentionCategory{RetentionCategoryWork, RetentionCategoryIntegrity}, State: ExamExportQueued, PolicyRevision: 1, CreatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), SourceExpiresAt: at.Add(24 * time.Hour), SubmissionCount: 1}
	require.NoError(t, valid.Validate())
	for name, change := range map[string]func(*ExamExport){
		"unconfigured lifetime":      func(e *ExamExport) { e.ExpiresAt = at },
		"lifetime beyond seven days": func(e *ExamExport) { e.ExpiresAt = e.ExpiresAt.Add(time.Nanosecond) },
		"extended source protection": func(e *ExamExport) { e.SourceExpiresAt = e.SourceExpiresAt.Add(time.Nanosecond) },
		"empty Sitting archive":      func(e *ExamExport) { e.SubmissionCount = 0 },
		"oversized Sitting archive":  func(e *ExamExport) { e.SubmissionCount = ExamExportMaximumSubmissions + 1 },
		"oversized file content":     func(e *ExamExport) { e.SourceBytes = ExamExportMaximumSourceBytes + 1 },
		"implicit categories":        func(e *ExamExport) { e.Categories = nil },
		"duplicate categories":       func(e *ExamExport) { e.Categories = []RetentionCategory{RetentionCategoryWork, RetentionCategoryWork} },
		"unverified readiness":       func(e *ExamExport) { e.State = ExamExportReady },
		"expired archive disclosure": func(e *ExamExport) {
			e.State = ExamExportExpired
			e.ArchiveSizeBytes = 1
			e.ArchiveSHA256 = strings.Repeat("a", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			change(&value)
			require.Error(t, value.Validate(), "invalid export accepted")
		})
	}
	ready := valid
	ready.State, ready.ReadyAt, ready.ArchiveSizeBytes, ready.ArchiveSHA256 = ExamExportReady, OptionalTimeFrom(at.Add(time.Minute)), 42, strings.Repeat("a", 64)
	require.NoError(t, ready.Validate())
	ready.ReadyAt = OptionalTimeFrom(ready.ExpiresAt)
	require.Error(t, ready.Validate(), "publication at the expiry boundary accepted")
	require.False(t, ValidExamExportContent(42, strings.Repeat("A", 64)), "unbounded or noncanonical content accepted")
	require.False(t, ValidExamExportContent(ExamExportMaximumArchiveBytes+1, strings.Repeat("a", 64)), "unbounded or noncanonical content accepted")
}
