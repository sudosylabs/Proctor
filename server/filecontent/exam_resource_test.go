// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamResourceContentStoresAndOpensExactVerifiedBytes(t *testing.T) {
	validPDF := testExamResourcePDF(t, nil)
	for _, backend := range contentTestBackends() {
		backend := backend
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			for _, test := range []struct {
				name  string
				media model.ExamResourceMediaType
				body  string
			}{
				{"text", model.ExamResourceMediaText, "hello, student\n"},
				{"empty text", model.ExamResourceMediaText, ""},
				{"markdown", model.ExamResourceMediaMarkdown, "# Reference\n"},
				{"csv", model.ExamResourceMediaCSV, "name,value\nalpha,1\n"},
				{"json", model.ExamResourceMediaJSON, `{"language":"go"}`},
				{"authored json", model.ExamResourceMediaJSON, " \n" + `{"n":1e999999,"duplicate":1,"duplicate":2,"escaped":"\ud800"}` + "\n"},
				{"pdf", model.ExamResourceMediaPDF, string(validPDF)},
			} {
				t.Run(test.name, func(t *testing.T) {
					revisionID := model.NewFileRevisionID()
					rendition, err := content.StoreExamResource(context.Background(), revisionID, test.media, strings.NewReader(test.body), int64(len(test.body)), time.Now().UTC())
					require.NoError(t, err)
					require.Equal(t, "original", rendition.Name)
					require.Equal(t, string(test.media), rendition.MediaType)
					require.Equal(t, int64(len(test.body)), rendition.Size)
					require.Len(t, rendition.SHA256, 64)
					opened, err := content.OpenExamResource(context.Background(), revisionID, rendition.ID)
					require.NoError(t, err)
					got, readErr := io.ReadAll(opened)
					_ = opened.Close()
					require.NoError(t, readErr, "opened = %q error", got)
					require.True(t, bytes.Equal(got, []byte(test.body)), "opened = %q error", got)
				})
			}
		})
	}
}

func TestExamResourceContentStoresAtPreallocatedRenditionIdentity(t *testing.T) {
	t.Parallel()

	for _, filesystem := range []vfs.FileSystem{memory.New(), &examResourceNonConditionalVFS{FileSystem: memory.New()}} {
		content, err := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
		require.NoError(t, err)
		revisionID, renditionID := model.NewFileRevisionID(), model.NewFileRenditionID()
		body := "corrected reference"
		rendition, err := content.StoreExamResourceRendition(context.Background(), revisionID, renditionID,
			model.ExamResourceMediaText, strings.NewReader(body), int64(len(body)), time.Now().UTC())
		require.NoError(t, err)
		require.Equal(t, renditionID, rendition.ID)
		require.Equal(t, revisionID, rendition.RevisionID)
		replayed, err := content.StoreExamResourceRendition(context.Background(), revisionID, renditionID,
			model.ExamResourceMediaText, strings.NewReader(body), int64(len(body)), rendition.CreatedAt)
		require.NoError(t, err, "exact replay rendition")
		require.Equal(t, rendition, replayed, "exact replay rendition")
		_, err = content.StoreExamResourceRendition(context.Background(), revisionID, renditionID,
			model.ExamResourceMediaText, strings.NewReader("different"), int64(len("different")), rendition.CreatedAt)
		require.Error(t, err, "mismatched bytes at preallocated identity unexpectedly succeeded")
		opened, err := content.OpenExamResource(context.Background(), revisionID, renditionID)
		require.NoError(t, err)
		got, readErr := io.ReadAll(opened)
		_ = opened.Close()
		require.NoError(t, readErr, "opened")
		require.Equal(t, body, string(got), "opened")
	}
}

