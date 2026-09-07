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

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func exportContentFixture(t *testing.T, filesystem vfspkg.FileSystem) (*Content, app.ExamExportArchiveInput) {
	t.Helper()
	content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	objectID := model.NewAttemptWorkspaceObjectID()
	original := []byte("original answer bytes\x00\n")
	staged, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, bytes.NewReader(original), int64(len(original)), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	input := app.ExamExportArchiveInput{ExportID: model.NewExamExportID(), AttemptID: model.NewJobAttemptID()}
	input.Records = []byte(`{"schema_version":1,"export_id":"` + input.ExportID.String() + `","categories":["work"],"submissions":[]}`)
	input.Files = []app.ExamExportArchiveFile{{SubmissionID: model.NewSubmissionID(), Entry: model.ExamSubmissionManifestEntry{EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile, Path: "answers/answer.txt", ContentVersion: model.NewWorkspaceContentVersion(), MediaType: staged.MediaType, SizeBytes: staged.SizeBytes, SHA256: staged.SHA256, StorageOrigin: model.AttemptWorkspaceStorageAttempt, AttemptObjectID: objectID}}}
	return content, input
}

func TestExamExportArchiveIsStandaloneAndVerifiable(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, input := exportContentFixture(t, backend.open(t))
			result, err := content.BuildExamExport(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			body, err := content.OpenExamExport(context.Background(), input.ExportID, input.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(body)
			_ = body.Close()
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			if int64(len(data)) != result.SizeBytes || hex.EncodeToString(digest[:]) != result.SHA256 {
				t.Fatal("archive hash or size does not match")
			}
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			members := map[string][]byte{}
			for _, f := range archive.File {
				r, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					t.Fatal(err)
				}
				if _, exists := members[f.Name]; exists {
					t.Fatal("duplicate ZIP member")
				}
				members[f.Name] = b
			}
			var manifest examExportManifestV1
			if err = json.Unmarshal(members["manifest.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.SchemaVersion != 1 || manifest.ExportID != input.ExportID || len(manifest.Entries) != 3 || len(members) != 4 {
				t.Fatal("incomplete standalone manifest")
			}
			for _, item := range manifest.Entries {
				b, exists := members[item.ArchivePath]
				sum := sha256.Sum256(b)
				if !exists || int64(len(b)) != item.SizeBytes || hex.EncodeToString(sum[:]) != item.SHA256 {
					t.Fatalf("unverifiable member %s", item.ArchivePath)
				}
			}
			file := manifest.Entries[2]
			if file.OriginalPath != input.Files[0].Entry.Path || file.SubmissionID != input.Files[0].SubmissionID || string(members[file.ArchivePath]) != "original answer bytes\x00\n" {
				t.Fatal("original file content or path was lost")
			}
			if bytes.Contains(data, []byte(input.Files[0].Entry.AttemptObjectID.String())) {
				t.Fatal("private VFS identity leaked into portable archive")
			}
			if err = content.PurgeExamExport(context.Background(), input.ExportID, input.AttemptID); err != nil {
				t.Fatal(err)
			}
			if _, err = content.OpenExamExport(context.Background(), input.ExportID, input.AttemptID); !IsNotFound(err) {
				t.Fatalf("purged archive reopened: %v", err)
			}
		})
	}
}

func TestExamExportRejectsBadSourcesAndCleansSpool(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, test := range []struct {
		name   string
		change func(*app.ExamExportArchiveInput)
	}{
		{"mismatched hash", func(i *app.ExamExportArchiveInput) { i.Files[0].Entry.SHA256 = strings.Repeat("a", 64) }},
		{"mismatched size", func(i *app.ExamExportArchiveInput) { i.Files[0].Entry.SizeBytes++ }},
		{"duplicate member", func(i *app.ExamExportArchiveInput) { i.Files = append(i.Files, i.Files[0]) }},
		{"invalid original path", func(i *app.ExamExportArchiveInput) { i.Files[0].Entry.Path = "../outside" }},
		{"foreign envelope", func(i *app.ExamExportArchiveInput) { i.ExportID = model.NewExamExportID() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := memoryvfs.New()
			content, input := exportContentFixture(t, fs)
			test.change(&input)
			if _, err := content.BuildExamExport(context.Background(), input); err == nil {
				t.Fatal("invalid export input accepted")
			}
			entries, err := os.ReadDir(temp)
			if err != nil || len(entries) != 0 {
				t.Fatalf("private spool remains: %v %v", entries, err)
			}
			if _, err = fs.Stat(context.Background(), examExportKey(input.ExportID, input.AttemptID)); !errors.Is(err, vfspkg.ErrNotFound) {
				t.Fatalf("unverified archive published: %v", err)
			}
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
			if (err != nil) != test.wantError || test.wantError && result != nil {
				t.Fatalf("result=%#v error=%v", result, err)
			}
			var uncertain interface{ ExamExportWriteUncertain() }
			if errors.As(err, &uncertain) != (test.uncertain && test.wantError) {
				t.Fatal("unverified remote acknowledgement lost uncertainty classification")
			}
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
	if _, err := content.BuildExamExport(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	content.filesystem = exportAcknowledgedRemoveVFS{FileSystem: fs}
	if err := content.PurgeExamExport(context.Background(), input.ExportID, input.AttemptID); err == nil {
		t.Fatal("acknowledgement was mistaken for absence")
	}
}

func TestExamExportCancellationAndCapacityDoNotConsumeInput(t *testing.T) {
	content, input := exportContentFixture(t, memoryvfs.New())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := content.BuildExamExport(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	release, err := content.beginWork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = content.BuildExamExport(context.Background(), input); !errors.Is(err, ErrWorkCapacity) {
		t.Fatalf("capacity=%v", err)
	}
}
