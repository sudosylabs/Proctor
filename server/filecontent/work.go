// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"context"
	"io"
	"time"
)

type workCapacityError struct{}

func (workCapacityError) Error() string         { return "file content: processing capacity is unavailable" }
func (workCapacityError) WorkCapacityExceeded() {}

// ErrWorkCapacity means no processing slot is currently available. Callers may
// retry; the rejected operation has not consumed input or written content.
var ErrWorkCapacity error = workCapacityError{}

func (c *Content) beginWork(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case c.work <- struct{}{}:
	default:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.recorder != nil {
			c.recorder.Rejected()
		}
		return nil, ErrWorkCapacity
	}
	if err := ctx.Err(); err != nil {
		<-c.work
		return nil, err
	}
	started := time.Now()
	if c.recorder != nil {
		c.recorder.Started()
	}
	return func() {
		if c.recorder != nil {
			c.recorder.Finished(time.Since(started))
		}
		<-c.work
	}, nil
}

// Cancellation can stop between reads, but it never releases the slot while
// an underlying synchronous reader, codec, parser, or VFS call is still running.
type workReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r workReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}

// Validators may rewind their private spool and request large reads. Cap each
// read so cancellation is observed between bounded amounts of input, including
// when a parser buffers one large value. CPU work inside a synchronous codec
// remains non-interruptible, and the caller still owns the processing permit.
type workReadSeeker struct {
	ctx    context.Context
	reader io.ReadSeeker
}

func (r workReadSeeker) Read(body []byte) (int, error) {
	return (workReader{ctx: r.ctx, reader: r.reader}).Read(body[:min(len(body), examResourceCopyBuffer)])
}

func (r workReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Seek(offset, whence)
}
