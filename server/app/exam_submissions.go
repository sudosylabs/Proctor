// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamSubmissionReceipt = store.ExamSubmissionReceipt
type ExamSubmissionManagerView = examattempt.ManagedSubmission
type ExamSubmissionManifestPage = examattempt.SubmissionManifestPage
type GetExamSubmissionQuery = examattempt.GetSubmissionQuery
type ListExamSubmissionManifestQuery = examattempt.ListSubmissionManifestQuery
type OpenExamSubmissionFileQuery = examattempt.OpenSubmissionFileQuery

type SubmitExamAttemptCommand = examattempt.SubmitCommand
