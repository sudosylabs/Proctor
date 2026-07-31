// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package externalauth defines the instance-scoped provider registry and the
// protocol-neutral boundary used by Proctor's application layer.
package externalauth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
)

var (
	ErrAuthenticationRejected = errors.New("external authentication rejected")
	ErrInvalidResponse        = errors.New("external authentication response is invalid")
	ErrProviderUnavailable    = errors.New("external authentication provider is unavailable")
)

type BeginRequest struct {
	CallbackURL string
	State       string
	Proof       string
}

type BeginResponse struct {
	RedirectURL string
}

type CompleteRequest struct {
	CallbackURL string
	State       string
	Proof       string
	Callback    model.ExternalAuthenticationCallback
}

// Provider owns one configured protocol integration. It returns only a
// normalized assertion; account resolution, provisioning, auditing, and
// session creation remain in the application layer.
type Provider interface {
	Descriptor() model.ExternalAuthenticationProvider
	AutoProvision() bool
	Begin(context.Context, BeginRequest) (*BeginResponse, error)
	State(model.ExternalAuthenticationCallback) (string, error)
	Complete(
		context.Context,
		CompleteRequest,
	) (*model.ExternalAuthenticationAssertion, error)
}

// Factory constructs one protocol adapter. Factories are registered on an
// instance Registry at the platform composition root; there is no process-wide
// mutable provider map or init-time side effect.
type Factory interface {
	Type() string
	New(config.ExternalAuthenticationProvider) (Provider, error)
}

type Registry struct {
	mutex     sync.RWMutex
	factories map[string]Factory
	providers map[string]Provider
}

func NewRegistry(factories ...Factory) (*Registry, error) {
	registry := &Registry{
		factories: make(map[string]Factory, len(factories)),
		providers: make(map[string]Provider),
	}
	for _, factory := range factories {
		if factory == nil {
			return nil, errors.New("external authentication factory is nil")
		}
		providerType := strings.TrimSpace(factory.Type())
		if providerType == "" {
			return nil, errors.New("external authentication factory type is empty")
		}
		if _, exists := registry.factories[providerType]; exists {
			return nil, fmt.Errorf(
				"external authentication factory %q is duplicated",
				providerType,
			)
		}
		registry.factories[providerType] = factory
	}
	return registry, nil
}

func (r *Registry) Configure(
	settings config.ExternalAuthentication,
) error {
	configured := make(map[string]Provider)
	for _, definition := range settings.Providers {
		if !definition.Enabled {
			continue
		}
		factory, exists := r.factories[definition.Type]
		if !exists {
			return fmt.Errorf(
				"external authentication provider %q uses unregistered type %q",
				definition.ID,
				definition.Type,
			)
		}
		provider, err := factory.New(definition)
		if err != nil {
			return fmt.Errorf(
				"configure external authentication provider %q: %w",
				definition.ID,
				err,
			)
		}
		if provider == nil {
			return fmt.Errorf(
				"external authentication provider %q factory returned nil",
				definition.ID,
			)
		}
		descriptor := provider.Descriptor()
		if descriptor.Id != definition.ID ||
			descriptor.Type != definition.Type {
			return fmt.Errorf(
				"external authentication provider %q returned a mismatched descriptor",
				definition.ID,
			)
		}
		if _, exists := configured[descriptor.Id]; exists {
			return fmt.Errorf(
				"external authentication provider %q is duplicated",
				descriptor.Id,
			)
		}
		configured[descriptor.Id] = provider
	}
	r.mutex.Lock()
	r.providers = configured
	r.mutex.Unlock()
	return nil
}

func (r *Registry) Provider(id string) (Provider, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	provider, exists := r.providers[id]
	return provider, exists
}

func (r *Registry) Descriptors() []model.ExternalAuthenticationProvider {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	descriptors := make(
		[]model.ExternalAuthenticationProvider,
		0,
		len(r.providers),
	)
	for _, provider := range r.providers {
		descriptors = append(descriptors, provider.Descriptor())
	}
	sort.Slice(descriptors, func(left, right int) bool {
		return descriptors[left].Id < descriptors[right].Id
	})
	return descriptors
}

// OperationError exposes a stable classification without allowing provider
// URLs, authorization codes, tickets, tokens, claims, or response bodies from
// the wrapped diagnostic error to appear through Error().
type OperationError struct {
	Kind      error
	Operation string
	Cause     error
}

func (e *OperationError) Error() string {
	return e.Operation + ": provider operation failed"
}

func (e *OperationError) Unwrap() error {
	return e.Cause
}

func (e *OperationError) Is(target error) bool {
	return target == e.Kind || errors.Is(e.Cause, target)
}

func Rejected(operation string, cause error) error {
	return &OperationError{
		Kind: ErrAuthenticationRejected, Operation: operation, Cause: cause,
	}
}

func InvalidResponse(operation string, cause error) error {
	return &OperationError{
		Kind: ErrInvalidResponse, Operation: operation, Cause: cause,
	}
}

func Unavailable(operation string, cause error) error {
	return &OperationError{
		Kind: ErrProviderUnavailable, Operation: operation, Cause: cause,
	}
}
