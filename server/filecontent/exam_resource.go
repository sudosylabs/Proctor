// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/model"
)

type invalidExamResourceContentError struct{}

func (invalidExamResourceContentError) Error() string {
	return "file content: invalid exam resource content"
}
func (invalidExamResourceContentError) InvalidExamResourceContent() {}

var ErrInvalidExamResourceContent error = invalidExamResourceContentError{}

const (
	maximumExamResourcePixels = 25_000_000
	examResourceCopyBuffer    = 32 << 10
)

var disablePDFCPUConfigDir sync.Once

// StoreExamResource validates one complete bounded body and writes the exact
// authored bytes under an opaque immutable rendition identity. The returned
// metadata is safe to pass to the named SQL finalization operation; the VFS
// key remains private to File Content.
func (c *Content) StoreExamResource(ctx context.Context, revisionID model.FileRevisionID, mediaType model.ExamResourceMediaType, body io.Reader, declaredSize int64, at time.Time) (model.FileRendition, error) {
	return c.StoreExamResourceRendition(ctx, revisionID, model.NewFileRenditionID(), mediaType, body, declaredSize, at)
}

// StoreExamResourceRendition validates and writes exact authored bytes at a
// caller-preallocated opaque identity. Preallocation lets an idempotent Stage
// command resume after an unknown VFS or database commit outcome without
// discovering object keys or allocating a second rendition.
func (c *Content) StoreExamResourceRendition(ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID, mediaType model.ExamResourceMediaType, body io.Reader, declaredSize int64, at time.Time) (model.FileRendition, error) {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() || !renditionID.IsValid() || !mediaType.IsValid() || body == nil || declaredSize < 0 || declaredSize > model.ExamResourceMaximumBytes {
		return model.FileRendition{}, ErrInvalidExamResourceContent
	}

	spool, err := os.CreateTemp("", "proctor-exam-resource-*")
	if err != nil {
		return model.FileRendition{}, sanitize("create exam resource spool", err)
	}
	spoolPath := spool.Name()
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spoolPath)
	}()

	digest := sha256.New()
	limited := io.LimitReader(struct{ io.Reader }{body}, model.ExamResourceMaximumBytes+1)
	size, err := io.CopyBuffer(io.MultiWriter(spool, digest), limited, make([]byte, examResourceCopyBuffer))
	if err != nil || size != declaredSize || size > model.ExamResourceMaximumBytes {
		return model.FileRendition{}, ErrInvalidExamResourceContent
	}
	if err = validateExamResource(spool, mediaType, size); err != nil {
		return model.FileRendition{}, err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return model.FileRendition{}, sanitize("rewind exam resource spool", err)
	}

	checksum := fmt.Sprintf("%x", digest.Sum(nil))
	rendition, err := model.NewFileRendition(renditionID, revisionID, "original", string(mediaType), size, 0, 0, checksum, at)
	if err != nil {
		return model.FileRendition{}, ErrInvalidExamResourceContent
	}
	conditionalWrite := c.filesystem.Capabilities().ConditionalWrite
	if !conditionalWrite {
		matching, openErr := c.openMatchingExamResource(ctx, revisionID, renditionID, size, checksum)
		switch {
		case openErr == nil && matching:
			return *rendition, nil
		case openErr == nil:
			return model.FileRendition{}, sanitize("stage exam resource", vfspkg.ErrConflict)
		case !errors.Is(openErr, vfspkg.ErrNotFound):
			return model.FileRendition{}, sanitize("stage exam resource", openErr)
		}
	}
	_, err = c.filesystem.Write(ctx, examResourceRenditionKey(revisionID, renditionID), spool, vfspkg.WriteOptions{Size: &size, NoOverwrite: conditionalWrite})
	if err != nil {
		if existing, verifyErr := c.openMatchingExamResource(ctx, revisionID, renditionID, size, checksum); verifyErr == nil && existing {
			return *rendition, nil
		}
		return model.FileRendition{}, sanitize("stage exam resource", err)
	}
	return *rendition, nil
}

func (c *Content) openMatchingExamResource(ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID, size int64, checksum string) (bool, error) {
	file, err := c.filesystem.Open(ctx, examResourceRenditionKey(revisionID, renditionID), vfspkg.OpenOptions{})
	if err != nil {
		return false, err
	}
	defer file.Body.Close()
	if file.Info.Size != size {
		return false, nil
	}
	digest := sha256.New()
	read, err := io.Copy(digest, io.LimitReader(file.Body, model.ExamResourceMaximumBytes+1))
	if err != nil {
		return false, err
	}
	return read == size && fmt.Sprintf("%x", digest.Sum(nil)) == checksum, nil
}

