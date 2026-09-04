// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"fmt"
	"strings"
	"testing"
)

type pathMatch uint8

const (
	matchExact pathMatch = iota
	matchSubtree
	matchDescendants
)

type pathPattern struct {
	path  string
	match pathMatch
}

func exact(path string) pathPattern {
	return pathPattern{path: path, match: matchExact}
}

func subtree(path string) pathPattern {
	return pathPattern{path: path, match: matchSubtree}
}

func descendants(path string) pathPattern {
	return pathPattern{path: path, match: matchDescendants}
}

func (pattern pathPattern) matches(path string) bool {
	switch pattern.match {
	case matchExact:
		return path == pattern.path
	case matchSubtree:
		return path == pattern.path || strings.HasPrefix(path, pattern.path+"/")
	case matchDescendants:
		return strings.HasPrefix(path, pattern.path+"/")
	default:
		return false
	}
}

type importAccess struct {
	allowUnlisted bool
	allowed       []pathPattern
	denied        []pathPattern
}

func only(patterns ...pathPattern) importAccess {
	return importAccess{allowed: patterns}
}

func allImports() importAccess {
	return importAccess{allowUnlisted: true}
}

func allExcept(patterns ...pathPattern) importAccess {
	return importAccess{allowUnlisted: true, denied: patterns}
}

func allowWithin(denied []pathPattern, allowed ...pathPattern) importAccess {
	return importAccess{allowUnlisted: true, allowed: allowed, denied: denied}
}

func (access importAccess) forbids(path string) bool {
	if matchesAny(access.allowed, path) {
		return false
	}
	if matchesAny(access.denied, path) {
		return true
	}
	return !access.allowUnlisted
}

type dependencyRule struct {
	name            string
	sources         []pathPattern
	deniedStandard  []pathPattern
	project         importAccess
	thirdParty      importAccess
	denyEveryImport bool
}

func (rule dependencyRule) forbids(importPath string) bool {
	if rule.denyEveryImport {
		return true
	}
	if projectImport(importPath) {
		return rule.project.forbids(importPath)
	}
	if thirdPartyImport(importPath) {
		return rule.thirdParty.forbids(importPath)
	}
	return matchesAny(rule.deniedStandard, importPath)
}

var (
	standardInfrastructure = []pathPattern{
		exact("net/http"),
		subtree("database/sql"),
		exact("net/smtp"),
		exact("os"),
		exact("io/fs"),
		exact("path/filepath"),
	}
	standardInfrastructureExceptHTTP = []pathPattern{
		subtree("database/sql"),
		exact("net/smtp"),
		exact("os"),
		exact("io/fs"),
		exact("path/filepath"),
	}
	standardInfrastructureExceptFS = []pathPattern{
		exact("net/http"),
		subtree("database/sql"),
		exact("net/smtp"),
		exact("os"),
		exact("path/filepath"),
	}
	standardInfrastructureExceptOS = []pathPattern{
		exact("net/http"),
		subtree("database/sql"),
		exact("net/smtp"),
		exact("io/fs"),
		exact("path/filepath"),
	}
	commandInfrastructure = []pathPattern{
		subtree("database/sql"),
		exact("net/smtp"),
	}
)

