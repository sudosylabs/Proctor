// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The checks in this file read the generated OpenAPI document, which the
// agreement tests hold equal to the runtime routes and DTOs, and reject any
// operation that departs from one of the wire conventions. Each check is a
// pure function over a document so a planted violation proves it fails.

// wireDocument is a generic view of an OpenAPI document.
type wireDocument struct {
	paths      map[string]map[string]any
	components map[string]any
}

func parseWireDocument(t *testing.T, encoded []byte) wireDocument {
	t.Helper()
	var raw struct {
		Paths      map[string]map[string]any `json:"paths"`
		Components map[string]any            `json:"components"`
	}
	require.NoError(t, json.Unmarshal(encoded, &raw))
	return wireDocument{paths: raw.Paths, components: raw.Components}
}

func readWireDocument(t *testing.T) wireDocument {
	t.Helper()
	return parseWireDocument(t, readOpenAPIDocumentBytes(t))
}

var wireOperationMethods = []string{"get", "put", "post", "delete", "patch"}

type wireOperation struct {
	method, path string
	value        map[string]any
	pathItem     map[string]any
}

func (o wireOperation) key() string { return strings.ToUpper(o.method) + " " + o.path }

func (d wireDocument) operations() []wireOperation {
	var operations []wireOperation
	for path, item := range d.paths {
		for _, method := range wireOperationMethods {
			if value, ok := item[method].(map[string]any); ok {
				operations = append(operations, wireOperation{method: method, path: path, value: value, pathItem: item})
			}
		}
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].key() < operations[j].key() })
	return operations
}

// resolve follows local $ref chains.
func (d wireDocument) resolve(value any) map[string]any {
	object, _ := value.(map[string]any)
	for range 32 {
		ref, ok := object["$ref"].(string)
		if !ok {
			return object
		}
		parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
		if len(parts) != 2 {
			return nil
		}
		section, _ := d.components[parts[0]].(map[string]any)
		object, _ = section[parts[1]].(map[string]any)
	}
	return nil
}

// properties merges an object schema's properties, including allOf members.
func (d wireDocument) properties(schema map[string]any) map[string]any {
	schema = d.resolve(schema)
	merged := map[string]any{}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, value := range properties {
			merged[name] = value
		}
	}
	if members, ok := schema["allOf"].([]any); ok {
		for _, member := range members {
			for name, value := range d.properties(d.resolve(member)) {
				merged[name] = value
			}
		}
	}
	return merged
}

func (d wireDocument) parameters(operation wireOperation) []map[string]any {
	var parameters []map[string]any
	for _, source := range []any{operation.pathItem["parameters"], operation.value["parameters"]} {
		list, _ := source.([]any)
		for _, parameter := range list {
			if resolved := d.resolve(parameter); resolved != nil {
				parameters = append(parameters, resolved)
			}
		}
	}
	return parameters
}

// requestSchemas returns each object schema a request body carries: the JSON
// body itself and every object part of a multipart body.
func (d wireDocument) requestSchemas(operation wireOperation) []map[string]any {
	body := d.resolve(operation.value["requestBody"])
	content, _ := body["content"].(map[string]any)
	var schemas []map[string]any
	for mediaType, value := range content {
		media, _ := value.(map[string]any)
		schema := d.resolve(media["schema"])
		switch {
		case strings.HasPrefix(mediaType, "application/json"):
			schemas = append(schemas, schema)
		case strings.HasPrefix(mediaType, "multipart/"):
			for _, part := range d.properties(schema) {
				if resolved := d.resolve(part); len(d.properties(resolved)) > 0 {
					schemas = append(schemas, resolved)
				}
			}
		}
	}
	return schemas
}

// successResponses returns each 2xx response object of an operation.
func (d wireDocument) successResponses(operation wireOperation) map[string]map[string]any {
	responses, _ := operation.value["responses"].(map[string]any)
	success := map[string]map[string]any{}
	for status, value := range responses {
		if strings.HasPrefix(status, "2") {
			success[status] = d.resolve(value)
		}
	}
	return success
}

func (d wireDocument) jsonSchema(response map[string]any) map[string]any {
	content, _ := response["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	if media == nil {
		return nil
	}
	return d.resolve(media["schema"])
}

var revisionFenceMember = regexp.MustCompile(`^expected(_[a-z0-9_]+)?_revision$`)

