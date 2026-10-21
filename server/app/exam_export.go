// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	examengine "github.com/sudosylabs/proctor/server/app/exam"
)

// CreateExamExportCommand requests an Examination Export of the selected
// retention categories within a scope; IdempotencyKey makes it replayable.
type CreateExamExportCommand = examengine.CreateExportCommand

// ExamExportQuery names one Examination Export within a scope.
type ExamExportQuery = examengine.ExportQuery

// ExamExportDownload is an Examination Export with its archive stream; the
// caller must close Body.
type ExamExportDownload = examengine.ExportDownload