func TestExamResourceContentRejectsUnverifiedOrUnboundedInputBeforeStorage(t *testing.T) {
	t.Parallel()
	filesystem := memory.New()
	content, _ := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
	pseudoPDF := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	compressedActivePDF := testExamResourcePDF(t, func(xRefTable *pdfmodel.XRefTable, root types.Dict) {
		script, err := xRefTable.NewStreamDictForBuf([]byte("app.alert('unsafe')"))
		require.NoError(t, err)
		err = script.Encode()
		require.NoError(t, err)
		scriptRef, err := xRefTable.IndRefForNewObject(*script)
		require.NoError(t, err)
		action := types.Dict{"Type": types.Name("Action"), "S": types.Name("JavaScript"), "JS": *scriptRef}
		ref, err := xRefTable.IndRefForNewObject(action)
		require.NoError(t, err)
		root.Insert("OpenAction", *ref)
	})
	require.False(t, bytes.Contains(compressedActivePDF, []byte("app.alert('unsafe')")), "active-content fixture did not compress its script stream")
	attachmentPDF := testExamResourceAttachmentPDF(t)
	tests := []struct {
		name     string
		media    model.ExamResourceMediaType
		body     []byte
		declared int64
	}{
		{"executable", model.ExamResourceMediaText, []byte{'M', 'Z', 0, 1}, 4},
		{"archive", model.ExamResourceMediaText, []byte{'P', 'K', 3, 4}, 4},
		{"invalid utf8", model.ExamResourceMediaMarkdown, []byte{0xff}, 1},
		{"invalid json", model.ExamResourceMediaJSON, []byte(`{"x":`), 5},
		{"malformed pseudo pdf", model.ExamResourceMediaPDF, pseudoPDF, int64(len(pseudoPDF))},
		{"active pdf", model.ExamResourceMediaPDF, []byte("%PDF-1.7\n/JavaScript true\n%%EOF"), 31},
		{"escaped active pdf", model.ExamResourceMediaPDF, []byte("%PDF-1.7\n/J#53 true\n%%EOF"), 25},
		{"compressed active pdf", model.ExamResourceMediaPDF, compressedActivePDF, int64(len(compressedActivePDF))},
		{"attachment pdf", model.ExamResourceMediaPDF, attachmentPDF, int64(len(attachmentPDF))},
		{"size mismatch", model.ExamResourceMediaText, []byte("text"), 3},
		{"unsupported", model.ExamResourceMediaType("application/zip"), []byte("zip"), 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), test.media, bytes.NewReader(test.body), test.declared, time.Now().UTC())
			require.ErrorIs(t, err, ErrInvalidExamResourceContent)
		})
	}
	page, err := filesystem.List(context.Background(), vfs.ListOptions{Prefix: "files/", Limit: 10})
	require.NoError(t, err, "rejected content was stored: page=%#v error", page)
	require.Empty(t, page.Entries, "rejected content was stored: page=%#v error", page)
}

