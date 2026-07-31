// Copyright (c) 2026 Sudo Systems Labs Ltd
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package architecture_test

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	repositoryModule = "github.com/sudosylabs/proctor"
	serverModule     = repositoryModule + "/server"
)

// initialDependencyDebt is the immutable ceiling established by migration
// ticket 03. The active debt file may remove these entries but may not add or
// replace them.
const initialDependencyDebt = `app/academic_administration.go	net/http
app/account_recovery.go	github.com/sudosylabs/proctor/packages/mail
app/account_recovery.go	github.com/sudosylabs/proctor/server/mlog
app/account_recovery.go	github.com/sudosylabs/proctor/server/platform
app/account_recovery.go	net/http
app/api/api.go	github.com/sudosylabs/proctor/server/mlog
app/api/api.go	github.com/sudosylabs/proctor/server/store
app/api/authentication.go	github.com/sudosylabs/proctor/server/mlog
app/api/handler.go	github.com/sudosylabs/proctor/server/mlog
app/api/middleware.go	github.com/sudosylabs/proctor/server/mlog
app/api/user_administration.go	github.com/sudosylabs/proctor/server/store
app/api/websocket.go	github.com/sudosylabs/proctor/server/mlog
app/app.go	github.com/sudosylabs/proctor/packages/vfs
app/app.go	github.com/sudosylabs/proctor/server/config
app/app.go	github.com/sudosylabs/proctor/server/mlog
app/app.go	github.com/sudosylabs/proctor/server/platform
app/audit.go	net/http
app/authentication.go	github.com/sudosylabs/proctor/server/config
app/authentication.go	github.com/sudosylabs/proctor/server/mlog
app/authentication.go	github.com/sudosylabs/proctor/server/platform
app/authentication.go	net/http
app/authorization.go	net/http
app/bootstrap.go	net/http
app/external_authentication.go	github.com/sudosylabs/proctor/server/mlog
app/external_authentication.go	github.com/sudosylabs/proctor/server/platform
app/external_authentication.go	github.com/sudosylabs/proctor/server/platform/externalauth
app/external_authentication.go	net/http
app/membership_administration.go	net/http
app/mfa.go	github.com/sudosylabs/proctor/server/config
app/mfa.go	github.com/sudosylabs/proctor/server/platform
app/mfa.go	net/http
app/password.go	github.com/sudosylabs/proctor/server/config
app/personal_access_token.go	net/http
app/realtime.go	github.com/sudosylabs/proctor/server/mlog
app/realtime.go	github.com/sudosylabs/proctor/server/platform
app/realtime.go	net/http
app/role_administration.go	net/http
app/server.go	github.com/sudosylabs/proctor/packages/vfs
app/server.go	github.com/sudosylabs/proctor/server/app/api
app/server.go	github.com/sudosylabs/proctor/server/config
app/server.go	github.com/sudosylabs/proctor/server/mlog
app/server.go	github.com/sudosylabs/proctor/server/platform
app/server.go	net/http
app/session_management.go	net/http
app/version.go	github.com/sudosylabs/proctor/server/app/api
cmd/proctor/main.go	github.com/sudosylabs/proctor/server/app
cmd/proctor/main.go	github.com/sudosylabs/proctor/server/config
cmd/proctor/main.go	github.com/sudosylabs/proctor/server/store/sqlstore
model/app_error.go	net/http
platform/cluster_redis.go	github.com/redis/rueidis
platform/infrastructure.go	github.com/redis/rueidis
platform/infrastructure.go	github.com/sudosylabs/proctor/packages/cache/memory
platform/infrastructure.go	github.com/sudosylabs/proctor/packages/cache/redis
platform/infrastructure.go	github.com/sudosylabs/proctor/packages/mail/smtp
platform/infrastructure.go	github.com/sudosylabs/proctor/packages/vfs/local
platform/infrastructure.go	github.com/sudosylabs/proctor/packages/vfs/s3
platform/service.go	github.com/sudosylabs/proctor/server/platform/externalauth/cas
platform/service.go	github.com/sudosylabs/proctor/server/platform/externalauth/oidc
platform/service.go	github.com/sudosylabs/proctor/server/store/sqlstore`

type importViolation struct {
	file       string
	importPath string
}

func (v importViolation) key() string {
	return v.file + "\t" + v.importPath
}

