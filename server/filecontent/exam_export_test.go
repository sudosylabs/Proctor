// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
)

func exportContentFixture(t *testing.T, filesystem vfspkg.FileSystem) (*Content, exam.ExportArchiveInput) {
	t.Helper()
	content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, nil)
	require.NoError(t, err)
	objectID := model.NewAttemptWorkspaceObjectID()
	original := []byte("original answer bytes\x00\n")
	staged, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, bytes.NewReader(original), int64(len(original)), "application/octet-stream")
	require.NoError(t, err)
	input := exam.ExportArchiveInput{ExportID: model.NewExamExportID(), AttemptID: model.NewJobAttemptID()}
	input.Records = []byte(`{"export_id":"` + input.ExportID.String() + `","categories":["work"],"submissions":[]}`)
	input.Files = []exam.ExportArchiveFile{{SubmissionID: model.NewSubmissionID(), Entry: model.ExamSubmissionManifestEntry{EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile, Path: "answers/answer.txt", ContentVersion: model.NewWorkspaceContentVersion(), MediaType: staged.MediaType, SizeBytes: staged.SizeBytes, SHA256: staged.SHA256, StorageOrigin: model.AttemptWorkspaceStorageAttempt, AttemptObjectID: objectID}}}
	return content, input
}

func TestExamExportArchiveIsStandaloneAndVerifiable(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, input := exportContentFixture(t, backend.open(t))
			result, err := content.BuildExamExport(context.Background(), input)
			require.NoError(t, err)
			body, err := content.OpenExamExport(context.Background(), input.ExportID, input.AttemptID)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			_ = body.Close()
			require.NoError(t, err)
			digest := sha256.Sum256(data)
			require.Equal(t, result.SizeBytes, int64(len(data)), "archive hash or size does not match")
			require.Equal(t, result.SHA256, hex.EncodeToString(digest[:]), "archive hash or size does not match")
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			require.NoError(t, err)
			members := map[string][]byte{}
			for _, f := range archive.File {
				r, err := f.Open()
				require.NoError(t, err)
				b, err := io.ReadAll(r)
				_ = r.Close()
				require.NoError(t, err)
				_, exists := members[f.Name]
				require.False(t, exists, "duplicate ZIP member")
				members[f.Name] = b
			}
			var manifest examExportManifest
			err = json.Unmarshal(members["manifest.json"], &manifest)
			require.NoError(t, err)
			require.Equal(t, input.ExportID, manifest.ExportID, "incomplete standalone manifest")
			require.Len(t, manifest.Entries, 3, "incomplete standalone manifest")
			require.Len(t, members, 4, "incomplete standalone manifest")
			for _, item := range manifest.Entries {
				b, exists := members[item.ArchivePath]
				sum := sha256.Sum256(b)
				require.True(t, exists)
				require.Equal(t, item.SizeBytes, int64(len(b)))
				require.Equal(t, item.SHA256, hex.EncodeToString(sum[:]))
			}
			file := manifest.Entries[2]
			require.Equal(t, input.Files[0].Entry.Path, file.OriginalPath, "original file content or path was lost")
			require.Equal(t, input.Files[0].SubmissionID, file.SubmissionID, "original file content or path was lost")
			require.Equal(t, "original answer bytes\x00\n", string(members[file.ArchivePath]), "original file content or path was lost")
			require.False(t, bytes.Contains(data, []byte(input.Files[0].Entry.AttemptObjectID.String())), "private VFS identity leaked into portable archive")
			err = content.PurgeExamExport(context.Background(), input.ExportID, input.AttemptID)
			require.NoError(t, err)
			_, err = content.OpenExamExport(context.Background(), input.ExportID, input.AttemptID)
			require.True(t, IsNotFound(err), "purged archive reopened")
		})
	}
}

