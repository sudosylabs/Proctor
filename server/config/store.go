// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var ErrStoreClosed = errors.New("configuration store is closed")

// Listener applies a configuration change to one dynamically reconfigurable
// consumer. It returns an error, leaving its consumer on old, when it cannot
// apply current; the store then restores old on every listener that already
// applied the change and keeps old in force. A Listener runs while the change
// is pending and must not call Set or Reload.
type Listener func(old, current Config) error

// ReloadResult describes a successful Reload.
type ReloadResult struct {
	// RestartRequired lists, by configuration path, the changed settings that
	// Reload left at their running values because they apply only at startup.
	RestartRequired []string
}

type StoreOptions struct {
	LookupEnv LookupEnv
}

type registeredListener struct {
	id       string
	listener Listener
}

type Store struct {
	operationMu sync.Mutex
	mu          sync.RWMutex
	backing     BackingStore
	lookupEnv   LookupEnv
	persisted   Config
	current     Config
	overrides   appliedEnvironment
	listeners   []registeredListener
	nextID      uint64
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
	}
	persisted, current, overrides, err := store.load(ctx)
	if err != nil {
		_ = backing.Close()
		return nil, err
	}
	store.persisted, store.current, store.overrides = persisted, current, overrides
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
	return s.overrides.keys()
}

func (s *Store) Describe() string {
	return s.backing.String()
}

// Reload re-reads and validates the backing source. Only the sections that
// running consumers reconfigure dynamically (Log and
// Authentication.External) take the new values; every other changed setting
// keeps its running value and is named in RestartRequired. The change is
// committed only after every listener has applied it; a failing listener
// rolls the others back, Reload returns the failure, and the running
// configuration stays in force, so a later Reload of the same source tries
// again.
func (s *Store) Reload(ctx context.Context) (ReloadResult, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	persisted, candidate, overrides, err := s.load(ctx)
	if err != nil {
		return ReloadResult{}, err
	}
	s.mu.RLock()
	closed, running, listeners := s.closed, s.current.Clone(), s.copyListenersLocked()
	s.mu.RUnlock()
	if closed {
		return ReloadResult{}, ErrStoreClosed
	}
	effective := reloadable(running, candidate)
	if err := effective.Validate(); err != nil {
		return ReloadResult{}, fmt.Errorf("validate reloaded configuration: %w", err)
	}
	var result ReloadResult
	for _, change := range Diff(effective, candidate) {
		result.RestartRequired = append(result.RestartRequired, change.Path)
	}
	if !reflect.DeepEqual(running, effective) {
		if err := applyListeners(listeners, running, effective); err != nil {
			return ReloadResult{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ReloadResult{}, ErrStoreClosed
	}
	s.persisted = persisted
	s.current = effective
	s.overrides = overrides
	return result, nil
}

// reloadable returns running with the dynamically reconfigurable sections of
// candidate applied.
func reloadable(running, candidate Config) Config {
	effective := running.Clone()
	effective.Log = candidate.Clone().Log
	effective.Authentication.External = candidate.Clone().Authentication.External
	return effective
}

// load reads, decodes, and validates the backing source and applies the
// environment overrides.
func (s *Store) load(ctx context.Context) (Config, Config, appliedEnvironment, error) {
	data, err := s.backing.Load(ctx)
	if err != nil {
		return Config{}, Config{}, appliedEnvironment{}, err
	}
	persisted := Default()
	if len(bytes.TrimSpace(data)) != 0 {
		if err = decodeStrict(data, &persisted); err != nil {
			return Config{}, Config{}, appliedEnvironment{}, fmt.Errorf("decode configuration from %s: %w", s.backing.String(), err)
		}
	}
	if err = persisted.Validate(); err != nil {
		return Config{}, Config{}, appliedEnvironment{}, fmt.Errorf("validate persisted configuration: %w", err)
	}
	current := persisted.Clone()
	overrides, err := applyEnvironment(&current, s.lookupEnv)
	if err != nil {
		return Config{}, Config{}, appliedEnvironment{}, err
	}
	if err := current.Validate(); err != nil {
		return Config{}, Config{}, appliedEnvironment{}, fmt.Errorf("validate effective configuration: %w", err)
	}
	return persisted, current, overrides.clone(), nil
}

// Set validates and persists candidate as the complete configuration. Every
// listener applies the change before it is saved; a listener or save failure
// restores the previous configuration on the listeners that applied it.
func (s *Store) Set(ctx context.Context, candidate Config) (Config, Config, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	s.mu.RLock()
	closed, old, previousPersisted, previousOverrides, listeners := s.closed, s.current.Clone(), s.persisted.Clone(), s.overrides.clone(), s.copyListenersLocked()
	s.mu.RUnlock()
	if closed {
		return Config{}, Config{}, ErrStoreClosed
	}

	persisted := candidate.Clone()
	previousOverrides.restore(&persisted, previousPersisted)
	if err := persisted.Validate(); err != nil {
		return Config{}, Config{}, err
	}
	current := persisted.Clone()
	overrides, err := applyEnvironment(&current, s.lookupEnv)
	if err != nil {
		return Config{}, Config{}, err
	}
	if err = current.Validate(); err != nil {
		return Config{}, Config{}, err
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return Config{}, Config{}, fmt.Errorf("encode configuration: %w", err)
	}
	data = append(data, '\n')
	changed := !reflect.DeepEqual(old, current)
	if changed {
		if err := applyListeners(listeners, old, current); err != nil {
			return Config{}, Config{}, err
		}
	}
	if err := s.backing.Save(ctx, data); err != nil {
		if changed {
			err = errors.Join(err, rollbackListeners(listeners, old, current))
		}
		return Config{}, Config{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.persisted = persisted
	s.current = current.Clone()
	s.overrides = overrides.clone()
	return old, current, nil
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
	s.listeners = append(s.listeners, registeredListener{id: id, listener: listener})
	return id
}

func (s *Store) RemoveListener(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = slices.DeleteFunc(s.listeners, func(registered registeredListener) bool { return registered.id == id })
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
	for _, registered := range s.listeners {
		listeners = append(listeners, registered.listener)
	}
	return listeners
}

// applyListeners applies old->current to each listener in registration
// order. When one fails, the listeners that already applied the change are
// restored to old in reverse order and the joined failure is returned.
func applyListeners(listeners []Listener, old, current Config) error {
	for index, listener := range listeners {
		if err := listener(old.Clone(), current.Clone()); err != nil {
			return errors.Join(
				fmt.Errorf("apply configuration change: %w", err),
				rollbackListeners(listeners[:index], old, current),
			)
		}
	}
	return nil
}

// rollbackListeners restores old on listeners that applied old->current.
func rollbackListeners(applied []Listener, old, current Config) error {
	var result error
	for index := len(applied) - 1; index >= 0; index-- {
		if err := applied[index](current.Clone(), old.Clone()); err != nil {
			result = errors.Join(result, fmt.Errorf("restore previous configuration: %w", err))
		}
	}
	return result
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
