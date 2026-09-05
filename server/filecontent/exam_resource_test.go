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
	"strings"
	"testing"
	"time"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamResourceContentStoresAndOpensExactVerifiedBytes(t *testing.T) {
	validPDF := testExamResourcePDF(t, nil)
	for _, backend := range contentTestBackends() {
		backend := backend
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			if err != nil {
				t.Fatal(err)
			}
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
					if err != nil {
						t.Fatalf("StoreExamResource() error = %v", err)
					}
					if rendition.Name != "original" || rendition.MediaType != string(test.media) || rendition.Size != int64(len(test.body)) || len(rendition.SHA256) != 64 {
						t.Fatalf("rendition = %#v", rendition)
					}
					opened, err := content.OpenExamResource(context.Background(), revisionID, rendition.ID)
					if err != nil {
						t.Fatalf("OpenExamResource() error = %v", err)
					}
					got, readErr := io.ReadAll(opened)
					_ = opened.Close()
					if readErr != nil || !bytes.Equal(got, []byte(test.body)) {
						t.Fatalf("opened = %q error=%v", got, readErr)
					}
				})
			}
		})
	}
}

func TestExamResourceContentStoresAtPreallocatedRenditionIdentity(t *testing.T) {
	t.Parallel()

	for _, filesystem := range []vfspkg.FileSystem{memoryvfs.New(), &examResourceNonConditionalVFS{FileSystem: memoryvfs.New()}} {
		content, err := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
		if err != nil {
			t.Fatal(err)
		}
		revisionID, renditionID := model.NewFileRevisionID(), model.NewFileRenditionID()
		body := "corrected reference"
		rendition, err := content.StoreExamResourceRendition(context.Background(), revisionID, renditionID,
			model.ExamResourceMediaText, strings.NewReader(body), int64(len(body)), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if rendition.ID != renditionID || rendition.RevisionID != revisionID {
			t.Fatalf("rendition=%#v", rendition)
		}
		replayed, err := content.StoreExamResourceRendition(context.Background(), revisionID, renditionID,
			model.ExamResourceMediaText, strings.NewReader(body), int64(len(body)), rendition.CreatedAt)
		if err != nil || replayed != rendition {
			t.Fatalf("exact replay rendition=%#v error=%v", replayed, err)
		}
		if _, err = content.StoreExamResourceRendition(context.Background(), revisionID, renditionID,
			model.ExamResourceMediaText, strings.NewReader("different"), int64(len("different")), rendition.CreatedAt); err == nil {
			t.Fatal("mismatched bytes at preallocated identity unexpectedly succeeded")
		}
		opened, err := content.OpenExamResource(context.Background(), revisionID, renditionID)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(opened)
		_ = opened.Close()
		if readErr != nil || string(got) != body {
			t.Fatalf("opened=%q error=%v", got, readErr)
		}
	}
}

func TestExamResourceContentRejectsUnverifiedOrUnboundedInputBeforeStorage(t *testing.T) {
	t.Parallel()
	filesystem := memoryvfs.New()
	content, _ := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
	pseudoPDF := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	compressedActivePDF := testExamResourcePDF(t, func(xRefTable *pdfmodel.XRefTable, root types.Dict) {
		script, err := xRefTable.NewStreamDictForBuf([]byte("app.alert('unsafe')"))
		if err != nil {
			t.Fatal(err)
		}
		if err = script.Encode(); err != nil {
			t.Fatal(err)
		}
		scriptRef, err := xRefTable.IndRefForNewObject(*script)
		if err != nil {
			t.Fatal(err)
		}
		action := types.Dict{"Type": types.Name("Action"), "S": types.Name("JavaScript"), "JS": *scriptRef}
		ref, err := xRefTable.IndRefForNewObject(action)
		if err != nil {
			t.Fatal(err)
		}
		root.Insert("OpenAction", *ref)
	})
	if bytes.Contains(compressedActivePDF, []byte("app.alert('unsafe')")) {
		t.Fatal("active-content fixture did not compress its script stream")
	}
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
			if !errors.Is(err, ErrInvalidExamResourceContent) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	page, err := filesystem.List(context.Background(), vfspkg.ListOptions{Prefix: "files/", Limit: 10})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("rejected content was stored: page=%#v error=%v", page, err)
	}
}

