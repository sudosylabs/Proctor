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

type ListExamsQuery = examengine.ListQuery

type ExamArchiveFilter = store.ExamArchiveFilter

const (
	ExamArchiveActive   = store.ExamArchiveActive
	ExamArchiveArchived = store.ExamArchiveArchived
	ExamArchiveAll      = store.ExamArchiveAll
)

type ExamSummary = store.ExamSummary

type ExamCatalogPage = examengine.CatalogPage

type ArchiveExamCommand = examengine.ArchiveCommand