// dependencyRules is ordered from the most specific package boundary to the
// broadest. The first matching rule owns a production package.
var dependencyRules = []dependencyRule{
	{
		name: "reusable modules",
		sources: []pathPattern{
			subtree(repositoryModule + "/packages/cache"),
			subtree(repositoryModule + "/packages/mail"),
			subtree(repositoryModule + "/packages/vfs"),
		},
		project:    allExcept(subtree(serverModule)),
		thirdParty: allImports(),
	},
	{
		name:       "composition root",
		sources:    []pathPattern{exact(serverModule)},
		project:    allImports(),
		thirdParty: allImports(),
	},
	{
		name:    "identity-provider contracts",
		sources: []pathPattern{exact(serverModule + "/identityprovider")},
	},
	{
		name:    "model build tools",
		sources: []pathPattern{subtree(serverModule + "/model/internal/idgen")},
	},
	{
		name:    "ACME adapter",
		sources: []pathPattern{exact(serverModule + "/internal/autocert")},
		thirdParty: only(
			exact("golang.org/x/crypto/acme"),
			exact("golang.org/x/net/idna"),
		),
	},
	{
		name:           "OpenAPI compiler",
		sources:        []pathPattern{exact(serverModule + "/internal/openapidoc")},
		deniedStandard: standardInfrastructureExceptFS,
		thirdParty: only(
			exact("github.com/getkin/kin-openapi/openapi3"),
			exact("gopkg.in/yaml.v3"),
		),
	},
	{
		name:           "domain model",
		sources:        []pathPattern{exact(serverModule + "/model")},
		deniedStandard: standardInfrastructure,
		project:        only(exact(serverModule + "/identityprovider")),
		thirdParty:     only(exact("golang.org/x/net/idna")),
	},
	{
		name:           "store contracts",
		sources:        []pathPattern{exact(serverModule + "/store")},
		deniedStandard: standardInfrastructure,
		project: allowWithin(
			[]pathPattern{descendants(serverModule)},
			exact(serverModule+"/model"),
		),
	},
	{
		name:    "SQL store adapter",
		sources: []pathPattern{subtree(serverModule + "/store/sqlstore")},
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
			exact(serverModule+"/config"),
			exact(serverModule+"/migrations"),
		),
		thirdParty: allImports(),
	},
	{
		name:           "local cache store layer",
		sources:        []pathPattern{subtree(serverModule + "/store/localcachelayer")},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
			exact(repositoryModule+"/packages/cache"),
		),
	},
	{
		name: "generic store layers",
		sources: []pathPattern{
			subtree(serverModule + "/store/timerlayer"),
			subtree(serverModule + "/store/retrylayer"),
		},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
		),
	},
	{
		name:           "concrete jobs",
		sources:        []pathPattern{subtree(serverModule + "/app/jobs")},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
			exact(serverModule+"/secretseal"),
			exact(serverModule+"/app/job"),
			exact(serverModule+"/app/mail"),
			exact(serverModule+"/app/exam/attempt"),
		),
	},
	{
		name:           "job engine",
		sources:        []pathPattern{subtree(serverModule + "/app/job")},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
		),
	},
	{
		name:           "realtime application",
		sources:        []pathPattern{subtree(serverModule + "/app/realtime")},
		deniedStandard: standardInfrastructure,
		project:        only(exact(serverModule + "/model")),
	},
	{
		name:           "execution application",
		sources:        []pathPattern{subtree(serverModule + "/app/execution")},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
		),
	},
	{
		name:           "mail application",
		sources:        []pathPattern{subtree(serverModule + "/app/mail")},
		deniedStandard: standardInfrastructureExceptFS,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
			exact(serverModule+"/secretseal"),
			exact(serverModule+"/app/exam"),
			exact(serverModule+"/localization"),
		),
	},
	{
		name:           "idempotency application",
		sources:        []pathPattern{exact(serverModule + "/app/idempotency")},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
		),
	},
	{
		name: "exam idempotency consumers",
		sources: []pathPattern{
			exact(serverModule + "/app/exam"),
			exact(serverModule + "/app/exam/attempt"),
			exact(serverModule + "/app/exam/correction"),
			exact(serverModule + "/app/exam/resource"),
			exact(serverModule + "/app/exam/review"),
			exact(serverModule + "/app/exam/sitting"),
			exact(serverModule + "/app/exam/workspace"),
		},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/app/idempotency"),
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
			exact(serverModule+"/app/exam/safemarkdown"),
		),
	},
	{
		name:           "exam application",
		sources:        []pathPattern{subtree(serverModule + "/app/exam")},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
			exact(serverModule+"/app/exam/safemarkdown"),
		),
	},
	{
		name:           "file-content adapter",
		sources:        []pathPattern{subtree(serverModule + "/filecontent")},
		deniedStandard: standardInfrastructureExceptOS,
		project: only(
			exact(serverModule+"/app"),
			exact(serverModule+"/model"),
			exact(repositoryModule+"/packages/vfs"),
		),
		thirdParty: only(
			exact("github.com/HugoSmits86/nativewebp"),
			exact("github.com/disintegration/imaging"),
			descendants("github.com/pdfcpu/pdfcpu"),
			exact("golang.org/x/image/webp"),
		),
	},
	{
		name:           "secret sealing",
		sources:        []pathPattern{exact(serverModule + "/secretseal")},
		deniedStandard: standardInfrastructure,
	},
	{
		name:           "localization",
		sources:        []pathPattern{exact(serverModule + "/localization")},
		deniedStandard: standardInfrastructureExceptFS,
		thirdParty: only(
			subtree("github.com/nicksnyder/go-i18n/v2/i18n"),
			exact("golang.org/x/text/language"),
		),
	},
	{
		name:           "application",
		sources:        []pathPattern{subtree(serverModule + "/app")},
		deniedStandard: standardInfrastructure,
		project: only(
			exact(serverModule+"/app/idempotency"),
			exact(serverModule+"/model"),
			exact(serverModule+"/store"),
			exact(serverModule+"/app/job"),
			exact(serverModule+"/app/jobs"),
			exact(serverModule+"/app/realtime"),
			exact(serverModule+"/app/mail"),
			exact(serverModule+"/app/execution"),
			exact(serverModule+"/secretseal"),
			subtree(serverModule+"/app/exam"),
		),
		thirdParty: only(exact("golang.org/x/crypto/argon2")),
	},
	{
		name:    "hosted webapp transport",
		sources: []pathPattern{exact(serverModule + "/webui")},
	},
	{
		name: "HTTP and WebSocket transports",
		sources: []pathPattern{
			subtree(serverModule + "/httpapi"),
			subtree(serverModule + "/websocket"),
		},
		deniedStandard: standardInfrastructureExceptHTTP,
		project: only(
			exact(serverModule+"/app"),
			exact(serverModule+"/app/realtime"),
			exact(serverModule+"/model"),
			exact(serverModule+"/localization"),
		),
		thirdParty: only(descendants("github.com/gorilla")),
	},
	{
		name:    "cluster contracts",
		sources: []pathPattern{exact(serverModule + "/cluster")},
	},
	{
		name: "cluster adapters",
		sources: []pathPattern{
			subtree(serverModule + "/cluster/local"),
			subtree(serverModule + "/cluster/memberlist"),
		},
		project:    only(exact(serverModule + "/cluster")),
		thirdParty: allImports(),
	},
	{
		name:    "metrics",
		sources: []pathPattern{exact(serverModule + "/metrics")},
		project: only(
			exact(serverModule+"/config"),
			exact(serverModule+"/store/timerlayer"),
			exact(serverModule+"/store/localcachelayer"),
		),
		thirdParty: only(subtree("github.com/prometheus/client_golang")),
	},
	{
		name:    "external-auth adapters",
		sources: []pathPattern{subtree(serverModule + "/platform/externalauth")},
		project: only(
			exact(serverModule+"/app"),
			exact(serverModule+"/config"),
			exact(serverModule+"/model"),
			exact(serverModule+"/platform/externalauth"),
		),
		thirdParty: allImports(),
	},
	{
		name:    "platform lifecycle",
		sources: []pathPattern{exact(serverModule + "/platform")},
		project: allExcept(
			descendants(repositoryModule+"/packages/cache"),
			descendants(repositoryModule+"/packages/mail"),
			descendants(repositoryModule+"/packages/vfs"),
			descendants(serverModule+"/cluster"),
			descendants(serverModule+"/platform/externalauth"),
			exact(serverModule+"/store/sqlstore"),
			subtree(serverModule+"/app"),
			subtree(serverModule+"/httpapi"),
			subtree(serverModule+"/websocket"),
		),
	},
	{
		name:    "execution-host adapter",
		sources: []pathPattern{exact(serverModule + "/executionhost")},
		project: only(exact(serverModule + "/app/execution")),
		thirdParty: only(
			subtree("github.com/sudosylabs/execenv"),
		),
	},
	{
		name:    "healthcheck executable",
		sources: []pathPattern{exact(serverModule + "/cmd/proctor-healthcheck")},
	},
	{
		name:    "operator executable",
		sources: []pathPattern{exact(serverModule + "/cmd/proctor")},
		project: only(exact(serverModule + "/cmd/proctor/commands")),
	},
	{
		name:           "operator commands",
		sources:        []pathPattern{subtree(serverModule + "/cmd/proctor/commands")},
		deniedStandard: commandInfrastructure,
		project: only(
			exact(serverModule),
			exact(serverModule+"/localization"),
		),
		thirdParty: only(exact("github.com/spf13/cobra")),
	},
	{
		name:    "ptool executable",
		sources: []pathPattern{exact(serverModule + "/cmd/ptool")},
		project: only(exact(serverModule + "/cmd/ptool/commands")),
	},
	{
		name:    "ptool commands",
		sources: []pathPattern{subtree(serverModule + "/cmd/ptool/commands")},
		project: only(
			exact(serverModule+"/app/mail"),
			exact(serverModule+"/cmd/proctor/commands"),
			exact(serverModule+"/httpapi"),
			exact(serverModule+"/internal/openapidoc"),
			exact(serverModule+"/localization"),
			exact(serverModule+"/websocket"),
		),
		thirdParty: only(exact("github.com/spf13/cobra")),
	},
	{
		name:            "unknown ptool children",
		sources:         []pathPattern{descendants(serverModule + "/cmd/ptool")},
		denyEveryImport: true,
	},
	{
		name:    "mail preview executable",
		sources: []pathPattern{exact(serverModule + "/cmd/mailpreview")},
		project: only(
			exact(serverModule+"/app/mail"),
			exact(serverModule+"/localization"),
			exact(serverModule+"/model"),
		),
	},
	{
		name:       "configuration",
		sources:    []pathPattern{exact(serverModule + "/config")},
		project:    only(exact(serverModule + "/identityprovider")),
		thirdParty: allImports(),
	},
	{
		name: "leaf infrastructure",
		sources: []pathPattern{
			exact(serverModule + "/logging"),
			exact(serverModule + "/migrations"),
		},
		thirdParty: allImports(),
	},
}

