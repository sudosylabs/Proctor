//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	s3vfs "github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/server/model"
)

func TestFilePurgeAdapterOnS3(t *testing.T) {
	endpoint, bucket := os.Getenv("VFS_S3_ENDPOINT"), os.Getenv("VFS_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set VFS_S3_ENDPOINT and VFS_S3_BUCKET to run S3 purge integration")
	}
	secure, err := strconv.ParseBool(environmentDefault(os.Getenv("VFS_S3_SECURE"), "true"))
	if err != nil {
		t.Fatal(err)
	}
	filesystem, err := s3vfs.New(s3vfs.Config{Endpoint: endpoint, AccessKey: os.Getenv("VFS_S3_ACCESS_KEY"), SecretKey: os.Getenv("VFS_S3_SECRET_KEY"), SessionToken: os.Getenv("VFS_S3_SESSION_TOKEN"), Bucket: bucket, Region: os.Getenv("VFS_S3_REGION"), Secure: secure, Prefix: fmt.Sprintf("proctor-file-purge/%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	adapter := fileContentAdapter{filesystem: filesystem}
	revisionID := model.NewFileRevisionID()
	for range 2 {
		body := []byte("partial")
		size := int64(len(body))
		if _, err = filesystem.Write(context.Background(), profilePictureRenditionPath(revisionID, model.NewFileRenditionID()), bytes.NewReader(body), vfspkg.WriteOptions{Size: &size, NoOverwrite: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err = adapter.RemoveFileRevisionContent(context.Background(), revisionID, nil); err != nil {
		t.Fatal(err)
	}
	if err = adapter.RemoveFileRevisionContent(context.Background(), revisionID, nil); err != nil {
		t.Fatal(err)
	}
}

func environmentDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