func TestExamResourceContentReadsAndWritesThroughBoundedBuffers(t *testing.T) {
	privateTemp := t.TempDir()
	t.Setenv("TMPDIR", privateTemp)
	content, err := New(memory.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	const size = int64(2 << 20)
	reader := &boundedExamResourceReader{remaining: size, maximumRequest: 64 << 10}
	_, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, reader, size, time.Now().UTC())
	require.NoError(t, err)
	require.LessOrEqual(t, reader.maximumObserved, reader.maximumRequest, "upload read buffer")
	entries, err := os.ReadDir(privateTemp)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestExamResourceContentRemovesPrivateSpoolAfterEveryOutcome(t *testing.T) {
	for _, test := range []struct {
		name       string
		filesystem vfs.FileSystem
		body       string
		want       func(error) bool
	}{
		{name: "validation rejection", filesystem: memory.New(), body: "\x00", want: func(err error) bool { return errors.Is(err, ErrInvalidExamResourceContent) }},
		{name: "uncertain exact VFS write", filesystem: &examResourceWriteFailureVFS{FileSystem: memory.New()}, body: "notes", want: func(err error) bool { return err == nil }},
		{name: "uncertain mismatched VFS write", filesystem: &examResourceWriteFailureVFS{FileSystem: memory.New(), mismatch: true}, body: "notes", want: IsUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			privateTemp := t.TempDir()
			t.Setenv("TMPDIR", privateTemp)
			content, err := New(test.filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			_, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader(test.body), int64(len(test.body)), time.Now().UTC())
			require.True(t, test.want(err), "StoreExamResource() error")
			entries, err := os.ReadDir(privateTemp)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestExamResourceContentRejectsDeclaredOversizeWithoutReading(t *testing.T) {
	t.Parallel()
	content, _ := New(memory.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	reader := &panicReader{}
	_, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, reader, model.ExamResourceMaximumBytes+1, time.Now().UTC())
	require.ErrorIs(t, err, ErrInvalidExamResourceContent)
}

func TestExamResourceContentStopsAtFirstByteBeyondDeclaredSize(t *testing.T) {
	privateTemp := t.TempDir()
	t.Setenv("TMPDIR", privateTemp)
	filesystem := &contentWorkVFS{FileSystem: memory.New()}
	content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, nil)
	require.NoError(t, err)
	const inputSize = 64 << 10
	for _, declared := range []int64{0, 5, examResourceCopyBuffer} {
		body := &boundedExamResourceReader{remaining: inputSize, maximumRequest: examResourceCopyBuffer}
		rendition, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, body, declared, time.Unix(1, 0))
		require.ErrorIs(t, err, ErrInvalidExamResourceContent, "declared=%d: rendition=%#v error", declared, rendition)
		require.Equal(t, model.FileRendition{}, rendition, "declared=%d: rendition=%#v error", declared, rendition)
		consumed := inputSize - body.remaining
		require.Equal(t, declared+1, consumed, "declared=%d: consumed", declared)
	}
	require.Equal(t, int64(0), filesystem.writes.Load(), "mismatched bodies reached storage")
	assertExamResourceSpoolRemoved(t, privateTemp)
	_, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader("notes"), 5, time.Unix(1, 0))
	require.NoError(t, err)
}

func TestExamResourceValidationObservesCancellationBetweenBoundedReads(t *testing.T) {
	var imageBody bytes.Buffer
	require.NoError(t, png.Encode(&imageBody, image.NewNRGBA(image.Rect(0, 0, 16, 16))))
	for _, test := range []struct {
		name       string
		media      model.ExamResourceMediaType
		body       string
		cancelPass int
	}{
		{"text", model.ExamResourceMediaText, strings.Repeat("a", 64<<10), 1},
		{"markdown", model.ExamResourceMediaMarkdown, strings.Repeat("a", 64<<10), 1},
		{"json UTF-8", model.ExamResourceMediaJSON, `"` + strings.Repeat("a", 64<<10) + `"`, 1},
		{"json string syntax", model.ExamResourceMediaJSON, `"` + strings.Repeat("a", 64<<10) + `"`, 2},
		{"json number syntax", model.ExamResourceMediaJSON, "1" + strings.Repeat("0", 64<<10), 2},
		{"json array syntax", model.ExamResourceMediaJSON, "[" + strings.Repeat("0,", 32<<10) + "0]", 2},
		{"json trailing whitespace", model.ExamResourceMediaJSON, "null" + strings.Repeat(" ", 64<<10), 2},
		{"CSV UTF-8", model.ExamResourceMediaCSV, strings.Repeat("name,value\n", 8<<10), 1},
		{"CSV records", model.ExamResourceMediaCSV, strings.Repeat("name,value\n", 8<<10), 2},
		{"image configuration", model.ExamResourceMediaPNG, imageBody.String(), 1},
		{"image decode", model.ExamResourceMediaPNG, imageBody.String(), 2},
		{"PDF read", model.ExamResourceMediaPDF, string(testExamResourcePDF(t, nil)), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &canceledValidationReader{ReadSeeker: strings.NewReader(test.body), cancel: cancel, cancelPass: test.cancelPass, pass: 1}
			err := validateExamResource(ctx, body, test.media, int64(len(test.body)))
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, 1, body.canceledReads, "cancellation boundary: reads")
			require.LessOrEqual(t, body.canceledBytes, examResourceCopyBuffer, "cancellation boundary: reads")
			require.Equal(t, 0, body.afterCancelReads, "cancellation boundary: reads")
			require.Equal(t, 0, body.afterCancelSeeks, "cancellation boundary: reads")
		})
	}
}

func TestExamResourceUTF8SignaturePrefixKeepsConstantAllocations(t *testing.T) {
	allocations := func(runes int) float64 {
		body := `"` + strings.Repeat("😀", runes) + `"`
		return testing.AllocsPerRun(3, func() {
			require.NoError(t, validateExamResourceUTF8(strings.NewReader(body)))
		})
	}
	small, large := allocations(8), allocations(16<<10)
	require.LessOrEqual(t, large, small+1, "UTF-8 prefix allocations grew with content: small=%g large", small)
}