func TestDependencyRulesAreWellFormed(t *testing.T) {
	t.Parallel()

	names := make(map[string]struct{}, len(dependencyRules))
	sources := make(map[string]string)
	for _, rule := range dependencyRules {
		if rule.name == "" {
			t.Error("dependency rule has an empty name")
		}
		if _, exists := names[rule.name]; exists {
			t.Errorf("duplicate dependency rule name %q", rule.name)
		}
		names[rule.name] = struct{}{}
		if len(rule.sources) == 0 {
			t.Errorf("dependency rule %q has no source packages", rule.name)
		}
		for _, source := range rule.sources {
			if source.path == "" {
				t.Errorf("dependency rule %q has an empty source pattern", rule.name)
				continue
			}
			if source.match > matchDescendants {
				t.Errorf("dependency rule %q has an invalid source match for %q", rule.name, source.path)
			}
			key := fmt.Sprintf("%d:%s", source.match, source.path)
			if owner, exists := sources[key]; exists {
				t.Errorf("dependency rules %q and %q declare the same source pattern %q", owner, rule.name, source.path)
			}
			sources[key] = rule.name
		}
	}
}

func TestDependencyRulePrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		packagePath string
		wantRule    string
	}{
		{packagePath: serverModule + "/app/jobs", wantRule: "concrete jobs"},
		{packagePath: serverModule + "/app/idempotency", wantRule: "idempotency application"},
		{packagePath: serverModule + "/app/exam/review", wantRule: "exam idempotency consumers"},
		{packagePath: serverModule + "/app/exam/safemarkdown", wantRule: "exam application"},
		{packagePath: serverModule + "/store/sqlstore", wantRule: "SQL store adapter"},
		{packagePath: serverModule + "/platform/externalauth/oidc", wantRule: "external-auth adapters"},
		{packagePath: serverModule + "/internal/openapidoc", wantRule: "OpenAPI compiler"},
		{packagePath: serverModule + "/cmd/proctor-healthcheck", wantRule: "healthcheck executable"},
		{packagePath: serverModule + "/cmd/ptool/commands", wantRule: "ptool commands"},
	}

	for _, tt := range tests {
		t.Run(tt.packagePath, func(t *testing.T) {
			t.Parallel()
			rule, ok := dependencyRuleFor(tt.packagePath)
			if !ok {
				t.Fatalf("dependencyRuleFor(%q) found no rule", tt.packagePath)
			}
			if rule.name != tt.wantRule {
				t.Fatalf("dependencyRuleFor(%q) = %q, want %q", tt.packagePath, rule.name, tt.wantRule)
			}
		})
	}
}

