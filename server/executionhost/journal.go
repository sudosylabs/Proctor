// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package executionhost

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/sudosylabs/execenv"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// This adapter is activated only with a dependency implementing the explicit
// journal capabilities. Merely satisfying a remote handle interface is not proof.
func adaptEnvironment(base *environment, capabilities execenv.Capabilities) appexecution.Environment {
	if !capabilities.OrderedControl || !capabilities.JournalProjection || !capabilities.SemanticObservations {
		return base
	}
	native, ok := base.native.(execenv.SemanticEnv)
	if !ok || !model.ValidExecutionEnvironmentEpoch(native.Epoch()) {
		return base
	}
	return &journalEnvironment{environment: base, epoch: native.Epoch()}
}

type journalEnvironment struct {
	*environment
	epoch string
}

func (e *journalEnvironment) Epoch() string { return e.epoch }
func (e *journalEnvironment) call(ctx context.Context, fn func(execenv.SemanticEnv) error) error {
	return e.withEnvironment(ctx, func(native execenv.Env) error {
		semantic, ok := native.(execenv.SemanticEnv)
		if !ok || semantic.Epoch() != e.epoch {
			return execenv.ErrUnavailable
		}
		return fn(semantic)
	})
}
func hostFence(f model.ExecutionFence) execenv.ProjectionFence {
	return execenv.ProjectionFence{ExecutionGrantID: execenv.ID(f.GrantID.String()), EnvironmentEpoch: f.EnvironmentEpoch, FenceRevision: uint64(f.ControlRevision)}
}
func applicationFence(f execenv.ProjectionFence) (model.ExecutionFence, error) {
	id, err := model.ParseExecutionGrantID(string(f.ExecutionGrantID))
	if err != nil || f.FenceRevision > execenv.MaxProjectionCursor {
		return model.ExecutionFence{}, appexecution.ErrInvalid
	}
	result := model.ExecutionFence{GrantID: id, EnvironmentEpoch: f.EnvironmentEpoch, ControlRevision: int64(f.FenceRevision)}
	if result.Validate() != nil {
		return model.ExecutionFence{}, appexecution.ErrInvalid
	}
	return result, nil
}
func (e *journalEnvironment) Control(ctx context.Context, fence model.ExecutionFence, state model.ExecutionControlState) (appexecution.ControlReceipt, error) {
	if fence.Validate() != nil || fence.EnvironmentEpoch != e.epoch {
		return appexecution.ControlReceipt{}, appexecution.ErrInvalid
	}
	var receipt execenv.ControlReceipt
	err := e.call(ctx, func(native execenv.SemanticEnv) error {
		var err error
		receipt, err = native.Control(ctx, execenv.ControlRequest{Fence: hostFence(fence), State: execenv.ControlState(state)})
		return err
	})
	if err != nil {
		return appexecution.ControlReceipt{}, err
	}
	returned, err := applicationFence(receipt.Fence)
	if err != nil {
		return appexecution.ControlReceipt{}, err
	}
	return appexecution.ControlReceipt{Fence: returned, State: model.ExecutionControlState(receipt.State), Confirmed: receipt.Confirmed}, nil
}
func hostContent(value *store.ExecutionProjectionContent) *execenv.ContentReference {
	if value == nil {
		return nil
	}
	return &execenv.ContentReference{TransferID: value.TransferID, Size: value.Size, Digest: value.SHA256}
}
func applicationContent(value *execenv.ContentReference) *store.ExecutionProjectionContent {
	if value == nil {
		return nil
	}
	return &store.ExecutionProjectionContent{TransferID: value.TransferID, Size: value.Size, SHA256: value.Digest}
}
func (e *journalEnvironment) UploadProjectionContent(ctx context.Context, f model.ExecutionFence, body io.Reader) (store.ExecutionProjectionContent, error) {
	if f.Validate() != nil || f.EnvironmentEpoch != e.epoch {
		return store.ExecutionProjectionContent{}, appexecution.ErrInvalid
	}
	var result execenv.ContentReference
	err := e.call(ctx, func(native execenv.SemanticEnv) error {
		var err error
		result, err = native.UploadProjectionContent(ctx, hostFence(f), body)
		return err
	})
	if err != nil {
		return store.ExecutionProjectionContent{}, err
	}
	if execenv.ValidateContentReference(result) != nil {
		return store.ExecutionProjectionContent{}, appexecution.ErrInvalid
	}
	return *applicationContent(&result), nil
}
func hostMutation(value store.ExecutionProjectionMutation) execenv.ProjectionMutation {
	c := value.Change
	result := execenv.ProjectionMutation{JournalCursor: uint64(c.Cursor), EntryID: c.EntryID.String(), Kind: execenv.ProjectionKind(c.EntryKind), ExpectedContentVersion: execenv.Version(value.ExpectedContentVersion.String()), Content: hostContent(value.Content)}
	switch c.Operation {
	case model.AttemptWorkspaceMutationCreateFile, model.AttemptWorkspaceMutationCreateDirectory:
		result.Operation, result.Path = execenv.ProjectionCreate, c.NewPath
		result.ResultingContentVersion = execenv.Version(c.ContentVersion.String())
	case model.AttemptWorkspaceMutationReplaceFile:
		result.Operation, result.Path = execenv.ProjectionWrite, c.NewPath
		result.ResultingContentVersion = execenv.Version(c.ContentVersion.String())
	case model.AttemptWorkspaceMutationMoveEntry:
		result.Operation, result.Path, result.DestinationPath = execenv.ProjectionMove, c.OldPath, c.NewPath
		result.ResultingContentVersion = execenv.Version(c.ContentVersion.String())
	case model.AttemptWorkspaceMutationDeleteEntry:
		result.Operation, result.Path = execenv.ProjectionDelete, c.OldPath
		if c.EntryKind == model.StarterWorkspaceEntryDirectory {
			recursive := c.Recursive
			result.Recursive = &recursive
		}
	}
	return result
}
func (e *journalEnvironment) ApplyProjection(ctx context.Context, request store.ExecutionProjectionRequest) (store.ExecutionProjectionReceipt, error) {
	if request.Validate() != nil || request.Fence.EnvironmentEpoch != e.epoch {
		return store.ExecutionProjectionReceipt{}, appexecution.ErrInvalid
	}
	var receipt execenv.ProjectionReceipt
	err := e.call(ctx, func(native execenv.SemanticEnv) error {
		var err error
		if request.Initial {
			initial := execenv.ProjectionInitialization{Fence: hostFence(request.Fence), MutationID: request.MutationID, WorkspaceCursor: uint64(request.ThroughWorkspaceCursor)}
			for _, entry := range request.Entries {
				initial.Entries = append(initial.Entries, execenv.ProjectionEntry{EntryID: entry.EntryID.String(), Kind: execenv.ProjectionKind(entry.Kind), Path: entry.Path, ContentVersion: execenv.Version(entry.ContentVersion.String()), Content: hostContent(entry.Content)})
			}
			receipt, err = native.InitializeProjection(ctx, initial)
		} else {
			apply := execenv.ProjectionApply{Fence: hostFence(request.Fence), MutationID: request.MutationID, FromWorkspaceCursor: uint64(request.FromWorkspaceCursor), ThroughWorkspaceCursor: uint64(request.ThroughWorkspaceCursor), ExpectedHostCursor: uint64(request.ExpectedHostCursor)}
			for _, mutation := range request.Mutations {
				apply.Mutations = append(apply.Mutations, hostMutation(mutation))
			}
			receipt, err = native.ApplyProjection(ctx, apply)
		}
		return err
	})
	if err != nil {
		if errors.Is(err, appexecution.ErrConflict) && !request.Initial {
			var page execenv.ObservationPage
			probeErr := e.call(ctx, func(native execenv.SemanticEnv) error {
				var err error
				page, err = native.Observe(ctx, hostFence(request.Fence), uint64(request.ExpectedHostCursor), 1)
				return err
			})
			if probeErr == nil && page.HostCursor > uint64(request.ExpectedHostCursor) {
				return store.ExecutionProjectionReceipt{}, appexecution.ErrHostCursorConflict
			}
			if probeErr != nil {
				return store.ExecutionProjectionReceipt{}, probeErr
			}
		}
		return store.ExecutionProjectionReceipt{}, err
	}
	fence, err := applicationFence(receipt.Fence)
	if err != nil || receipt.AppliedWorkspaceCursor > execenv.MaxProjectionCursor || receipt.HostCursor > execenv.MaxProjectionCursor {
		return store.ExecutionProjectionReceipt{}, appexecution.ErrInvalid
	}
	return store.ExecutionProjectionReceipt{Fence: fence, MutationID: receipt.MutationID, AppliedWorkspaceCursor: int64(receipt.AppliedWorkspaceCursor), HostCursor: int64(receipt.HostCursor)}, nil
}

