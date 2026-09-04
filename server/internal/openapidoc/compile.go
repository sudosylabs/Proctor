// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package openapidoc compiles the human-authored OpenAPI source tree into the
// reviewed JSON artifact consumed by the server and documentation site.
package openapidoc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

const baseFile = "base.yaml"

var allowedBaseKeys = map[string]struct{}{
	"externalDocs":      {},
	"info":              {},
	"jsonSchemaDialect": {},
	"openapi":           {},
	"security":          {},
	"servers":           {},
	"tags":              {},
}

// Compile discovers and merges a source tree, validates the resulting OpenAPI
// contract, and returns its deterministic JSON representation.
func Compile(source fs.FS) ([]byte, error) {
	if source == nil {
		return nil, errors.New("OpenAPI source filesystem is required")
	}

	document, err := decodeYAMLFile(source, baseFile)
	if err != nil {
		return nil, err
	}
	for key := range document {
		if _, allowed := allowedBaseKeys[key]; !allowed && !strings.HasPrefix(key, "x-") {
			return nil, fmt.Errorf("%s: top-level key %q belongs in a fragment", baseFile, key)
		}
	}
	document["paths"] = map[string]any{}
	document["components"] = map[string]any{}

	fragmentNames, err := discoverFragments(source)
	if err != nil {
		return nil, err
	}
	if len(fragmentNames) == 0 {
		return nil, errors.New("OpenAPI source contains no fragments")
	}
	owners := fragmentOwners{paths: make(map[string]string), components: make(map[string]string)}
	for _, name := range fragmentNames {
		fragment, err := decodeYAMLFile(source, name)
		if err != nil {
			return nil, err
		}
		if err := mergeFragment(document, fragment, name, owners); err != nil {
			return nil, err
		}
	}

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode compiled OpenAPI document: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := validate(encoded); err != nil {
		return nil, fmt.Errorf("validate compiled OpenAPI document: %w", err)
	}
	return encoded, nil
}

func discoverFragments(source fs.FS) ([]string, error) {
	var names []string
	err := fs.WalkDir(source, "fragments", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		extension := strings.ToLower(path.Ext(name))
		if extension == ".yaml" || extension == ".yml" {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover OpenAPI fragments: %w", err)
	}
	sort.Strings(names)
	return names, nil
}

func decodeYAMLFile(source fs.FS, name string) (map[string]any, error) {
	encoded, err := fs.ReadFile(source, name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	var value map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple YAML documents")
		}
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if value == nil {
		return nil, fmt.Errorf("decode %s: document is empty", name)
	}
	return value, nil
}

type fragmentOwners struct {
	paths      map[string]string
	components map[string]string
}

func mergeFragment(document, fragment map[string]any, name string, owners fragmentOwners) error {
	if len(fragment) == 0 {
		return fmt.Errorf("%s: fragment is empty", name)
	}
	for key := range fragment {
		if key != "paths" && key != "components" {
			return fmt.Errorf("%s: unsupported top-level key %q; fragments may contain only paths and components", name, key)
		}
	}

	if rawPaths, exists := fragment["paths"]; exists {
		fragmentPaths, ok := rawPaths.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: paths must be an object", name)
		}
		documentPaths := document["paths"].(map[string]any)
		for route, item := range fragmentPaths {
			if _, duplicate := documentPaths[route]; duplicate {
				return fmt.Errorf("%s: path %q is already declared by %s", name, route, owners.paths[route])
			}
			documentPaths[route] = item
			owners.paths[route] = name
		}
	}

	if rawComponents, exists := fragment["components"]; exists {
		fragmentComponents, ok := rawComponents.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: components must be an object", name)
		}
		documentComponents := document["components"].(map[string]any)
		for kind, rawEntries := range fragmentComponents {
			fragmentEntries, ok := rawEntries.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: components.%s must be an object", name, kind)
			}
			documentEntries, exists := documentComponents[kind]
			if !exists {
				documentEntries = map[string]any{}
				documentComponents[kind] = documentEntries
			}
			entries := documentEntries.(map[string]any)
			for componentName, component := range fragmentEntries {
				componentID := kind + "." + componentName
				if _, duplicate := entries[componentName]; duplicate {
					return fmt.Errorf("%s: component %s is already declared by %s", name, componentID, owners.components[componentID])
				}
				entries[componentName] = component
				owners.components[componentID] = name
			}
		}
	}
	return nil
}

func validate(encoded []byte) error {
	document, err := openapi3.NewLoader().LoadFromData(encoded)
	if err != nil {
		return err
	}
	if err := document.Validate(context.Background()); err != nil {
		return err
	}

	documentedTags := make(map[string]struct{}, len(document.Tags))
	for index, tag := range document.Tags {
		if tag == nil || strings.TrimSpace(tag.Name) == "" {
			return fmt.Errorf("tag %d name is required", index)
		}
		if strings.TrimSpace(tag.Description) == "" {
			return fmt.Errorf("tag %q description is required", tag.Name)
		}
		if _, duplicate := documentedTags[tag.Name]; duplicate {
			return fmt.Errorf("tag %q is duplicated", tag.Name)
		}
		documentedTags[tag.Name] = struct{}{}
	}
	if len(documentedTags) == 0 {
		return errors.New("at least one top-level tag is required")
	}

	operationIDs := make(map[string]string)
	for route, pathItem := range document.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			location := method + " " + route
			if strings.TrimSpace(operation.OperationID) == "" {
				return fmt.Errorf("%s operationId is required", location)
			}
			if prior, duplicate := operationIDs[operation.OperationID]; duplicate {
				return fmt.Errorf("operationId %q is shared by %s and %s", operation.OperationID, prior, location)
			}
			operationIDs[operation.OperationID] = location
			if strings.TrimSpace(operation.Summary) == "" {
				return fmt.Errorf("%s summary is required", location)
			}
			if len(operation.Tags) != 1 {
				return fmt.Errorf("%s exactly one tag is required", location)
			}
			if _, exists := documentedTags[operation.Tags[0]]; !exists {
				return fmt.Errorf("%s tag %q is not declared", location, operation.Tags[0])
			}
			if operation.Security == nil {
				return fmt.Errorf("%s security is required", location)
			}
			auth, ok := operation.Extensions["x-proctor-auth"].(string)
			if !ok || strings.TrimSpace(auth) == "" {
				return fmt.Errorf("%s x-proctor-auth is required", location)
			}
			if _, ok := operation.Extensions["x-proctor-error-codes"].([]any); !ok {
				return fmt.Errorf("%s x-proctor-error-codes must be an array", location)
			}
			idempotency, ok := operation.Extensions["x-proctor-idempotency"].(string)
			if !ok || idempotency != "none" && idempotency != "optional" && idempotency != "required" {
				return fmt.Errorf("%s x-proctor-idempotency must be none, optional, or required", location)
			}
		}
	}
	return nil
}