func TestExamIdempotencyConsumerAllowlist(t *testing.T) {
	t.Parallel()

	for _, packagePath := range []string{
		serverModule + "/app/exam",
		serverModule + "/app/exam/attempt",
		serverModule + "/app/exam/correction",
		serverModule + "/app/exam/resource",
		serverModule + "/app/exam/review",
		serverModule + "/app/exam/sitting",
		serverModule + "/app/exam/workspace",
	} {
		if forbiddenImport(packagePath, serverModule+"/app/idempotency") {
			t.Errorf("%s cannot import the shared idempotency leaf", packagePath)
		}
	}
	if !forbiddenImport(serverModule+"/app/exam/safemarkdown", serverModule+"/app/idempotency") {
		t.Error("unlisted Exam child can import the shared idempotency leaf")
	}
	if !forbiddenImport(serverModule+"/app/exam/attempt/internal", serverModule+"/app/idempotency") {
		t.Error("descendant of a listed Exam command owner can import the shared idempotency leaf")
	}
}

func TestUnclassifiedProductionPackageFailsClosed(t *testing.T) {
	t.Parallel()

	packagePath := serverModule + "/services"
	if knownProductionPackage(packagePath) {
		t.Fatalf("knownProductionPackage(%q) = true, want false", packagePath)
	}
	if !forbiddenImport(packagePath, "context") {
		t.Fatalf("forbiddenImport(%q, %q) = false, want true", packagePath, "context")
	}
}

