// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type executionObservationOutcome struct {
	Ignored         bool
	Workspace       attemptWorkspaceMutationOutcomeV1
	ExpectedVersion model.WorkspaceContentVersion
}

type executionObservedNodeRow struct {
	EntryID   string         `db:"entry_id"`
	Kind      string         `db:"kind"`
	Path      string         `db:"path"`
	Expected  sql.NullString `db:"expected_content_version"`
	Resulting sql.NullString `db:"resulting_content_version"`
	Cursor    int64          `db:"workspace_cursor"`
	Deleted   bool           `db:"deleted"`
}

func (s *SQLExamAttemptWorkspaceStore) ResolveObservation(ctx context.Context, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExecutionObservationTarget, error) {
	if access.SourceObservation == nil || !validAttemptWorkspaceMutationAccess(access) {
		return nil, store.NewErrInvalidInput("execution_observation", "access", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve execution observation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExecutionObservationTarget, error) {
		target, _, err := lockAttemptWorkspaceMutationTarget(ctx, tx, access)
		if err != nil {
			return nil, err
		}
		return resolveExecutionObservation(ctx, tx, access, target.WorkspaceID)
	})
}

func executionObservationConflict(field string) error {
	return store.NewErrConflict("execution_observation", field, nil)
}

// Called only after the ordinary candidate mutation access locks. The grant row
// serializes host sequence decisions with projection and control updates.
func resolveExecutionObservation(ctx context.Context, tx *sqlxTxWrapper, access store.ExamAttemptWorkspaceMutationAccess, workspaceID model.ExamAttemptWorkspaceID) (*store.ExecutionObservationTarget, error) {
	event := access.SourceObservation
	if event == nil || event.Validate() != nil || event.Fence.GrantID != access.SourceGrantID {
		return nil, store.NewErrInvalidInput("execution_observation", "event", nil)
	}
	var row executionGrantRow
	if err := tx.Get(ctx, &row, `SELECT `+executionGrantColumns+` FROM execution_grants WHERE id=$1 FOR UPDATE`, access.SourceGrantID.String()); err != nil {
		return nil, err
	}
	grant, err := executionGrantModel(row)
	if err != nil {
		return nil, err
	}
	authority, err := readExecutionControlAuthority(ctx, tx, access.AttemptID)
	if err != nil {
		return nil, err
	}
	if grant.AttemptID != access.AttemptID || grant.State != model.ExecutionGrantReady || grant.Fence() != event.Fence || grant.DesiredControlState != model.ExecutionControlRunning || grant.ControlAcknowledgedRevision != grant.ControlRevision || authority.desired(grant) != model.ExecutionControlRunning || authority.digest() != grant.ControlAuthorityDigest {
		return nil, executionObservationConflict("fence")
	}
	body, _ := json.Marshal(event)
	digest := sha256.Sum256(body)
	var retained struct {
		Digest  []byte `db:"observation_digest"`
		Outcome []byte `db:"outcome_canonical"`
	}
	err = tx.Get(ctx, &retained, `SELECT observation_digest,outcome_canonical FROM execution_observation_outcomes WHERE execution_grant_id=$1 AND host_sequence=$2`, grant.ID.String(), event.HostSequence)
	if err == nil {
		if !bytes.Equal(retained.Digest, digest[:]) {
			return nil, executionObservationConflict("sequence_digest")
		}
		var saved executionObservationOutcome
		if json.Unmarshal(retained.Outcome, &saved) != nil {
			return nil, invalidPersistedState("execution_observation", "outcome", nil)
		}
		if saved.Ignored {
			return &store.ExecutionObservationTarget{Ignored: true, Processed: true}, nil
		}
		if validateAttemptWorkspaceMutationOutcome(saved.Workspace) != nil {
			return nil, invalidPersistedState("execution_observation", "outcome", nil)
		}
		result, err := attemptWorkspaceMutationResult(saved.Workspace)
		if err != nil {
			return nil, err
		}
		result.Replayed = true
		return &store.ExecutionObservationTarget{Processed: true, EntryID: result.Change.EntryID, ExpectedContentVersion: saved.ExpectedVersion, WorkspaceCursor: result.Change.Cursor, Outcome: result}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if event.HostSequence != grant.ProcessedHostSequence+1 || event.HostSequence > 65536 {
		return nil, executionObservationConflict("sequence_gap")
	}
	if event.BasedOnWorkspaceCursor > grant.AppliedWorkspaceCursor {
		return nil, executionObservationConflict("baseline")
	}
	// Projection effects do not emit observations on the implemented host. An
	// unsolicited claimed echo must never suppress an otherwise real guest edit.
	if event.OriginProjectionMutationID != "" {
		return nil, executionObservationConflict("unknown_projection_echo")
	}
	ignoredPath, ignoredDestination := ignoredObservedExecutionPath(event.Path), ignoredObservedExecutionPath(event.DestinationPath)
	if event.Operation == model.AttemptWorkspaceMutationMoveEntry && ignoredPath != ignoredDestination {
		return nil, executionObservationConflict("unsupported_boundary")
	}
	if ignoredPath {
		return &store.ExecutionObservationTarget{Ignored: true}, nil
	}
	var cursor int64
	if err := tx.Get(ctx, &cursor, `SELECT cursor FROM exam_attempt_workspaces WHERE id=$1`, workspaceID.String()); err != nil {
		return nil, err
	}
	if cursor-event.BasedOnWorkspaceCursor > model.AttemptWorkspaceJournalRetention {
		return nil, executionObservationConflict("journal_gap")
	}
	var changes []struct {
		Cursor int64          `db:"cursor"`
		Old    sql.NullString `db:"old_path"`
		New    sql.NullString `db:"new_path"`
		Source sql.NullString `db:"source_grant_id"`
	}
	if err := tx.Select(ctx, &changes, `SELECT cursor,old_path,new_path,source_grant_id FROM exam_attempt_workspace_journal WHERE workspace_id=$1 AND cursor>$2 ORDER BY cursor`, workspaceID.String(), event.BasedOnWorkspaceCursor); err != nil {
		return nil, err
	}
	if int64(len(changes)) != cursor-event.BasedOnWorkspaceCursor {
		return nil, executionObservationConflict("journal_gap")
	}
	for i, change := range changes {
		if change.Cursor != event.BasedOnWorkspaceCursor+int64(i)+1 {
			return nil, executionObservationConflict("journal_gap")
		}
		if change.Source.String == grant.ID.String() {
			continue
		}
		for _, path := range []string{change.Old.String, change.New.String} {
			if executionPathsOverlap(path, event.Path) || executionPathsOverlap(path, event.DestinationPath) {
				return nil, executionObservationConflict("competing_workspace_change")
			}
		}
	}
	var node executionObservedNodeRow
	err = tx.Get(ctx, &node, `SELECT entry_id,kind,path,expected_content_version,resulting_content_version,workspace_cursor,deleted FROM execution_observed_nodes WHERE execution_grant_id=$1 AND node_identity=$2`, grant.ID.String(), event.NodeIdentity)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	create := event.Operation == model.AttemptWorkspaceMutationCreateFile || event.Operation == model.AttemptWorkspaceMutationCreateDirectory
	if create {
		if known {
			return nil, executionObservationConflict("reused_node_identity")
		}
		return &store.ExecutionObservationTarget{WorkspaceCursor: cursor}, nil
	}
	if known && (node.Deleted || node.Kind != string(event.Kind) || node.Path != event.Path) {
		return nil, executionObservationConflict("node_identity")
	}
	var current attemptWorkspaceEntryMutationRow
	query := attemptWorkspaceEntryMutationSelect + ` WHERE e.workspace_id=$1 AND e.path=$2`
	if err := tx.Get(ctx, &current, query, workspaceID.String(), event.Path); err != nil {
		return nil, translateError("execution_observation", event.NodeIdentity, err)
	}
	if current.Kind != string(event.Kind) || (known && current.ID != node.EntryID) {
		return nil, executionObservationConflict("node_identity")
	}
	id, err := model.ParseAttemptWorkspaceEntryID(current.ID)
	if err != nil {
		return nil, invalidPersistedState("execution_observation", "entry_id", err)
	}
	expected := event.ExpectedContentVersion
	if event.Kind == model.StarterWorkspaceEntryFile {
		if known && node.Cursor > event.BasedOnWorkspaceCursor && node.Expected.String == event.ExpectedContentVersion.String() {
			// This exact node's prior durable guest outcome extends the same original
			// projected baseline. It is not a substitution from the latest manifest.
			expected, err = model.ParseWorkspaceContentVersion(node.Resulting.String)
			if err != nil {
				return nil, invalidPersistedState("execution_observation", "resulting_version", err)
			}
		}
		version, err := current.workspaceContentVersion()
		if err != nil {
			return nil, err
		}
		if !expected.IsValid() || version != expected {
			return nil, executionObservationConflict("content_version")
		}
	}
	return &store.ExecutionObservationTarget{EntryID: id, ExpectedContentVersion: expected, WorkspaceCursor: cursor}, nil
}

func executionPathsOverlap(a, b string) bool {
	return a != "" && b != "" && (a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/"))
}
func ignoredObservedExecutionPath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		switch part {
		case ".proctor", ".git", "node_modules", "target", "__pycache__":
			return true
		}
	}
	return false
}

