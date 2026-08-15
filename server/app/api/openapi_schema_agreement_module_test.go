// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func evaluateOpenAPISchemaAgreement(
	violations []openAPIAgreementViolation,
	document openAPIDocument,
	contract openAPIAgreementSchema,
) []openAPIAgreementViolation {
	target := "schema " + contract.Name
	schema, exists := document.Components.Schemas[contract.Name]
	if !exists {
		return appendAgreementViolation(violations, target, "component", "is missing")
	}
	if schema.Type != "object" || schema.AdditionalProperties != false {
		violations = appendAgreementViolation(violations, target, "shape", "is not a closed object")
	}

	dto := contract.DTO
	for dto.Kind() == reflect.Pointer {
		dto = dto.Elem()
	}
	gotProperties := sortedMapKeys(schema.Properties)
	wantProperties := jsonFieldNames(dto)
	if !reflect.DeepEqual(gotProperties, wantProperties) {
		violations = appendAgreementViolation(violations, target, "fields", fmt.Sprintf("got %v, want DTO fields %v", gotProperties, wantProperties))
	}
	gotRequired := sortedStrings(schema.Required)
	wantRequired := sortedStrings(contract.Required)
	if !reflect.DeepEqual(gotRequired, wantRequired) {
		violations = appendAgreementViolation(violations, target, "required fields", fmt.Sprintf("got %v, want %v", gotRequired, wantRequired))
	}

	for index := 0; index < dto.NumField(); index++ {
		field := dto.Field(index)
		name := jsonFieldName(field)
		if name == "" {
			continue
		}
		raw, exists := schema.Properties[name]
		if !exists {
			continue
		}
		var shape openAPISchemaShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			violations = appendAgreementViolation(violations, target+"."+name, "decode", err.Error())
			continue
		}
		violations = evaluateOpenAPIShapeAgreement(
			violations,
			document,
			target+"."+name,
			shape,
			field.Type,
			strings.HasSuffix(contract.Name, "Request"),
			!stringSliceContains(contract.Required, name),
			stringSliceContains(contract.Nullable, name),
			stringSliceContains(contract.NonNullable, name),
			nestedAgreementPaths(contract.Nullable, name),
			nestedAgreementPaths(contract.NonNullable, name),
		)
	}
	return violations
}

