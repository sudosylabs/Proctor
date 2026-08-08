// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type interfaceSpec struct {
	name    string
	methods []methodSpec
}

type methodSpec struct {
	name    string
	params  []valueSpec
	results []valueSpec
}

type valueSpec struct {
	name     string
	typeName string
	variadic bool
}

func main() {
	layer := flag.String("layer", "timer", "store layer to generate: timer, retry, or localcache")
	source := flag.String("source", "..", "store contract source file or directory")
	output := flag.String("output", "forwarding_gen.go", "generated output")
	flag.Parse()

	interfaces, storeTypes, err := parseInterfaces(*source)
	if err != nil {
		fatal(err)
	}
	var generated []byte
	switch *layer {
	case "timer":
		generated, err = renderTimer(interfaces, storeTypes)
	case "retry":
		generated, err = renderEmbedded(interfaces, "retrylayer", "retry")
	case "localcache":
		generated, err = renderEmbedded(interfaces, "localcachelayer", "localCache")
	default:
		err = fmt.Errorf("unsupported store layer %q", *layer)
	}
	if err != nil {
		fatal(err)
	}
	formatted, err := format.Source(generated)
	if err != nil {
		fatal(fmt.Errorf("format generated forwarding: %w\n%s", err, generated))
	}
	if err := os.WriteFile(*output, formatted, 0o644); err != nil {
		fatal(fmt.Errorf("write %s: %w", *output, err))
	}
}

func parseInterfaces(path string) ([]interfaceSpec, map[string]struct{}, error) {
	paths, err := sourceFiles(path)
	if err != nil {
		return nil, nil, err
	}
	parsedFiles := make([]*ast.File, 0, len(paths))
	for _, sourcePath := range paths {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), sourcePath, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", sourcePath, parseErr)
		}
		parsedFiles = append(parsedFiles, parsed)
	}
	storeTypes := make(map[string]struct{})
	for _, parsed := range parsedFiles {
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, raw := range general.Specs {
				spec := raw.(*ast.TypeSpec)
				storeTypes[spec.Name.Name] = struct{}{}
			}
		}
	}

	var interfaces []interfaceSpec
	for _, parsed := range parsedFiles {
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, raw := range general.Specs {
				spec := raw.(*ast.TypeSpec)
				contract, ok := spec.Type.(*ast.InterfaceType)
				if !ok || (spec.Name.Name != "Store" && !strings.HasSuffix(spec.Name.Name, "Store")) {
					continue
				}
				parsedInterface := interfaceSpec{name: spec.Name.Name}
				for _, field := range contract.Methods.List {
					if len(field.Names) != 1 {
						return nil, nil, fmt.Errorf("%s contains an embedded or grouped method", spec.Name.Name)
					}
					function, ok := field.Type.(*ast.FuncType)
					if !ok {
						return nil, nil, fmt.Errorf("%s.%s is not a method", spec.Name.Name, field.Names[0].Name)
					}
					method := methodSpec{name: field.Names[0].Name}
					method.params, err = values(function.Params, "arg", storeTypes)
					if err != nil {
						return nil, nil, fmt.Errorf("parse %s.%s parameters: %w", spec.Name.Name, method.name, err)
					}
					method.results, err = values(function.Results, "result", storeTypes)
					if err != nil {
						return nil, nil, fmt.Errorf("parse %s.%s results: %w", spec.Name.Name, method.name, err)
					}
					parsedInterface.methods = append(parsedInterface.methods, method)
				}
				interfaces = append(interfaces, parsedInterface)
			}
		}
	}
	return interfaces, storeTypes, nil
}

func sourceFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(path, entry.Name()))
	}
	return paths, nil
}

func values(fields *ast.FieldList, prefix string, storeTypes map[string]struct{}) ([]valueSpec, error) {
	if fields == nil {
		return nil, nil
	}
	var result []valueSpec
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			name := fmt.Sprintf("%s%d", prefix, len(result))
			if len(field.Names) > 0 && field.Names[index].Name != "_" {
				name = field.Names[index].Name
			}
			_, variadic := field.Type.(*ast.Ellipsis)
			typeName, err := renderType(field.Type, storeTypes)
			if err != nil {
				return nil, err
			}
			result = append(result, valueSpec{
				name: name, typeName: typeName, variadic: variadic,
			})
		}
	}
	return result, nil
}