func TestExamResourceUTF8PreservesSignatureRejection(t *testing.T) {
	t.Parallel()
	for _, signature := range []string{"MZ", "PK\x03\x04", "\x7fELF", "#!"} {
		for _, leading := range []string{"", " \n\t", "\u2003"} {
			body := leading + signature + strings.Repeat("😀", 8)
			require.ErrorIs(t, validateExamResourceUTF8(strings.NewReader(body)), ErrInvalidExamResourceContent, "signature=%q: error", signature)
		}
	}
	for _, body := range []string{"", " \u2003", "😀MZ", "😀PK\x03\x04", "😀\x7fELF", "😀#!"} {
		require.NoError(t, validateExamResourceUTF8(strings.NewReader(body)))
	}
}

func TestExamResourceContentCancellationReturnsNoRenditionAndReleasesResources(t *testing.T) {
	for _, outcome := range []string{"upload completed", "write succeeded", "write acknowledgement lost", "matching replay read", "mismatched replay metadata"} {
		t.Run(outcome, func(t *testing.T) {
			privateTemp := t.TempDir()
			t.Setenv("TMPDIR", privateTemp)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			filesystem := &canceledExamResourceVFS{FileSystem: memory.New(), cancel: cancel, outcome: outcome}
			recorder := &contentWorkRecorder{}
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, recorder)
			require.NoError(t, err)
			var body io.Reader = strings.NewReader("[0]")
			if outcome == "upload completed" {
				body = &canceledExamResourceBody{ReadCloser: io.NopCloser(body), cancel: cancel}
			}
			rendition, err := content.StoreExamResource(ctx, model.NewFileRevisionID(), model.ExamResourceMediaJSON, body, 3, time.Unix(1, 0))
			require.ErrorIs(t, err, context.Canceled, "canceled rendition")
			require.Equal(t, model.FileRendition{}, rendition, "canceled rendition")
			require.Equal(t, int64(0), recorder.active.Load(), "canceled operation retained its processing permit")
			require.Equal(t, int64(1), recorder.finished.Load(), "canceled operation retained its processing permit")
			assertExamResourceSpoolRemoved(t, privateTemp)
			filesystem.cancel = nil
			filesystem.outcome = "write succeeded"
			_, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaJSON, strings.NewReader("[0]"), 3, time.Unix(1, 0))
			require.NoError(t, err)
		})
	}
}

func assertExamResourceSpoolRemoved(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err, "private spool remains: entries")
	require.Empty(t, entries, "private spool remains: entries")
}

type canceledValidationReader struct {
	io.ReadSeeker
	cancel           context.CancelFunc
	cancelPass       int
	pass             int
	readInPass       bool
	canceled         bool
	canceledReads    int
	canceledBytes    int
	afterCancelReads int
	afterCancelSeeks int
}

func (r *canceledValidationReader) Read(body []byte) (int, error) {
	if r.canceled {
		r.afterCancelReads++
	}
	r.readInPass = true
	n, err := r.ReadSeeker.Read(body)
	if r.pass == r.cancelPass {
		r.canceledReads++
		r.canceledBytes += n
		r.canceled = true
		r.cancel()
	}
	return n, err
}

func (r *canceledValidationReader) Seek(offset int64, whence int) (int64, error) {
	if r.canceled {
		r.afterCancelSeeks++
	}
	if offset == 0 && whence == io.SeekStart && r.readInPass {
		r.pass++
		r.readInPass = false
	}
	return r.ReadSeeker.Seek(offset, whence)
}

type canceledExamResourceVFS struct {
	vfs.FileSystem
	cancel  context.CancelFunc
	outcome string
}

func (f *canceledExamResourceVFS) Write(ctx context.Context, path string, body io.Reader, options vfs.WriteOptions) (vfs.Info, error) {
	info, err := f.FileSystem.Write(ctx, path, body, options)
	if err != nil {
		return info, err
	}
	if f.cancel != nil && f.outcome != "matching replay read" && f.outcome != "mismatched replay metadata" {
		f.cancel()
	}
	if f.outcome != "write succeeded" {
		return info, errors.New("write acknowledgement lost")
	}
	return info, nil
}

