// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"io"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// WorkspaceQuery selects one page of the candidate's Attempt Workspace
// manifest at ExpectedCursor, after AfterEntryID; ExpectedCursor -1 starts at
// the current cursor.
type WorkspaceQuery struct {
	Access         CandidateAccess
	ExpectedCursor int64
	AfterEntryID   model.AttemptWorkspaceEntryID
	Limit          int
}

// WorkspacePage is one page of the Attempt Workspace manifest at Cursor.
// RefreshRequired reports that the workspace changed since the expected
// cursor, and HasMore that a later page exists.
type WorkspacePage struct {
	WorkspaceID     model.ExamAttemptWorkspaceID
	Cursor          int64
	Items           []store.CandidateAttemptWorkspaceItem
	HasMore         bool
	RefreshRequired bool
}

// ListCandidateExamWorkspace returns one page of the candidate's Attempt
// Workspace manifest. ExpectedCursor -1 starts a first page at the current
// cursor; RefreshRequired reports that the workspace changed since that
// cursor.
func (s *Service) ListCandidateExamWorkspace(ctx context.Context, invocation invocation.Invocation, query WorkspaceQuery) (_ WorkspacePage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	selector, err := candidateSelector(invocation, query.Access)
	if err != nil {
		return WorkspacePage{}, err
	}
	if query.Limit < 1 || query.Limit > model.AttemptWorkspaceJournalReadMaximum || query.ExpectedCursor < -1 ||
		(query.ExpectedCursor == -1 && !query.AfterEntryID.IsZero()) {
		return WorkspacePage{}, apperror.Invalid("exam.attempt.invalid", "workspace_list")
	}
	page, err := s.deps.Workspace.List(ctx, store.CandidateWorkspaceListOptions{
		Access: selector, ExpectedCursor: query.ExpectedCursor, AfterEntryID: query.AfterEntryID, Limit: query.Limit,
	})
	if err != nil {
		return WorkspacePage{}, storeError(storeErrors, err)
	}
	if page == nil || !page.WorkspaceID.IsValid() || page.Cursor < 0 ||
		(page.RefreshRequired && (len(page.Items) != 0 || page.HasMore)) {
		return WorkspacePage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("missing candidate Workspace page"))
	}
	return WorkspacePage{WorkspaceID: page.WorkspaceID, Cursor: page.Cursor,
		Items:   append([]store.CandidateAttemptWorkspaceItem(nil), page.Items...),
		HasMore: page.HasMore, RefreshRequired: page.RefreshRequired}, nil
}

// OpenWorkspaceFileQuery names one Workspace file through the candidate's
// Attempt access.
type OpenWorkspaceFileQuery struct {
	Access  CandidateAccess
	EntryID model.AttemptWorkspaceEntryID
}

// OpenCandidateExamWorkspaceFile opens the current content of one file in the
// candidate's Attempt Workspace, whether it is still the Starter Workspace
// copy or Attempt-written content.
func (s *Service) OpenCandidateExamWorkspaceFile(ctx context.Context, invocation invocation.Invocation, query OpenWorkspaceFileQuery) (_ OpenedContent, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	selector, err := candidateSelector(invocation, query.Access)
	if err != nil {
		return OpenedContent{}, err
	}
	if !query.EntryID.IsValid() {
		return OpenedContent{}, apperror.Invalid("exam.attempt.invalid", "attempt_workspace_entry_id")
	}
	resolved, err := s.deps.Workspace.ResolveFile(ctx, selector, query.EntryID)
	if err != nil {
		return OpenedContent{}, storeError(storeErrors, err)
	}
	if resolved == nil || resolved.Entry.EntryID != query.EntryID || resolved.ContentVersion.IsZero() {
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete candidate Workspace selector"))
	}
	var body io.ReadCloser
	switch resolved.StorageOrigin {
	case model.AttemptWorkspaceStorageStarter:
		if !resolved.StarterObjectID.IsValid() || !resolved.AttemptObjectID.IsValid() {
			return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent starter Workspace selector"))
		}
		body, err = s.deps.Content.OpenStarterWorkspaceObject(ctx, resolved.StarterObjectID)
	case model.AttemptWorkspaceStorageAttempt:
		if !resolved.AttemptObjectID.IsValid() || resolved.StarterObjectID.IsValid() {
			return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent attempt-origin Workspace selector"))
		}
		body, err = s.deps.Content.OpenAttemptWorkspaceObject(ctx, resolved.AttemptObjectID)
	default:
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("invalid candidate Workspace storage origin"))
	}
	if err != nil {
		return OpenedContent{}, apperror.New("exam.attempt.unavailable").Wrap(err)
	}
	return OpenedContent{Body: body, MediaType: resolved.Entry.MediaType, SizeBytes: resolved.Entry.SizeBytes,
		SHA256: resolved.Entry.SHA256, ContentVersion: resolved.ContentVersion}, nil
}
