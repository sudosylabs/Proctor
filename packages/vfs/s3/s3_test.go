package s3

import (
	"errors"
	"net/http"
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

func TestMoveRejectsSourceRevisionBeforeNetworkIO(t *testing.T) {
	t.Parallel()

	for _, revision := range []string{"etag:original", "version:original", "invalid"} {
		t.Run(revision, func(t *testing.T) {
			t.Parallel()
			requests := 0
			client, err := minio.New("localhost:9000", &minio.Options{
				Creds:  credentials.NewStaticV4("access", "secret", ""),
				Region: "us-east-1",
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					requests++
					return nil, errors.New("unexpected network request")
				}),
			})
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			filesystem, err := NewWithClient(client, "bucket", "")
			if err != nil {
				t.Fatalf("new filesystem: %v", err)
			}

			_, err = filesystem.Move(t.Context(), "source.txt", "destination.txt", vfs.TransferOptions{
				SourceRevision: revision,
			})
			if !errors.Is(err, vfs.ErrUnsupported) {
				t.Fatalf("Move() error = %v, want ErrUnsupported", err)
			}
			if requests != 0 {
				t.Fatalf("Move() made %d network requests before rejecting the condition", requests)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
