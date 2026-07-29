// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mlog

import (
	"bytes"
	"sync"
)

type Buffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *Buffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffer.Reset()
}
