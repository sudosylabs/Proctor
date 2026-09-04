//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	s3vfs "github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/server/filecontent"
	"github.com/sudosylabs/proctor/server/model"
)

func TestAttemptWorkspaceContentIntegrationOnLocalVFS(t *testing.T) {
	filesystem, err := localvfs.New(filepath.Join(t.TempDir(), "vfs"))
	if err != nil {
		t.Fatal(err)
	}
	proveAttemptWorkspaceContentConformance(t, filesystem)
}

func TestAttemptWorkspaceContentIntegrationOnS3(t *testing.T) {
	endpoint, bucket := os.Getenv("VFS_S3_ENDPOINT"), os.Getenv("VFS_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set VFS_S3_ENDPOINT and VFS_S3_BUCKET to run S3 Attempt Workspace content integration")
	}
	secure, err := strconv.ParseBool(environmentDefault(os.Getenv("VFS_S3_SECURE"), "true"))
	if err != nil {
		t.Fatal(err)
	}
	filesystem, err := s3vfs.New(s3vfs.Config{
		Endpoint: endpoint, AccessKey: os.Getenv("VFS_S3_ACCESS_KEY"), SecretKey: os.Getenv("VFS_S3_SECRET_KEY"),
		SessionToken: os.Getenv("VFS_S3_SESSION_TOKEN"), Bucket: bucket, Region: os.Getenv("VFS_S3_REGION"),
		Secure: secure, Prefix: fmt.Sprintf("proctor-attempt-workspace/%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatal(err)
	}
	proveAttemptWorkspaceContentConformance(t, filesystem)
}

func proveAttemptWorkspaceContentConformance(t *testing.T, filesystem vfspkg.FileSystem) {
	t.Helper()
	ctx := context.Background()
	content, err := filecontent.New(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	objectID, retainedID := model.NewAttemptWorkspaceObjectID(), model.NewAttemptWorkspaceObjectID()
	t.Cleanup(func() {
		_ = content.RemoveAttemptWorkspaceObject(context.Background(), objectID)
		_ = content.RemoveAttemptWorkspaceObject(context.Background(), retainedID)
	})

	body := []byte("package main\n")
	staged, err := content.StageAttemptWorkspaceObject(ctx, objectID, bytes.NewReader(body), int64(len(body)), "text/x-go")
	if err != nil {
		t.Fatalf("stage Attempt Workspace object: %v", err)
	}
	if staged.SizeBytes != int64(len(body)) || staged.MediaType != "text/x-go" ||
		staged.SHA256 != "df1d036cbbf3df46e2045071e082245ece204c7f53ecf0a4e022bff9bb228f47" {
		t.Fatalf("staged metadata = %#v", staged)
	}
	assertAttemptWorkspaceObjectBytes(t, content, objectID, body)

	replayed, err := content.StageAttemptWorkspaceObject(ctx, objectID, bytes.NewReader(body), int64(len(body)), "text/x-go")
	if err != nil || *replayed != *staged {
		t.Fatalf("exact stage replay = %#v, %v; want %#v", replayed, err, staged)
	}
	if _, err = content.StageAttemptWorkspaceObject(ctx, objectID, bytes.NewReader([]byte("package evil\n")),
		int64(len(body)), "text/x-go"); !filecontent.IsConflict(err) {
		t.Fatalf("mismatched stage error = %v, want storage conflict", err)
	}
	assertAttemptWorkspaceObjectBytes(t, content, objectID, body)

	retained := []byte("retained")
	if _, err = content.StageAttemptWorkspaceObject(ctx, retainedID, bytes.NewReader(retained), int64(len(retained)), "text/plain"); err != nil {
		t.Fatalf("stage retained object: %v", err)
	}
	if err = content.RemoveAttemptWorkspaceObject(ctx, objectID); err != nil {
		t.Fatalf("reclaim object: %v", err)
	}
	if err = content.RemoveAttemptWorkspaceObject(ctx, objectID); err != nil {
		t.Fatalf("repeat reclaim object: %v", err)
	}
	if opened, openErr := content.OpenAttemptWorkspaceObject(ctx, objectID); !filecontent.IsNotFound(openErr) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("reclaimed object open error = %v, want not found", openErr)
	}
	assertAttemptWorkspaceObjectBytes(t, content, retainedID, retained)
}

func assertAttemptWorkspaceObjectBytes(t *testing.T, content *filecontent.Content,
	objectID model.AttemptWorkspaceObjectID, want []byte,
) {
	t.Helper()
	opened, err := content.OpenAttemptWorkspaceObject(context.Background(), objectID)
	if err != nil {
		t.Fatalf("open Attempt Workspace object: %v", err)
	}
	got, readErr := io.ReadAll(opened)
	closeErr := opened.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("opened bytes = %q, read error = %v, close error = %v; want %q", got, readErr, closeErr, want)
	}
}