func checkExecutionObservationMutation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, target *store.ExecutionObservationTarget) error {
	e := input.Access.SourceObservation
	if e == nil {
		return nil
	}
	if target.Ignored {
		return executionObservationConflict("ignored_workspace_mutation")
	}
	if input.Operation != e.Operation || input.Recursive != (e.Recursive != nil && *e.Recursive) {
		return executionObservationConflict("mutation")
	}
	switch input.Operation {
	case model.AttemptWorkspaceMutationCreateFile, model.AttemptWorkspaceMutationCreateDirectory:
		if input.DestinationPath != e.Path {
			return executionObservationConflict("path")
		}
	case model.AttemptWorkspaceMutationMoveEntry:
		if input.EntryID != target.EntryID || input.ExpectedPath != e.Path || input.DestinationPath != e.DestinationPath {
			return executionObservationConflict("path")
		}
	default:
		if input.EntryID != target.EntryID || input.ExpectedPath != e.Path || input.ExpectedContentVersion != target.ExpectedContentVersion {
			return executionObservationConflict("baseline")
		}
	}
	if e.Content != nil {
		var content struct {
			Size int64  `db:"size_bytes"`
			SHA  string `db:"sha256"`
		}
		if err := tx.Get(ctx, &content, `SELECT size_bytes,sha256 FROM exam_attempt_workspace_objects WHERE id=$1`, input.ObjectID.String()); err != nil {
			return err
		}
		if content.Size != e.Content.Size || content.SHA != e.Content.SHA256 {
			return executionObservationConflict("captured_content")
		}
	}
	if input.Recursive && (input.ExpectedWorkspaceCursor == nil || *input.ExpectedWorkspaceCursor != target.WorkspaceCursor) {
		return executionObservationConflict("subtree_cursor")
	}
	return nil
}

