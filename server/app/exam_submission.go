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

// ExamSubmissionReceipt is the receipt of a sealed Submission: its immutable
// content identity and current Browser Activity delivery settlement, without
// manifest, paths, credentials or review state.
type ExamSubmissionReceipt = store.ExamSubmissionReceipt

// ExamSubmissionManagerView is a sealed Submission as shown to an Exam
// Manager, with the authorization it was read under.
type ExamSubmissionManagerView = examattempt.ManagedSubmission

// ExamSubmissionManifestPage is one page of a sealed Submission's manifest at
// its frozen Workspace cursor and manifest digest.
type ExamSubmissionManifestPage = examattempt.SubmissionManifestPage

// GetExamSubmissionQuery names one Submission of an Exam Attempt for an Exam
// Manager.
type GetExamSubmissionQuery = examattempt.GetSubmissionQuery

// ListExamSubmissionManifestQuery pages a Submission's manifest after a
// Workspace Entry.
type ListExamSubmissionManifestQuery = examattempt.ListSubmissionManifestQuery

// OpenExamSubmissionFileQuery names one file in a Submission's manifest.
type OpenExamSubmissionFileQuery = examattempt.OpenSubmissionFileQuery

// SubmitExamAttemptCommand seals the candidate's Exam Attempt into an
// immutable Submission. It is fenced by the live Participation Generation,
// ExpectedCurrentRevisionID, ExpectedWorkspaceCursor and
// FinalFocusLossSequence, and replayed by IdempotencyKey.
type SubmitExamAttemptCommand = examattempt.SubmitCommand
