// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package localcachelayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const maxAllowedTTL = 5 * time.Minute

// Cache is the process-local byte-cache contract consumed by the store layer.
// Implementations bound retained state, copy mutable bytes at their boundary,
// honor context cancellation, and return failures without panicking. Layer
// treats cache failures as misses so PostgreSQL remains authoritative.
type Cache interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
}

// Policy bounds the lifetime of every cached store value.
type Policy struct {
	TTL time.Duration
}

// DefaultPolicy returns the conservative production cache policy.
func DefaultPolicy() Policy { return Policy{TTL: 30 * time.Second} }

// Operation is a closed, low-cardinality cache operation label.
type Operation struct{ name string }

func (o Operation) String() string { return o.name }

// Outcome is the bounded cache lookup result.
type Outcome string

const (
	OutcomeHit  Outcome = "hit"
	OutcomeMiss Outcome = "miss"
)

// Recorder consumes argument-free cache observations. Layer isolates recorder
// panics so observability cannot change persistence results.
type Recorder interface{ Observe(Operation, Outcome) }

// RecorderFunc adapts a function to Recorder.
type RecorderFunc func(Operation, Outcome)

func (f RecorderFunc) Observe(operation Operation, outcome Outcome) { f(operation, outcome) }

// NopRecorder discards observations when no metrics backend is configured.
type NopRecorder struct{}

func (NopRecorder) Observe(Operation, Outcome) {}

// InvalidationFanout carries best-effort academic-period invalidations between
// nodes. Delivery failures are advisory because TTL expiry restores authority.
type InvalidationFanout interface {
	RegisterAcademicPeriod(func(context.Context, string) error) error
	BroadcastAcademicPeriod(context.Context, string) error
}

// NopInvalidationFanout is the single-node/test invalidation boundary.
type NopInvalidationFanout struct{}

func (NopInvalidationFanout) RegisterAcademicPeriod(func(context.Context, string) error) error {
	return nil
}
func (NopInvalidationFanout) BroadcastAcademicPeriod(context.Context, string) error { return nil }

// Layer is a safe-by-default local-cache decorator. Only handwritten methods
// cache values; all promoted store methods remain authoritative pass-throughs.
type Layer struct {
	store.Store
	cache        Cache
	policy       Policy
	recorder     Recorder
	invalidation InvalidationFanout
	cacheMu      sync.Mutex
	generation   uint64
	stores       localCacheStores
}

// New constructs a constrained process-local cache layer around next.
func New(
	next store.Store,
	cache Cache,
	policy Policy,
	recorder Recorder,
	invalidation InvalidationFanout,
) (*Layer, error) {
	if next == nil {
		return nil, errors.New("local cache store is nil")
	}
	if cache == nil {
		return nil, errors.New("local store cache is nil")
	}
	if policy.TTL <= 0 || policy.TTL > maxAllowedTTL {
		return nil, fmt.Errorf("local store cache TTL must be between 1ns and %s", maxAllowedTTL)
	}
	if recorder == nil {
		return nil, errors.New("local store cache recorder is nil")
	}
	if invalidation == nil {
		return nil, errors.New("local store cache invalidation fan-out is nil")
	}
	layer := &Layer{
		Store: next, cache: cache, policy: policy, recorder: recorder,
		invalidation: invalidation,
	}
	if err := invalidation.RegisterAcademicPeriod(layer.invalidateAcademicPeriod); err != nil {
		return nil, fmt.Errorf("register academic-period cache invalidation: %w", err)
	}
	return layer, nil
}

