// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type BackingStore interface {
	Load(context.Context) ([]byte, error)
	Save(context.Context, []byte) error
	String() string
	Close() error
}

type MemoryStore struct {
	mu     sync.RWMutex
	data   []byte
	closed bool
}

func NewMemoryStore(data []byte) *MemoryStore {
	return &MemoryStore{data: append([]byte(nil), data...)}
}

func (s *MemoryStore) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errors.New("memory configuration store is closed")
	}
	return append([]byte(nil), s.data...), nil
}

func (s *MemoryStore) Save(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("memory configuration store is closed")
	}
	s.data = append(s.data[:0], data...)
	return nil
}

func (s *MemoryStore) String() string {
	return "memory"
}

func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

type FileStore struct {
	path string
	mu   sync.RWMutex
}

func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("configuration file path is required")
	}
	return &FileStore{path: filepath.Clean(path)}, nil
}

func (s *FileStore) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read configuration %q: %w", s.path, err)
	}
	return data, ctx.Err()
}

func (s *FileStore) Save(ctx context.Context, data []byte) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	directory := filepath.Dir(s.path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if resultErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary configuration: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary configuration: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace configuration %q: %w", s.path, err)
	}

	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open configuration directory: %w", err)
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("sync configuration directory: %w", err)
	}
	return nil
}

func (s *FileStore) String() string {
	return s.path
}

func (s *FileStore) Close() error {
	return nil
}
