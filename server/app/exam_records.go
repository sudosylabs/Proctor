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

type CompleteExamSittingRecordsCommand = examengine.CompleteRecordsCommand
type WaiveSubmissionReviewCommand = examengine.WaiveReviewCommand
type CreateRetentionHoldCommand = examengine.CreateRetentionHoldCommand
type ReleaseRetentionHoldCommand = examengine.ReleaseRetentionHoldCommand
type ListRetentionHoldsQuery = store.ExamRecordsHoldListOptions
type ExamSittingRecordsSnapshot = store.ExamRecordsCompletionSnapshot
type RetentionHoldPage = store.ExamRecordsHoldPage

var _ examengine.RecordsAuthorizer = (*accessControlService)(nil)
