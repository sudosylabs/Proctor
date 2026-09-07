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
	"fmt"
	"io"
	"os"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type examExportManifestV1 struct {
	SchemaVersion int                         `json:"schema_version"`
	ExportID      model.ExamExportID          `json:"export_id"`
	Entries       []examExportManifestEntryV1 `json:"entries"`
}

type uncertainExamExportWrite struct{ cause error }

func (e *uncertainExamExportWrite) Error() string           { return "Exam export write outcome is uncertain" }
func (e *uncertainExamExportWrite) Unwrap() error           { return e.cause }
func (*uncertainExamExportWrite) ExamExportWriteUncertain() {}

type examExportManifestEntryV1 struct {
	ArchivePath  string                        `json:"archive_path"`
	SizeBytes    int64                         `json:"size_bytes"`
	SHA256       string                        `json:"sha256"`
	SubmissionID model.SubmissionID            `json:"submission_id,omitempty"`
	EntryID      model.AttemptWorkspaceEntryID `json:"entry_id,omitempty"`
	OriginalPath string                        `json:"original_path,omitempty"`
	MediaType    string                        `json:"media_type,omitempty"`
}

const examExportReadme = `Proctor examination export, format version 1

records.json contains the selected structured records as captured when requested.
manifest.json lists every data member with its exact byte size and SHA-256 hash.
The manifest itself is excluded from that list to avoid a circular checksum.

Original files are unchanged. Their ZIP member names use opaque identifiers;
original workspace paths and empty directories are recorded in records.json.
Do not execute exported files or render private review text as trusted HTML.

To verify independently, read manifest.json, reject duplicate/unlisted data
members, and compute the size and SHA-256 of each uncompressed member. Compare
the complete ZIP SHA-256 with the value supplied by the authenticated export API.
Hashes detect changes; they are not a digital signature or proof of authorship.
This format is a portable record archive, not an installation backup or importer.
`

