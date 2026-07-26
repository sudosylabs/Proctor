package s3_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/packages/vfs/vfstest"
)

var integrationSequence atomic.Uint64

func TestIntegrationConformance(t *testing.T) {
	endpoint := os.Getenv("VFS_S3_ENDPOINT")
	bucket := os.Getenv("VFS_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set VFS_S3_ENDPOINT and VFS_S3_BUCKET to run S3 conformance tests")
	}
	secure, err := strconv.ParseBool(defaultValue(os.Getenv("VFS_S3_SECURE"), "true"))
	if err != nil {
		t.Fatalf("parse VFS_S3_SECURE: %v", err)
	}

	vfstest.Run(t, func(t *testing.T) vfs.FileSystem {
		t.Helper()
		prefix := fmt.Sprintf(
			"vfstest/%d/%d",
			time.Now().UnixNano(),
			integrationSequence.Add(1),
		)
		filesystem, err := s3.New(s3.Config{
			Endpoint:     endpoint,
			AccessKey:    os.Getenv("VFS_S3_ACCESS_KEY"),
			SecretKey:    os.Getenv("VFS_S3_SECRET_KEY"),
			SessionToken: os.Getenv("VFS_S3_SESSION_TOKEN"),
			Bucket:       bucket,
			Prefix:       prefix,
			Region:       os.Getenv("VFS_S3_REGION"),
			Secure:       secure,
		})
		if err != nil {
			t.Fatalf("new S3 filesystem: %v", err)
		}
		t.Cleanup(func() {
			cleanup(t, filesystem)
		})
		return filesystem
	})
}

func cleanup(t *testing.T, filesystem vfs.FileSystem) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cursor := ""
	for {
		page, err := filesystem.List(ctx, vfs.ListOptions{Cursor: cursor})
		if err != nil {
			t.Errorf("list S3 test cleanup: %v", err)
			return
		}
		for _, entry := range page.Entries {
			if entry.IsDir {
				continue
			}
			if err := filesystem.Remove(ctx, entry.Path, vfs.RemoveOptions{}); err != nil {
				t.Errorf("remove S3 test object %q: %v", entry.Path, err)
			}
		}
		if page.NextCursor == "" {
			return
		}
		cursor = page.NextCursor
	}
}

func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
