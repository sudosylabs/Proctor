package s3_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	ensureIntegrationBucket(t, endpoint, bucket, secure)

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
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanup(t, filesystem)
		})
		return filesystem
	})
}

func ensureIntegrationBucket(t *testing.T, endpoint, bucket string, secure bool) {
	t.Helper()
	if os.Getenv("VFS_S3_CREATE_BUCKET") != "true" {
		return
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(
			os.Getenv("VFS_S3_ACCESS_KEY"), os.Getenv("VFS_S3_SECRET_KEY"), os.Getenv("VFS_S3_SESSION_TOKEN"),
		),
		Secure: secure,
		Region: os.Getenv("VFS_S3_REGION"),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exists, err := client.BucketExists(ctx, bucket)
	require.NoError(t, err)
	if !exists {
		require.NoError(t, client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: os.Getenv("VFS_S3_REGION")}))
	}
}

func cleanup(t *testing.T, filesystem vfs.FileSystem) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cursor := ""
	for {
		page, err := filesystem.List(ctx, vfs.ListOptions{Cursor: cursor})
		if !assert.NoError(t, err) {
			return
		}
		for _, entry := range page.Entries {
			if entry.IsDir {
				continue
			}
			assert.NoError(t, filesystem.Remove(ctx, entry.Path, vfs.RemoveOptions{}), "remove S3 test object %q", entry.Path)
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
