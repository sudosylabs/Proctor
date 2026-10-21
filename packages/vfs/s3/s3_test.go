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
	"github.com/stretchr/testify/require"

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
		require.NoError(t, err, "normalize %q", input)
		require.Equal(t, expected, got, "normalize %q", input)
	}
	_, err := normalizeStoragePrefix("../escape")
	require.ErrorIs(t, err, vfs.ErrInvalidPath)
}

func TestRevision(t *testing.T) {
	require.Equal(t, "version:version-id", revision("version-id", "etag"))
	require.Equal(t, "etag:etag", revision("", `"etag"`))
	kind, value, ok := parseRevision("etag:value")
	require.True(t, ok, "parse revision")
	require.Equal(t, "etag", kind, "parse revision")
	require.Equal(t, "value", value, "parse revision")
}

func TestFilesystemPrefixAndCapabilities(t *testing.T) {
	client, err := minio.New("localhost:9000", &minio.Options{
		Creds:  credentials.NewStaticV4("access", "secret", ""),
		Secure: false,
	})
	require.NoError(t, err)
	filesystem, err := NewWithClient(client, "bucket", "tenant/files")
	require.NoError(t, err)
	require.Equal(t, "tenant/files/answer.txt", filesystem.key("answer.txt"))
	capabilities := filesystem.Capabilities()
	require.False(t, capabilities.AtomicMove)
	require.False(t, capabilities.ConditionalWrite)
	require.True(t, capabilities.RangeRead)
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
			require.NoError(t, err)
			filesystem, err := NewWithClient(client, "bucket", "")
			require.NoError(t, err)

			_, err = filesystem.Move(t.Context(), "source.txt", "destination.txt", vfs.TransferOptions{
				SourceRevision: revision,
			})
			require.ErrorIs(t, err, vfs.ErrUnsupported, "Move() error")
			require.Equal(t, 0, requests, "Move() made")
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPutObjectOptionsBoundUnsizedPartSize(t *testing.T) {
	t.Parallel()

	require.Equal(t, uint64(unsizedPartSize), putObjectOptions(-1).PartSize, "unsized PartSize")
	require.Equal(t, uint64(0), putObjectOptions(0).PartSize, "empty sized PartSize")
	require.Equal(t, uint64(0), putObjectOptions(1<<40).PartSize, "sized PartSize")
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
	require.NoError(t, err)
	filesystem, err := NewWithClient(client, "bucket", "")
	require.NoError(t, err)

	const size = unsizedPartSize + 1<<20
	body := io.LimitReader(zeroReader{}, size)
	_, err = filesystem.Write(t.Context(), "answer.bin", body, vfs.WriteOptions{})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 2, parts, "uploaded")
	require.LessOrEqual(t, largestBody, int64(unsizedPartSize), "largest request body")
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestCheckRootReportsMissingBucket(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		status int
		want   error
	}{
		{"present", http.StatusOK, nil},
		{"missing", http.StatusNotFound, vfs.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests []string
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				mu.Lock()
				requests = append(requests, request.Method+" "+request.URL.Path)
				mu.Unlock()
				response.WriteHeader(test.status)
			}))
			defer server.Close()
			client, err := minio.New(strings.TrimPrefix(server.URL, "http://"), &minio.Options{
				Creds:  credentials.NewStaticV4("access", "secret", ""),
				Region: "us-east-1",
			})
			require.NoError(t, err)
			filesystem, err := NewWithClient(client, "bucket", "tenant")
			require.NoError(t, err)

			err = filesystem.CheckRoot(t.Context())
			if test.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.want)
			}
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, []string{"HEAD /bucket/"}, requests, "CheckRoot requests")
		})
	}
}
