// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const defaultOpaqueCursorMaximumEncodedLength = 512

type opaqueCursorSpec[T any] struct {
	label                string
	maximumEncodedLength int
	currentVersion       int
	members              []string
	version              func(T) int
	setVersion           func(*T, int)
	acceptsVersion       func(int) bool
	validate             func(T) error
}

func encodeOpaqueCursor[T any](value T, spec opaqueCursorSpec[T]) (string, error) {
	if err := validateOpaqueCursorSpec(spec); err != nil {
		return "", err
	}
	spec.setVersion(&value, spec.currentVersion)
	if err := spec.validate(value); err != nil {
		return "", invalidOpaqueCursorError(spec.label, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", invalidOpaqueCursorError(spec.label, err)
	}
	if !utf8.Valid(encoded) || len(encoded) == 0 || len(encoded) > base64.RawURLEncoding.DecodedLen(spec.maximumEncodedLength) {
		return "", invalidOpaqueCursorError(spec.label, errors.New("encoded document exceeds the safety bound"))
	}
	if err = scanOpaqueCursorJSONObject(encoded, spec.members); err != nil {
		return "", invalidOpaqueCursorError(spec.label, err)
	}
	raw := base64.RawURLEncoding.EncodeToString(encoded)
	if len(raw) > spec.maximumEncodedLength {
		return "", invalidOpaqueCursorError(spec.label, errors.New("encoded cursor exceeds the safety bound"))
	}
	return raw, nil
}

func decodeOpaqueCursor[T any](raw string, spec opaqueCursorSpec[T]) (T, error) {
	var zero T
	if err := validateOpaqueCursorSpec(spec); err != nil {
		return zero, err
	}
	if raw == "" || len(raw) > spec.maximumEncodedLength {
		return zero, invalidOpaqueCursorError(spec.label, errors.New("encoded cursor exceeds the safety bound"))
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return zero, invalidOpaqueCursorError(spec.label, err)
	}
	if len(decoded) == 0 || len(decoded) > base64.RawURLEncoding.DecodedLen(spec.maximumEncodedLength) || !utf8.Valid(decoded) {
		return zero, invalidOpaqueCursorError(spec.label, errors.New("decoded cursor is invalid"))
	}
	if err = scanOpaqueCursorJSONObject(decoded, spec.members); err != nil {
		return zero, invalidOpaqueCursorError(spec.label, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var value T
	if err = decoder.Decode(&value); err != nil {
		return zero, invalidOpaqueCursorError(spec.label, err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("cursor contains trailing data")
		}
		return zero, invalidOpaqueCursorError(spec.label, err)
	}
	if !spec.acceptsVersion(spec.version(value)) {
		return zero, invalidOpaqueCursorError(spec.label, errors.New("unsupported version"))
	}
	if err = spec.validate(value); err != nil {
		return zero, invalidOpaqueCursorError(spec.label, err)
	}
	return value, nil
}

func validateOpaqueCursorSpec[T any](spec opaqueCursorSpec[T]) error {
	if spec.label == "" || len(spec.label) > 64 || !utf8.ValidString(spec.label) ||
		spec.maximumEncodedLength <= 0 || spec.maximumEncodedLength > 4096 || spec.currentVersion <= 0 ||
		len(spec.members) == 0 ||
		spec.version == nil || spec.setVersion == nil || spec.acceptsVersion == nil || spec.validate == nil {
		return errors.New("invalid opaque cursor specification")
	}
	seen := make(map[string]struct{}, len(spec.members))
	for _, member := range spec.members {
		if member == "" || !utf8.ValidString(member) {
			return errors.New("invalid opaque cursor specification")
		}
		if _, duplicate := seen[member]; duplicate {
			return errors.New("invalid opaque cursor specification")
		}
		seen[member] = struct{}{}
	}
	return nil
}

func invalidOpaqueCursorError(label string, err error) error {
	if err == nil {
		err = errors.New("invalid value")
	}
	return fmt.Errorf("invalid %s cursor: %w", label, err)
}

// scanOpaqueCursorJSONObject verifies the cursor envelope without sharing the
// labels or ownership of request-body duplicate-member validation.
func scanOpaqueCursorJSONObject(data []byte, members []string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("cursor document must be a JSON object")
	}
	allowed := make(map[string]struct{}, len(members))
	for _, member := range members {
		allowed[member] = struct{}{}
	}
	seen := make(map[string]struct{}, len(members))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("cursor member name is invalid")
		}
		if _, exists := allowed[key]; !exists {
			return errors.New("cursor contains an unknown member")
		}
		if _, exists := seen[key]; exists {
			return errors.New("cursor contains a duplicate member")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return err
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New("cursor document is incomplete")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("cursor contains trailing data")
		}
		return err
	}
	return nil
}
