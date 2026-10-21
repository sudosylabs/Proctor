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

type ListExamManagersQuery = examengine.ListManagersQuery

type ExamManagerPage = examengine.ManagerPage
type ExamManagerSummary = examengine.ManagerSummary
type ExamManagerChange = examengine.ManagerChange

type AddExamManagerCommand = examengine.AddManagerCommand

type RemoveExamManagerCommand = examengine.RemoveManagerCommand
type TransferExamOwnershipCommand = examengine.TransferOwnerCommand