func TestExamResourceContentReadsAndWritesThroughBoundedBuffers(t *testing.T) {
	privateTemp := t.TempDir()
	t.Setenv("TMPDIR", privateTemp)
	content, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(2 << 20)
	reader := &boundedExamResourceReader{remaining: size, maximumRequest: 64 << 10}
	if _, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, reader, size, time.Now().UTC()); err != nil {
		t.Fatalf("StoreExamResource() error = %v", err)
	}
	if reader.maximumObserved > reader.maximumRequest {
		t.Fatalf("upload read buffer=%d, want <=%d", reader.maximumObserved, reader.maximumRequest)
	}
	entries, err := os.ReadDir(privateTemp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("private spool files remain after upload: %#v", entries)
	}
}

func TestExamResourceContentRemovesPrivateSpoolAfterEveryOutcome(t *testing.T) {
	for _, test := range []struct {
		name       string
		filesystem vfspkg.FileSystem
		body       string
		want       func(error) bool
	}{
		{name: "validation rejection", filesystem: memoryvfs.New(), body: "\x00", want: func(err error) bool { return errors.Is(err, ErrInvalidExamResourceContent) }},
		{name: "uncertain exact VFS write", filesystem: &examResourceWriteFailureVFS{FileSystem: memoryvfs.New()}, body: "notes", want: func(err error) bool { return err == nil }},
		{name: "uncertain mismatched VFS write", filesystem: &examResourceWriteFailureVFS{FileSystem: memoryvfs.New(), mismatch: true}, body: "notes", want: IsUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			privateTemp := t.TempDir()
			t.Setenv("TMPDIR", privateTemp)
			content, err := New(test.filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader(test.body), int64(len(test.body)), time.Now().UTC())
			if !test.want(err) {
				t.Fatalf("StoreExamResource() error = %v", err)
			}
			entries, err := os.ReadDir(privateTemp)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("private spool files remain after outcome: %#v", entries)
			}
		})
	}
}

func TestExamResourceContentRejectsDeclaredOversizeWithoutReading(t *testing.T) {
	t.Parallel()
	content, _ := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	reader := &panicReader{}
	_, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, reader, model.ExamResourceMaximumBytes+1, time.Now().UTC())
	if !errors.Is(err, ErrInvalidExamResourceContent) {
		t.Fatalf("error = %v", err)
	}
}

func TestExamResourceContentStopsAtFirstByteBeyondDeclaredSize(t *testing.T) {
	privateTemp := t.TempDir()
	t.Setenv("TMPDIR", privateTemp)
	filesystem := &contentWorkVFS{FileSystem: memoryvfs.New()}
	content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const inputSize = 64 << 10
	for _, declared := range []int64{0, 5, examResourceCopyBuffer} {
		body := &boundedExamResourceReader{remaining: inputSize, maximumRequest: examResourceCopyBuffer}
		rendition, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, body, declared, time.Unix(1, 0))
		if !errors.Is(err, ErrInvalidExamResourceContent) || rendition != (model.FileRendition{}) {
			t.Fatalf("declared=%d: rendition=%#v error=%v", declared, rendition, err)
		}
		if consumed := inputSize - body.remaining; consumed != declared+1 {
			t.Fatalf("declared=%d: consumed=%d, want first excess byte only", declared, consumed)
		}
	}
	if filesystem.writes.Load() != 0 {
		t.Fatal("mismatched bodies reached storage")
	}
	assertExamResourceSpoolRemoved(t, privateTemp)
	if _, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaText, strings.NewReader("notes"), 5, time.Unix(1, 0)); err != nil {
		t.Fatalf("capacity was not released after size rejection: %v", err)
	}
}