func recordExecutionObservation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, outcome attemptWorkspaceMutationOutcomeV1, expectedVersion model.WorkspaceContentVersion) error {
	event := input.Access.SourceObservation
	if event == nil {
		return nil
	}
	body, _ := json.Marshal(event)
	digest := sha256.Sum256(body)
	outcome.ProtectedObjectIDs = nil
	encoded, err := json.Marshal(executionObservationOutcome{Workspace: outcome, ExpectedVersion: expectedVersion})
	if err != nil {
		return err
	}
	if len(encoded) > 8192 {
		return store.NewErrInvalidInput("execution_observation", "outcome_size", nil)
	}
	grant := event.Fence.GrantID.String()
	if _, err := tx.Exec(ctx, `INSERT INTO execution_observation_outcomes(execution_grant_id,host_sequence,observation_digest,outcome_canonical) VALUES($1,$2,$3,$4)`, grant, event.HostSequence, digest[:], encoded); err != nil {
		return err
	}
	path := event.Path
	if event.Operation == model.AttemptWorkspaceMutationMoveEntry {
		path = event.DestinationPath
	}
	if _, err := tx.Exec(ctx, `INSERT INTO execution_observed_nodes(execution_grant_id,node_identity,entry_id,kind,path,expected_content_version,resulting_content_version,workspace_cursor,deleted)
 VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9)
 ON CONFLICT(execution_grant_id,node_identity) DO UPDATE SET path=excluded.path,expected_content_version=excluded.expected_content_version,resulting_content_version=excluded.resulting_content_version,workspace_cursor=excluded.workspace_cursor,deleted=excluded.deleted`, grant, event.NodeIdentity, outcome.Change.EntryID.String(), string(event.Kind), path, event.ExpectedContentVersion.String(), outcome.Change.ContentVersion.String(), outcome.Change.Cursor, event.Operation == model.AttemptWorkspaceMutationDeleteEntry); err != nil {
		return err
	}
	if event.Kind == model.StarterWorkspaceEntryDirectory {
		if event.Operation == model.AttemptWorkspaceMutationMoveEntry {
			if _, err := tx.Exec(ctx, `UPDATE execution_observed_nodes SET path=$3||substring(path FROM length($2)+1) WHERE execution_grant_id=$1 AND path LIKE $4 ESCAPE '!'`, grant, event.Path, event.DestinationPath, escapeLike(event.Path)+"/%"); err != nil {
				return err
			}
		} else if event.Operation == model.AttemptWorkspaceMutationDeleteEntry && input.Recursive {
			if _, err := tx.Exec(ctx, `UPDATE execution_observed_nodes SET deleted=true WHERE execution_grant_id=$1 AND path LIKE $2 ESCAPE '!'`, grant, escapeLike(event.Path)+"/%"); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE execution_grants SET processed_host_sequence=$2,updated_at=GREATEST(updated_at,statement_timestamp()),revision=revision+1 WHERE id=$1`, grant, event.HostSequence)
	return err
}

func (s *SQLExamAttemptWorkspaceStore) RecordIgnoredObservation(ctx context.Context, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExecutionObservationTarget, error) {
	if access.SourceObservation == nil || !validAttemptWorkspaceMutationAccess(access) {
		return nil, store.NewErrInvalidInput("execution_observation", "access", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "record ignored execution observation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExecutionObservationTarget, error) {
		workspace, _, err := lockAttemptWorkspaceMutationTarget(ctx, tx, access)
		if err != nil {
			return nil, err
		}
		target, err := resolveExecutionObservation(ctx, tx, access, workspace.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if !target.Ignored {
			return nil, executionObservationConflict("not_ignored")
		}
		if target.Processed {
			return target, nil
		}
		event := access.SourceObservation
		body, _ := json.Marshal(event)
		digest := sha256.Sum256(body)
		encoded, err := json.Marshal(executionObservationOutcome{Ignored: true})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO execution_observation_outcomes(execution_grant_id,host_sequence,observation_digest,outcome_canonical) VALUES($1,$2,$3,$4)`, access.SourceGrantID.String(), event.HostSequence, digest[:], encoded); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE execution_grants SET processed_host_sequence=$2,updated_at=GREATEST(updated_at,statement_timestamp()),revision=revision+1 WHERE id=$1`, access.SourceGrantID.String(), event.HostSequence); err != nil {
			return nil, err
		}
		target.Processed = true
		return target, nil
	})
}
