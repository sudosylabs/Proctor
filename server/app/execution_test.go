// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"io"
	"sync"

	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/model"
)

type executionUseCasesStub struct {
	mu           sync.Mutex
	images       []appexecution.ImageOption
	placement    *appexecution.Placement
	observation  appexecution.Observation
	observations []appexecution.Observation
	watchCalls   int
	terminal     appexecution.Terminal
	openBody     io.ReadCloser
	err          error
	ensure       []appexecution.Request
	released     []model.ExamAttemptID
	synchronized []model.ExamAttemptID
	changes      []model.AttemptWorkspaceJournalEntry
}

func (stub *executionUseCasesStub) Ensure(_ context.Context, request appexecution.Request) (*appexecution.Placement, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.ensure = append(stub.ensure, request)
	return stub.placement, stub.err
}

func (stub *executionUseCasesStub) Images(context.Context) ([]appexecution.ImageOption, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]appexecution.ImageOption(nil), stub.images...), stub.err
}

func (stub *executionUseCasesStub) Watch(context.Context, model.ExamAttemptID, appexecution.Cursor) (appexecution.Observation, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.watchCalls < len(stub.observations) {
		observation := stub.observations[stub.watchCalls]
		stub.watchCalls++
		return observation, stub.err
	}
	stub.watchCalls++
	return stub.observation, stub.err
}

func (stub *executionUseCasesStub) Attach(context.Context, model.ExamAttemptID, appexecution.Window) (appexecution.Terminal, error) {
	return stub.terminal, stub.err
}

func (stub *executionUseCasesStub) OpenFile(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error) {
	return stub.openBody, stub.err
}

func (stub *executionUseCasesStub) Sync(_ context.Context, attemptID model.ExamAttemptID) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.synchronized = append(stub.synchronized, attemptID)
	return stub.err
}

func (stub *executionUseCasesStub) SyncChange(_ context.Context, _ model.ExamAttemptID, change model.AttemptWorkspaceJournalEntry) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.changes = append(stub.changes, change)
	return stub.err
}

func (stub *executionUseCasesStub) Release(_ context.Context, attemptID model.ExamAttemptID) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.released = append(stub.released, attemptID)
	return stub.err
}
