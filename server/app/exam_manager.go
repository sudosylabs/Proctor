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

// ListExamManagersQuery pages the Exam Managers of one Exam, newest grant
// first.
type ListExamManagersQuery = examengine.ListManagersQuery

// ExamManagerPage is one page of Exam Manager summaries.
type ExamManagerPage = examengine.ManagerPage

// ExamManagerSummary is the bounded projection of one Exam Manager
// relationship; User profile data is not included.
type ExamManagerSummary = examengine.ManagerSummary

// ExamManagerChange is the committed result of adding, removing or
// transferring an Exam Manager relationship.
type ExamManagerChange = examengine.ManagerChange

// AddExamManagerCommand grants a User Exam Manager authority over one Exam,
// fenced by ExpectedExamRevision and replayed by IdempotencyKey.
type AddExamManagerCommand = examengine.AddManagerCommand

// RemoveExamManagerCommand revokes a User's Exam Manager authority over one
// Exam, fenced by ExpectedExamRevision and replayed by IdempotencyKey.
type RemoveExamManagerCommand = examengine.RemoveManagerCommand

// TransferExamOwnershipCommand makes another User the owning Exam Manager of
// one Exam, fenced by ExpectedExamRevision and replayed by IdempotencyKey.
type TransferExamOwnershipCommand = examengine.TransferOwnerCommand