// revisionFenceViolations enforces one optimistic-concurrency idiom: a JSON
// (or multipart metadata) body member named expected_revision. Qualified
// expected_<resource>_revision members appear only beside expected_revision,
// when one command fences more than one resource. No query or header
// parameter carries a revision, and If-Match is reserved for binary
// representations whose GET serves an entity tag.
func revisionFenceViolations(document wireDocument) []string {
	var violations []string
	for _, operation := range document.operations() {
		for _, parameter := range document.parameters(operation) {
			name, _ := parameter["name"].(string)
			location, _ := parameter["in"].(string)
			if (location == "query" || location == "header") && strings.Contains(strings.ToLower(name), "revision") {
				violations = append(violations, fmt.Sprintf("%s: %s parameter %q carries a revision fence; use a body expected_revision", operation.key(), location, name))
			}
			if location == "header" && strings.EqualFold(name, "If-Match") && !document.servesBinaryRepresentation(operation) {
				violations = append(violations, fmt.Sprintf("%s: If-Match on a JSON resource; use a body expected_revision", operation.key()))
			}
		}
		for _, schema := range document.requestSchemas(operation) {
			var fences []string
			for name := range document.properties(schema) {
				if revisionFenceMember.MatchString(name) {
					fences = append(fences, name)
				}
			}
			sort.Strings(fences)
			if len(fences) > 0 && !containsString(fences, "expected_revision") {
				violations = append(violations, fmt.Sprintf("%s: revision fence %v must be named expected_revision", operation.key(), fences))
			}
		}
	}
	return violations
}

// servesBinaryRepresentation reports whether the operation's path has a GET
// whose success is a non-JSON representation with an ETag.
func (d wireDocument) servesBinaryRepresentation(operation wireOperation) bool {
	get, ok := operation.pathItem["get"].(map[string]any)
	if !ok {
		return false
	}
	for _, response := range d.successResponses(wireOperation{method: "get", path: operation.path, value: get, pathItem: operation.pathItem}) {
		headers, _ := response["headers"].(map[string]any)
		content, _ := response["content"].(map[string]any)
		if _, etag := headers["ETag"]; !etag {
			continue
		}
		for mediaType := range content {
			if !strings.HasPrefix(mediaType, "application/json") {
				return true
			}
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestOpenAPIRevisionFencesUseOneIdiom(t *testing.T) {
	t.Parallel()
	require.Empty(t, revisionFenceViolations(readWireDocument(t)))
}

func TestOpenAPIRevisionFenceCheckRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	document := parseWireDocument(t, []byte(`{
	  "paths": {
	    "/things/{id}": {
	      "patch": {"requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Patch"}}}}},
	      "delete": {"parameters": [{"name": "expected_revision", "in": "query"}]},
	      "put": {"parameters": [{"name": "If-Match", "in": "header"}],
	              "requestBody": {"content": {"application/json": {"schema": {"properties": {"expected_revision": {}, "expected_parent_revision": {}}}}}}},
	      "post": {"requestBody": {"content": {"multipart/form-data": {"schema": {"properties": {"metadata": {"properties": {"expected_draft_revision": {}}}}}}}}}
	    },
	    "/pictures/{id}": {
	      "get": {"responses": {"200": {"headers": {"ETag": {}}, "content": {"image/webp": {}}}}},
	      "delete": {"parameters": [{"name": "If-Match", "in": "header"}]}
	    }
	  },
	  "components": {"schemas": {"Patch": {"properties": {"expected_thing_revision": {}}}}}
	}`))
	require.Equal(t, []string{
		"DELETE /things/{id}: query parameter \"expected_revision\" carries a revision fence; use a body expected_revision",
		"PATCH /things/{id}: revision fence [expected_thing_revision] must be named expected_revision",
		"POST /things/{id}: revision fence [expected_draft_revision] must be named expected_revision",
		"PUT /things/{id}: If-Match on a JSON resource; use a body expected_revision",
	}, revisionFenceViolations(document))
}

// optionalInstantViolations enforces omit-when-absent for instants: no
// date-time member, in a request or a response, is ever nullable. An absent
// optional instant is left out of the document.
func optionalInstantViolations(document wireDocument) []string {
	var violations []string
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch value := node.(type) {
		case map[string]any:
			if properties, ok := value["properties"].(map[string]any); ok {
				for name, property := range properties {
					if schema, ok := property.(map[string]any); ok && isDateTimeSchema(schema) && isNullableSchema(schema) {
						violations = append(violations, fmt.Sprintf("%s/%s: optional instant is nullable; omit it when absent", path, name))
					}
				}
			}
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(value[key], path+"/"+key)
			}
		case []any:
			for index, item := range value {
				walk(item, fmt.Sprintf("%s/%d", path, index))
			}
		}
	}
	walk(map[string]any{"paths": anyPaths(document.paths), "components": document.components}, "")
	sort.Strings(violations)
	return violations
}

