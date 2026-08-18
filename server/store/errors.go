// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/errors.go. Proctor keeps a
// smaller error vocabulary and preserves wrapped driver errors for internal
// errors.Is/errors.As inspection.

package store

import (
	"errors"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
)

// ErrAuthenticationMethodDisabled reports that the current Access Policy no
// longer permits the authentication method at the terminal durable commit.
// It intentionally carries no provider or account detail.
var ErrAuthenticationMethodDisabled = errors.New("authentication method is disabled by current access policy")

// ErrLastUsableAuthenticationMethod reports that removing a credential would
// leave the User without a method admitted by current policy and deployment.
var ErrLastUsableAuthenticationMethod = errors.New("cannot remove the last usable authentication method")

// ErrInvalidInput reports that a repository received a model in a state it
// cannot persist.
type ErrInvalidInput struct {
	Entity string
	Field  string
	Value  any
	cause  error
}

func NewErrInvalidInput(entity, field string, value any) *ErrInvalidInput {
	return &ErrInvalidInput{Entity: entity, Field: field, Value: value}
}

func (e *ErrInvalidInput) Error() string {
	return fmt.Sprintf("invalid input: entity=%s field=%s", e.Entity, e.Field)
}

func (e *ErrInvalidInput) Wrap(cause error) *ErrInvalidInput {
	e.cause = cause
	return e
}

func (e *ErrInvalidInput) Unwrap() error {
	return e.cause
}

// ErrNotFound reports that the requested durable resource does not exist.
type ErrNotFound struct {
	Resource string
	ID       string
	cause    error
}

func NewErrNotFound(resource, id string) *ErrNotFound {
	return &ErrNotFound{Resource: resource, ID: id}
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("resource %q not found: %s", e.Resource, e.ID)
}

func (e *ErrNotFound) Wrap(cause error) *ErrNotFound {
	e.cause = cause
	return e
}

func (e *ErrNotFound) Unwrap() error {
	return e.cause
}

// ErrConflict reports a uniqueness or state conflict.
type ErrConflict struct {
	Resource   string
	Constraint string
	cause      error
}

// ErrIdempotencyConflict reports reuse of a live command key with different
// semantic input.
type ErrIdempotencyConflict struct{}

func (*ErrIdempotencyConflict) Error() string {
	return "idempotency key conflicts with another command"
}

// ErrIdempotencyInProgress reports that another transaction still owns the
// same command namespace after the bounded wait.
type ErrIdempotencyInProgress struct{}

func (*ErrIdempotencyInProgress) Error() string { return "idempotent command is still in progress" }

// ErrUserSettingsRevisionConflict carries only the safe current opaque
// revision hint. It never carries the current settings source.
type ErrUserSettingsRevisionConflict struct {
	CurrentRevision model.UserSettingsRevision
}

func (e *ErrUserSettingsRevisionConflict) Error() string {
	return "user settings revision conflicts with current state"
}

func NewErrConflict(resource, constraint string, cause error) *ErrConflict {
	return &ErrConflict{Resource: resource, Constraint: constraint, cause: cause}
}

func (e *ErrConflict) Error() string {
	if e.Constraint == "" {
		return fmt.Sprintf("resource %q conflicts with existing state", e.Resource)
	}
	return fmt.Sprintf("resource %q conflicts with constraint %q", e.Resource, e.Constraint)
}

func (e *ErrConflict) Unwrap() error {
	return e.cause
}

// ErrReference reports an invalid foreign-key relationship.
type ErrReference struct {
	Resource   string
	Constraint string
	cause      error
}

func NewErrReference(resource, constraint string, cause error) *ErrReference {
	return &ErrReference{Resource: resource, Constraint: constraint, cause: cause}
}

func (e *ErrReference) Error() string {
	return fmt.Sprintf("resource %q has an invalid reference (%s)", e.Resource, e.Constraint)
}

func (e *ErrReference) Unwrap() error {
	return e.cause
}

func IsNotFound(err error) bool {
	var target *ErrNotFound
	return errors.As(err, &target)
}

func IsConflict(err error) bool {
	var target *ErrConflict
	return errors.As(err, &target)
}