// BuildExamExport writes only the pre-registered opaque attempt identity. It
// verifies every original file while copying, closes the ZIP, then reopens and
// hashes the managed object before returning verified publication metadata.
func (c *Content) BuildExamExport(ctx context.Context, input app.ExamExportArchiveInput) (*app.ExamExportArchiveContent, error) {
	if c == nil || c.filesystem == nil || !input.ExportID.IsValid() || !input.AttemptID.IsValid() || len(input.Records) < 1 || len(input.Records) > model.ExamExportMaximumSnapshotBytes || !json.Valid(input.Records) || len(input.Files) > model.ExamExportMaximumEntries {
		return nil, errors.New("invalid Exam export input")
	}
	var envelope struct {
		SchemaVersion int                `json:"schema_version"`
		ExportID      model.ExamExportID `json:"export_id"`
	}
	if json.Unmarshal(input.Records, &envelope) != nil || envelope.SchemaVersion != 1 || envelope.ExportID != input.ExportID {
		return nil, errors.New("invalid Exam export records version or owner")
	}
	seen := make(map[string]struct{}, len(input.Files))
	var total int64
	for _, f := range input.Files {
		if !f.SubmissionID.IsValid() || f.Entry.Validate() != nil || f.Entry.Kind != model.StarterWorkspaceEntryFile {
			return nil, errors.New("invalid Exam export file")
		}
		name := examExportFileMember(f)
		if _, exists := seen[name]; exists {
			return nil, errors.New("duplicate Exam export file")
		}
		seen[name] = struct{}{}
		total += f.Entry.SizeBytes
		if total > model.ExamExportMaximumSourceBytes {
			return nil, errors.New("Exam export exceeds byte limit")
		}
	}
	release, err := c.beginWork(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	spool, err := os.CreateTemp("", "proctor-exam-export-*")
	if err != nil {
		return nil, sanitize("create Exam export spool", err)
	}
	defer func() { _ = spool.Close(); _ = os.Remove(spool.Name()) }()
	bounded := &examExportBoundedWriter{writer: spool, remaining: model.ExamExportMaximumArchiveBytes}
	archive := zip.NewWriter(bounded)
	manifest := examExportManifestV1{SchemaVersion: 1, ExportID: input.ExportID, Entries: make([]examExportManifestEntryV1, 0, len(input.Files)+2)}
	for _, item := range []struct {
		name string
		body []byte
	}{{"records.json", input.Records}, {"README.txt", []byte(examExportReadme)}} {
		size, digest, err := writeExamExportMember(ctx, archive, item.name, bytes.NewReader(item.body), int64(len(item.body)))
		if err != nil {
			return nil, sanitize("write Exam export records", err)
		}
		manifest.Entries = append(manifest.Entries, examExportManifestEntryV1{ArchivePath: item.name, SizeBytes: size, SHA256: digest})
	}
	for _, f := range input.Files {
		var body io.ReadCloser
		switch f.Entry.StorageOrigin {
		case model.AttemptWorkspaceStorageStarter:
			body, err = c.OpenStarterWorkspaceObject(ctx, f.Entry.StarterObjectID)
		case model.AttemptWorkspaceStorageAttempt:
			body, err = c.OpenAttemptWorkspaceObject(ctx, f.Entry.AttemptObjectID)
		default:
			return nil, errors.New("invalid Exam export storage origin")
		}
		if err != nil {
			return nil, sanitize("open Exam export source", err)
		}
		size, digest, copyErr := writeExamExportMember(ctx, archive, examExportFileMember(f), body, f.Entry.SizeBytes)
		closeErr := body.Close()
		if copyErr != nil || closeErr != nil {
			return nil, sanitize("copy Exam export source", errors.Join(copyErr, closeErr))
		}
		if size != f.Entry.SizeBytes || digest != f.Entry.SHA256 {
			return nil, errors.New("Exam export source verification failed")
		}
		manifest.Entries = append(manifest.Entries, examExportManifestEntryV1{ArchivePath: examExportFileMember(f), SizeBytes: size, SHA256: digest, SubmissionID: f.SubmissionID, EntryID: f.Entry.EntryID, OriginalPath: f.Entry.Path, MediaType: f.Entry.MediaType})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, sanitize("encode Exam export manifest", err)
	}
	if _, _, err = writeExamExportMember(ctx, archive, "manifest.json", bytes.NewReader(encoded), int64(len(encoded))); err != nil {
		return nil, sanitize("write Exam export manifest", err)
	}
	if err = archive.Close(); err != nil {
		return nil, sanitize("close Exam export archive", err)
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return nil, sanitize("rewind Exam export spool", err)
	}
	hash := sha256.New()
	size, err := io.CopyBuffer(hash, workReader{ctx: ctx, reader: spool}, make([]byte, 32<<10))
	if err != nil {
		return nil, sanitize("hash Exam export archive", err)
	}
	result := &app.ExamExportArchiveContent{SizeBytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	if !model.ValidExamExportContent(result.SizeBytes, result.SHA256) {
		return nil, errors.New("invalid Exam export archive size")
	}
	key := examExportKey(input.ExportID, input.AttemptID)
	conditional := c.filesystem.Capabilities().ConditionalWrite
	if !conditional {
		matching, verifyErr := c.verifyExamExport(ctx, key, result)
		if verifyErr == nil {
			if matching {
				return result, nil
			}
			return nil, errors.New("Exam export object conflict")
		}
		if !errors.Is(verifyErr, vfspkg.ErrNotFound) {
			return nil, sanitize("inspect Exam export object", verifyErr)
		}
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return nil, sanitize("rewind Exam export spool", err)
	}
	_, writeErr := c.filesystem.Write(ctx, key, workReader{ctx: ctx, reader: spool}, vfspkg.WriteOptions{Size: &size, NoOverwrite: conditional})
	// An error acknowledgement can still mean the exact immutable write
	// committed. Never publish until an independent read proves all bytes.
	matching, verifyErr := c.verifyExamExport(ctx, key, result)
	if verifyErr != nil || !matching {
		if writeErr != nil {
			return nil, &uncertainExamExportWrite{cause: sanitize("store Exam export archive", writeErr)}
		}
		if verifyErr != nil {
			return nil, sanitize("verify Exam export archive", verifyErr)
		}
		return nil, errors.New("Exam export archive verification failed")
	}
	return result, nil
}

func examExportFileMember(f app.ExamExportArchiveFile) string {
	return "files/" + f.SubmissionID.String() + "/" + f.Entry.EntryID.String()
}

func writeExamExportMember(ctx context.Context, archive *zip.Writer, name string, body io.Reader, expected int64) (int64, string, error) {
	h := &zip.FileHeader{Name: name, Method: zip.Store, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
	h.SetMode(0600)
	member, err := archive.CreateHeader(h)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	size, err := io.CopyBuffer(io.MultiWriter(member, hash), io.LimitReader(workReader{ctx: ctx, reader: body}, expected+1), make([]byte, 32<<10))
	if err != nil {
		return 0, "", err
	}
	if size != expected {
		return 0, "", errors.New("Exam export member size mismatch")
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

type examExportBoundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *examExportBoundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("Exam export archive exceeds size limit")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func (c *Content) verifyExamExport(ctx context.Context, key string, expected *app.ExamExportArchiveContent) (bool, error) {
	file, err := c.filesystem.Open(ctx, key, vfspkg.OpenOptions{})
	if err != nil {
		return false, err
	}
	defer file.Body.Close()
	if file.Info.Size != expected.SizeBytes {
		return false, nil
	}
	hash := sha256.New()
	size, err := io.CopyBuffer(hash, io.LimitReader(workReader{ctx: ctx, reader: file.Body}, expected.SizeBytes+1), make([]byte, 32<<10))
	return err == nil && size == expected.SizeBytes && hex.EncodeToString(hash.Sum(nil)) == expected.SHA256, err
}

func (c *Content) OpenExamExport(ctx context.Context, id model.ExamExportID, attempt model.JobAttemptID) (io.ReadCloser, error) {
	if c == nil || c.filesystem == nil || !id.IsValid() || !attempt.IsValid() {
		return nil, errors.New("invalid Exam export identity")
	}
	file, err := c.filesystem.Open(ctx, examExportKey(id, attempt), vfspkg.OpenOptions{})
	if err != nil {
		return nil, sanitize("open Exam export archive", err)
	}
	return file.Body, nil
}

func (c *Content) PurgeExamExport(ctx context.Context, id model.ExamExportID, attempt model.JobAttemptID) error {
	if c == nil || c.filesystem == nil || !id.IsValid() || !attempt.IsValid() {
		return errors.New("invalid Exam export identity")
	}
	key := examExportKey(id, attempt)
	if err := c.filesystem.Remove(ctx, key, vfspkg.RemoveOptions{}); err != nil && !errors.Is(err, vfspkg.ErrNotFound) {
		return sanitize("remove Exam export archive", err)
	}
	_, err := c.filesystem.Stat(ctx, key)
	if errors.Is(err, vfspkg.ErrNotFound) {
		return nil
	}
	if err != nil {
		return sanitize("observe Exam export absence", err)
	}
	return errors.New("Exam export archive remains present")
}

func examExportKey(id model.ExamExportID, attempt model.JobAttemptID) string {
	return fmt.Sprintf("exam-exports/%s/%s.zip", id, attempt)
}