func anyPaths(paths map[string]map[string]any) map[string]any {
	converted := make(map[string]any, len(paths))
	for path, item := range paths {
		converted[path] = item
	}
	return converted
}

func isDateTimeSchema(schema map[string]any) bool {
	if schema["format"] == "date-time" {
		return true
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		members, _ := schema[key].([]any)
		for _, member := range members {
			if object, ok := member.(map[string]any); ok && object["format"] == "date-time" {
				return true
			}
		}
	}
	return false
}

func isNullableSchema(schema map[string]any) bool {
	if schema["nullable"] == true || schema["type"] == "null" {
		return true
	}
	if types, ok := schema["type"].([]any); ok {
		for _, entry := range types {
			if entry == "null" {
				return true
			}
		}
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		members, _ := schema[key].([]any)
		for _, member := range members {
			if object, ok := member.(map[string]any); ok && isNullableSchema(object) {
				return true
			}
		}
	}
	return false
}

func TestOpenAPIOptionalInstantsAreOmittedNotNull(t *testing.T) {
	t.Parallel()
	require.Empty(t, optionalInstantViolations(readWireDocument(t)))
}

func TestOpenAPIOptionalInstantCheckRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	document := parseWireDocument(t, []byte(`{
	  "paths": {"/things": {"get": {"responses": {"200": {"content": {"application/json": {"schema": {
	    "properties": {"closed_at": {"type": ["string", "null"], "format": "date-time"}, "created_at": {"type": "string", "format": "date-time"}}}}}}}}}},
	  "components": {"schemas": {"Thing": {"properties": {
	    "ended_at": {"oneOf": [{"type": "string", "format": "date-time"}, {"type": "null"}]},
	    "note": {"type": ["string", "null"]}}}}}
	}`))
	require.Equal(t, []string{
		"/components/schemas/Thing/ended_at: optional instant is nullable; omit it when absent",
		"/paths//things/get/responses/200/content/application/json/schema/closed_at: optional instant is nullable; omit it when absent",
	}, optionalInstantViolations(document))
}

var pagingParameterName = regexp.MustCompile(`^(after|before|first|offset|page|since|start)(_|$)`)

// listEnvelopeViolations enforces one collection idiom. No success body is a
// bare JSON array. A GET whose body has items or next_cursor, takes limit or
// cursor, or answers a Link header is a list operation: it takes exactly the
// limit and cursor paging parameters and answers an object whose required
// items member is an array and whose optional next_cursor member is a
// string, with no other paging member (has_more, next_*) and no Link header.
// A GET answering an object whose only member is an array is a collection
// under another name and is rejected too.
func listEnvelopeViolations(document wireDocument) []string {
	var violations []string
	for _, operation := range document.operations() {
		for status, response := range document.successResponses(operation) {
			schema := document.jsonSchema(response)
			if schema == nil {
				continue
			}
			if schema["type"] == "array" {
				violations = append(violations, fmt.Sprintf("%s %s: success body is a bare array; use the items/next_cursor envelope", operation.key(), status))
				continue
			}
			if operation.method != "get" {
				continue
			}
			properties := document.properties(schema)
			parameters := map[string]bool{}
			for _, parameter := range document.parameters(operation) {
				if parameter["in"] == "query" {
					name, _ := parameter["name"].(string)
					parameters[name] = true
				}
			}
			headers, _ := response["headers"].(map[string]any)
			_, link := headers["Link"]
			_, items := properties["items"]
			_, next := properties["next_cursor"]
			if !(items || next || parameters["limit"] || parameters["cursor"] || link || soleArrayMember(document, properties)) {
				continue
			}
			key := operation.key()
			if link {
				violations = append(violations, key+": list operation answers a Link header; return next_cursor in the body")
			}
			if !parameters["limit"] || !parameters["cursor"] {
				violations = append(violations, key+": list operation must take the limit and cursor query parameters")
			}
			for name := range parameters {
				if pagingParameterName.MatchString(name) {
					violations = append(violations, fmt.Sprintf("%s: list operation takes paging parameter %q; page only with limit and cursor", key, name))
				}
			}
			if !items || document.resolve(properties["items"])["type"] != "array" || !containsAny(schema["required"], "items") {
				violations = append(violations, key+": list response must have a required items array")
			}
			if !next || document.resolve(properties["next_cursor"])["type"] != "string" || containsAny(schema["required"], "next_cursor") {
				violations = append(violations, key+": list response must have an optional next_cursor string")
			}
			for name := range properties {
				if name == "has_more" || (strings.HasPrefix(name, "next_") && name != "next_cursor") {
					violations = append(violations, fmt.Sprintf("%s: list response has paging member %q; next_cursor alone signals continuation", key, name))
				}
			}
		}
	}
	sort.Strings(violations)
	return violations
}

