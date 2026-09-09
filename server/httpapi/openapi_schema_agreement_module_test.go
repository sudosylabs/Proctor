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
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
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

	for _, field := range serializedJSONFields(dto) {
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
	if goType == reflect.TypeOf(model.BrowserActivityEvent{}) {
		return evaluateBrowserEventCodecAgreement(violations, document, target, shape, requestSchema)
	}
	if goType == reflect.TypeOf(model.NativeRecord{}) {
		return evaluateNativeRecordCodecAgreement(violations, document, target, shape, requestSchema)
	}
	if goType == reflect.TypeOf(model.NativeFamilyPolicy{}) {
		return evaluateNativeFamilyCodecAgreement(violations, document, target, shape, requestSchema)
	}
	if goType == reflect.TypeOf(model.SecurityPolicyScope{}) {
		return evaluateSecurityScopeCodecAgreement(violations, document, target, shape, requestSchema)
	}
	unionNullable := false
	if len(shape.OneOf) > 0 {
		if len(shape.OneOf) != 2 {
			return appendAgreementViolation(violations, target, "oneOf", "nullable union must contain exactly one value schema and null")
		}
		var value *openAPISchemaShape
		for index := range shape.OneOf {
			candidate := shape.OneOf[index]
			if openAPITypesEqual(candidate.Type, []string{"null"}) {
				unionNullable = true
				continue
			}
			if value != nil {
				return appendAgreementViolation(violations, target, "oneOf", "nullable union contains multiple value schemas")
			}
			value = &candidate
		}
		if !unionNullable || value == nil {
			return appendAgreementViolation(violations, target, "oneOf", "nullable union is missing its value or null schema")
		}
		shape = *value
		nullable = true
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
	if unionNullable {
		switch value := shape.Type.(type) {
		case string:
			shape.Type = []any{value, "null"}
		case []any:
			shape.Type = append(value, "null")
		}
	}

	if goType == reflect.TypeOf(time.Time{}) {
		wantTimeTypes := []string{"string"}
		if nullable {
			wantTimeTypes = append(wantTimeTypes, "null")
		}
		if !openAPITypesEqual(shape.Type, wantTimeTypes) || shape.Format != "date-time" {
			return appendAgreementViolation(violations, target, "time codec", "must be a date-time string")
		}
		return violations
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
	for _, field := range serializedJSONFields(goType) {
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
	for _, field := range serializedJSONFields(dto) {
		name := jsonFieldName(field)
		if name != "" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func requiredJSONFieldNames(dto reflect.Type) []string {
	fields := make([]string, 0, dto.NumField())
	for _, field := range serializedJSONFields(dto) {
		tag := field.Tag.Get("json")
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

// The native family is a custom codec with private typed state. Verify every
// branch against its actual serialized fields instead of reflecting that state.
func evaluateNativeFamilyCodecAgreement(violations []openAPIAgreementViolation, document openAPIDocument, target string, shape openAPISchemaShape, requestSchema bool) []openAPIAgreementViolation {
	families := model.DefaultNativeSecurityPolicy().Families
	if len(shape.OneOf) != len(families) {
		return appendAgreementViolation(violations, target, "oneOf", "must describe all ten native family codec branches")
	}
	for index, family := range families {
		raw, err := json.Marshal(family)
		if err != nil {
			panic(err)
		}
		var members map[string]any
		if err := json.Unmarshal(raw, &members); err != nil {
			panic(err)
		}
		names := make([]string, 0, len(members))
		for name := range members {
			names = append(names, name)
		}
		sort.Strings(names)
		fields := make([]reflect.StructField, 0, len(names))
		for i, name := range names {
			typ := reflect.TypeOf(members[name])
			if _, ok := members[name].([]any); ok {
				typ = reflect.TypeOf([]string{})
			}
			fields = append(fields, reflect.StructField{Name: fmt.Sprintf("Field%d", i), Type: typ, Tag: reflect.StructTag(fmt.Sprintf(`json:"%s"`, name))})
		}
		branch := shape.OneOf[index]
		var id struct {
			Const string `json:"const"`
		}
		_ = json.Unmarshal(branch.Properties["id"], &id)
		if id.Const != family.ID() {
			violations = appendAgreementViolation(violations, target, "family id", fmt.Sprintf("branch %d must identify %s", index, family.ID()))
		}
		if string(branch.AdditionalProperties) != "false" {
			violations = appendAgreementViolation(violations, target, "family shape", "must reject extra fields")
		}
		violations = evaluateOpenAPIShapeAgreement(violations, document, target+"."+family.ID(), branch, reflect.StructOf(fields), requestSchema, false, false, false, nil, nil)
	}
	return violations
}

func serializedJSONFields(dto reflect.Type) []reflect.StructField {
	var fields []reflect.StructField
	for i := 0; i < dto.NumField(); i++ {
		field := dto.Field(i)
		embeddedType := field.Type
		if embeddedType.Kind() == reflect.Pointer {
			embeddedType = embeddedType.Elem()
		}
		if field.Anonymous && field.Tag.Get("json") == "" && embeddedType.Kind() == reflect.Struct {
			fields = append(fields, serializedJSONFields(embeddedType)...)
		} else {
			fields = append(fields, field)
		}
	}
	return fields
}
func evaluateSecurityScopeCodecAgreement(violations []openAPIAgreementViolation, document openAPIDocument, target string, shape openAPISchemaShape, requestSchema bool) []openAPIAgreementViolation {
	scopes := []model.SecurityPolicyScope{{Kind: "admission", AdmissionScopeID: "admission"}, {Kind: "attempt", AttemptID: model.NewExamAttemptID()}}
	if len(shape.OneOf) != len(scopes) {
		return appendAgreementViolation(violations, target, "scope union", "must contain exact admission and attempt variants")
	}
	for i, scope := range scopes {
		raw, _ := json.Marshal(scope)
		var members map[string]string
		if err := json.Unmarshal(raw, &members); err != nil {
			panic(err)
		}
		names := make([]string, 0, len(members))
		for name := range members {
			names = append(names, name)
		}
		sort.Strings(names)
		fields := make([]reflect.StructField, 0, len(names))
		for j, name := range names {
			fields = append(fields, reflect.StructField{Name: fmt.Sprintf("Field%d", j), Type: reflect.TypeOf(""), Tag: reflect.StructTag(fmt.Sprintf(`json:"%s"`, name))})
		}
		branch := shape.OneOf[i]
		var kind struct {
			Const string `json:"const"`
		}
		_ = json.Unmarshal(branch.Properties["kind"], &kind)
		if kind.Const != scope.Kind || string(branch.AdditionalProperties) != "false" {
			violations = appendAgreementViolation(violations, target, "scope variant", "must be closed and identify the exact kind")
		}
		violations = evaluateOpenAPIShapeAgreement(violations, document, target+"."+scope.Kind, branch, reflect.StructOf(fields), requestSchema, false, false, false, nil, nil)
	}
	return violations
}

func evaluateNativeRecordCodecAgreement(violations []openAPIAgreementViolation, document openAPIDocument, target string, shape openAPISchemaShape, requestSchema bool) []openAPIAgreementViolation {
	if shape.Ref != "" {
		component, ok := document.Components.Schemas[strings.TrimPrefix(shape.Ref, "#/components/schemas/")]
		if !ok {
			return appendAgreementViolation(violations, target, "reference", "missing native record schema")
		}
		raw, err := json.Marshal(component)
		if err != nil {
			panic(err)
		}
		if err := json.Unmarshal(raw, &shape); err != nil {
			panic(err)
		}
	}
	variants := []reflect.Type{reflect.TypeOf(model.NativeOccurrence{}), reflect.TypeOf(model.NativeCoverageTransition{}), reflect.TypeOf(model.NativeSourceReset{}), reflect.TypeOf(model.NativeSourceGap{})}
	if len(shape.OneOf) != len(variants) {
		return appendAgreementViolation(violations, target, "oneOf", "must describe the four closed native record variants")
	}
	for i, variant := range variants {
		violations = evaluateOpenAPIShapeAgreement(violations, document, target+"."+variant.Name(), shape.OneOf[i], variant, requestSchema, false, false, false, nil, nil)
	}
	return violations
}

// The Browser event codec is a closed discriminated union, despite its compact
// in-memory representation. These independent wire shapes cover its five cases.
type browserLifecycleAgreement struct {
	Sequence         int64     `json:"sequence"`
	Kind             string    `json:"kind"`
	PolicyRevisionID string    `json:"policy_revision_id"`
	ClientOccurredAt time.Time `json:"client_occurred_at"`
}
type browserNavigationAgreement struct {
	browserLifecycleAgreement
	Location      model.BrowserLocation `json:"location"`
	MatchedRuleID string                `json:"matched_rule_id"`
}
type browserRedirectAgreement struct {
	browserNavigationAgreement
	RedirectFromSequence int64 `json:"redirect_from_sequence"`
}
type browserBlockedAgreement struct {
	browserLifecycleAgreement
	Location             model.BrowserLocation `json:"location"`
	MatchedRuleID        string                `json:"matched_rule_id,omitempty"`
	BlockReason          string                `json:"block_reason"`
	RedirectFromSequence int64                 `json:"redirect_from_sequence,omitempty"`
}

func evaluateBrowserEventCodecAgreement(violations []openAPIAgreementViolation, document openAPIDocument, target string, shape openAPISchemaShape, request bool) []openAPIAgreementViolation {
	if shape.Ref != "" {
		component, ok := document.Components.Schemas[strings.TrimPrefix(shape.Ref, "#/components/schemas/")]
		if !ok {
			return appendAgreementViolation(violations, target, "reference", "missing Browser record schema")
		}
		raw, err := json.Marshal(component)
		if err != nil {
			panic(err)
		}
		if err := json.Unmarshal(raw, &shape); err != nil {
			panic(err)
		}
	}
	variants := []reflect.Type{reflect.TypeOf(browserLifecycleAgreement{}), reflect.TypeOf(browserLifecycleAgreement{}), reflect.TypeOf(browserNavigationAgreement{}), reflect.TypeOf(browserRedirectAgreement{}), reflect.TypeOf(browserBlockedAgreement{})}
	if len(shape.OneOf) != len(variants) {
		return appendAgreementViolation(violations, target, "oneOf", "must describe five closed Browser record variants")
	}
	for i, variant := range variants {
		violations = evaluateOpenAPIShapeAgreement(violations, document, fmt.Sprintf("%s.variant%d", target, i), shape.OneOf[i], variant, request, false, false, false, nil, nil)
	}
	return violations
}