// Observe starts from the host's retained acknowledgement, so a lost Confirm
// or ACK is retried using Proctor's durable outcome instead of skipping evidence.
func (e *journalEnvironment) Observe(ctx context.Context, f model.ExecutionFence, processed int64) (appexecution.SemanticObservation, error) {
	if f.Validate() != nil || f.EnvironmentEpoch != e.epoch || processed < 0 || processed > 1<<53-1 {
		return nil, appexecution.ErrInvalid
	}
	var page execenv.ObservationPage
	err := e.call(ctx, func(native execenv.SemanticEnv) error {
		var err error
		page, err = native.Observe(ctx, hostFence(f), uint64(processed), 1)
		return err
	})
	if err != nil {
		return nil, err
	}
	if page.AcknowledgedHostCursor > uint64(processed) || page.HostCursor < uint64(processed) {
		return nil, appexecution.ErrObservationLost
	}
	return &journalObservation{environment: e, fence: f, after: int64(page.AcknowledgedHostCursor), done: make(chan struct{})}, nil
}

type journalObservation struct {
	environment *journalEnvironment
	fence       model.ExecutionFence
	mu          sync.Mutex
	after       int64
	current     *store.ExecutionObservation
	done        chan struct{}
	once        sync.Once
}

func (o *journalObservation) Cursor() appexecution.Cursor {
	o.mu.Lock()
	defer o.mu.Unlock()
	return appexecution.Cursor(strconv.FormatInt(o.after, 10))
}
func (o *journalObservation) Close() error { o.once.Do(func() { close(o.done) }); return nil }
func (o *journalObservation) Next(ctx context.Context) (appexecution.Event, error) {
	for {
		event, available, err := o.NextPending(ctx)
		if (err != nil && !errors.Is(err, appexecution.ErrProjectionPending)) || available {
			return event, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return appexecution.Event{}, ctx.Err()
		case <-o.done:
			timer.Stop()
			return appexecution.Event{}, io.ErrClosedPipe
		case <-timer.C:
		}
	}
}
func (o *journalObservation) NextPending(ctx context.Context) (appexecution.Event, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	select {
	case <-o.done:
		return appexecution.Event{}, false, io.ErrClosedPipe
	default:
	}
	if o.current != nil {
		event := cloneObservation(*o.current)
		return appexecution.Event{Semantic: &event}, true, nil
	}
	var status execenv.ControlReceipt
	if err := o.environment.call(ctx, func(native execenv.SemanticEnv) error {
		var err error
		status, err = native.ControlStatus(ctx)
		return err
	}); err != nil {
		return appexecution.Event{}, false, err
	}
	if status.Fence.ExecutionGrantID != hostFence(o.fence).ExecutionGrantID || status.Fence.EnvironmentEpoch != o.fence.EnvironmentEpoch || status.Fence.FenceRevision < uint64(o.fence.ControlRevision) {
		return appexecution.Event{}, false, appexecution.ErrObservationLost
	}
	if !status.Confirmed || status.State == execenv.ControlFrozen {
		return appexecution.Event{}, false, appexecution.ErrProjectionPending
	}
	if status.State != execenv.ControlRunning {
		return appexecution.Event{}, false, appexecution.ErrRevoked
	}
	currentFence, err := applicationFence(status.Fence)
	if err != nil {
		return appexecution.Event{}, false, err
	}
	o.fence = currentFence
	var page execenv.ObservationPage
	err = o.environment.call(ctx, func(native execenv.SemanticEnv) error {
		var err error
		page, err = native.Observe(ctx, hostFence(o.fence), uint64(o.after), 1)
		return err
	})
	if err != nil {
		return appexecution.Event{}, false, err
	}
	if page.AcknowledgedHostCursor > uint64(o.after) || page.HostCursor < uint64(o.after) {
		return appexecution.Event{}, false, appexecution.ErrObservationLost
	}
	if len(page.Observations) == 0 {
		if page.HostCursor != uint64(o.after) {
			return appexecution.Event{}, false, appexecution.ErrObservationLost
		}
		return appexecution.Event{}, false, nil
	}
	event, err := applicationObservation(page.Observations[0])
	if err != nil || event.HostSequence != o.after+1 || event.Fence != o.fence {
		return appexecution.Event{}, false, appexecution.ErrObservationLost
	}
	retained := cloneObservation(event)
	o.current = &retained
	return appexecution.Event{Semantic: &event}, true, nil
}
func (o *journalObservation) OpenContent(ctx context.Context, event store.ExecutionObservation) (io.ReadCloser, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.matches(event) || event.Content == nil {
		return nil, appexecution.ErrInvalid
	}
	var body io.ReadCloser
	err := o.environment.call(ctx, func(native execenv.SemanticEnv) error {
		var err error
		body, err = native.OpenObservationContent(ctx, hostFence(o.fence), *hostContent(event.Content))
		return err
	})
	if err != nil && body != nil {
		_ = body.Close()
		body = nil
	}
	return body, err
}
func (o *journalObservation) Confirm(ctx context.Context, event store.ExecutionObservation, mutation store.ExecutionProjectionMutation) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.matches(event) {
		return appexecution.ErrInvalid
	}
	return o.environment.call(ctx, func(native execenv.SemanticEnv) error {
		return native.ConfirmObservationProjection(ctx, execenv.ObservationProjection{Fence: hostFence(o.fence), HostSequence: uint64(event.HostSequence), Mutation: hostMutation(mutation)})
	})
}
func (o *journalObservation) Acknowledge(ctx context.Context, sequence int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.current == nil || o.current.HostSequence != sequence {
		return appexecution.ErrInvalid
	}
	err := o.environment.call(ctx, func(native execenv.SemanticEnv) error {
		return native.AcknowledgeObservations(ctx, hostFence(o.fence), uint64(sequence))
	})
	if err == nil {
		o.after = sequence
		o.current = nil
	}
	return err
}
func (o *journalObservation) matches(event store.ExecutionObservation) bool {
	// Content references and every original precondition must remain exact.
	if o.current == nil {
		return false
	}
	a, b := *o.current, event
	if (a.Content == nil) != (b.Content == nil) || (a.Recursive == nil) != (b.Recursive == nil) {
		return false
	}
	if a.Content != nil && *a.Content != *b.Content || a.Recursive != nil && *a.Recursive != *b.Recursive {
		return false
	}
	a.Content, b.Content, a.Recursive, b.Recursive = nil, nil, nil, nil
	return a == b
}
func applicationObservation(value execenv.ExecutionObservation) (store.ExecutionObservation, error) {
	if execenv.ValidateExecutionObservation(value) != nil {
		return store.ExecutionObservation{}, appexecution.ErrInvalid
	}
	fence, err := applicationFence(value.Fence)
	if err != nil {
		return store.ExecutionObservation{}, err
	}
	event := store.ExecutionObservation{Fence: fence, HostSequence: int64(value.HostSequence), BasedOnWorkspaceCursor: int64(value.BasedOnWorkspaceCursor), NodeIdentity: value.NodeIdentity, Kind: model.StarterWorkspaceEntryKind(value.Kind), Path: value.Path, DestinationPath: value.DestinationPath, Recursive: value.Recursive, Content: applicationContent(value.Content), OriginProjectionMutationID: value.OriginProjectionMutationID}
	if value.ExpectedContentVersion != "" {
		event.ExpectedContentVersion, err = model.ParseWorkspaceContentVersion(string(value.ExpectedContentVersion))
		if err != nil {
			return event, appexecution.ErrInvalid
		}
	}
	switch value.Operation {
	case execenv.ObservationCreateFile:
		event.Operation = model.AttemptWorkspaceMutationCreateFile
	case execenv.ObservationCreateDirectory:
		event.Operation = model.AttemptWorkspaceMutationCreateDirectory
	case execenv.ObservationWriteFile:
		event.Operation = model.AttemptWorkspaceMutationReplaceFile
	case execenv.ObservationMove:
		event.Operation = model.AttemptWorkspaceMutationMoveEntry
	case execenv.ObservationDelete:
		event.Operation = model.AttemptWorkspaceMutationDeleteEntry
	default:
		return event, appexecution.ErrInvalid
	}
	if event.Validate() != nil {
		return event, appexecution.ErrInvalid
	}
	return event, nil
}

var _ appexecution.ProjectionEnvironment = (*journalEnvironment)(nil)
var _ appexecution.SemanticObservation = (*journalObservation)(nil)

func cloneObservation(value store.ExecutionObservation) store.ExecutionObservation {
	if value.Content != nil {
		content := *value.Content
		value.Content = &content
	}
	if value.Recursive != nil {
		recursive := *value.Recursive
		value.Recursive = &recursive
	}
	return value
}
