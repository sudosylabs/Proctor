// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ProjectionStatus is the acknowledged projection at the point of attachment.
type ProjectionStatus struct {
	EnvironmentEpoch       string
	AppliedWorkspaceCursor int64
	State                  store.ExecutionProjectionState
}

const PendingRevocationPageSize = 100
const CurrentReconciliationPageSize = 100
const SittingGrantPageSize = 200

type Request struct {
	ExpectedWorkspaceCursor int64
	AttemptID               model.ExamAttemptID
	Image                   string
	Network                 Network
}

type Placement struct {
	Projection ProjectionStatus
	GrantID    model.ExecutionGrantID
	AttemptID  model.ExamAttemptID
	HostID     string
	Image      string
	Network    Network
	Ready      bool
	Revision   int64
}

type ImageOption struct {
	ID       string
	Networks []Network
}

type Service struct {
	grants  store.ExecutionGrantStore
	hosts   HostDirectory
	content Content
	now     func() time.Time
	newID   func() model.ExecutionGrantID

	reconciliationMu    sync.Mutex
	reconciliationAfter model.ExecutionGrantID
	revocationAfter     model.ExecutionGrantID
}

func New(grants store.ExecutionGrantStore, hosts HostDirectory, content Content, now func() time.Time,
	newID func() model.ExecutionGrantID,
) (*Service, error) {
	if grants == nil || hosts == nil || content == nil || now == nil || newID == nil {
		return nil, errors.New("execution: dependencies are required")
	}
	return &Service{grants: grants, hosts: hosts, content: content, now: now, newID: newID}, nil
}

// Ensure persists placement before occupying the host and makes the current
// authoritative PostgreSQL/VFS tree exact before reporting readiness.
// Capacity or availability failures reassign deterministically.
func (s *Service) Ensure(ctx context.Context, request Request) (*Placement, error) {
	if !request.AttemptID.IsValid() || request.Image == "" || request.ExpectedWorkspaceCursor < 0 || request.ExpectedWorkspaceCursor > (1<<53)-1 ||
		(request.Network != NetworkNone && request.Network != NetworkAllowlist) {
		return nil, ErrInvalid
	}
	if request.ExpectedWorkspaceCursor > 0 {
		snapshot, err := s.grants.WorkspaceSnapshot(ctx, request.AttemptID)
		if err != nil {
			return nil, err
		}
		if snapshot == nil || request.ExpectedWorkspaceCursor > snapshot.Cursor {
			return nil, ErrInvalid
		}
	}
	catalog, err := s.hosts.Catalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("execution catalog: %w", err)
	}
	current, err := s.grants.Current(ctx, request.AttemptID)
	if err != nil && !store.IsNotFound(err) {
		return nil, fmt.Errorf("read execution placement: %w", err)
	}
	currentHostID := ""
	if current != nil {
		if current.Image != request.Image || Network(current.Network) != request.Network {
			return nil, ErrConflict
		}
		currentHostID = current.HostID
	}
	candidates := suitableHosts(catalog, request.Image, request.Network, currentHostID)
	if len(candidates) == 0 {
		return nil, ErrUnavailable
	}
	if current != nil && current.EnvironmentEpoch != "" {
		resumed, err := s.resumeProjection(ctx, current.ID)
		if err != nil {
			if resumed != nil && errors.Is(err, ErrProjectionPending) {
				return placement(resumed), err
			}
			return nil, err
		}
		return placement(resumed), nil
	}
	if current != nil && current.State == model.ExecutionGrantReady {
		// A ready grant may still have guest processes or an observation. The
		// host cannot atomically replace its tree and install a fresh watch.
		if err := s.retireForReopen(ctx, current); err != nil {
			return nil, err
		}
		current = nil
	}
	if current != nil {
		candidates = preferCurrent(candidates, current.HostID)
	}

	for _, candidate := range candidates {
		var previous *model.ExecutionGrant
		if current == nil {
			current, err = s.grants.Reserve(ctx, store.ExecutionGrantReservation{
				ID: s.newID(), AttemptID: request.AttemptID, HostID: candidate.ID,
				Image: request.Image, Network: model.ExecutionNetwork(request.Network), At: s.now(),
			})
		} else if current.HostID != candidate.ID {
			var changed *store.ExecutionGrantReassignmentResult
			changed, err = s.grants.Reassign(ctx, store.ExecutionGrantReassignment{
				CurrentID: current.ID, CurrentRevision: current.Revision,
				Replacement: store.ExecutionGrantReservation{ID: s.newID(), AttemptID: request.AttemptID,
					HostID: candidate.ID, Image: request.Image, Network: model.ExecutionNetwork(request.Network), At: s.now()},
			})
			if changed != nil {
				previous, current = changed.Previous, changed.Current
			}
		}
		if err != nil {
			return nil, fmt.Errorf("persist execution placement: %w", err)
		}
		if previous != nil {
			s.revokeReleased(ctx, previous)
		}

		var hostErr error
		current, hostErr = s.prepareEnvironment(ctx, current)
		if hostErr != nil {
			if errors.Is(hostErr, ErrConflict) || (!errors.Is(hostErr, ErrUnavailable) && !errors.Is(hostErr, ErrCapacity) && !errors.Is(hostErr, ErrRevoked)) {
				return nil, fmt.Errorf("prepare execution environment: %w", hostErr)
			}
			continue
		}
		return placement(current), nil
	}
	return nil, ErrUnavailable
}

