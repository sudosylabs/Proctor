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
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExampleConfigurationIsCompleteCanonicalDefault(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("config.example.json")
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &document))
	for _, key := range []string{"Version", "Server", "Metrics", "Database", "Cache", "Cluster"} {
		_, exists := document[key]
		require.True(t, exists, "example configuration is missing PascalCase key %q", key)
	}
	_, exists := document["version"]
	require.False(t, exists, "example configuration contains legacy lower-case key version")
	assertExampleContainsEveryField(t, data, reflect.TypeOf(Config{}), "Config")

	var example Config
	require.NoError(t, decodeStrict(data, &example))
	require.Equal(t, Default(), example, "example configuration does not match config.Default()\nexample")

	fileStore, err := NewFileStore("config.example.json")
	require.NoError(t, err)
	store, err := NewStore(context.Background(), fileStore, StoreOptions{LookupEnv: noEnvironment})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
}

func TestConfigurationSerializationNeverOmitsFields(t *testing.T) {
	t.Parallel()

	assertNoOmitEmptyFields(t, reflect.TypeOf(Config{}), map[reflect.Type]bool{})
}

func TestStructuredConfigurationExamplesAreCompleteAndValid(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		path string
	}{
		{name: "CAS provider", path: "examples/cas-provider.json"},
		{name: "OIDC provider", path: "examples/oidc-provider.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var provider ExternalAuthenticationProvider
			data := decodeStructuredExample(t, test.path, &provider)
			assertExampleContainsEveryField(
				t,
				data,
				reflect.TypeOf(provider),
				"ExternalAuthenticationProvider",
			)
			configuration := Default()
			configuration.Authentication.External.Providers = []ExternalAuthenticationProvider{provider}
			require.NoError(t, configuration.Validate())
		})
	}
}

func decodeStructuredExample[T any](t *testing.T, path string, target *T) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(target), "decode %s", path)
	require.Equal(t, io.EOF, decoder.Decode(new(any)), "%s must contain exactly one JSON value", path)
	return data
}

func assertNoOmitEmptyFields(
	t *testing.T,
	typeOf reflect.Type,
	visited map[reflect.Type]bool,
) {
	t.Helper()
	for typeOf.Kind() == reflect.Pointer || typeOf.Kind() == reflect.Slice {
		typeOf = typeOf.Elem()
	}
	if typeOf.Kind() != reflect.Struct || implementsJSONMarshaler(typeOf) || visited[typeOf] {
		return
	}
	visited[typeOf] = true
	for index := range typeOf.NumField() {
		field := typeOf.Field(index)
		if !field.IsExported() {
			continue
		}
		parts := strings.Split(field.Tag.Get("json"), ",")
		require.NotEmpty(t, parts)
		require.NotEmpty(t, parts[0])
		for _, option := range parts[1:] {
			require.NotEqual(t, "omitempty", option)
		}
		assertNoOmitEmptyFields(t, field.Type, visited)
	}
}

func assertExampleContainsEveryField(
	t *testing.T,
	data []byte,
	typeOf reflect.Type,
	path string,
) {
	t.Helper()
	for typeOf.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return
		}
		typeOf = typeOf.Elem()
	}
	if typeOf.Kind() == reflect.Slice {
		var elements []json.RawMessage
		require.NoError(t, json.Unmarshal(data, &elements), "%s must be an array", path)
		for index, element := range elements {
			assertExampleContainsEveryField(
				t,
				element,
				typeOf.Elem(),
				fmt.Sprintf("%s[%d]", path, index),
			)
		}
		return
	}
	if typeOf.Kind() != reflect.Struct || implementsJSONMarshaler(typeOf) {
		return
	}
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &object), "%s must be an object", path)
	for index := range typeOf.NumField() {
		field := typeOf.Field(index)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		value, exists := object[name]
		require.True(t, exists, "%s is missing field %s", path, name)
		assertExampleContainsEveryField(t, value, field.Type, path+"."+name)
	}
}

func implementsJSONMarshaler(typeOf reflect.Type) bool {
	marshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	return typeOf.Implements(marshaler) || reflect.PointerTo(typeOf).Implements(marshaler)
}