func TestExamResourceValidationObservesCancellationBetweenBoundedReads(t *testing.T) {
	var imageBody bytes.Buffer
	if err := png.Encode(&imageBody, image.NewNRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
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
			body := &cancelledValidationReader{ReadSeeker: strings.NewReader(test.body), cancel: cancel, cancelPass: test.cancelPass, pass: 1}
			err := validateExamResource(ctx, body, test.media, int64(len(test.body)))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want cancellation", err)
			}
			if body.cancelledReads != 1 || body.cancelledBytes > examResourceCopyBuffer || body.afterCancelReads != 0 || body.afterCancelSeeks != 0 {
				t.Fatalf("cancellation boundary: reads=%d bytes=%d later reads=%d later seeks=%d", body.cancelledReads, body.cancelledBytes, body.afterCancelReads, body.afterCancelSeeks)
			}
		})
	}
}

func TestExamResourceUTF8SignaturePrefixKeepsConstantAllocations(t *testing.T) {
	allocations := func(runes int) float64 {
		body := `"` + strings.Repeat("😀", runes) + `"`
		return testing.AllocsPerRun(3, func() {
			if err := validateExamResourceUTF8(strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := allocations(8), allocations(16<<10)
	if large > small+1 {
		t.Fatalf("UTF-8 prefix allocations grew with content: small=%g large=%g", small, large)
	}
}

func TestExamResourceUTF8PreservesSignatureRejection(t *testing.T) {
	t.Parallel()
	for _, signature := range []string{"MZ", "PK\x03\x04", "\x7fELF", "#!"} {
		for _, leading := range []string{"", " \n\t", "\u2003"} {
			body := leading + signature + strings.Repeat("😀", 8)
			if err := validateExamResourceUTF8(strings.NewReader(body)); !errors.Is(err, ErrInvalidExamResourceContent) {
				t.Fatalf("signature=%q: error=%v", signature, err)
			}
		}
	}
	for _, body := range []string{"", " \u2003", "😀MZ", "😀PK\x03\x04", "😀\x7fELF", "😀#!"} {
		if err := validateExamResourceUTF8(strings.NewReader(body)); err != nil {
			t.Fatalf("ordinary UTF-8 content was rejected: %v", err)
		}
	}
}

func TestExamResourceContentCancellationReturnsNoRenditionAndReleasesResources(t *testing.T) {
	for _, outcome := range []string{"upload completed", "write succeeded", "write acknowledgement lost", "matching replay read", "mismatched replay metadata"} {
		t.Run(outcome, func(t *testing.T) {
			privateTemp := t.TempDir()
			t.Setenv("TMPDIR", privateTemp)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			filesystem := &cancelledExamResourceVFS{FileSystem: memoryvfs.New(), cancel: cancel, outcome: outcome}
			recorder := &contentWorkRecorder{}
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 1}, recorder)
			if err != nil {
				t.Fatal(err)
			}
			var body io.Reader = strings.NewReader("[0]")
			if outcome == "upload completed" {
				body = &cancelledExamResourceBody{ReadCloser: io.NopCloser(body), cancel: cancel}
			}
			rendition, err := content.StoreExamResource(ctx, model.NewFileRevisionID(), model.ExamResourceMediaJSON, body, 3, time.Unix(1, 0))
			if !errors.Is(err, context.Canceled) || rendition != (model.FileRendition{}) {
				t.Fatalf("cancelled rendition=%#v error=%v", rendition, err)
			}
			if recorder.active.Load() != 0 || recorder.finished.Load() != 1 {
				t.Fatal("cancelled operation retained its processing permit")
			}
			assertExamResourceSpoolRemoved(t, privateTemp)
			filesystem.cancel = nil
			filesystem.outcome = "write succeeded"
			if _, err := content.StoreExamResource(context.Background(), model.NewFileRevisionID(), model.ExamResourceMediaJSON, strings.NewReader("[0]"), 3, time.Unix(1, 0)); err != nil {
				t.Fatalf("capacity was not released after cancellation: %v", err)
			}
		})
	}
}

func assertExamResourceSpoolRemoved(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("private spool remains: entries=%v error=%v", entries, err)
	}
}

type cancelledValidationReader struct {
	io.ReadSeeker
	cancel           context.CancelFunc
	cancelPass       int
	pass             int
	readInPass       bool
	cancelled        bool
	cancelledReads   int
	cancelledBytes   int
	afterCancelReads int
	afterCancelSeeks int
}

func (r *cancelledValidationReader) Read(body []byte) (int, error) {
	if r.cancelled {
		r.afterCancelReads++
	}
	r.readInPass = true
	n, err := r.ReadSeeker.Read(body)
	if r.pass == r.cancelPass {
		r.cancelledReads++
		r.cancelledBytes += n
		r.cancelled = true
		r.cancel()
	}
	return n, err
}

func (r *cancelledValidationReader) Seek(offset int64, whence int) (int64, error) {
	if r.cancelled {
		r.afterCancelSeeks++
	}
	if offset == 0 && whence == io.SeekStart && r.readInPass {
		r.pass++
		r.readInPass = false
	}
	return r.ReadSeeker.Seek(offset, whence)
}

type cancelledExamResourceVFS struct {
	vfspkg.FileSystem
	cancel  context.CancelFunc
	outcome string
}

func (f *cancelledExamResourceVFS) Write(ctx context.Context, path string, body io.Reader, options vfspkg.WriteOptions) (vfspkg.Info, error) {
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

func (f *cancelledExamResourceVFS) Open(ctx context.Context, path string, options vfspkg.OpenOptions) (*vfspkg.File, error) {
	file, err := f.FileSystem.Open(ctx, path, options)
	if err == nil && f.outcome == "matching replay read" {
		file.Body = &cancelledExamResourceBody{ReadCloser: file.Body, cancel: f.cancel}
	}
	if err == nil && f.outcome == "mismatched replay metadata" {
		file.Info.Size++
		f.cancel()
	}
	return file, err
}

type cancelledExamResourceBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelledExamResourceBody) Read(body []byte) (int, error) {
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
	vfspkg.FileSystem
	mismatch bool
}

type examResourceNonConditionalVFS struct{ vfspkg.FileSystem }

func (f *examResourceNonConditionalVFS) Capabilities() vfspkg.Capabilities {
	capabilities := f.FileSystem.Capabilities()
	capabilities.ConditionalWrite = false
	return capabilities
}

func (f *examResourceWriteFailureVFS) Write(ctx context.Context, path string, body io.Reader, options vfspkg.WriteOptions) (vfspkg.Info, error) {
	if f.mismatch {
		wrongSize := int64(len("wrong"))
		info, err := f.FileSystem.Write(ctx, path, strings.NewReader("wrong"), vfspkg.WriteOptions{Size: &wrongSize, NoOverwrite: options.NoOverwrite})
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
	if err != nil {
		t.Fatal(err)
	}
	root, err := xRefTable.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	page := pdfmodel.Page{MediaBox: types.RectForFormat("A4"), Fm: pdfmodel.FontMap{}, Buf: new(bytes.Buffer)}
	pdfcpu.CreateTestPageContent(page)
	if err = pdfcpu.AddPageTreeWithSamplePage(xRefTable, root, page); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(xRefTable, root)
	}
	conf := examResourcePDFConfiguration()
	conf.WriteObjectStream = true
	conf.WriteXRefStream = true
	path := filepath.Join(t.TempDir(), "resource.pdf")
	if err = pdfapi.CreatePDFFile(xRefTable, path, conf); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testExamResourceAttachmentPDF(t *testing.T) []byte {
	t.Helper()
	directory := t.TempDir()
	input := filepath.Join(directory, "input.pdf")
	output := filepath.Join(directory, "output.pdf")
	attachment := filepath.Join(directory, "answers.txt")
	if err := os.WriteFile(input, testExamResourcePDF(t, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachment, []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pdfapi.AddAttachmentsFile(input, output, []string{attachment}, false, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
