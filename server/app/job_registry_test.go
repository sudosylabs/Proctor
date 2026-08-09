// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

type jobHandlerFunc func(context.Context, JobExecution) JobOutcome

func (f jobHandlerFunc) Run(ctx context.Context, execution JobExecution) JobOutcome {
	return f(ctx, execution)
}

func TestJobRegistryIsClosedImmutableAndVersionAware(t *testing.T) {
	t.Parallel()
	handler := jobHandlerFunc(func(context.Context, JobExecution) JobOutcome {
		return DefaultProfilePictureJobSucceeded(model.NewFileEntryID())
	})
	descriptor := testJobDescriptor(handler)
	registry, err := NewJobRegistry([]JobDescriptor{descriptor})
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
	if _, err = registry.Resolve(model.JobTypeCleanup, 1); err == nil {
		t.Fatal("Resolve() accepted an unregistered type")
	}
	if described, err := registry.Descriptor(model.JobTypeProfilePictureGenerateDefault); err != nil || described.Type != descriptor.Type {
		t.Fatalf("Descriptor() = %#v, %v", described, err)
	}
}

func TestJobRegistryRejectsDuplicatesAndMissingHandlers(t *testing.T) {
	t.Parallel()
	valid := testJobDescriptor(jobHandlerFunc(func(context.Context, JobExecution) JobOutcome {
		return DefaultProfilePictureJobSucceeded(model.NewFileEntryID())
	}))
	if _, err := NewJobRegistry([]JobDescriptor{valid, valid}); err == nil {
		t.Fatal("NewJobRegistry() accepted duplicate type")
	}
	valid.Handler = nil
	if _, err := NewJobRegistry([]JobDescriptor{valid}); err == nil {
		t.Fatal("NewJobRegistry() accepted missing handler")
	}
}

func TestDefaultProfilePictureCommandIsVersionedAndTyped(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	document, err := EncodeDefaultProfilePictureCommand(DefaultProfilePictureCommandV1{UserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDefaultProfilePictureCommand(1, document)
	if err != nil || decoded.UserID != userID {
		t.Fatalf("DecodeDefaultProfilePictureCommand() = %#v, %v", decoded, err)
	}
	if _, err = DecodeDefaultProfilePictureCommand(2, document); err == nil {
		t.Fatal("DecodeDefaultProfilePictureCommand() accepted unsupported version")
	}
	if _, err = DecodeDefaultProfilePictureCommand(1, json.RawMessage(`{"user_id":"bad","extra":true}`)); err == nil {
		t.Fatal("DecodeDefaultProfilePictureCommand() accepted invalid/unknown fields")
	}
}
