// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The route-path vocabulary is deliberately private. Resource modules
// describe canonical path shapes with these values; gorilla/mux syntax remains
// an implementation detail of the catalog compiler.

type parameterKind uint8

const (
	parameterCanonicalID parameterKind = iota + 1
	parameterProviderID
)

type pathPart interface {
	compile() (template string, normalized string, err error)
}

type pathLiteral string

func literal(value string) pathPart { return pathLiteral(value) }

func (part pathLiteral) compile() (string, string, error) {
	value := string(part)
	if !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(value) {
		return "", "", fmt.Errorf("path literal %q is not canonical", value)
	}
	return value, value, nil
}

type pathParameter struct {
	name string
	kind parameterKind
}

func canonicalID(name string) pathPart {
	return pathParameter{name: name, kind: parameterCanonicalID}
}

func providerID(name string) pathPart {
	return pathParameter{name: name, kind: parameterProviderID}
}

func (part pathParameter) compile() (string, string, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(part.name) {
		return "", "", fmt.Errorf("path parameter name %q is not canonical", part.name)
	}
	var pattern, normalized string
	switch part.kind {
	case parameterCanonicalID:
		pattern = canonicalIDRoutePattern()
		normalized = "canonical_id"
	case parameterProviderID:
		pattern = providerIDRoutePattern()
		normalized = "provider_id"
	default:
		return "", "", fmt.Errorf("path parameter %q has unsupported parameter kind %d", part.name, part.kind)
	}
	return "{" + part.name + ":" + pattern + "}", "{" + normalized + "}", nil
}

type routePath struct {
	parts       []pathPart
	rootMounted bool
}

func apiPath(parts ...pathPart) routePath {
	return routePath{parts: append([]pathPart(nil), parts...)}
}

func rootPath(parts ...pathPart) routePath {
	return routePath{parts: append([]pathPart(nil), parts...), rootMounted: true}
}

func (path routePath) compile(apiPrefix string) (string, string, error) {
	if apiPrefix == "" || !strings.HasPrefix(apiPrefix, "/") || strings.HasSuffix(apiPrefix, "/") {
		return "", "", fmt.Errorf("API prefix %q is not canonical", apiPrefix)
	}
	if len(path.parts) == 0 {
		return "", "", errors.New("route path is empty")
	}
	template := make([]string, 0, len(path.parts))
	normalized := make([]string, 0, len(path.parts))
	for _, part := range path.parts {
		if part == nil {
			return "", "", errors.New("route path contains a nil part")
		}
		compiled, shape, err := part.compile()
		if err != nil {
			return "", "", err
		}
		template = append(template, compiled)
		normalized = append(normalized, shape)
	}
	prefix := apiPrefix
	if path.rootMounted {
		prefix = ""
	}
	return prefix + "/" + strings.Join(template, "/"),
		prefix + "/" + strings.Join(normalized, "/"), nil
}
