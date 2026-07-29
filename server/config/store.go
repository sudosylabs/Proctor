// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var ErrStoreClosed = errors.New("configuration store is closed")

type Listener func(old, current Config)

type StoreOptions struct {
	LookupEnv LookupEnv
}

type Store struct {
	operationMu sync.Mutex
	mu          sync.RWMutex
	backing     BackingStore
	lookupEnv   LookupEnv
	persisted   Config
	current     Config
	overrides   []string
	listeners   map[string]Listener
	nextID      uint64
	initialized bool
	closed      bool
}

func NewStore(ctx context.Context, backing BackingStore, options StoreOptions) (*Store, error) {
	if backing == nil {
		return nil, errors.New("configuration backing store is required")
	}
	lookup := options.LookupEnv
	if lookup == nil {
		lookup = systemEnvironment
	}
	store := &Store{
		backing:   backing,
		lookupEnv: lookup,
		listeners: make(map[string]Listener),
	}
	if err := store.reload(ctx, false); err != nil {
		_ = backing.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.Clone()
}

func (s *Store) GetPersisted() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.persisted.Clone()
}

func (s *Store) EnvironmentOverrides() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.overrides...)
}

func (s *Store) Describe() string {
	return s.backing.String()
}

func (s *Store) Reload(ctx context.Context) error {
	return s.reload(ctx, true)
}

func (s *Store) reload(ctx context.Context, notify bool) error {
	var old Config
	var current Config
	var listeners []Listener
	var changed bool
	err := func() error {
		s.operationMu.Lock()
		defer s.operationMu.Unlock()

		data, err := s.backing.Load(ctx)
		if err != nil {
			return err
		}
		persisted := Default()
		if len(bytes.TrimSpace(data)) != 0 {
			if err := decodeStrict(data, &persisted); err != nil {
				return fmt.Errorf("decode configuration from %s: %w", s.backing.String(), err)
			}
		}
		if err := persisted.Validate(); err != nil {
			return fmt.Errorf("validate persisted configuration: %w", err)
		}
		current = persisted.Clone()
		overrides, err := applyEnvironment(&current, s.lookupEnv)
		if err != nil {
			return err
		}
		if err := current.Validate(); err != nil {
			return fmt.Errorf("validate effective configuration: %w", err)
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return ErrStoreClosed
		}
		old = s.current.Clone()
		hadValue := s.initialized
		s.persisted = persisted.Clone()
		s.current = current.Clone()
		s.overrides = append([]string(nil), overrides...)
		s.initialized = true
		listeners = s.copyListenersLocked()
		changed = notify && hadValue && !reflect.DeepEqual(old, current)
		return nil
	}()
	if err != nil {
		return err
	}
	if changed {
		invokeListeners(listeners, old, current)
	}
	return nil
}

func (s *Store) Set(ctx context.Context, candidate Config) (Config, Config, error) {
	var old Config
	var current Config
	var listeners []Listener
	var changed bool
	err := func() error {
		s.operationMu.Lock()
		defer s.operationMu.Unlock()

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return ErrStoreClosed
		}

		persisted := candidate.Clone()
		removeEnvironmentOverrides(&persisted, s.persisted, s.overrides)
		if err := persisted.Validate(); err != nil {
			return err
		}
		current = persisted.Clone()
		overrides, err := applyEnvironment(&current, s.lookupEnv)
		if err != nil {
			return err
		}
		if err := current.Validate(); err != nil {
			return err
		}
		data, err := json.MarshalIndent(persisted, "", "  ")
		if err != nil {
			return fmt.Errorf("encode configuration: %w", err)
		}
		data = append(data, '\n')
		if err := s.backing.Save(ctx, data); err != nil {
			return err
		}

		old = s.current.Clone()
		s.persisted = persisted.Clone()
		s.current = current.Clone()
		s.overrides = append([]string(nil), overrides...)
		listeners = s.copyListenersLocked()
		changed = !reflect.DeepEqual(old, current)
		return nil
	}()
	if err != nil {
		return Config{}, Config{}, err
	}
	if changed {
		invokeListeners(listeners, old, current)
	}
	return old, current.Clone(), nil
}

func (s *Store) AddListener(listener Listener) string {
	if listener == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ""
	}
	s.nextID++
	id := strconv.FormatUint(s.nextID, 10)
	s.listeners[id] = listener
	return id
}

func (s *Store) RemoveListener(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.listeners, id)
}

func (s *Store) Close() error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.listeners = nil
	s.mu.Unlock()
	return s.backing.Close()
}

func (s *Store) copyListenersLocked() []Listener {
	listeners := make([]Listener, 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	return listeners
}

func invokeListeners(listeners []Listener, old, current Config) {
	for _, listener := range listeners {
		listener(old.Clone(), current.Clone())
	}
}

func decodeStrict(data []byte, target *Config) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("configuration must contain exactly one JSON object")
		}
		return fmt.Errorf("read trailing configuration data: %w", err)
	}
	return nil
}

type Change struct {
	Path string
}

func Diff(old, current Config) []Change {
	var paths []string
	diffValue(reflect.ValueOf(old), reflect.ValueOf(current), "", &paths)
	sort.Strings(paths)
	changes := make([]Change, 0, len(paths))
	for _, path := range paths {
		changes = append(changes, Change{Path: path})
	}
	return changes
}

func diffValue(old, current reflect.Value, prefix string, paths *[]string) {
	if old.Type() != current.Type() {
		*paths = append(*paths, prefix)
		return
	}
	if old.Kind() != reflect.Struct || old.Type() == reflect.TypeOf(Duration{}) {
		if !reflect.DeepEqual(old.Interface(), current.Interface()) {
			*paths = append(*paths, prefix)
		}
		return
	}
	for index := 0; index < old.NumField(); index++ {
		field := old.Type().Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		diffValue(old.Field(index), current.Field(index), path, paths)
	}
}