func (s *Service) retireForReopen(ctx context.Context, grant *model.ExecutionGrant) (resultErr error) {
	lease, err := s.grants.AcquireLifecycleLease(ctx, grant.ID)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, releaseLifecycleLease(ctx, lease)) }()
	current, err := s.grants.Current(ctx, grant.AttemptID)
	if err != nil || current.ID != grant.ID || current.Revision != grant.Revision {
		return errors.Join(ErrConflict, err)
	}
	return s.releaseGrant(ctx, current)
}

// prepareEnvironment holds the same cross-node lease used by incremental and
// Sitting effects, including while the first authoritative tree is captured.
func (s *Service) prepareEnvironment(ctx context.Context, grant *model.ExecutionGrant) (current *model.ExecutionGrant, resultErr error) {
	current = grant
	lease, err := s.grants.AcquireLifecycleLease(ctx, grant.ID)
	if err != nil {
		return current, err
	}
	defer func() { resultErr = errors.Join(resultErr, releaseLifecycleLease(ctx, lease)) }()
	current, err = s.grants.Current(ctx, grant.AttemptID)
	if err != nil || current.ID != grant.ID {
		return grant, errors.Join(ErrConflict, err)
	}
	if current.State != model.ExecutionGrantReserved {
		// Another initializer won while this caller waited for the lease.
		return current, ErrConflict
	}
	if current.LifecyclePending || current.WorkspacePending {
		return current, s.projectionFailed(ctx, current, ErrConflict)
	}
	// Ensure may refuse capacity before any tree effect was prepared, allowing
	// the caller to reassign this still-reserved placement.
	environment, err := s.hosts.Ensure(ctx, current.HostID, Spec{ID: current.ID.String(), Image: current.Image, Network: Network(current.Network)})
	if err != nil {
		return current, err
	}
	if controlled, ok := environment.(ControlledEnvironment); ok {
		current, err = s.controlEnvironment(ctx, current, controlled, lease)
		if err != nil || current.DesiredControlState != model.ExecutionControlRunning {
			return current, s.projectionFailed(ctx, current, errors.Join(ErrUnavailable, err))
		}
	}
	if projection, ok := environment.(ProjectionEnvironment); ok {
		return s.projectEnvironment(ctx, current, projection, lease)
	}
	snapshot, err := s.grants.WorkspaceSnapshot(ctx, current.AttemptID)
	if err != nil {
		return current, s.projectionFailed(ctx, current, err)
	}
	tree, err := s.treeFromSnapshot(ctx, snapshot)
	if err != nil {
		return current, s.projectionFailed(ctx, current, err)
	}
	if err = lease.Validate(ctx); err != nil {
		return current, s.projectionFailed(ctx, current, err)
	}
	prepared, err := s.grants.PrepareWorkspaceEffect(ctx, current.ID, current.Revision, snapshot.Cursor, s.now())
	if err != nil {
		return current, s.projectionFailed(ctx, current, err)
	}
	if err = environment.ReplaceTree(ctx, tree); err != nil {
		return prepared, s.projectionFailed(ctx, prepared, err)
	}
	if err = lease.Validate(ctx); err != nil {
		return prepared, s.projectionFailed(ctx, prepared, err)
	}
	current, err = s.grants.MarkWorkspaceApplied(ctx, prepared.ID, prepared.Revision, snapshot.Cursor, s.now())
	if err != nil {
		return prepared, s.projectionFailed(ctx, prepared, err)
	}
	return current, nil
}

