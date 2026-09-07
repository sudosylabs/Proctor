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

	vfs "github.com/sudosylabs/proctor/packages/vfs"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
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
		{name: "cancelled observation", statErr: context.Canceled, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := &retentionObservationVFS{FileSystem: memoryvfs.New()}
			content, err := New(fs, Policy{MaximumConcurrentOperations: 2}, nil)
			if err != nil {
				t.Fatal(err)
			}
			id, other := model.NewAttemptWorkspaceObjectID(), model.NewAttemptWorkspaceObjectID()
			for _, target := range []model.AttemptWorkspaceObjectID{id, other} {
				if _, err = content.StageAttemptWorkspaceObject(context.Background(), target, strings.NewReader("answer"), 6, "text/plain"); err != nil {
					t.Fatal(err)
				}
			}
			fs.retain, fs.statErr = test.retain, test.statErr
			fs.stats = 0
			err = content.PurgeRetiredAttemptWorkspaceObject(context.Background(), id)
			if (err != nil) != test.wantError || fs.removes != 1 || fs.stats != 1 || fs.removedKey != attemptWorkspaceObjectKey(id) || fs.observedKey != fs.removedKey {
				t.Fatalf("purge err=%v removes=%d stats=%d", err, fs.removes, fs.stats)
			}
			if err != nil && strings.Contains(err.Error(), "provider secret detail") {
				t.Fatal("unsafe provider error exposed")
			}
			if _, err = fs.FileSystem.Stat(context.Background(), attemptWorkspaceObjectKey(other)); err != nil {
				t.Fatal("purge touched another object")
			}
			fs.retain, fs.statErr = false, nil
			if err = content.PurgeRetiredAttemptWorkspaceObject(context.Background(), id); err != nil {
				t.Fatal("retry did not reconcile absence", err)
			}
			if err = content.PurgeRetiredAttemptWorkspaceObject(context.Background(), id); err != nil {
				t.Fatal("verified absence was not idempotent", err)
			}
		})
	}
}