func forbiddenImport(from, imported string) bool {
	if exemptProductionHelper(from) {
		return false
	}
	rule, ok := dependencyRuleFor(from)
	if !ok {
		return true
	}
	return rule.forbids(imported)
}

func dependencyRuleFor(packagePath string) (dependencyRule, bool) {
	for _, rule := range dependencyRules {
		if matchesAny(rule.sources, packagePath) {
			return rule, true
		}
	}
	return dependencyRule{}, false
}

func matchesAny(patterns []pathPattern, path string) bool {
	for _, pattern := range patterns {
		if pattern.matches(path) {
			return true
		}
	}
	return false
}

func projectImport(importPath string) bool {
	return strings.HasPrefix(importPath, repositoryModule+"/")
}

func thirdPartyImport(importPath string) bool {
	if projectImport(importPath) {
		return false
	}
	first, _, _ := strings.Cut(importPath, "/")
	return strings.Contains(first, ".")
}

func packageOrBelow(packagePath, root string) bool {
	return packagePath == root || strings.HasPrefix(packagePath, root+"/")
}

func exemptProductionHelper(packagePath string) bool {
	return packageOrBelow(packagePath, serverModule+"/testlib") ||
		packageOrBelow(packagePath, serverModule+"/store/storetest") ||
		packageOrBelow(packagePath, serverModule+"/config/configtest")
}

func knownProductionPackage(packagePath string) bool {
	if exemptProductionHelper(packagePath) {
		return true
	}
	_, ok := dependencyRuleFor(packagePath)
	return ok
}