func releaseLifecycleLease(ctx context.Context, lease store.ExecutionLifecycleLease) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return lease.Release(cleanup)
}

func (s *Service) projectionFailed(ctx context.Context, grant *model.ExecutionGrant, cause error) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	// A pending marker survives even if persistence is unavailable here.
	// Reconciliation retires that exact grant after the lease is released.
	return errors.Join(ErrConflict, cause, s.releaseGrant(cleanup, grant))
}

// Images returns the safe installation catalog projection. Host identities,
// addresses, live capacity, and release details remain operator-only.
func (s *Service) Images(ctx context.Context) ([]ImageOption, error) {
	catalog, err := s.hosts.Catalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("execution catalog: %w", err)
	}
	images := make(map[string]map[Network]struct{})
	for _, host := range catalog {
		if !host.Usable || !host.Isolated {
			continue
		}
		for _, image := range host.Images {
			if images[image] == nil {
				images[image] = make(map[Network]struct{})
			}
			for _, network := range host.Networks {
				images[image][network] = struct{}{}
			}
		}
	}
	ids := make([]string, 0, len(images))
	for id := range images {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ImageOption, 0, len(ids))
	for _, id := range ids {
		networks := make([]Network, 0, len(images[id]))
		for _, network := range []Network{NetworkNone, NetworkAllowlist} {
			if _, exists := images[id][network]; exists {
				networks = append(networks, network)
			}
		}
		result = append(result, ImageOption{ID: id, Networks: networks})
	}
	return result, nil
}

func (s *Service) treeFromSnapshot(ctx context.Context, snapshot *store.ExecutionWorkspaceSnapshot) (Tree, error) {
	if snapshot == nil || snapshot.Cursor < 0 || len(snapshot.Nodes) > model.AttemptWorkspaceMaximumEntries {
		return nil, ErrInvalid
	}
	tree := make(Tree, 0, len(snapshot.Nodes))
	var total int64
	for _, node := range snapshot.Nodes {
		projected, err := s.projectNode(ctx, node)
		if err != nil {
			return nil, err
		}
		total += int64(len(projected.Data))
		if total > model.AttemptWorkspaceMaximumTotalBytes {
			return nil, ErrInvalid
		}
		tree = append(tree, projected)
	}
	return tree, nil
}

