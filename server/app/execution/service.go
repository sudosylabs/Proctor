// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

const PendingRevocationPageSize = 100
const CurrentReconciliationPageSize = 100
const SittingGrantPageSize = 200

type Request struct {
	AttemptID model.ExamAttemptID
	Image     string
	Network   Network
}

type Placement struct {
	GrantID   model.ExecutionGrantID
	AttemptID model.ExamAttemptID
	HostID    string
	Image     string
	Network   Network
	Ready     bool
	Revision  int64
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
	if !request.AttemptID.IsValid() || request.Image == "" ||
		(request.Network != NetworkNone && request.Network != NetworkAllowlist) {
		return nil, ErrInvalid
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
	tree, err := s.authoritativeTree(ctx, request.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("load authoritative execution workspace: %w", err)
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

		environment, hostErr := s.hosts.Ensure(ctx, current.HostID, Spec{
			ID: current.ID.String(), Image: current.Image, Network: Network(current.Network),
		})
		if hostErr == nil {
			hostErr = environment.ReplaceTree(ctx, cloneTree(tree))
		}
		if hostErr != nil {
			if !errors.Is(hostErr, ErrUnavailable) && !errors.Is(hostErr, ErrCapacity) && !errors.Is(hostErr, ErrRevoked) {
				return nil, fmt.Errorf("prepare execution environment: %w", hostErr)
			}
			continue
		}
		if current.State == model.ExecutionGrantReserved {
			current, err = s.grants.MarkReady(ctx, current.ID, current.Revision, s.now())
			if err != nil {
				return nil, fmt.Errorf("mark execution placement ready: %w", err)
			}
		}
		return placement(current), nil
	}
	return nil, ErrUnavailable
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

func (s *Service) authoritativeTree(ctx context.Context, attemptID model.ExamAttemptID) (Tree, error) {
	snapshot, err := s.grants.WorkspaceSnapshot(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || len(snapshot.Nodes) > model.AttemptWorkspaceMaximumEntries {
		return nil, ErrInvalid
	}
	tree := make(Tree, 0, len(snapshot.Nodes))
	var total int64
	for _, node := range snapshot.Nodes {
		if _, err := model.NormalizeAttemptWorkspacePath(node.Path); err != nil {
			return nil, ErrInvalid
		}
		if node.Kind == model.StarterWorkspaceEntryDirectory {
			tree = append(tree, Node{Path: node.Path, Kind: NodeDirectory})
			continue
		}
		if node.Kind != model.StarterWorkspaceEntryFile || !node.ContentVersion.IsValid() ||
			node.SizeBytes < 0 || node.SizeBytes > model.AttemptWorkspaceMaximumFileBytes {
			return nil, ErrInvalid
		}
		var body io.ReadCloser
		switch node.StorageOrigin {
		case model.AttemptWorkspaceStorageStarter:
			body, err = s.content.OpenStarterWorkspaceObject(ctx, node.StarterObjectID)
		case model.AttemptWorkspaceStorageAttempt:
			body, err = s.content.OpenAttemptWorkspaceObject(ctx, node.AttemptObjectID)
		default:
			return nil, ErrInvalid
		}
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(body, model.AttemptWorkspaceMaximumFileBytes+1))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil {
			return nil, errors.Join(readErr, closeErr)
		}
		if int64(len(data)) != node.SizeBytes || int64(len(data)) > model.AttemptWorkspaceMaximumFileBytes {
			return nil, ErrInvalid
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != node.SHA256 {
			return nil, ErrInvalid
		}
		total += int64(len(data))
		if total > model.AttemptWorkspaceMaximumTotalBytes {
			return nil, ErrInvalid
		}
		tree = append(tree, Node{Path: node.Path, Kind: NodeFile, Version: node.ContentVersion.String(), Data: data})
	}
	return tree, nil
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

// Sync converges an existing ready grant after an acknowledged IDE mutation.
// Attempts without a grant require no transient work.
func (s *Service) Sync(ctx context.Context, attemptID model.ExamAttemptID) error {
	grant, err := s.grants.Current(ctx, attemptID)
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read execution placement: %w", err)
	}
	if grant.State != model.ExecutionGrantReady {
		return nil
	}
	tree, err := s.authoritativeTree(ctx, attemptID)
	if err != nil {
		return fmt.Errorf("load authoritative execution workspace: %w", err)
	}
	environment, err := s.hosts.Ensure(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
	if err != nil {
		return err
	}
	return environment.ReplaceTree(ctx, tree)
}

// SyncChange applies one acknowledged authoritative IDE change without
// invalidating the guest observation stream.
func (s *Service) SyncChange(ctx context.Context, attemptID model.ExamAttemptID, change model.AttemptWorkspaceJournalEntry) error {
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
	if grant.State != model.ExecutionGrantReady {
		return nil
	}
	environment, err := s.hosts.Ensure(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
	if err != nil {
		return err
	}
	mutation := Mutation{Kind: NodeKind(0)}
	switch change.Operation {
	case model.AttemptWorkspaceMutationCreateDirectory:
		mutation.Operation, mutation.Path, mutation.Kind = OperationCreate, change.NewPath, NodeDirectory
	case model.AttemptWorkspaceMutationCreateFile, model.AttemptWorkspaceMutationReplaceFile:
		tree, treeErr := s.authoritativeTree(ctx, attemptID)
		if treeErr != nil {
			return treeErr
		}
		var found bool
		for _, node := range tree {
			if node.Path == change.NewPath && node.Kind == NodeFile && node.Version == change.ContentVersion.String() {
				mutation.Path, mutation.Kind, mutation.Version, mutation.Data = node.Path, NodeFile, node.Version, node.Data
				found = true
				break
			}
		}
		if !found {
			return ErrConflict
		}
		if change.Operation == model.AttemptWorkspaceMutationCreateFile {
			mutation.Operation = OperationCreate
		} else {
			mutation.Operation = OperationReplace
		}
	case model.AttemptWorkspaceMutationMoveEntry:
		mutation.Operation, mutation.From, mutation.Path = OperationMove, change.OldPath, change.NewPath
		if change.EntryKind == model.StarterWorkspaceEntryDirectory {
			mutation.Kind = NodeDirectory
		} else {
			mutation.Kind = NodeFile
			mutation.Version = change.ContentVersion.String()
		}
	case model.AttemptWorkspaceMutationDeleteEntry:
		mutation.Operation, mutation.Path = OperationDelete, change.OldPath
		if change.EntryKind == model.StarterWorkspaceEntryDirectory {
			mutation.Kind = NodeDirectory
		} else {
			mutation.Kind = NodeFile
		}
	default:
		return ErrInvalid
	}
	return environment.Apply(ctx, []Mutation{mutation})
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

	pending, err := s.grants.ListPendingRevocations(ctx, PendingRevocationPageSize)
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

func (s *Service) Attach(ctx context.Context, attemptID model.ExamAttemptID, window Window) (Terminal, error) {
	environment, _, err := s.open(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	return environment.Attach(ctx, window)
}

func (s *Service) Watch(ctx context.Context, attemptID model.ExamAttemptID, after Cursor) (Observation, error) {
	environment, _, err := s.open(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	return environment.Watch(ctx, after)
}

func (s *Service) OpenFile(ctx context.Context, attemptID model.ExamAttemptID, path string) (io.ReadCloser, error) {
	environment, _, err := s.open(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	return environment.Open(ctx, path)
}

func (s *Service) Freeze(ctx context.Context, attemptID model.ExamAttemptID) error {
	environment, _, err := s.open(ctx, attemptID)
	if err != nil {
		return err
	}
	return environment.Freeze(ctx)
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
	defer func() { resultErr = errors.Join(resultErr, lease.Release(context.Background())) }()
	for attempt := 0; attempt < 3; attempt++ {
		convergence, err := s.grants.CurrentForReconciliation(ctx, grantID)
		if store.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read leased execution lifecycle: %w", err)
		}
		grant := convergence.Grant
		if grant.LifecyclePending {
			return s.releaseGrant(ctx, grant)
		}
		if convergence.AttemptState != model.ExamAttemptActive || convergence.AcknowledgementRequired ||
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
	return s.hosts.Ensure(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
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

func (s *Service) open(ctx context.Context, attemptID model.ExamAttemptID) (Environment, *model.ExecutionGrant, error) {
	grant, err := s.grants.Current(ctx, attemptID)
	if err != nil {
		return nil, nil, fmt.Errorf("read execution placement: %w", err)
	}
	if grant.State != model.ExecutionGrantReady {
		return nil, nil, ErrUnavailable
	}
	convergence, err := s.grants.CurrentForReconciliation(ctx, grant.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("read execution interaction fence: %w", err)
	}
	if convergence == nil || convergence.Grant == nil || convergence.Grant.ID != grant.ID ||
		convergence.AttemptState != model.ExamAttemptActive || convergence.SittingState != model.ExamSittingOpen ||
		convergence.AcknowledgementRequired {
		return nil, nil, ErrUnavailable
	}
	environment, err := s.hosts.Ensure(ctx, grant.HostID, Spec{ID: grant.ID.String(), Image: grant.Image, Network: Network(grant.Network)})
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

func cloneTree(tree Tree) Tree {
	cloned := make(Tree, len(tree))
	copy(cloned, tree)
	for index := range cloned {
		cloned[index].Data = append([]byte(nil), tree[index].Data...)
	}
	return cloned
}

func placement(grant *model.ExecutionGrant) *Placement {
	return &Placement{GrantID: grant.ID, AttemptID: grant.AttemptID, HostID: grant.HostID, Image: grant.Image,
		Network: Network(grant.Network), Ready: grant.State == model.ExecutionGrantReady, Revision: grant.Revision}
}