// OpenExamResource opens only the exact rendition selected by authoritative
// Exam Resource metadata. It performs no discovery and exposes no object key.
func (c *Content) OpenExamResource(ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID) (io.ReadCloser, error) {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() || !renditionID.IsValid() {
		return nil, ErrInvalidExamResourceContent
	}
	file, err := c.filesystem.Open(ctx, examResourceRenditionKey(revisionID, renditionID), vfspkg.OpenOptions{})
	if err != nil {
		return nil, sanitize("open exam resource", err)
	}
	return file.Body, nil
}

func (c *Content) RemoveExamResource(ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID) error {
	if c == nil || c.filesystem == nil || !revisionID.IsValid() || !renditionID.IsValid() {
		return ErrInvalidExamResourceContent
	}
	err := c.filesystem.Remove(ctx, examResourceRenditionKey(revisionID, renditionID), vfspkg.RemoveOptions{})
	if errors.Is(err, vfspkg.ErrNotFound) {
		return nil
	}
	return sanitize("remove exam resource", err)
}

func examResourceRenditionKey(revisionID model.FileRevisionID, renditionID model.FileRenditionID) string {
	return revisionPrefix(revisionID) + renditionID.String() + ".resource"
}

func validateExamResource(file *os.File, mediaType model.ExamResourceMediaType, size int64) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return sanitize("rewind exam resource spool", err)
	}
	var err error
	switch mediaType {
	case model.ExamResourceMediaPDF:
		err = validateExamResourcePDF(file)
	case model.ExamResourceMediaPNG, model.ExamResourceMediaJPEG, model.ExamResourceMediaWebP:
		err = validateExamResourceImage(file, mediaType, size)
	case model.ExamResourceMediaJSON:
		err = validateExamResourceJSON(file)
	case model.ExamResourceMediaCSV:
		err = validateExamResourceCSV(file)
	case model.ExamResourceMediaText, model.ExamResourceMediaMarkdown:
		err = validateExamResourceUTF8(file)
	default:
		err = ErrInvalidExamResourceContent
	}
	if err != nil {
		return ErrInvalidExamResourceContent
	}
	return nil
}

func examResourcePDFConfiguration() *pdfmodel.Configuration {
	disablePDFCPUConfigDir.Do(pdfapi.DisableConfigDir)
	configuration := pdfmodel.NewDefaultConfiguration()
	configuration.CheckFileNameExt = false
	configuration.DecodeAllStreams = false
	configuration.ValidationMode = pdfmodel.ValidationStrict
	configuration.Limits.MaxStreamBytes = model.ExamResourceMaximumBytes + 1
	configuration.Limits.MaxDecodeBytes = 32 << 20
	configuration.Limits.MaxImagePixels = maximumExamResourcePixels
	configuration.Limits.MaxImageBytes = 100 << 20
	configuration.Limits.MaxObjectCount = 100_000
	configuration.Limits.MaxObjectStreamCount = 100_000
	configuration.Limits.MaxObjectStreamFirst = model.ExamResourceMaximumBytes + 1
	configuration.Limits.MaxXRefEntries = 100_000
	return configuration
}

func validateExamResourcePDF(file io.ReadSeeker) error {
	ctx, err := pdfapi.ReadAndValidate(file, examResourcePDFConfiguration())
	if err != nil || ctx.Encrypt != nil {
		return ErrInvalidExamResourceContent
	}
	for objectNumber, entry := range ctx.Table {
		if entry == nil || entry.Free || entry.Generation == nil {
			continue
		}
		ref := types.NewIndirectRef(objectNumber, *entry.Generation)
		object, err := ctx.Dereference(*ref)
		if err != nil || examResourcePDFObjectIsUnsafe(object) {
			return ErrInvalidExamResourceContent
		}
	}
	return nil
}

func examResourcePDFObjectIsUnsafe(object types.Object) bool {
	switch value := object.(type) {
	case types.Dict:
		return examResourcePDFDictIsUnsafe(value)
	case types.StreamDict:
		return examResourcePDFDictIsUnsafe(value.Dict)
	case *types.StreamDict:
		return value != nil && examResourcePDFDictIsUnsafe(value.Dict)
	case types.ObjectStreamDict:
		return examResourcePDFDictIsUnsafe(value.Dict)
	case *types.ObjectStreamDict:
		return value != nil && examResourcePDFDictIsUnsafe(value.Dict)
	case types.XRefStreamDict:
		return examResourcePDFDictIsUnsafe(value.Dict)
	case *types.XRefStreamDict:
		return value != nil && examResourcePDFDictIsUnsafe(value.Dict)
	case types.Array:
		for _, item := range value {
			if examResourcePDFObjectIsUnsafe(item) {
				return true
			}
		}
	}
	return false
}