func (s *Service) projectNode(ctx context.Context, node store.ExecutionWorkspaceNode) (Node, error) {
	if normalized, err := model.NormalizeAttemptWorkspacePath(node.Path); err != nil || normalized != node.Path {
		return Node{}, ErrInvalid
	}
	if node.Kind == model.StarterWorkspaceEntryDirectory {
		return Node{Path: node.Path, Kind: NodeDirectory}, nil
	}
	if node.Kind != model.StarterWorkspaceEntryFile || !node.ContentVersion.IsValid() || node.SizeBytes < 0 || node.SizeBytes > model.AttemptWorkspaceMaximumFileBytes {
		return Node{}, ErrInvalid
	}
	var body io.ReadCloser
	var err error
	switch node.StorageOrigin {
	case model.AttemptWorkspaceStorageStarter:
		body, err = s.content.OpenStarterWorkspaceObject(ctx, node.StarterObjectID)
	case model.AttemptWorkspaceStorageAttempt:
		body, err = s.content.OpenAttemptWorkspaceObject(ctx, node.AttemptObjectID)
	default:
		return Node{}, ErrInvalid
	}
	if err != nil {
		return Node{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(body, model.AttemptWorkspaceMaximumFileBytes+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return Node{}, errors.Join(readErr, closeErr)
	}
	if int64(len(data)) != node.SizeBytes {
		return Node{}, ErrInvalid
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != node.SHA256 {
		return Node{}, ErrInvalid
	}
	return Node{Path: node.Path, Kind: NodeFile, Version: node.ContentVersion.String(), Data: data}, nil
}

// Release first makes the placement inactive durably, then converges host
// cleanup. A cleanup failure leaves a bounded reconciliation record.
func (s *Service) Release(ctx context.Context, attemptID model.ExamAttemptID) error {
	grant, err := s.grants.Release(ctx, attemptID, s.now())
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release execution placement: %w", err)
	}
	if err := s.revokeReleased(ctx, grant); err != nil {
		return fmt.Errorf("revoke released execution environment: %w", err)
	}
	return nil
}

// ReleaseGrant releases only the named placement. Connection-scoped cleanup
// uses this fence so it can never revoke a successor that another node placed
// for the same Attempt.
func (s *Service) ReleaseGrant(ctx context.Context, grantID model.ExecutionGrantID) error {
	grant, err := s.grants.ReleaseGrant(ctx, grantID, s.now())
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release exact execution placement: %w", err)
	}
	if err := s.revokeReleased(ctx, grant); err != nil {
		return fmt.Errorf("revoke exact released execution environment: %w", err)
	}
	return nil
}

// SyncChange applies one acknowledged candidate change without resetting the
// guest observation stream. A missing, uncertain or out-of-order effect retires
// the grant; a later authorized open reconstructs it from durable state.
func (s *Service) SyncChange(ctx context.Context, attemptID model.ExamAttemptID, change model.AttemptWorkspaceJournalEntry) error {
	return s.syncChange(ctx, attemptID, change, "")
}

// AcknowledgeChange advances projection progress for a harvested guest change
// without echoing that change through Apply.
func (s *Service) AcknowledgeChange(ctx context.Context, attemptID model.ExamAttemptID, sourceGrantID model.ExecutionGrantID, change model.AttemptWorkspaceJournalEntry) error {
	if !sourceGrantID.IsValid() {
		return ErrInvalid
	}
	return s.syncChange(ctx, attemptID, change, sourceGrantID)
}

func (s *Service) syncChange(ctx context.Context, attemptID model.ExamAttemptID, change model.AttemptWorkspaceJournalEntry, sourceGrantID model.ExecutionGrantID) (resultErr error) {
	if !attemptID.IsValid() || change.Validate() != nil {
		return ErrInvalid
	}
	grant, err := s.grants.Current(ctx, attemptID)
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read execution placement: %w", err)
	}
	alreadyApplied := sourceGrantID.IsValid()
	if alreadyApplied && grant.ID != sourceGrantID {
		return nil
	}
	if grant.EnvironmentEpoch != "" {
		_, err := s.resumeProjection(ctx, grant.ID)
		return err
	}
	lease, err := s.grants.AcquireLifecycleLease(ctx, grant.ID)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, releaseLifecycleLease(ctx, lease)) }()
	current, err := s.grants.Current(ctx, attemptID)
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// A successor initializes from current durable state.
	if current.ID != grant.ID {
		return nil
	}
	grant = current
	if grant.LifecyclePending || grant.WorkspacePending {
		return s.projectionFailed(ctx, grant, ErrConflict)
	}
	if change.Cursor <= grant.AppliedWorkspaceCursor {
		return nil
	}
	if grant.State != model.ExecutionGrantReady || change.Cursor != grant.AppliedWorkspaceCursor+1 {
		return s.projectionFailed(ctx, grant, ErrConflict)
	}
	var mutation Mutation
	if !alreadyApplied {
		mutation, err = s.mutationForChange(ctx, attemptID, change)
		if err != nil {
			return s.projectionFailed(ctx, grant, err)
		}
	}
	if err = lease.Validate(ctx); err != nil {
		return s.projectionFailed(ctx, grant, err)
	}
	prepared, err := s.grants.PrepareWorkspaceEffect(ctx, grant.ID, grant.Revision, change.Cursor, s.now())
	if err != nil {
		return s.projectionFailed(ctx, grant, err)
	}
	if !alreadyApplied {
		environment, openErr := s.openGrant(ctx, prepared)
		if openErr != nil {
			return s.projectionFailed(ctx, prepared, openErr)
		}
		if err = environment.Apply(ctx, []Mutation{mutation}); err != nil {
			return s.projectionFailed(ctx, prepared, err)
		}
	}
	if err = lease.Validate(ctx); err != nil {
		return s.projectionFailed(ctx, prepared, err)
	}
	if _, err = s.grants.MarkWorkspaceApplied(ctx, prepared.ID, prepared.Revision, change.Cursor, s.now()); err != nil {
		return s.projectionFailed(ctx, prepared, err)
	}
	return nil
}