func renderType(expression ast.Expr, storeTypes map[string]struct{}) (string, error) {
	switch value := expression.(type) {
	case *ast.Ident:
		if _, ok := storeTypes[value.Name]; ok {
			return "store." + value.Name, nil
		}
		return value.Name, nil
	case *ast.SelectorExpr:
		prefix, err := renderType(value.X, storeTypes)
		return prefix + "." + value.Sel.Name, err
	case *ast.StarExpr:
		element, err := renderType(value.X, storeTypes)
		return "*" + element, err
	case *ast.ArrayType:
		length := ""
		if value.Len != nil {
			var err error
			length, err = renderType(value.Len, storeTypes)
			if err != nil {
				return "", err
			}
		}
		element, err := renderType(value.Elt, storeTypes)
		return "[" + length + "]" + element, err
	case *ast.MapType:
		key, err := renderType(value.Key, storeTypes)
		if err != nil {
			return "", err
		}
		element, err := renderType(value.Value, storeTypes)
		return "map[" + key + "]" + element, err
	case *ast.Ellipsis:
		element, err := renderType(value.Elt, storeTypes)
		return "..." + element, err
	case *ast.BasicLit:
		return value.Value, nil
	default:
		return "", fmt.Errorf("unsupported store contract type %T", expression)
	}
}

func renderTimer(interfaces []interfaceSpec, _ map[string]struct{}) ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("// Code generated by go generate; DO NOT EDIT.\n\n")
	output.WriteString("// Copyright 2026 SudoSylabs\n// SPDX-License-Identifier: AGPL-3.0-only\n\n")
	output.WriteString("package timerlayer\n\n")
	output.WriteString("import (\n\t\"context\"\n\t\"sync\"\n\n")
	output.WriteString("\t\"github.com/sudosylabs/proctor/server/model\"\n")
	output.WriteString("\t\"github.com/sudosylabs/proctor/server/store\"\n)\n\n")

	var root interfaceSpec
	var modelStores []interfaceSpec
	for _, spec := range interfaces {
		if spec.name == "Store" {
			root = spec
		} else {
			modelStores = append(modelStores, spec)
		}
	}
	if root.name == "" {
		return nil, fmt.Errorf("root Store interface not found")
	}

	output.WriteString("type timedStores struct {\n")
	for _, spec := range modelStores {
		field := lowerFirst(strings.TrimSuffix(spec.name, "Store"))
		fmt.Fprintf(&output, "\t%s store.%s\n\t%sOnce sync.Once\n", field, spec.name, field)
	}
	output.WriteString("}\n\n")

	for _, spec := range modelStores {
		fmt.Fprintf(&output, "type timed%s struct {\n\tlayer *Layer\n\tnext store.%s\n}\n\n", spec.name, spec.name)
	}

	for _, method := range root.methods {
		if accessorStore, ok := accessorResult(method); ok {
			field := lowerFirst(strings.TrimSuffix(accessorStore, "Store"))
			fmt.Fprintf(&output, "func (l *Layer) %s() store.%s {\n", method.name, accessorStore)
			fmt.Fprintf(&output, "\tl.stores.%sOnce.Do(func() {\n", field)
			fmt.Fprintf(&output, "\t\tnext := l.next.%s()\n", method.name)
			fmt.Fprintf(&output, "\t\tif next != nil {\n")
			fmt.Fprintf(&output, "\t\t\tl.stores.%s = &timed%s{layer: l, next: next}\n", field, accessorStore)
			fmt.Fprintf(&output, "\t\t}\n")
			fmt.Fprintf(&output, "\t})\n\treturn l.stores.%s\n}\n\n", field)
			continue
		}
		if err := renderMethod(&output, "Layer", "l", "l", "l.next", "store", method); err != nil {
			return nil, err
		}
	}
	for _, spec := range modelStores {
		aggregate := camelToSnake(strings.TrimSuffix(spec.name, "Store"))
		for _, method := range spec.methods {
			if err := renderMethod(&output, "timed"+spec.name, "s", "s.layer", "s.next", aggregate, method); err != nil {
				return nil, err
			}
		}
	}

	output.WriteString("var (\n\t_ store.Store = (*Layer)(nil)\n")
	for _, spec := range modelStores {
		fmt.Fprintf(&output, "\t_ store.%s = (*timed%s)(nil)\n", spec.name, spec.name)
	}
	output.WriteString(")\n")
	return output.Bytes(), nil
}

