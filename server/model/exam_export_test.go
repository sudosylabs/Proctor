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
)

func TestExamExportBoundsAndAvailability(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	valid := ExamExport{ID: NewExamExportID(), Scope: RetentionHoldScope{ExamID: NewExamID(), SittingID: NewExamSittingID()}, RequesterUserID: NewUserID(), Categories: []RetentionCategory{RetentionCategoryWork, RetentionCategoryIntegrity}, State: ExamExportQueued, PolicyRevision: 1, CreatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), SourceExpiresAt: at.Add(24 * time.Hour), SubmissionCount: 1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
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
			if value.Validate() == nil {
				t.Fatal("invalid export accepted")
			}
		})
	}
	ready := valid
	ready.State, ready.ReadyAt, ready.ArchiveSizeBytes, ready.ArchiveSHA256 = ExamExportReady, OptionalTimeFrom(at.Add(time.Minute)), 42, strings.Repeat("a", 64)
	if err := ready.Validate(); err != nil {
		t.Fatal(err)
	}
	ready.ReadyAt = OptionalTimeFrom(ready.ExpiresAt)
	if ready.Validate() == nil {
		t.Fatal("publication at the expiry boundary accepted")
	}
	if ValidExamExportContent(42, strings.Repeat("A", 64)) || ValidExamExportContent(ExamExportMaximumArchiveBytes+1, strings.Repeat("a", 64)) {
		t.Fatal("unbounded or noncanonical content accepted")
	}
}
