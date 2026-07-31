// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package externalauth

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
)

func TestRegistryIsInstanceScopedAndRejectsInvalidFactories(t *testing.T) {
	first, err := NewRegistry(fakeFactory{providerType: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Configure(config.ExternalAuthentication{
		Providers: []config.ExternalAuthenticationProvider{{
			ID: "z-provider", Type: "first", Enabled: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	second, err := NewRegistry(fakeFactory{providerType: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := second.Provider("z-provider"); exists {
		t.Fatal("provider leaked between registry instances")
	}
	if _, err := NewRegistry(
		fakeFactory{providerType: "duplicate"},
		fakeFactory{providerType: "duplicate"},
	); err == nil {
		t.Fatal("duplicate factory was accepted")
	}
	if err := first.Configure(config.ExternalAuthentication{
		Providers: []config.ExternalAuthenticationProvider{{
			ID: "unknown", Type: "missing", Enabled: true,
		}},
	}); err == nil {
		t.Fatal("unregistered provider type was accepted")
	}
}

type fakeFactory struct {
	providerType string
}

func (f fakeFactory) Type() string {
	return f.providerType
}

func (f fakeFactory) New(
	settings config.ExternalAuthenticationProvider,
) (Provider, error) {
	if settings.ID == "" {
		return nil, errors.New("missing id")
	}
	return fakeProvider{
		descriptor: model.ExternalAuthenticationProvider{
			Id: settings.ID, Type: settings.Type,
		},
	}, nil
}

type fakeProvider struct {
	descriptor model.ExternalAuthenticationProvider
}

func (p fakeProvider) Descriptor() model.ExternalAuthenticationProvider {
	return p.descriptor
}

func (fakeProvider) AutoProvision() bool {
	return false
}

func (fakeProvider) State(
	model.ExternalAuthenticationCallback,
) (string, error) {
	return model.NewCredentialToken(), nil
}

func (fakeProvider) Begin(
	context.Context,
	BeginRequest,
) (*BeginResponse, error) {
	return &BeginResponse{RedirectURL: "https://example.test"}, nil
}

func (fakeProvider) Complete(
	context.Context,
	CompleteRequest,
) (*model.ExternalAuthenticationAssertion, error) {
	return nil, ErrAuthenticationRejected
}
