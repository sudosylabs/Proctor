// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

type retentionObservationVFS struct {
	vfs.FileSystem
	retain                  bool
	statErr                 error
	removes, stats          int
	removedKey, observedKey string
}

func (f *retentionObservationVFS) Remove(ctx context.Context, key string, options vfs.RemoveOptions) error {
	f.removes++
	f.removedKey = key
	if f.retain {
		return nil
	}
	return f.FileSystem.Remove(ctx, key, options)
}
func (f *retentionObservationVFS) Stat(ctx context.Context, key string) (vfs.Info, error) {
	f.stats++
	f.observedKey = key
	if f.statErr != nil {
		return vfs.Info{}, f.statErr
	}
	return f.FileSystem.Stat(ctx, key)
}

func TestRetentionPurgeRequiresIndependentExactKeyAbsence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		retain    bool
		statErr   error
		wantError bool
	}{
		{name: "confirmed removal"},
		{name: "acknowledged but still present", retain: true, wantError: true},
		{name: "observation unavailable", statErr: errors.New("provider secret detail"), wantError: true},
		{name: "canceled observation", statErr: context.Canceled, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := &retentionObservationVFS{FileSystem: memory.New()}
			content, err := New(fs, Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			id, other := model.NewAttemptWorkspaceObjectID(), model.NewAttemptWorkspaceObjectID()
			for _, target := range []model.AttemptWorkspaceObjectID{id, other} {
				_, err = content.StageAttemptWorkspaceObject(context.Background(), target, strings.NewReader("answer"), 6, "text/plain")
				require.NoError(t, err)
			}
			fs.retain, fs.statErr = test.retain, test.statErr
			fs.stats = 0
			err = content.PurgeRetiredAttemptWorkspaceObject(context.Background(), id)
			require.Equal(t, test.wantError, err != nil, "purge err=%v removes=%d stats", err, fs.removes)
			require.Equal(t, 1, fs.removes, "purge err=%v removes=%d stats", err, fs.removes)
			require.Equal(t, 1, fs.stats, "purge err=%v removes=%d stats", err, fs.removes)
			require.Equal(t, attemptWorkspaceObjectKey(id), fs.removedKey, "purge err=%v removes=%d stats", err, fs.removes)
			require.Equal(t, fs.removedKey, fs.observedKey, "purge err=%v removes=%d stats", err, fs.removes)
			if err != nil {
				require.NotContains(t, err.Error(), "provider secret detail", "unsafe provider error exposed")
			}
			_, err = fs.FileSystem.Stat(context.Background(), attemptWorkspaceObjectKey(other))
			require.NoError(t, err, "purge touched another object")
			fs.retain, fs.statErr = false, nil
			err = content.PurgeRetiredAttemptWorkspaceObject(context.Background(), id)
			require.NoError(t, err, "retry did not reconcile absence")
			err = content.PurgeRetiredAttemptWorkspaceObject(context.Background(), id)
			require.NoError(t, err, "verified absence was not idempotent")
		})
	}
}
