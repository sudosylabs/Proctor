// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/store"
)

// ListExamsQuery pages the Exams visible to the caller, optionally narrowed by
// Academic Unit, text, archive state, Exam Sitting state and schedule window.
type ListExamsQuery = examengine.ListQuery

// ExamArchiveFilter selects active, archived or all Exams in a catalog
// listing.
type ExamArchiveFilter = store.ExamArchiveFilter

// Exam archive filters for ListExamsQuery.
const (
	ExamArchiveActive   = store.ExamArchiveActive
	ExamArchiveArchived = store.ExamArchiveArchived
	ExamArchiveAll      = store.ExamArchiveAll
)

// ExamSummary is the bounded catalog projection of one Exam; authored
// Markdown, policy, Exam Resources and Starter Workspace state are absent.
type ExamSummary = store.ExamSummary

// ExamCatalogPage is one page of Exam summaries.
type ExamCatalogPage = examengine.CatalogPage

// ArchiveExamCommand archives an Exam, fenced by ExpectedExamRevision and
// replayed by IdempotencyKey.
type ArchiveExamCommand = examengine.ArchiveCommand