func (s *Service) mutationForChange(ctx context.Context, attemptID model.ExamAttemptID, change model.AttemptWorkspaceJournalEntry) (Mutation, error) {
	mutation := Mutation{Kind: NodeKind(0)}
	switch change.Operation {
	case model.AttemptWorkspaceMutationCreateDirectory:
		mutation.Operation, mutation.Path, mutation.Kind = OperationCreate, change.NewPath, NodeDirectory
	case model.AttemptWorkspaceMutationCreateFile, model.AttemptWorkspaceMutationReplaceFile:
		snapshot, treeErr := s.grants.WorkspaceSnapshot(ctx, attemptID)
		if treeErr != nil {
			return Mutation{}, treeErr
		}
		var found bool
		if snapshot == nil || snapshot.Cursor != change.Cursor || len(snapshot.Nodes) > model.AttemptWorkspaceMaximumEntries {
			return Mutation{}, ErrConflict
		}
		for _, stored := range snapshot.Nodes {
			if stored.Path != change.NewPath {
				continue
			}
			node, err := s.projectNode(ctx, stored)
			if err != nil {
				return Mutation{}, err
			}
			if node.Path == change.NewPath && node.Kind == NodeFile && node.Version == change.ContentVersion.String() {
				mutation.Path, mutation.Kind, mutation.Version, mutation.Data = node.Path, NodeFile, node.Version, node.Data
				found = true
				break
			}
		}
		if !found {
			return Mutation{}, ErrConflict
		}
		if change.Operation == model.AttemptWorkspaceMutationCreateFile {
			mutation.Operation = OperationCreate
		} else {
			mutation.Operation = OperationReplace
		}
	case model.AttemptWorkspaceMutationMoveEntry:
		if change.EntryKind == model.StarterWorkspaceEntryDirectory {
			return Mutation{}, ErrConflict
		}
		mutation.Operation, mutation.From, mutation.Path = OperationMove, change.OldPath, change.NewPath
		mutation.Kind, mutation.Version = NodeFile, change.ContentVersion.String()
	case model.AttemptWorkspaceMutationDeleteEntry:
		if change.Recursive {
			return Mutation{}, ErrConflict
		}
		mutation.Operation, mutation.Path = OperationDelete, change.OldPath
		if change.EntryKind == model.StarterWorkspaceEntryDirectory {
			mutation.Kind = NodeDirectory
		} else {
			mutation.Kind = NodeFile
		}
	default:
		return Mutation{}, ErrInvalid
	}
	return mutation, nil
}

func (s *Service) Reconcile(ctx context.Context) (int, error) {
	current, err := s.nextReconciliationPage(ctx)
	if err != nil {
		return 0, fmt.Errorf("list current execution grants for reconciliation: %w", err)
	}
	completed := 0
	var joined error
	for _, convergence := range current {
		if err := s.convergeCurrent(ctx, convergence); err != nil {
			joined = errors.Join(joined, fmt.Errorf("grant %s: %w", convergence.Grant.ID, err))
			continue
		}
		completed++
	}

	pending, err := s.nextRevocationPage(ctx)
	if err != nil {
		return completed, errors.Join(joined, fmt.Errorf("list pending execution revocations: %w", err))
	}
	for _, grant := range pending {
		if err := s.revokeReleased(ctx, grant); err != nil {
			joined = errors.Join(joined, fmt.Errorf("grant %s: %w", grant.ID, err))
			continue
		}
		completed++
	}
	return completed, joined
}

func (s *Service) nextRevocationPage(ctx context.Context) ([]*model.ExecutionGrant, error) {
	s.reconciliationMu.Lock()
	defer s.reconciliationMu.Unlock()
	page, err := s.grants.ListPendingRevocations(ctx, s.revocationAfter, PendingRevocationPageSize)
	if err != nil {
		return nil, err
	}
	if len(page) < PendingRevocationPageSize {
		s.revocationAfter = ""
	} else {
		s.revocationAfter = page[len(page)-1].ID
	}
	return page, nil
}

func (s *Service) nextReconciliationPage(ctx context.Context) ([]store.ExecutionGrantConvergence, error) {
	s.reconciliationMu.Lock()
	defer s.reconciliationMu.Unlock()
	page, err := s.grants.ListCurrentForReconciliation(ctx, s.reconciliationAfter, CurrentReconciliationPageSize)
	if err != nil {
		return nil, err
	}
	if len(page) == 0 || len(page) < CurrentReconciliationPageSize {
		s.reconciliationAfter = model.ExecutionGrantID("")
	} else {
		s.reconciliationAfter = page[len(page)-1].Grant.ID
	}
	return page, nil
}

func (s *Service) convergeCurrent(ctx context.Context, convergence store.ExecutionGrantConvergence) error {
	grant := convergence.Grant
	if grant == nil || grant.Validate() != nil {
		return ErrInvalid
	}
	return s.convergeGrant(ctx, grant.ID)
}