func (f *canceledExamResourceVFS) Open(ctx context.Context, path string, options vfs.OpenOptions) (*vfs.File, error) {
	file, err := f.FileSystem.Open(ctx, path, options)
	if err == nil && f.outcome == "matching replay read" {
		file.Body = &canceledExamResourceBody{ReadCloser: file.Body, cancel: f.cancel}
	}
	if err == nil && f.outcome == "mismatched replay metadata" {
		file.Info.Size++
		f.cancel()
	}
	return file, err
}

type canceledExamResourceBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *canceledExamResourceBody) Read(body []byte) (int, error) {
	n, err := b.ReadCloser.Read(body)
	b.cancel()
	return n, err
}

type panicReader struct{}

func (*panicReader) Read([]byte) (int, error) { panic("oversized declared content was read") }

type boundedExamResourceReader struct {
	remaining       int64
	maximumRequest  int
	maximumObserved int
}

type examResourceWriteFailureVFS struct {
	vfs.FileSystem
	mismatch bool
}

type examResourceNonConditionalVFS struct{ vfs.FileSystem }

func (f *examResourceNonConditionalVFS) Capabilities() vfs.Capabilities {
	capabilities := f.FileSystem.Capabilities()
	capabilities.ConditionalWrite = false
	return capabilities
}

func (f *examResourceWriteFailureVFS) Write(ctx context.Context, path string, body io.Reader, options vfs.WriteOptions) (vfs.Info, error) {
	if f.mismatch {
		wrongSize := int64(len("wrong"))
		info, err := f.FileSystem.Write(ctx, path, strings.NewReader("wrong"), vfs.WriteOptions{Size: &wrongSize, NoOverwrite: options.NoOverwrite})
		if err != nil {
			return info, err
		}
		return info, errors.New("write acknowledgement lost")
	}
	info, err := f.FileSystem.Write(ctx, path, body, options)
	if err != nil {
		return info, err
	}
	return info, errors.New("write acknowledgement lost")
}

func (r *boundedExamResourceReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.maximumObserved {
		r.maximumObserved = len(buffer)
	}
	if len(buffer) > r.maximumRequest {
		return 0, errors.New("consumer requested an unbounded upload buffer")
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(buffer)), r.remaining)
	for index := int64(0); index < n; index++ {
		buffer[index] = 'a'
	}
	r.remaining -= n
	return int(n), nil
}

func testExamResourcePDF(t *testing.T, mutate func(*pdfmodel.XRefTable, types.Dict)) []byte {
	t.Helper()
	xRefTable, err := pdfcpu.CreateDemoXRef()
	require.NoError(t, err)
	root, err := xRefTable.Catalog()
	require.NoError(t, err)
	page := pdfmodel.Page{MediaBox: types.RectForFormat("A4"), Fm: pdfmodel.FontMap{}, Buf: new(bytes.Buffer)}
	pdfcpu.CreateTestPageContent(page)
	err = pdfcpu.AddPageTreeWithSamplePage(xRefTable, root, page)
	require.NoError(t, err)
	if mutate != nil {
		mutate(xRefTable, root)
	}
	conf := examResourcePDFConfiguration()
	conf.WriteObjectStream = true
	conf.WriteXRefStream = true
	path := filepath.Join(t.TempDir(), "resource.pdf")
	err = pdfapi.CreatePDFFile(xRefTable, path, conf)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

func testExamResourceAttachmentPDF(t *testing.T) []byte {
	t.Helper()
	directory := t.TempDir()
	input := filepath.Join(directory, "input.pdf")
	output := filepath.Join(directory, "output.pdf")
	attachment := filepath.Join(directory, "answers.txt")
	require.NoError(t, os.WriteFile(input, testExamResourcePDF(t, nil), 0o600))
	require.NoError(t, os.WriteFile(attachment, []byte("hidden"), 0o600))
	require.NoError(t, pdfapi.AddAttachmentsFile(input, output, []string{attachment}, false, nil))
	raw, err := os.ReadFile(output)
	require.NoError(t, err)
	return raw
}

func TestExamResourceContentSpoolHasNoNameDuringProcessing(t *testing.T) {
	privateTemp := t.TempDir()
	t.Setenv("TMPDIR", privateTemp)
	observer := &tempDirectoryObserverVFS{FileSystem: memory.New(), directory: privateTemp}
	content, err := New(observer, Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	_, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText,
		strings.NewReader("notes"), 5, time.Now().UTC())
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Empty(t, observer.observed)
	}
}