func soleArrayMember(document wireDocument, properties map[string]any) bool {
	if len(properties) != 1 {
		return false
	}
	for _, property := range properties {
		return document.resolve(property)["type"] == "array"
	}
	return false
}

func containsAny(list any, want string) bool {
	values, _ := list.([]any)
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestOpenAPIListOperationsUseOneEnvelope(t *testing.T) {
	t.Parallel()
	require.Empty(t, listEnvelopeViolations(readWireDocument(t)))
}

func TestOpenAPIListEnvelopeCheckRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	limit := `{"name": "limit", "in": "query"}`
	cursor := `{"name": "cursor", "in": "query"}`
	envelope := `{"type": "object", "required": ["items"], "properties": {"items": {"type": "array"}, "next_cursor": {"type": "string"}}}`
	document := parseWireDocument(t, []byte(`{
	  "paths": {
	    "/good": {"get": {"parameters": [`+limit+`, `+cursor+`], "responses": {"200": {"content": {"application/json": {"schema": `+envelope+`}}}}}},
	    "/bare": {"get": {"responses": {"200": {"content": {"application/json": {"schema": {"type": "array"}}}}}}},
	    "/linked": {"get": {"parameters": [`+limit+`, `+cursor+`, {"name": "after_id", "in": "query"}], "responses": {"200": {"headers": {"Link": {}}, "content": {"application/json": {"schema": `+envelope+`}}}}}},
	    "/renamed": {"get": {"parameters": [`+limit+`], "responses": {"200": {"content": {"application/json": {"schema": {"type": "object", "required": ["events", "has_more"], "properties": {"events": {"type": "array"}, "has_more": {"type": "boolean"}}}}}}}}},
	    "/sources": {"get": {"responses": {"200": {"content": {"application/json": {"schema": {"type": "object", "properties": {"sources": {"type": "array"}}}}}}}}},
	    "/resource": {"get": {"responses": {"200": {"content": {"application/json": {"schema": {"type": "object", "properties": {"id": {"type": "string"}, "tags": {"type": "array"}}}}}}}}}
	  },
	  "components": {}
	}`))
	require.Equal(t, []string{
		"GET /bare 200: success body is a bare array; use the items/next_cursor envelope",
		"GET /linked: list operation answers a Link header; return next_cursor in the body",
		"GET /linked: list operation takes paging parameter \"after_id\"; page only with limit and cursor",
		"GET /renamed: list operation must take the limit and cursor query parameters",
		"GET /renamed: list response has paging member \"has_more\"; next_cursor alone signals continuation",
		"GET /renamed: list response must have a required items array",
		"GET /renamed: list response must have an optional next_cursor string",
		"GET /sources: list operation must take the limit and cursor query parameters",
		"GET /sources: list response must have a required items array",
		"GET /sources: list response must have an optional next_cursor string",
	}, listEnvelopeViolations(document))
}

// requireOpenAPIListEnvelope asserts that schema is the collection envelope
// whose items reference itemRef.
func requireOpenAPIListEnvelope(t *testing.T, schema openAPISchema, itemRef string) {
	t.Helper()
	require.Equal(t, "object", schema.Type)
	require.Equal(t, []string{"items"}, schema.Required)
	var items struct {
		Type  string           `json:"type"`
		Items openAPIReference `json:"items"`
	}
	require.NoError(t, json.Unmarshal(schema.Properties["items"], &items))
	require.Equal(t, "array", items.Type)
	require.Equal(t, itemRef, items.Items.Ref)
	var next openAPIReference
	require.NoError(t, json.Unmarshal(schema.Properties["next_cursor"], &next))
	require.Equal(t, "#/components/schemas/NextCursor", next.Ref)
}
