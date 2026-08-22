// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
)

func TestExampleConfigurationIsCompleteCanonicalDefault(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Version", "Server", "Database", "Cache", "Cluster"} {
		if _, exists := document[key]; !exists {
			t.Fatalf("example configuration is missing PascalCase key %q", key)
		}
	}
	if _, exists := document["version"]; exists {
		t.Fatal("example configuration contains legacy lower-case key version")
	}
	assertExampleContainsEveryField(t, data, reflect.TypeOf(Config{}), "Config")

	var example Config
	if err := decodeStrict(data, &example); err != nil {
		t.Fatalf("decode example configuration: %v", err)
	}
	if !reflect.DeepEqual(example, Default()) {
		t.Fatalf("example configuration does not match config.Default()\nexample: %#v\ndefault: %#v", example, Default())
	}

	fileStore, err := NewFileStore("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(context.Background(), fileStore, StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatalf("load example configuration: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
}

func TestConfigurationSerializationNeverOmitsFields(t *testing.T) {
	t.Parallel()

	assertNoOmitEmptyFields(t, reflect.TypeOf(Config{}), map[reflect.Type]bool{})
}

func TestStructuredConfigurationExamplesAreCompleteAndValid(t *testing.T) {
	t.Parallel()

	t.Run("execution host", func(t *testing.T) {
		var host ExecutionHost
		data := decodeStructuredExample(t, "examples/execution-host.json", &host)
		assertExampleContainsEveryField(t, data, reflect.TypeOf(host), "ExecutionHost")
		configuration := Default()
		configuration.Execution.Enabled = true
		configuration.Execution.Hosts = []ExecutionHost{host}
		if err := configuration.Validate(); err != nil {
			t.Fatalf("validate execution-host example: %v", err)
		}
	})

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
			if err := configuration.Validate(); err != nil {
				t.Fatalf("validate provider example: %v", err)
			}
		})
	}
}

func decodeStructuredExample[T any](t *testing.T, path string, target *T) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("%s must contain exactly one JSON value: %v", path, err)
	}
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
		if len(parts) == 0 || parts[0] == "" {
			t.Fatalf("%s.%s has no JSON field name", typeOf.Name(), field.Name)
		}
		for _, option := range parts[1:] {
			if option == "omitempty" {
				t.Fatalf("%s.%s uses omitempty", typeOf.Name(), field.Name)
			}
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
		if err := json.Unmarshal(data, &elements); err != nil {
			t.Fatalf("%s must be an array: %v", path, err)
		}
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
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("%s must be an object: %v", path, err)
	}
	for index := range typeOf.NumField() {
		field := typeOf.Field(index)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		value, exists := object[name]
		if !exists {
			t.Fatalf("%s is missing field %s", path, name)
		}
		assertExampleContainsEveryField(t, value, field.Type, path+"."+name)
	}
}

func implementsJSONMarshaler(typeOf reflect.Type) bool {
	marshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	return typeOf.Implements(marshaler) || reflect.PointerTo(typeOf).Implements(marshaler)
}