func TestDependencyPolicyRejectsForbiddenImports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		from     string
		imported string
	}{
		{name: "domain cannot import HTTP", from: serverModule + "/model", imported: "net/http"},
		{name: "domain cannot import PostgreSQL client", from: serverModule + "/model", imported: "github.com/jackc/pgx/v5"},
		{name: "store contracts cannot import SQL adapters", from: serverModule + "/store", imported: serverModule + "/store/sqlstore"},
		{name: "application cannot import platform", from: serverModule + "/app", imported: serverModule + "/platform"},
		{name: "application cannot import Redis client", from: serverModule + "/app", imported: "github.com/redis/go-redis/v9"},
		{name: "HTTP cannot import persistence", from: serverModule + "/app/api", imported: serverModule + "/store"},
		{name: "HTTP cannot import SQL driver", from: serverModule + "/app/api", imported: "database/sql"},
		{name: "SQL adapter cannot import application policy", from: serverModule + "/store/sqlstore", imported: serverModule + "/app"},
		{name: "store layer cannot import application policy", from: serverModule + "/store/retrylayer", imported: serverModule + "/app"},
		{name: "cluster adapter cannot import platform", from: serverModule + "/cluster/memberlist", imported: serverModule + "/platform"},
		{name: "cluster adapter cannot import persistence", from: serverModule + "/cluster/memberlist", imported: serverModule + "/store"},
		{name: "external adapter cannot import HTTP", from: serverModule + "/platform/externalauth/oidc", imported: serverModule + "/app/api"},
		{name: "platform cannot select concrete adapters", from: serverModule + "/platform", imported: "github.com/sudosylabs/proctor/packages/cache/redis"},
		{name: "command cannot bypass root composition", from: serverModule + "/cmd/proctor", imported: serverModule + "/app"},
		{name: "command cannot open SQL directly", from: serverModule + "/cmd/proctor", imported: "database/sql"},
		{name: "reusable module cannot import server", from: repositoryModule + "/packages/cache", imported: serverModule + "/model"},
		{name: "unknown package is fail closed", from: serverModule + "/services", imported: "context"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !forbiddenImport(tt.from, tt.imported) {
				t.Fatalf("forbiddenImport(%q, %q) = false, want true", tt.from, tt.imported)
			}
		})
	}
}

func TestDependencyPolicyAllowsInwardImports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		from     string
		imported string
	}{
		{name: "store contracts may import domain", from: serverModule + "/store", imported: serverModule + "/model"},
		{name: "application may import store contracts", from: serverModule + "/app", imported: serverModule + "/store"},
		{name: "HTTP may import application", from: serverModule + "/app/api", imported: serverModule + "/app"},
		{name: "HTTP may import router library", from: serverModule + "/app/api", imported: "github.com/gorilla/mux"},
		{name: "SQL adapter may import store contracts", from: serverModule + "/store/sqlstore", imported: serverModule + "/store"},
		{name: "cache store layer may import cache contract", from: serverModule + "/store/localcachelayer", imported: repositoryModule + "/packages/cache"},
		{name: "cluster adapter may import cluster contracts", from: serverModule + "/cluster/memberlist", imported: serverModule + "/cluster"},
		{name: "application may import password hashing library", from: serverModule + "/app", imported: "golang.org/x/crypto/argon2"},
		{name: "root composition may import platform", from: serverModule, imported: serverModule + "/platform"},
		{name: "standard library remains available", from: serverModule + "/app", imported: "context"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if forbiddenImport(tt.from, tt.imported) {
				t.Fatalf("forbiddenImport(%q, %q) = true, want false", tt.from, tt.imported)
			}
		})
	}
}

func TestProductionImportsMatchDependencyDebt(t *testing.T) {
	actual := productionImportViolations(t)
	debt := readDependencyDebt(t)
	initial := parseDependencyDebt(t, "compiled initial dependency debt", strings.NewReader(initialDependencyDebt))

	var expandedDebt []string
	for key := range debt {
		if _, ok := initial[key]; !ok {
			expandedDebt = append(expandedDebt, key)
		}
	}
	sort.Strings(expandedDebt)
	for _, key := range expandedDebt {
		violation := debt[key]
		t.Errorf("dependency debt grew beyond its initial ceiling: remove %s importing %s", violation.file, violation.importPath)
	}

	var newDebt []string
	for key := range actual {
		if _, ok := debt[key]; !ok {
			newDebt = append(newDebt, key)
		}
	}
	sort.Strings(newDebt)
	for _, key := range newDebt {
		violation := actual[key]
		t.Errorf("new forbidden production import: %s imports %s", violation.file, violation.importPath)
	}

	var staleDebt []string
	for key := range debt {
		if _, ok := actual[key]; !ok {
			staleDebt = append(staleDebt, key)
		}
	}
	sort.Strings(staleDebt)
	for _, key := range staleDebt {
		violation := debt[key]
		t.Errorf("stale dependency debt: remove %s importing %s from dependency_debt.txt", violation.file, violation.importPath)
	}
}

