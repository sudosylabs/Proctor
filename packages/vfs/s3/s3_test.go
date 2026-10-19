package s3

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

func TestPutObjectOptionsBoundUnsizedPartSize(t *testing.T) {
	t.Parallel()

	if got := putObjectOptions(-1).PartSize; got != unsizedPartSize {
		t.Fatalf("unsized PartSize = %d, want %d", got, unsizedPartSize)
	}
	if got := putObjectOptions(0).PartSize; got != 0 {
		t.Fatalf("empty sized PartSize = %d, want MinIO default", got)
	}
	if got := putObjectOptions(1 << 40).PartSize; got != 0 {
		t.Fatalf("sized PartSize = %d, want MinIO default", got)
	}
}

func TestWriteWithoutSizeUploadsBoundedParts(t *testing.T) {
	t.Parallel()

	var (
		mu          sync.Mutex
		largestBody int64
		parts       int
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		written, _ := io.Copy(io.Discard, request.Body)
		if decoded, err := strconv.ParseInt(request.Header.Get("X-Amz-Decoded-Content-Length"), 10, 64); err == nil {
			// Streaming signatures add per-chunk framing to the wire body.
			written = decoded
		}
		mu.Lock()
		largestBody = max(largestBody, written)
		mu.Unlock()
		query := request.URL.Query()
		switch {
		case request.Method == http.MethodPost && query.Has("uploads"):
			_, _ = io.WriteString(response, `<InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>answer.bin</Key><UploadId>upload</UploadId></InitiateMultipartUploadResult>`)
		case request.Method == http.MethodPut && query.Has("partNumber"):
			mu.Lock()
			parts++
			mu.Unlock()
			response.Header().Set("ETag", `"part-`+query.Get("partNumber")+`"`)
		case request.Method == http.MethodPost && query.Has("uploadId"):
			_, _ = io.WriteString(response, `<CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>answer.bin</Key><ETag>"complete"</ETag></CompleteMultipartUploadResult>`)
		case request.Method == http.MethodPut:
			response.Header().Set("ETag", `"single"`)
		default:
			response.WriteHeader(http.StatusNotImplemented)
		}
	}))
	defer server.Close()

	client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4("access", "secret", ""),
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	filesystem, err := NewWithClient(client, "bucket", "")
	if err != nil {
		t.Fatalf("new filesystem: %v", err)
	}

	const size = unsizedPartSize + 1<<20
	body := io.LimitReader(zeroReader{}, size)
	if _, err := filesystem.Write(t.Context(), "answer.bin", body, vfs.WriteOptions{}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if parts != 2 {
		t.Fatalf("uploaded %d parts, want 2 bounded parts", parts)
	}
	if largestBody > unsizedPartSize {
		t.Fatalf("largest request body = %d bytes, want at most %d", largestBody, unsizedPartSize)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