func (s *academicPeriodStore) Get(ctx context.Context, id string) (*model.AcademicPeriod, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !model.IsValidId(id) {
		return s.AcademicPeriodStore.Get(ctx, id)
	}
	key := academicPeriodKey(id)
	s.layer.cacheMu.Lock()
	generation := s.layer.generation
	data, found, err := s.layer.cache.Get(ctx, key)
	if err == nil && found {
		var period model.AcademicPeriod
		if json.Unmarshal(data, &period) == nil && period.ID.String() == id && period.Validate() == nil {
			s.layer.cacheMu.Unlock()
			s.layer.observe(Operation{name: "academic_period.get"}, OutcomeHit)
			return &period, nil
		}
		_ = s.layer.cache.Delete(ctx, key)
	}
	s.layer.cacheMu.Unlock()
	s.layer.observe(Operation{name: "academic_period.get"}, OutcomeMiss)
	period, err := s.AcademicPeriodStore.Get(ctx, id)
	if err != nil || period == nil {
		return period, err
	}
	if data, marshalErr := json.Marshal(period); marshalErr == nil {
		s.layer.cacheMu.Lock()
		if generation == s.layer.generation {
			_ = s.layer.cache.Set(ctx, key, data, s.layer.policy.TTL)
		}
		s.layer.cacheMu.Unlock()
	}
	return period, nil
}

func (s *academicPeriodStore) Create(ctx context.Context, command *store.AcademicPeriodCreation) (*model.AcademicPeriod, error) {
	period, err := s.AcademicPeriodStore.Create(ctx, command)
	if err == nil && period != nil {
		s.invalidateAfterMutation(ctx, period.ID.String())
	}
	return period, err
}

func (s *academicPeriodStore) UpdateWithAudit(ctx context.Context, command *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error) {
	period, err := s.AcademicPeriodStore.UpdateWithAudit(ctx, command)
	if err == nil && period != nil {
		s.invalidateAfterMutation(ctx, period.ID.String())
	}
	return period, err
}

func (s *academicPeriodStore) ArchiveWithAudit(ctx context.Context, command *store.AcademicPeriodArchive) (*model.AcademicPeriod, error) {
	period, err := s.AcademicPeriodStore.ArchiveWithAudit(ctx, command)
	if err == nil && period != nil {
		s.invalidateAfterMutation(ctx, period.ID.String())
	}
	return period, err
}

func (s *academicPeriodStore) Save(ctx context.Context, candidate *model.AcademicPeriod) (*model.AcademicPeriod, error) {
	period, err := s.AcademicPeriodStore.Save(ctx, candidate)
	if err == nil && period != nil {
		s.invalidateAfterMutation(ctx, period.ID.String())
	}
	return period, err
}

func (s *academicPeriodStore) Update(ctx context.Context, candidate *model.AcademicPeriod) (*model.AcademicPeriod, error) {
	period, err := s.AcademicPeriodStore.Update(ctx, candidate)
	if err == nil && period != nil {
		s.invalidateAfterMutation(ctx, period.ID.String())
	}
	return period, err
}

func (s *academicPeriodStore) Archive(ctx context.Context, id string, at int64) (*model.AcademicPeriod, error) {
	period, err := s.AcademicPeriodStore.Archive(ctx, id, at)
	if err == nil {
		s.invalidateAfterMutation(ctx, id)
	}
	return period, err
}

func (s *academicPeriodStore) invalidateAfterMutation(ctx context.Context, id string) {
	if !model.IsValidId(id) {
		return
	}
	_ = s.layer.invalidateAcademicPeriod(context.WithoutCancel(ctx), id)
	_ = s.layer.invalidation.BroadcastAcademicPeriod(ctx, id)
}

func (l *Layer) invalidateAcademicPeriod(ctx context.Context, id string) error {
	if !model.IsValidId(id) {
		return errors.New("academic-period cache invalidation ID is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.cacheMu.Lock()
	defer l.cacheMu.Unlock()
	l.generation++
	return l.cache.Delete(ctx, academicPeriodKey(id))
}

func academicPeriodKey(id string) string { return "store/academic_period/id/" + id }

func (l *Layer) observe(operation Operation, outcome Outcome) {
	func() {
		defer func() { _ = recover() }()
		l.recorder.Observe(operation, outcome)
	}()
}