func forbiddenImport(from, imported string) bool {
	if exemptProductionHelper(from) {
		return false
	}
	if packageOrBelow(from, repositoryModule+"/packages/cache") ||
		packageOrBelow(from, repositoryModule+"/packages/mail") ||
		packageOrBelow(from, repositoryModule+"/packages/vfs") {
		return packageOrBelow(imported, serverModule)
	}

	switch {
	case from == serverModule:
		return false
	case from == serverModule+"/model":
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			strings.HasPrefix(imported, repositoryModule+"/")
	case from == serverModule+"/store":
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			(strings.HasPrefix(imported, serverModule+"/") && imported != serverModule+"/model")
	case packageOrBelow(from, serverModule+"/store/sqlstore"):
		return forbiddenProjectImportExcept(imported,
			serverModule+"/model",
			serverModule+"/store",
			serverModule+"/config",
			serverModule+"/migrations",
		)
	case packageOrBelow(from, serverModule+"/store/localcachelayer"):
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported,
				serverModule+"/model",
				serverModule+"/store",
				repositoryModule+"/packages/cache",
			)
	case packageOrBelow(from, serverModule+"/store"):
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported, serverModule+"/model", serverModule+"/store")
	case applicationPackage(from):
		return standardInfrastructureImport(imported) ||
			(thirdPartyImport(imported) && imported != "golang.org/x/crypto/argon2") ||
			(strings.HasPrefix(imported, repositoryModule+"/") &&
				imported != serverModule+"/model" && imported != serverModule+"/store")
	case httpOrWebSocketPackage(from):
		return standardInfrastructureImportExceptHTTP(imported) ||
			(thirdPartyImport(imported) && !strings.HasPrefix(imported, "github.com/gorilla/")) ||
			(strings.HasPrefix(imported, repositoryModule+"/") &&
				imported != serverModule+"/app" && imported != serverModule+"/model")
	case packageOrBelow(from, serverModule+"/cluster"):
		if from == serverModule+"/cluster" {
			return thirdPartyImport(imported) || strings.HasPrefix(imported, repositoryModule+"/")
		}
		return forbiddenProjectImportExcept(imported, serverModule+"/cluster")
	case packageOrBelow(from, serverModule+"/platform/externalauth"):
		return forbiddenProjectImportExcept(imported,
			serverModule+"/app",
			serverModule+"/config",
			serverModule+"/model",
			serverModule+"/platform/externalauth",
		)
	case from == serverModule+"/platform":
		return thirdPartyImport(imported) || concreteInfrastructureImport(imported) ||
			packageOrBelow(imported, serverModule+"/app") ||
			packageOrBelow(imported, serverModule+"/websocket")
	case strings.HasPrefix(from, serverModule+"/cmd/"):
		return commandInfrastructureImport(imported) || thirdPartyImport(imported) ||
			(strings.HasPrefix(imported, repositoryModule+"/") && imported != serverModule)
	case from == serverModule+"/config", from == serverModule+"/mlog", from == serverModule+"/migrations":
		return strings.HasPrefix(imported, repositoryModule+"/")
	default:
		return true
	}
}

func standardInfrastructureImport(importPath string) bool {
	return importPath == "net/http" || standardInfrastructureImportExceptHTTP(importPath)
}

func standardInfrastructureImportExceptHTTP(importPath string) bool {
	return importPath == "database/sql" || strings.HasPrefix(importPath, "database/sql/") ||
		importPath == "net/smtp" || importPath == "os" || importPath == "io/fs" ||
		importPath == "path/filepath"
}

func commandInfrastructureImport(importPath string) bool {
	return importPath == "database/sql" || strings.HasPrefix(importPath, "database/sql/") ||
		importPath == "net/smtp"
}

func thirdPartyImport(importPath string) bool {
	if strings.HasPrefix(importPath, repositoryModule+"/") {
		return false
	}
	first, _, _ := strings.Cut(importPath, "/")
	return strings.Contains(first, ".")
}

func forbiddenProjectImportExcept(importPath string, allowed ...string) bool {
	if !strings.HasPrefix(importPath, repositoryModule+"/") {
		return false
	}
	for _, allowedPath := range allowed {
		if importPath == allowedPath {
			return false
		}
	}
	return true
}

func applicationPackage(packagePath string) bool {
	return packageOrBelow(packagePath, serverModule+"/app") &&
		!packageOrBelow(packagePath, serverModule+"/app/api")
}

