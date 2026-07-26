package s3

import (
	"errors"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/sudosylabs/proctor/packages/vfs"
)

func TestNormalizeStoragePrefix(t *testing.T) {
	tests := map[string]string{
		"":          "",
		"schools":   "schools/",
		"schools/":  "schools/",
		"/schools/": "schools/",
		"a/./b":     "a/b/",
	}
	for input, expected := range tests {
		got, err := normalizeStoragePrefix(input)
		if err != nil {
			t.Fatalf("normalize %q: %v", input, err)
		}
		if got != expected {
			t.Fatalf("normalize %q: got %q, expected %q", input, got, expected)
		}
	}
	if _, err := normalizeStoragePrefix("../escape"); !errors.Is(err, vfs.ErrInvalidPath) {
		t.Fatalf("expected invalid path, got %v", err)
	}
}

func TestRevision(t *testing.T) {
	if got := revision("version-id", "etag"); got != "version:version-id" {
		t.Fatalf("version revision: %q", got)
	}
	if got := revision("", `"etag"`); got != "etag:etag" {
		t.Fatalf("etag revision: %q", got)
	}
	kind, value, ok := parseRevision("etag:value")
	if !ok || kind != "etag" || value != "value" {
		t.Fatalf("parse revision: %q %q %v", kind, value, ok)
	}
}

func TestFilesystemPrefixAndCapabilities(t *testing.T) {
	client, err := minio.New("localhost:9000", &minio.Options{
		Creds:  credentials.NewStaticV4("access", "secret", ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	filesystem, err := NewWithClient(client, "bucket", "tenant/files")
	if err != nil {
		t.Fatalf("new filesystem: %v", err)
	}
	if got := filesystem.key("answer.txt"); got != "tenant/files/answer.txt" {
		t.Fatalf("key: %q", got)
	}
	capabilities := filesystem.Capabilities()
	if capabilities.AtomicMove || capabilities.ConditionalWrite || !capabilities.RangeRead {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
}