func (s *Service) Attach(ctx context.Context, attemptID model.ExamAttemptID, grantID model.ExecutionGrantID, window Window) (Terminal, error) {
	var terminal Terminal
	err := s.withOpenEnvironment(ctx, attemptID, grantID, func(environment Environment) error {
		var err error
		terminal, err = environment.Attach(ctx, window)
		return err
	})
	if err != nil && terminal != nil {
		_ = terminal.Close()
		terminal = nil
	}
	return terminal, err
}

func (s *Service) Watch(ctx context.Context, attemptID model.ExamAttemptID, grantID model.ExecutionGrantID, after Cursor) (Observation, error) {
	if !attemptID.IsValid() || !grantID.IsValid() {
		return nil, ErrInvalid
	}
	current, err := s.grants.Current(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if current.ID != grantID {
		return nil, ErrUnavailable
	}
	if current.EnvironmentEpoch != "" {
		if after != "" {
			return nil, ErrInvalid
		}
		return s.observeProjection(ctx, current)
	}
	var observation Observation
	err = s.withOpenEnvironment(ctx, attemptID, grantID, func(environment Environment) error {
		var err error
		observation, err = environment.Watch(ctx, after)
		return err
	})
	if err != nil && observation != nil {
		_ = observation.Close()
		observation = nil
	}
	return observation, err
}

func (s *Service) OpenFile(ctx context.Context, attemptID model.ExamAttemptID, grantID model.ExecutionGrantID, path string) (io.ReadCloser, error) {
	var body io.ReadCloser
	err := s.withOpenEnvironment(ctx, attemptID, grantID, func(environment Environment) error {
		var err error
		body, err = environment.Open(ctx, path)
		return err
	})
	if err != nil && body != nil {
		_ = body.Close()
		body = nil
	}
	return body, err
}

// The lease covers acquisition, not the lifetime of a PTY, observation, or body.
// Streams keep their original host handle and can never recreate a lost guest.
func (s *Service) withOpenEnvironment(ctx context.Context, attemptID model.ExamAttemptID, grantID model.ExecutionGrantID, operation func(Environment) error) (resultErr error) {
	if !attemptID.IsValid() || !grantID.IsValid() {
		return ErrInvalid
	}
	lease, err := s.grants.AcquireLifecycleLease(ctx, grantID)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, releaseLifecycleLease(ctx, lease)) }()
	environment, grant, err := s.open(ctx, attemptID, grantID)
	if err != nil {
		return err
	}
	if err := lease.Validate(ctx); err != nil {
		return s.projectionFailed(ctx, grant, err)
	}
	if err := operation(environment); err != nil {
		return err
	}
	if err := lease.Validate(ctx); err != nil {
		return s.projectionFailed(ctx, grant, err)
	}
	// Release can fence a grant while an interaction is in flight. Do not hand
	// an acquired resource to the caller after that durable fence has committed.
	_, _, err = s.open(ctx, attemptID, grantID)
	return err
}

func (s *Service) FreezeSitting(ctx context.Context, sittingID model.ExamSittingID, sittingRevision int64) error {
	if sittingRevision < 1 {
		return ErrInvalid
	}
	return s.forSitting(ctx, sittingID, func(ctx context.Context, grant *model.ExecutionGrant) error {
		return s.convergeGrant(ctx, grant.ID)
	})
}

func (s *Service) ThawSitting(ctx context.Context, sittingID model.ExamSittingID, sittingRevision int64) error {
	if sittingRevision < 1 {
		return ErrInvalid
	}
	return s.forSitting(ctx, sittingID, func(ctx context.Context, grant *model.ExecutionGrant) error {
		return s.convergeGrant(ctx, grant.ID)
	})
}

func (s *Service) ReleaseSitting(ctx context.Context, sittingID model.ExamSittingID) error {
	return s.forSitting(ctx, sittingID, func(ctx context.Context, grant *model.ExecutionGrant) error {
		return s.convergeGrant(ctx, grant.ID)
	})
}

