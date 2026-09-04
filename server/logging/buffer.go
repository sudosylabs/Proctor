// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package logging

import (
	"bytes"
	"sync"
)

type Buffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	flush  func() error
}

func (b *Buffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *Buffer) String() string {
	b.flushPending()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *Buffer) Bytes() []byte {
	b.flushPending()
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *Buffer) Reset() {
	b.flushPending()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffer.Reset()
}

func (b *Buffer) bindFlush(flush func() error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flush = flush
}

func (b *Buffer) flushPending() {
	b.mu.Lock()
	flush := b.flush
	b.mu.Unlock()
	if flush != nil {
		_ = flush()
	}
}