func evaluateOpenAPIShapeAgreement(
	violations []openAPIAgreementViolation,
	document openAPIDocument,
	target string,
	shape openAPISchemaShape,
	goType reflect.Type,
	requestSchema bool,
	fieldOptional bool,
	forceNullable bool,
	forceNonNullable bool,
	nullablePaths []string,
	nonNullablePaths []string,
) []openAPIAgreementViolation {
	nullable := forceNullable
	for goType.Kind() == reflect.Pointer {
		nullable = nullable || requestSchema && fieldOptional
		goType = goType.Elem()
	}
	if goType.PkgPath() == reflect.TypeOf(Optional[string]{}).PkgPath() &&
		strings.HasPrefix(goType.Name(), "Optional[") {
		nullable = true
		goType = goType.Field(0).Type
	}
	if forceNonNullable {
		nullable = false
	}
	if shape.Ref != "" {
		const prefix = "#/components/schemas/"
		name := strings.TrimPrefix(shape.Ref, prefix)
		component, exists := document.Components.Schemas[name]
		if name == shape.Ref || !exists {
			return appendAgreementViolation(violations, target, "reference", fmt.Sprintf("unresolved %q", shape.Ref))
		}
		encoded, err := json.Marshal(component)
		if err != nil {
			return appendAgreementViolation(violations, target, "reference", fmt.Sprintf("encode %q: %v", shape.Ref, err))
		}
		if err := json.Unmarshal(encoded, &shape); err != nil {
			return appendAgreementViolation(violations, target, "reference", fmt.Sprintf("decode %q: %v", shape.Ref, err))
		}
	}

	wantType := map[reflect.Kind]string{
		reflect.Bool: "boolean", reflect.Int: "integer", reflect.Int8: "integer",
		reflect.Int16: "integer", reflect.Int32: "integer", reflect.Int64: "integer",
		reflect.Uint: "integer", reflect.Uint8: "integer", reflect.Uint16: "integer",
		reflect.Uint32: "integer", reflect.Uint64: "integer", reflect.String: "string",
		reflect.Slice: "array", reflect.Array: "array", reflect.Map: "object",
		reflect.Struct: "object",
	}[goType.Kind()]
	if wantType == "" {
		return appendAgreementViolation(violations, target, "type", fmt.Sprintf("unsupported Go type %s", goType))
	}
	wantTypes := []string{wantType}
	if nullable {
		wantTypes = append(wantTypes, "null")
	}
	if !openAPITypesEqual(shape.Type, wantTypes) {
		return appendAgreementViolation(violations, target, "type", fmt.Sprintf("got %#v, want JSON types %v", shape.Type, wantTypes))
	}
	if goType.Kind() == reflect.Int64 || goType.Kind() == reflect.Uint64 {
		if shape.Format != "int64" {
			violations = appendAgreementViolation(violations, target, "format", fmt.Sprintf("got %q, want int64", shape.Format))
		}
	}
	if goType.Kind() == reflect.Slice || goType.Kind() == reflect.Array {
		if shape.Items == nil {
			return appendAgreementViolation(violations, target, "items", "array item schema is missing")
		}
		return evaluateOpenAPIShapeAgreement(violations, document, target+"[]", *shape.Items, goType.Elem(), requestSchema, false, false, false, nullablePaths, nonNullablePaths)
	}
	if goType.Kind() == reflect.Map {
		var additional openAPISchemaShape
		if goType.Key().Kind() != reflect.String ||
			len(shape.AdditionalProperties) == 0 ||
			json.Unmarshal(shape.AdditionalProperties, &additional) != nil {
			return appendAgreementViolation(violations, target, "additional properties", "map schema does not declare string-keyed values")
		}
		return evaluateOpenAPIShapeAgreement(violations, document, target+"{}", additional, goType.Elem(), requestSchema, false, false, false, nil, nil)
	}
	if goType.Kind() != reflect.Struct {
		return violations
	}

	gotProperties := sortedMapKeys(shape.Properties)
	wantProperties := jsonFieldNames(goType)
	if !reflect.DeepEqual(gotProperties, wantProperties) {
		violations = appendAgreementViolation(violations, target, "fields", fmt.Sprintf("got %v, want Go fields %v", gotProperties, wantProperties))
	}
	gotRequired := sortedStrings(shape.Required)
	wantRequired := requiredJSONFieldNames(goType)
	if !reflect.DeepEqual(gotRequired, wantRequired) {
		violations = appendAgreementViolation(violations, target, "required fields", fmt.Sprintf("got %v, Go JSON tags require %v", gotRequired, wantRequired))
	}
	for index := 0; index < goType.NumField(); index++ {
		field := goType.Field(index)
		name := jsonFieldName(field)
		if name == "" {
			continue
		}
		raw, exists := shape.Properties[name]
		if !exists {
			continue
		}
		var property openAPISchemaShape
		if err := json.Unmarshal(raw, &property); err != nil {
			violations = appendAgreementViolation(violations, target+"."+name, "decode", err.Error())
			continue
		}
		violations = evaluateOpenAPIShapeAgreement(
			violations,
			document,
			target+"."+name,
			property,
			field.Type,
			requestSchema,
			!stringSliceContains(shape.Required, name),
			stringSliceContains(nullablePaths, name),
			stringSliceContains(nonNullablePaths, name),
			nestedAgreementPaths(nullablePaths, name),
			nestedAgreementPaths(nonNullablePaths, name),
		)
	}
	return violations
}

func jsonFieldName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("json"), ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

func sortedMapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func jsonFieldNames(dto reflect.Type) []string {
	fields := make([]string, 0, dto.NumField())
	for index := 0; index < dto.NumField(); index++ {
		name := jsonFieldName(dto.Field(index))
		if name != "" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func requiredJSONFieldNames(dto reflect.Type) []string {
	fields := make([]string, 0, dto.NumField())
	for index := 0; index < dto.NumField(); index++ {
		tag := dto.Field(index).Tag.Get("json")
		parts := strings.Split(tag, ",")
		if parts[0] == "" || parts[0] == "-" {
			continue
		}
		optional := false
		for _, option := range parts[1:] {
			optional = optional || option == "omitempty"
		}
		if !optional {
			fields = append(fields, parts[0])
		}
	}
	sort.Strings(fields)
	return fields
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func nestedAgreementPaths(values []string, field string) []string {
	prefix := field + "."
	var nested []string
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			nested = append(nested, strings.TrimPrefix(value, prefix))
		}
	}
	return nested
}

func openAPITypesEqual(value any, want []string) bool {
	var got []string
	switch types := value.(type) {
	case string:
		got = []string{types}
	case []any:
		for _, candidate := range types {
			value, ok := candidate.(string)
			if !ok {
				return false
			}
			got = append(got, value)
		}
	default:
		return false
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	return reflect.DeepEqual(got, want)
}