func (s *Service) convergeGrant(ctx context.Context, grantID model.ExecutionGrantID) (resultErr error) {
	lease, err := s.grants.AcquireLifecycleLease(ctx, grantID)
	if err != nil {
		return fmt.Errorf("acquire execution lifecycle lease: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, releaseLifecycleLease(ctx, lease)) }()
	for attempt := 0; attempt < 3; attempt++ {
		convergence, err := s.grants.CurrentForReconciliation(ctx, grantID)
		if store.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read leased execution lifecycle: %w", err)
		}
		grant := convergence.Grant
		if grant.EnvironmentEpoch != "" {
			environment, openErr := s.hosts.Existing(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
			controlled, supported := environment.(ControlledEnvironment)
			if openErr != nil || !supported || controlled.Epoch() != grant.EnvironmentEpoch {
				return errors.Join(ErrUnavailable, openErr, s.releaseGrant(ctx, grant))
			}
			current, controlErr := s.controlEnvironment(ctx, grant, controlled, lease)
			if controlErr != nil {
				return controlErr
			}
			if current.DesiredControlState == model.ExecutionControlRevoked {
				return s.releaseGrant(ctx, current)
			}
			if projection, supported := environment.(ProjectionEnvironment); supported && current.DesiredControlState == model.ExecutionControlRunning {
				_, err := s.projectEnvironment(ctx, current, projection, lease)
				return err
			}
			return nil
		}
		if grant.LifecyclePending || grant.WorkspacePending || grant.AppliedWorkspaceCursor != convergence.WorkspaceCursor {
			return s.releaseGrant(ctx, grant)
		}
		if convergence.AttemptState != model.ExamAttemptActive || convergence.AcknowledgementRequired || convergence.SecurityBlocked ||
			(convergence.SittingState != model.ExamSittingOpen && convergence.SittingState != model.ExamSittingPaused) ||
			grant.State != model.ExecutionGrantReady {
			return s.releaseGrant(ctx, grant)
		}
		if grant.AppliedSittingState == convergence.SittingState && grant.AppliedSittingRevision == convergence.SittingRevision {
			return nil
		}
		environment, err := s.openGrant(ctx, grant)
		if err != nil {
			return errors.Join(err, s.releaseGrant(ctx, grant))
		}
		if err := lease.Validate(ctx); err != nil {
			return errors.Join(err, s.releaseGrant(ctx, grant))
		}
		prepared, err := s.grants.PrepareSittingStateEffect(ctx, grant.ID, grant.Revision, convergence.SittingState, convergence.SittingRevision, s.now())
		if store.IsConflict(err) {
			continue
		}
		if err != nil {
			return errors.Join(err, s.releaseGrant(ctx, grant))
		}
		if convergence.SittingState == model.ExamSittingPaused {
			err = environment.Freeze(ctx)
		} else {
			err = environment.Thaw(ctx)
		}
		if err != nil {
			return errors.Join(err, s.releaseGrant(ctx, prepared))
		}
		if err := lease.Validate(ctx); err != nil {
			return errors.Join(err, s.releaseGrant(ctx, prepared))
		}
		_, err = s.grants.MarkSittingStateApplied(ctx, prepared.ID, prepared.Revision, convergence.SittingState, convergence.SittingRevision, s.now())
		if err == nil {
			return nil
		}
		if !store.IsConflict(err) {
			return errors.Join(err, s.releaseGrant(ctx, grant))
		}
	}
	convergence, err := s.grants.CurrentForReconciliation(ctx, grantID)
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil || convergence == nil || convergence.Grant == nil {
		return err
	}
	return errors.Join(ErrConflict, s.releaseGrant(ctx, convergence.Grant))
}

func (s *Service) openGrant(ctx context.Context, grant *model.ExecutionGrant) (Environment, error) {
	if grant == nil || grant.State != model.ExecutionGrantReady || grant.Validate() != nil {
		return nil, ErrUnavailable
	}
	return s.hosts.Existing(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
}

func (s *Service) releaseGrant(ctx context.Context, grant *model.ExecutionGrant) error {
	if grant == nil || !grant.ID.IsValid() {
		return ErrInvalid
	}
	released, err := s.grants.ReleaseGrant(ctx, grant.ID, s.now())
	if store.IsNotFound(err) {
		err = s.hosts.Revoke(ctx, grant.HostID, grant.ID.String())
		if errors.Is(err, ErrRevoked) {
			return nil
		}
		return err
	}
	if err != nil {
		return fmt.Errorf("release exact execution placement: %w", err)
	}
	if err := s.revokeReleased(ctx, released); err != nil {
		return fmt.Errorf("revoke exact released execution environment: %w", err)
	}
	return nil
}

func (s *Service) forSitting(ctx context.Context, sittingID model.ExamSittingID, operation func(context.Context, *model.ExecutionGrant) error) error {
	if !sittingID.IsValid() {
		return ErrInvalid
	}
	var after model.ExecutionGrantID
	var joined error
	for {
		grants, err := s.grants.ListCurrentForSitting(ctx, sittingID, after, SittingGrantPageSize)
		if err != nil {
			return errors.Join(joined, err)
		}
		for _, grant := range grants {
			joined = errors.Join(joined, operation(ctx, grant))
			after = grant.ID
		}
		if len(grants) < SittingGrantPageSize {
			return joined
		}
	}
}

func (s *Service) open(ctx context.Context, attemptID model.ExamAttemptID, grantID model.ExecutionGrantID) (Environment, *model.ExecutionGrant, error) {
	if !attemptID.IsValid() || !grantID.IsValid() {
		return nil, nil, ErrInvalid
	}
	grant, err := s.grants.Current(ctx, attemptID)
	if err != nil {
		return nil, nil, fmt.Errorf("read execution placement: %w", err)
	}
	if grant.ID != grantID || grant.State != model.ExecutionGrantReady {
		return nil, nil, ErrUnavailable
	}
	convergence, err := s.grants.CurrentForReconciliation(ctx, grant.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("read execution interaction fence: %w", err)
	}
	if convergence == nil || convergence.Grant == nil || convergence.Grant.ID != grant.ID ||
		convergence.AttemptState != model.ExamAttemptActive || convergence.SittingState != model.ExamSittingOpen ||
		convergence.AcknowledgementRequired || convergence.SecurityBlocked || convergence.Grant.LifecyclePending {
		return nil, nil, ErrInteractionBlocked
	}
	if convergence.Grant.WorkspacePending || convergence.Grant.AppliedWorkspaceCursor != convergence.WorkspaceCursor {
		if grant.EnvironmentEpoch != "" {
			return nil, nil, ErrProjectionPending
		}
		return nil, nil, ErrUnavailable
	}
	if grant.EnvironmentEpoch != "" {
		current, controlErr := s.grants.PrepareControl(ctx, grant.ID, grant.EnvironmentEpoch, s.now())
		if controlErr != nil {
			return nil, nil, controlErr
		}
		if current.DesiredControlState != model.ExecutionControlRunning || current.ControlAcknowledgedRevision != current.ControlRevision {
			return nil, nil, ErrInteractionBlocked
		}
		grant = current
	}
	environment, err := s.hosts.Existing(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
	if err != nil {
		return nil, nil, err
	}
	return environment, grant, nil
}

func (s *Service) revokeReleased(ctx context.Context, grant *model.ExecutionGrant) error {
	err := s.hosts.Revoke(ctx, grant.HostID, grant.ID.String())
	if err != nil && !errors.Is(err, ErrRevoked) {
		return err
	}
	_, err = s.grants.MarkRevoked(ctx, grant.ID, grant.Revision, s.now())
	return err
}

func suitableHosts(catalog []HostStatus, image string, network Network, currentHostID string) []HostStatus {
	result := make([]HostStatus, 0, len(catalog))
	for _, host := range catalog {
		if host.Usable && host.Isolated && (host.Slots > 0 || host.ID == currentHostID) &&
			contains(host.Images, image) && containsNetwork(host.Networks, network) {
			result = append(result, host)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Slots != result[j].Slots {
			return result[i].Slots > result[j].Slots
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func preferCurrent(candidates []HostStatus, id string) []HostStatus {
	for index := range candidates {
		if candidates[index].ID == id {
			candidates[0], candidates[index] = candidates[index], candidates[0]
			break
		}
	}
	return candidates
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsNetwork(values []Network, wanted Network) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func placement(grant *model.ExecutionGrant) *Placement {
	state := store.ExecutionProjectionUnavailable
	if grant.EnvironmentEpoch != "" {
		state = store.ExecutionProjectionSynchronizing
		if grant.State == model.ExecutionGrantReady && !grant.WorkspacePending && !grant.LifecyclePending &&
			grant.ControlAcknowledgedRevision == grant.ControlRevision && grant.DesiredControlState == model.ExecutionControlRunning {
			state = store.ExecutionProjectionReady
		}
	}
	return &Placement{Projection: ProjectionStatus{EnvironmentEpoch: grant.EnvironmentEpoch, AppliedWorkspaceCursor: grant.AppliedWorkspaceCursor, State: state}, GrantID: grant.ID, AttemptID: grant.AttemptID, HostID: grant.HostID, Image: grant.Image,
		Network: Network(grant.Network), Ready: grant.State == model.ExecutionGrantReady, Revision: grant.Revision}
}
