// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type sessionPurgeStoreFake struct {
	passes []store.SessionPurgeResult
	inputs []store.SessionPurge
}

func (f *sessionPurgeStoreFake) PurgeRetired(_ context.Context, input *store.SessionPurge) (*store.SessionPurgeResult, error) {
	f.inputs = append(f.inputs, *input)
	if len(f.passes) == 0 {
		return &store.SessionPurgeResult{}, nil
	}
	pass := f.passes[0]
	f.passes = f.passes[1:]
	return &pass, nil
}

type sessionCredentialPurgeStoreFake struct {
	deleted []int64
	inputs  []store.SessionCredentialPurge
}

func (f *sessionCredentialPurgeStoreFake) PurgeRetired(_ context.Context, input *store.SessionCredentialPurge) (int64, error) {
	f.inputs = append(f.inputs, *input)
	if len(f.deleted) == 0 {
		return 0, nil
	}
	deleted := f.deleted[0]
	f.deleted = f.deleted[1:]
	return deleted, nil
}

func TestSessionPurgeWalksRetiredSessionsInOrder(t *testing.T) {
	t.Parallel()

	first := store.SessionPurgePosition{RetiredAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), SessionID: model.NewSessionID()}
	sessions := &sessionPurgeStoreFake{passes: []store.SessionPurgeResult{
		{Examined: 2, Sessions: 1, Credentials: 4, Next: first},
		{Examined: 1, Sessions: 1, Credentials: 2},
	}}
	credentials := &sessionCredentialPurgeStoreFake{deleted: []int64{2, 1}}
	command, err := json.Marshal(SessionPurgeCommandV1{BatchSize: 2})
	require.NoError(t, err)
	at := time.Now().UTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeSessionPurgeRetired, 1, command, "purge", at, at, 3)
	require.NoError(t, err)
	handler := sessionPurgeHandler{sessions: sessions, credentials: credentials, retention: 720 * time.Hour}
	outcome := handler.Run(context.Background(), jobengine.NewExecution(record, nil, nil, nil))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome = %#v", outcome)

	require.Equal(t, []store.SessionCredentialPurge{{Grace: 720 * time.Hour, Limit: 2}, {Grace: 720 * time.Hour, Limit: 2}}, credentials.inputs)
	require.Len(t, sessions.inputs, 2)
	require.Zero(t, sessions.inputs[0].After, "the first pass starts at the oldest retired Session")
	require.Equal(t, first, sessions.inputs[1].After, "a full pass continues after the last examined Session")
	var result SessionPurgeResultV1
	require.NoError(t, json.Unmarshal(outcome.Result, &result))
	require.Equal(t, SessionPurgeResultV1{Sessions: 2, Credentials: 9}, result)
}