func httpOrWebSocketPackage(packagePath string) bool {
	return packageOrBelow(packagePath, serverModule+"/app/api") ||
		packageOrBelow(packagePath, serverModule+"/websocket")
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
	if packageOrBelow(packagePath, repositoryModule+"/packages/cache") ||
		packageOrBelow(packagePath, repositoryModule+"/packages/mail") ||
		packageOrBelow(packagePath, repositoryModule+"/packages/vfs") {
		return true
	}
	if packagePath == serverModule || packagePath == serverModule+"/model" ||
		packagePath == serverModule+"/store" || packagePath == serverModule+"/config" ||
		packagePath == serverModule+"/mlog" || packagePath == serverModule+"/migrations" ||
		packagePath == serverModule+"/platform" || packagePath == serverModule+"/cmd/proctor" {
		return true
	}
	return applicationPackage(packagePath) || httpOrWebSocketPackage(packagePath) ||
		packageOrBelow(packagePath, serverModule+"/store/sqlstore") ||
		packageOrBelow(packagePath, serverModule+"/store/localcachelayer") ||
		packageOrBelow(packagePath, serverModule+"/store/timerlayer") ||
		packageOrBelow(packagePath, serverModule+"/store/retrylayer") ||
		packageOrBelow(packagePath, serverModule+"/cluster/local") ||
		packageOrBelow(packagePath, serverModule+"/cluster/memberlist") ||
		packagePath == serverModule+"/cluster" ||
		packageOrBelow(packagePath, serverModule+"/platform/externalauth")
}

func concreteInfrastructureImport(importPath string) bool {
	concretePrefixes := []string{
		repositoryModule + "/packages/cache/",
		repositoryModule + "/packages/mail/",
		repositoryModule + "/packages/vfs/",
		serverModule + "/cluster/",
		serverModule + "/platform/externalauth/",
	}
	for _, prefix := range concretePrefixes {
		if strings.HasPrefix(importPath, prefix) {
			return true
		}
	}
	return importPath == serverModule+"/store/sqlstore"
}

func productionImportViolations(t *testing.T) map[string]importViolation {
	t.Helper()

	violations := make(map[string]importViolation)
	repositoryRoot := filepath.Clean("../..")
	modules := []struct {
		root       string
		modulePath string
		filePrefix string
	}{
		{root: filepath.Join(repositoryRoot, "server"), modulePath: serverModule},
		{root: filepath.Join(repositoryRoot, "packages", "cache"), modulePath: repositoryModule + "/packages/cache", filePrefix: "packages/cache"},
		{root: filepath.Join(repositoryRoot, "packages", "mail"), modulePath: repositoryModule + "/packages/mail", filePrefix: "packages/mail"},
		{root: filepath.Join(repositoryRoot, "packages", "vfs"), modulePath: repositoryModule + "/packages/vfs", filePrefix: "packages/vfs"},
	}
	for _, module := range modules {
		if _, err := os.Stat(module.root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("inspect module root %q: %v", module.root, err)
		}
		if err := scanProductionImports(module.root, module.modulePath, module.filePrefix, violations); err != nil {
			t.Fatalf("scan production imports: %v", err)
		}
	}
	return violations
}

func scanProductionImports(moduleRoot, modulePath, filePrefix string, violations map[string]importViolation) error {
	return filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != moduleRoot && (entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		relativeFile, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return fmt.Errorf("make %q relative to module root: %w", path, err)
		}
		packagePath := modulePath
		if directory := filepath.Dir(relativeFile); directory != "." {
			packagePath += "/" + filepath.ToSlash(directory)
		}
		if exemptProductionHelper(packagePath) {
			return nil
		}
		if !knownProductionPackage(packagePath) {
			violation := importViolation{file: filepath.ToSlash(relativeFile), importPath: "<unclassified production package>"}
			if filePrefix != "" {
				violation.file = filepath.ToSlash(filepath.Join(filePrefix, relativeFile))
			}
			violations[violation.key()] = violation
			return nil
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse imports from %q: %w", relativeFile, err)
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("unquote import in %q: %w", relativeFile, err)
			}
			if forbiddenImport(packagePath, importPath) {
				file := filepath.ToSlash(relativeFile)
				if filePrefix != "" {
					file = filepath.ToSlash(filepath.Join(filePrefix, relativeFile))
				}
				violation := importViolation{file: file, importPath: importPath}
				violations[violation.key()] = violation
			}
		}
		return nil
	})
}

func readDependencyDebt(t *testing.T) map[string]importViolation {
	t.Helper()

	file, err := os.Open("dependency_debt.txt")
	if err != nil {
		t.Fatalf("open dependency debt: %v", err)
	}
	defer file.Close()

	return parseDependencyDebt(t, "dependency_debt.txt", file)
}

func parseDependencyDebt(t *testing.T, name string, reader io.Reader) map[string]importViolation {
	t.Helper()

	debt := make(map[string]importViolation)
	var entries []string
	scanner := bufio.NewScanner(reader)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			t.Fatalf("%s:%d: want <file><tab><import path>", name, lineNumber)
		}
		violation := importViolation{file: parts[0], importPath: parts[1]}
		if _, exists := debt[violation.key()]; exists {
			t.Fatalf("%s:%d: duplicate entry %q", name, lineNumber, violation.key())
		}
		debt[violation.key()] = violation
		entries = append(entries, violation.key())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if !sort.StringsAreSorted(entries) {
		t.Fatalf("%s entries must be sorted by file and import path", name)
	}
	return debt
}
