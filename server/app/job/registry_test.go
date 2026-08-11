// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package job

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

type handlerFunc func(context.Context, Execution) Outcome

func (f handlerFunc) Run(ctx context.Context, execution Execution) Outcome {
	return f(ctx, execution)
}

func TestRegistryIsClosedImmutableAndVersionAware(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome {
		return Outcome{Kind: OutcomeSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}
	}))
	registry, err := NewRegistry([]Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(model.JobTypeProfilePictureGenerateDefault, 1)
	if err != nil || resolved.Type != descriptor.Type {
		t.Fatalf("Resolve() = %#v, %v", resolved, err)
	}
	descriptor.CommandVersions[0] = 9
	if _, err = registry.Resolve(model.JobTypeProfilePictureGenerateDefault, 1); err != nil {
		t.Fatalf("registry retained caller-owned version slice: %v", err)
	}
	if _, err = registry.Resolve(model.JobTypeProfilePictureGenerateDefault, 2); err == nil {
		t.Fatal("Resolve() accepted unsupported payload version")
	}
}

func TestRegistryRejectsDuplicateTypesAndMissingHandlers(t *testing.T) {
	t.Parallel()
	valid := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	if _, err := NewRegistry([]Descriptor{valid, valid}); err == nil {
		t.Fatal("NewRegistry() accepted duplicate type")
	}
	valid.Handler = nil
	if _, err := NewRegistry([]Descriptor{valid}); err == nil {
		t.Fatal("NewRegistry() accepted missing handler")
	}
}