func TestExamExportRejectsBadSourcesAndCleansSpool(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, test := range []struct {
		name   string
		change func(*exam.ExportArchiveInput)
	}{
		{"mismatched hash", func(i *exam.ExportArchiveInput) { i.Files[0].Entry.SHA256 = strings.Repeat("a", 64) }},
		{"mismatched size", func(i *exam.ExportArchiveInput) { i.Files[0].Entry.SizeBytes++ }},
		{"duplicate member", func(i *exam.ExportArchiveInput) { i.Files = append(i.Files, i.Files[0]) }},
		{"invalid original path", func(i *exam.ExportArchiveInput) { i.Files[0].Entry.Path = "../outside" }},
		{"foreign envelope", func(i *exam.ExportArchiveInput) { i.ExportID = model.NewExamExportID() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := memoryvfs.New()
			content, input := exportContentFixture(t, fs)
			test.change(&input)
			_, err := content.BuildExamExport(context.Background(), input)
			require.Error(t, err, "invalid export input accepted")
			entries, err := os.ReadDir(temp)
			require.NoError(t, err, "private spool remains")
			require.Empty(t, entries, "private spool remains")
			_, err = fs.Stat(context.Background(), examExportKey(input.ExportID, input.AttemptID))
			require.ErrorIs(t, err, vfspkg.ErrNotFound)
		})
	}
}

type exportWriteOutcomeVFS struct {
	vfspkg.FileSystem
	corrupt   bool
	uncertain bool
}

func (v *exportWriteOutcomeVFS) Write(ctx context.Context, key string, body io.Reader, options vfspkg.WriteOptions) (vfspkg.Info, error) {
	if v.corrupt {
		b, err := io.ReadAll(body)
		if err != nil {
			return vfspkg.Info{}, err
		}
		b[len(b)/2] ^= 1
		body = bytes.NewReader(b)
	}
	info, err := v.FileSystem.Write(ctx, key, body, options)
	if err == nil && v.uncertain {
		return info, errors.New("ambiguous provider acknowledgement")
	}
	return info, err
}

func TestExamExportVerifiesStorageAfterEveryWriteOutcome(t *testing.T) {
	for _, test := range []struct {
		name                          string
		uncertain, corrupt, wantError bool
	}{
		{name: "successful acknowledgement"}, {name: "unknown acknowledgement verified", uncertain: true},
		{name: "successful acknowledgement corrupt", corrupt: true, wantError: true}, {name: "unknown acknowledgement corrupt", uncertain: true, corrupt: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := memoryvfs.New()
			content, input := exportContentFixture(t, fs)
			content.filesystem = &exportWriteOutcomeVFS{FileSystem: fs, uncertain: test.uncertain, corrupt: test.corrupt}
			result, err := content.BuildExamExport(context.Background(), input)
			require.Equal(t, test.wantError, err != nil, "result=%#v error=%v", result, err)
			require.False(t, test.wantError && result != nil, "result=%#v error=%v", result, err)
			var uncertain interface{ ExamExportWriteUncertain() }
			require.Equal(t, test.uncertain && test.wantError, errors.As(err, &uncertain), "unverified remote acknowledgement lost uncertainty classification")
		})
	}
}

type exportAcknowledgedRemoveVFS struct{ vfspkg.FileSystem }

func (v exportAcknowledgedRemoveVFS) Remove(context.Context, string, vfspkg.RemoveOptions) error {
	return nil
}

func TestExamExportPurgeRequiresObservedAbsence(t *testing.T) {
	fs := memoryvfs.New()
	content, input := exportContentFixture(t, fs)
	_, err := content.BuildExamExport(context.Background(), input)
	require.NoError(t, err)
	content.filesystem = exportAcknowledgedRemoveVFS{FileSystem: fs}
	require.Error(t, content.PurgeExamExport(context.Background(), input.ExportID, input.AttemptID), "acknowledgement was mistaken for absence")
}

func TestExamExportCancellationAndCapacityDoNotConsumeInput(t *testing.T) {
	content, input := exportContentFixture(t, memoryvfs.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := content.BuildExamExport(ctx, input)
	require.ErrorIs(t, err, context.Canceled)
	release, err := content.beginWork(context.Background())
	require.NoError(t, err)
	defer release()
	_, err = content.BuildExamExport(context.Background(), input)
	require.ErrorIs(t, err, ErrWorkCapacity)
}