func examResourcePDFDictIsUnsafe(dict types.Dict) bool {
	for key, value := range dict {
		if blockedExamResourcePDFKey(key) || blockedExamResourcePDFValue(key, value) || examResourcePDFObjectIsUnsafe(value) {
			return true
		}
	}
	return false
}

func blockedExamResourcePDFKey(key string) bool {
	switch strings.ToLower(key) {
	case "aa", "ef", "embeddedfiles", "javascript", "js", "openaction", "richmediacontent", "richmediasettings", "xfa":
		return true
	default:
		return false
	}
}

func blockedExamResourcePDFValue(key string, object types.Object) bool {
	name, ok := object.(types.Name)
	if !ok {
		return false
	}
	value := strings.ToLower(string(name))
	switch strings.ToLower(key) {
	case "s":
		switch value {
		case "gotoe", "gotor", "importdata", "javascript", "launch", "movie", "rendition", "sound", "submitform", "uri":
			return true
		}
	case "type", "subtype":
		switch value {
		case "3d", "embeddedfile", "fileattachment", "filespec", "movie", "richmedia", "screen", "sound":
			return true
		}
	}
	return false
}

func validateExamResourceImage(file io.ReadSeeker, mediaType model.ExamResourceMediaType, size int64) error {
	config, format, err := image.DecodeConfig(file)
	want := map[model.ExamResourceMediaType]string{model.ExamResourceMediaPNG: "png", model.ExamResourceMediaJPEG: "jpeg", model.ExamResourceMediaWebP: "webp"}[mediaType]
	if err != nil || format != want || config.Width <= 0 || config.Height <= 0 || config.Width > maximumExamResourcePixels/config.Height {
		return ErrInvalidExamResourceContent
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, _, err = image.Decode(file); err != nil {
		return ErrInvalidExamResourceContent
	}
	return validateExactImageEnvelope(file, mediaType, size)
}

func validateExactImageEnvelope(file io.ReadSeeker, mediaType model.ExamResourceMediaType, size int64) error {
	switch mediaType {
	case model.ExamResourceMediaPNG:
		if size < 12 {
			return ErrInvalidExamResourceContent
		}
		if _, err := file.Seek(-12, io.SeekEnd); err != nil {
			return err
		}
		var tail [12]byte
		if _, err := io.ReadFull(file, tail[:]); err != nil || tail != [12]byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82} {
			return ErrInvalidExamResourceContent
		}
	case model.ExamResourceMediaJPEG:
		if size < 2 {
			return ErrInvalidExamResourceContent
		}
		if _, err := file.Seek(-2, io.SeekEnd); err != nil {
			return err
		}
		var tail [2]byte
		if _, err := io.ReadFull(file, tail[:]); err != nil || tail != [2]byte{0xff, 0xd9} {
			return ErrInvalidExamResourceContent
		}
	case model.ExamResourceMediaWebP:
		if size < 12 {
			return ErrInvalidExamResourceContent
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		var header [12]byte
		if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WEBP" || uint64(binary.LittleEndian.Uint32(header[4:8]))+8 != uint64(size) {
			return ErrInvalidExamResourceContent
		}
	default:
		return ErrInvalidExamResourceContent
	}
	return nil
}

func validateExamResourceJSON(file io.ReadSeeker) error {
	if err := validateExamResourceUTF8(file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return ErrInvalidExamResourceContent
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidExamResourceContent
	}
	return nil
}

func validateExamResourceCSV(file io.ReadSeeker) error {
	if err := validateExamResourceUTF8(file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	for {
		if _, err := reader.Read(); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return ErrInvalidExamResourceContent
		}
	}
}

func validateExamResourceUTF8(file io.ReadSeeker) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, examResourceCopyBuffer)
	prefix := make([]byte, 0, 4)
	leading := true
	for {
		r, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || r == utf8.RuneError && size == 1 || r == 0 {
			return ErrInvalidExamResourceContent
		}
		if leading && unicode.IsSpace(r) {
			continue
		}
		leading = false
		if len(prefix) < cap(prefix) {
			encoded := make([]byte, utf8.RuneLen(r))
			utf8.EncodeRune(encoded, r)
			prefix = append(prefix, encoded...)
			if len(prefix) > cap(prefix) {
				prefix = prefix[:cap(prefix)]
			}
		}
	}
	for _, blocked := range [][]byte{{'M', 'Z'}, {'P', 'K', 3, 4}, {0x7f, 'E', 'L', 'F'}, {'#', '!'}} {
		if len(prefix) >= len(blocked) && string(prefix[:len(blocked)]) == string(blocked) {
			return ErrInvalidExamResourceContent
		}
	}
	return nil
}
