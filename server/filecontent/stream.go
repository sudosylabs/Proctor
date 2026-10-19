// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
)

// streamReadLimit bounds each read from a caller-supplied body, whatever
// buffer the consuming backend offers.
const streamReadLimit = 32 << 10

var (
	errContentShorterThanDeclared = errors.New("content is shorter than its declared size")
	errContentLongerThanDeclared  = errors.New("content exceeds its declared size")
)

// exactSizeReader yields exactly size bytes from a body and hashes them. A
// body that ends early or continues past size fails the stream, so a backend
// write that verifies its declared size cannot commit the wrong length.
type exactSizeReader struct {
	body      io.Reader
	remaining int64
	digest    hash.Hash
	err       error
	probed    bool
}

func newExactSizeReader(body io.Reader, size int64) *exactSizeReader {
	return &exactSizeReader{body: body, remaining: size, digest: sha256.New()}
}

func (r *exactSizeReader) Read(buffer []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.remaining == 0 {
		if err := r.probe(); err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	if len(buffer) > streamReadLimit {
		buffer = buffer[:streamReadLimit]
	}
	read, err := r.body.Read(buffer)
	r.remaining -= int64(read)
	_, _ = r.digest.Write(buffer[:read])
	switch {
	case errors.Is(err, io.EOF) && r.remaining > 0:
		r.err = errContentShorterThanDeclared
		return read, r.err
	case errors.Is(err, io.EOF):
		return read, nil
	case err != nil:
		r.err = err
		return read, err
	}
	return read, nil
}

// probe checks, once, that the body holds no byte beyond the declared size.
func (r *exactSizeReader) probe() error {
	if r.probed {
		return r.err
	}
	r.probed = true
	var extra [1]byte
	for {
		read, err := r.body.Read(extra[:])
		if read > 0 {
			r.err = errContentLongerThanDeclared
			return r.err
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			r.err = err
			return err
		}
	}
}

// finish consumes any bytes the backend did not read, so the digest covers
// the complete declared body, and reports whether the body had exactly the
// declared size.
func (r *exactSizeReader) finish() error {
	if _, err := io.CopyBuffer(io.Discard, r, make([]byte, streamReadLimit)); err != nil {
		return err
	}
	return r.err
}

func (r *exactSizeReader) checksum() string {
	return fmt.Sprintf("%x", r.digest.Sum(nil))
}

// createUnlinkedSpool creates a private (0600) temporary file for content that
// must be read more than once and removes its name at once where the
// platform allows it. The open descriptor keeps the bytes readable, nothing
// else can open them by name, and the storage is released when the
// descriptor closes, including when the process dies. Where an open file
// cannot be unlinked, release removes it after closing.
func createUnlinkedSpool(pattern string) (*os.File, func(), error) {
	spool, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, nil, err
	}
	name := spool.Name()
	unlinked := os.Remove(name) == nil
	release := func() {
		_ = spool.Close()
		if !unlinked {
			_ = os.Remove(name)
		}
	}
	return spool, release, nil
}