func renderEmbedded(interfaces []interfaceSpec, packageName, storesPrefix string) ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("// Code generated by go generate; DO NOT EDIT.\n\n")
	output.WriteString("// Copyright 2026 SudoSylabs\n// SPDX-License-Identifier: AGPL-3.0-only\n\n")
	fmt.Fprintf(&output, "package %s\n\n", packageName)
	output.WriteString("import (\n\t\"sync\"\n\n")
	output.WriteString("\t\"github.com/sudosylabs/proctor/server/store\"\n)\n\n")

	var root interfaceSpec
	var modelStores []interfaceSpec
	for _, spec := range interfaces {
		if spec.name == "Store" {
			root = spec
		} else {
			modelStores = append(modelStores, spec)
		}
	}
	if root.name == "" {
		return nil, fmt.Errorf("root Store interface not found")
	}

	fmt.Fprintf(&output, "type %sStores struct {\n", storesPrefix)
	for _, spec := range modelStores {
		field := lowerFirst(strings.TrimSuffix(spec.name, "Store"))
		fmt.Fprintf(&output, "\t%s store.%s\n\t%sOnce sync.Once\n", field, spec.name, field)
	}
	output.WriteString("}\n\n")

	for _, spec := range modelStores {
		fmt.Fprintf(
			&output,
			"type %sStore struct {\n\tstore.%s\n\tlayer *Layer\n}\n\n",
			lowerFirst(strings.TrimSuffix(spec.name, "Store")),
			spec.name,
		)
	}

	for _, method := range root.methods {
		accessorStore, ok := accessorResult(method)
		if !ok {
			continue
		}
		field := lowerFirst(strings.TrimSuffix(accessorStore, "Store"))
		fmt.Fprintf(&output, "func (l *Layer) %s() store.%s {\n", method.name, accessorStore)
		fmt.Fprintf(&output, "\tl.stores.%sOnce.Do(func() {\n", field)
		fmt.Fprintf(&output, "\t\tnext := l.Store.%s()\n", method.name)
		fmt.Fprintf(&output, "\t\tif next != nil {\n")
		fmt.Fprintf(&output, "\t\t\tl.stores.%s = &%sStore{%s: next, layer: l}\n", field, field, accessorStore)
		fmt.Fprintf(&output, "\t\t}\n")
		fmt.Fprintf(&output, "\t})\n\treturn l.stores.%s\n}\n\n", field)
	}

	output.WriteString("var (\n\t_ store.Store = (*Layer)(nil)\n")
	for _, spec := range modelStores {
		fmt.Fprintf(
			&output,
			"\t_ store.%s = (*%sStore)(nil)\n",
			spec.name,
			lowerFirst(strings.TrimSuffix(spec.name, "Store")),
		)
	}
	output.WriteString(")\n")
	return output.Bytes(), nil
}

func renderMethod(output *bytes.Buffer, receiverType, receiver, layer, next, aggregate string, method methodSpec) error {
	if len(method.results) == 0 || method.results[len(method.results)-1].typeName != "error" {
		return fmt.Errorf("%s.%s does not return error last", receiverType, method.name)
	}
	fmt.Fprintf(output, "func (%s *%s) %s(%s)%s {\n", receiver, receiverType, method.name, parameters(method.params), results(method.results))
	valueCount := len(method.results) - 1
	if valueCount > 2 {
		return fmt.Errorf("%s.%s returns %d values before error; add a handwritten timing helper", receiverType, method.name, valueCount)
	}
	fmt.Fprintf(
		output,
		"\treturn timeStoreCall%d(%s, storeOperation(aggregate%s, method%s), func()%s {\n",
		valueCount,
		layer,
		upperCamel(aggregate),
		methodIdentifier(method.name),
		results(method.results),
	)
	fmt.Fprintf(output, "\t\treturn %s.%s(%s)\n", next, method.name, arguments(method.params))
	output.WriteString("\t})\n}\n\n")
	return nil
}

func upperCamel(value string) string {
	if value == "mfa" {
		return "MFA"
	}
	parts := strings.Split(value, "_")
	for index, part := range parts {
		runes := []rune(part)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		parts[index] = string(runes)
	}
	return strings.Join(parts, "")
}

func methodIdentifier(value string) string {
	if strings.HasSuffix(value, "Ids") {
		return strings.TrimSuffix(value, "Ids") + "IDs"
	}
	if strings.HasSuffix(value, "Id") {
		return strings.TrimSuffix(value, "Id") + "ID"
	}
	return value
}

func accessorResult(method methodSpec) (string, bool) {
	if len(method.params) != 0 || len(method.results) != 1 {
		return "", false
	}
	name := strings.TrimPrefix(method.results[0].typeName, "store.")
	return name, strings.HasSuffix(name, "Store")
}

func parameters(values []valueSpec) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = value.name + " " + value.typeName
	}
	return strings.Join(parts, ", ")
}

func arguments(values []valueSpec) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = value.name
		if value.variadic {
			parts[index] += "..."
		}
	}
	return strings.Join(parts, ", ")
}

func results(values []valueSpec) string {
	if len(values) == 1 {
		return " " + values[0].typeName
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = value.typeName
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	limit := 1
	for limit < len(runes) && unicode.IsUpper(runes[limit]) {
		if limit+1 < len(runes) && unicode.IsLower(runes[limit+1]) {
			break
		}
		limit++
	}
	for index := 0; index < limit; index++ {
		runes[index] = unicode.ToLower(runes[index])
	}
	return string(runes)
}

func camelToSnake(value string) string {
	runes := []rune(value)
	var output []rune
	for index, current := range runes {
		if unicode.IsUpper(current) && index > 0 {
			previousLower := unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1])
			nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if previousLower || nextLower {
				output = append(output, '_')
			}
		}
		output = append(output, unicode.ToLower(current))
	}
	return string(output)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
