// Copyright (c) 2026 Sudo Systems Labs Ltd
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package architecture_test

import (
	"fmt"
	"go/parser"
	"go/token"
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
		{name: "identity-provider constraints cannot import domain", from: serverModule + "/identityprovider", imported: serverModule + "/model"},
		{name: "identity-provider constraints cannot import configuration", from: serverModule + "/identityprovider", imported: serverModule + "/config"},
		{name: "model build tools cannot import server code", from: serverModule + "/model/internal/idgen", imported: serverModule + "/app"},
		{name: "model build tools cannot import third-party code", from: serverModule + "/model/internal/idgen", imported: "golang.org/x/tools/go/packages"},
		{name: "ACME adapter cannot import application", from: serverModule + "/internal/autocert", imported: serverModule + "/app"},
		{name: "ACME adapter cannot import unrelated third-party code", from: serverModule + "/internal/autocert", imported: "github.com/gorilla/mux"},
		{name: "OpenAPI compiler cannot use the process filesystem", from: serverModule + "/internal/openapidoc", imported: "os"},
		{name: "OpenAPI compiler cannot import HTTP", from: serverModule + "/internal/openapidoc", imported: "net/http"},
		{name: "store contracts cannot import SQL adapters", from: serverModule + "/store", imported: serverModule + "/store/sqlstore"},
		{name: "application cannot import platform", from: serverModule + "/app", imported: serverModule + "/platform"},
		{name: "Exam child cannot import parent application", from: serverModule + "/app/exam", imported: serverModule + "/app"},
		{name: "Exam child cannot import HTTP transport", from: serverModule + "/app/exam", imported: serverModule + "/httpapi"},
		{name: "Exam child cannot import platform", from: serverModule + "/app/exam", imported: serverModule + "/platform"},
		{name: "Exam child cannot import SQL adapter", from: serverModule + "/app/exam", imported: serverModule + "/store/sqlstore"},
		{name: "Realtime child cannot import parent application", from: serverModule + "/app/realtime", imported: serverModule + "/app"},
		{name: "Realtime child cannot import persistence", from: serverModule + "/app/realtime", imported: serverModule + "/store"},
		{name: "Realtime child cannot import WebSocket", from: serverModule + "/app/realtime", imported: serverModule + "/websocket"},
		{name: "Realtime child cannot import platform", from: serverModule + "/app/realtime", imported: serverModule + "/platform"},
		{name: "Mail child cannot import parent application", from: serverModule + "/app/mail", imported: serverModule + "/app"},
		{name: "Mail child cannot import HTTP transport", from: serverModule + "/app/mail", imported: serverModule + "/httpapi"},
		{name: "Mail child cannot import platform", from: serverModule + "/app/mail", imported: serverModule + "/platform"},
		{name: "Mail child cannot import SQL adapter", from: serverModule + "/app/mail", imported: serverModule + "/store/sqlstore"},
		{name: "Execution child cannot import execenv", from: serverModule + "/app/execution", imported: "github.com/sudosylabs/execenv"},
		{name: "Execution child cannot import platform", from: serverModule + "/app/execution", imported: serverModule + "/platform"},
		{name: "Execution host adapter cannot import persistence", from: serverModule + "/executionhost", imported: serverModule + "/store"},
		{name: "application cannot import Redis client", from: serverModule + "/app", imported: "github.com/redis/go-redis/v9"},
		{name: "Job engine descendants cannot import application", from: serverModule + "/app/job/internal", imported: serverModule + "/app"},
		{name: "Job engine cannot import HTTP transport", from: serverModule + "/app/job", imported: serverModule + "/httpapi"},
		{name: "Job engine cannot import platform", from: serverModule + "/app/job", imported: serverModule + "/platform"},
		{name: "Job engine cannot import SQL adapter", from: serverModule + "/app/job", imported: serverModule + "/store/sqlstore"},
		{name: "Job engine descendants cannot import Argon2", from: serverModule + "/app/job/internal", imported: "golang.org/x/crypto/argon2"},
		{name: "concrete Jobs cannot import parent application", from: serverModule + "/app/jobs", imported: serverModule + "/app"},
		{name: "concrete Jobs cannot import HTTP transport", from: serverModule + "/app/jobs", imported: serverModule + "/httpapi"},
		{name: "concrete Jobs cannot import platform", from: serverModule + "/app/jobs", imported: serverModule + "/platform"},
		{name: "concrete Jobs cannot import SQL adapter", from: serverModule + "/app/jobs", imported: serverModule + "/store/sqlstore"},
		{name: "File Content cannot import persistence", from: serverModule + "/filecontent", imported: serverModule + "/store"},
		{name: "File Content cannot import HTTP", from: serverModule + "/filecontent", imported: "net/http"},
		{name: "File Content cannot import WebSocket", from: serverModule + "/filecontent", imported: serverModule + "/websocket"},
		{name: "File Content cannot locate platform services", from: serverModule + "/filecontent", imported: serverModule + "/platform"},
		{name: "File Content cannot select configuration", from: serverModule + "/filecontent", imported: serverModule + "/config"},
		{name: "File Content cannot run Jobs", from: serverModule + "/filecontent", imported: serverModule + "/app/job"},
		{name: "File Content cannot select a VFS backend", from: serverModule + "/filecontent", imported: repositoryModule + "/packages/vfs/local"},
		{name: "File Content cannot select S3", from: serverModule + "/filecontent", imported: repositoryModule + "/packages/vfs/s3"},
		{name: "HTTP cannot import persistence", from: serverModule + "/httpapi", imported: serverModule + "/store"},
		{name: "HTTP cannot import platform services", from: serverModule + "/httpapi", imported: serverModule + "/platform"},
		{name: "HTTP cannot import SQL driver", from: serverModule + "/httpapi", imported: "database/sql"},
		{name: "SQL adapter cannot import application policy", from: serverModule + "/store/sqlstore", imported: serverModule + "/app"},
		{name: "metrics cannot import application policy", from: serverModule + "/metrics", imported: serverModule + "/app"},
		{name: "metrics cannot import concrete infrastructure", from: serverModule + "/metrics", imported: repositoryModule + "/packages/cache/redis"},
		{name: "store layer cannot import application policy", from: serverModule + "/store/retrylayer", imported: serverModule + "/app"},
		{name: "cluster adapter cannot import platform", from: serverModule + "/cluster/memberlist", imported: serverModule + "/platform"},
		{name: "cluster adapter cannot import persistence", from: serverModule + "/cluster/memberlist", imported: serverModule + "/store"},
		{name: "external adapter cannot import HTTP", from: serverModule + "/platform/externalauth/oidc", imported: serverModule + "/httpapi"},
		{name: "platform cannot select concrete adapters", from: serverModule + "/platform", imported: "github.com/sudosylabs/proctor/packages/cache/redis"},
		{name: "operator executable cannot bypass its command tree", from: serverModule + "/cmd/proctor", imported: serverModule},
		{name: "command tree cannot bypass root composition", from: serverModule + "/cmd/proctor/commands", imported: serverModule + "/app"},
		{name: "command tree cannot open SQL directly", from: serverModule + "/cmd/proctor/commands", imported: "database/sql"},
		{name: "unknown operator child package fails closed", from: serverModule + "/cmd/proctor/helpers", imported: "context"},
		{name: "localization cannot import application", from: serverModule + "/localization", imported: serverModule + "/app"},
		{name: "mail rendering cannot import transport", from: serverModule + "/app/mail", imported: repositoryModule + "/packages/mail"},
		{name: "mail preview cannot import application", from: serverModule + "/cmd/mailpreview", imported: serverModule + "/app"},
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
		{name: "domain may import identity-provider constraints", from: serverModule + "/model", imported: serverModule + "/identityprovider"},
		{name: "configuration may import identity-provider constraints", from: serverModule + "/config", imported: serverModule + "/identityprovider"},
		{name: "application may import store contracts", from: serverModule + "/app", imported: serverModule + "/store"},
		{name: "application may import Job engine", from: serverModule + "/app", imported: serverModule + "/app/job"},
		{name: "application may import concrete Jobs", from: serverModule + "/app", imported: serverModule + "/app/jobs"},
		{name: "application may import Realtime child", from: serverModule + "/app", imported: serverModule + "/app/realtime"},
		{name: "application may import Exam child", from: serverModule + "/app", imported: serverModule + "/app/exam"},
		{name: "application may import Exam resource child", from: serverModule + "/app", imported: serverModule + "/app/exam/resource"},
		{name: "application may import Exam workspace child", from: serverModule + "/app", imported: serverModule + "/app/exam/workspace"},
		{name: "application may import Mail child", from: serverModule + "/app", imported: serverModule + "/app/mail"},
		{name: "Mail child may import domain models", from: serverModule + "/app/mail", imported: serverModule + "/model"},
		{name: "Mail child may import store contracts", from: serverModule + "/app/mail", imported: serverModule + "/store"},
		{name: "Mail child may import secret sealing", from: serverModule + "/app/mail", imported: serverModule + "/secretseal"},
		{name: "Mail child may import Exam mail contracts", from: serverModule + "/app/mail", imported: serverModule + "/app/exam"},
		{name: "Mail child may import localization", from: serverModule + "/app/mail", imported: serverModule + "/localization"},
		{name: "application may import Execution child", from: serverModule + "/app", imported: serverModule + "/app/execution"},
		{name: "Execution child may import domain models", from: serverModule + "/app/execution", imported: serverModule + "/model"},
		{name: "Execution child may import store contracts", from: serverModule + "/app/execution", imported: serverModule + "/store"},
		{name: "Execution host adapter may import execenv", from: serverModule + "/executionhost", imported: "github.com/sudosylabs/execenv/remote"},
		{name: "Mail child may accept presentation filesystems", from: serverModule + "/app/mail", imported: "io/fs"},
		{name: "Exam child may import domain models", from: serverModule + "/app/exam", imported: serverModule + "/model"},
		{name: "Exam child may import store contracts", from: serverModule + "/app/exam", imported: serverModule + "/store"},
		{name: "Exam children may import shared safe Markdown", from: serverModule + "/app/exam/review", imported: serverModule + "/app/exam/safemarkdown"},
		{name: "Realtime child may import domain models", from: serverModule + "/app/realtime", imported: serverModule + "/model"},
		{name: "Job engine may import store contracts", from: serverModule + "/app/job", imported: serverModule + "/store"},
		{name: "concrete Jobs may import Job engine", from: serverModule + "/app/jobs", imported: serverModule + "/app/job"},
		{name: "concrete Jobs may import Mail capabilities", from: serverModule + "/app/jobs", imported: serverModule + "/app/mail"},
		{name: "concrete Jobs may import Exam capabilities", from: serverModule + "/app/jobs", imported: serverModule + "/app/exam/attempt"},
		{name: "File Content may import domain models", from: serverModule + "/filecontent", imported: serverModule + "/model"},
		{name: "File Content may import application content contracts", from: serverModule + "/filecontent", imported: serverModule + "/app"},
		{name: "File Content may import VFS contracts", from: serverModule + "/filecontent", imported: repositoryModule + "/packages/vfs"},
		{name: "File Content may import the canonical encoder", from: serverModule + "/filecontent", imported: "github.com/HugoSmits86/nativewebp"},
		{name: "File Content may import bounded image transforms", from: serverModule + "/filecontent", imported: "github.com/disintegration/imaging"},
		{name: "File Content may register supported image decoders", from: serverModule + "/filecontent", imported: "golang.org/x/image/webp"},
		{name: "File Content may structurally validate PDFs", from: serverModule + "/filecontent", imported: "github.com/pdfcpu/pdfcpu/pkg/api"},
		{name: "File Content may use private temporary spools", from: serverModule + "/filecontent", imported: "os"},
		{name: "HTTP may import application", from: serverModule + "/httpapi", imported: serverModule + "/app"},
		{name: "WebSocket may import Realtime child", from: serverModule + "/websocket", imported: serverModule + "/app/realtime"},
		{name: "HTTP may import router library", from: serverModule + "/httpapi", imported: "github.com/gorilla/mux"},
		{name: "SQL adapter may import store contracts", from: serverModule + "/store/sqlstore", imported: serverModule + "/store"},
		{name: "metrics may import timer recorder", from: serverModule + "/metrics", imported: serverModule + "/store/timerlayer"},
		{name: "metrics may import Prometheus", from: serverModule + "/metrics", imported: "github.com/prometheus/client_golang/prometheus"},
		{name: "cache store layer may import cache contract", from: serverModule + "/store/localcachelayer", imported: repositoryModule + "/packages/cache"},
		{name: "cluster adapter may import cluster contracts", from: serverModule + "/cluster/memberlist", imported: serverModule + "/cluster"},
		{name: "application may import password hashing library", from: serverModule + "/app", imported: "golang.org/x/crypto/argon2"},
		{name: "root composition may import platform", from: serverModule, imported: serverModule + "/platform"},
		{name: "operator executable may import its command tree", from: serverModule + "/cmd/proctor", imported: serverModule + "/cmd/proctor/commands"},
		{name: "operator command tree may import the root server facade", from: serverModule + "/cmd/proctor/commands", imported: serverModule},
		{name: "operator command tree may import localization", from: serverModule + "/cmd/proctor/commands", imported: serverModule + "/localization"},
		{name: "operator command tree may import Cobra", from: serverModule + "/cmd/proctor/commands", imported: "github.com/spf13/cobra"},
		{name: "mail preview may import mail rendering", from: serverModule + "/cmd/mailpreview", imported: serverModule + "/app/mail"},
		{name: "mail preview may import localization", from: serverModule + "/cmd/mailpreview", imported: serverModule + "/localization"},
		{name: "standard library remains available", from: serverModule + "/app", imported: "context"},
		{name: "model build tools may import standard library", from: serverModule + "/model/internal/idgen", imported: "go/format"},
		{name: "ACME adapter may import ACME protocol", from: serverModule + "/internal/autocert", imported: "golang.org/x/crypto/acme"},
		{name: "ACME adapter may canonicalize IDNs", from: serverModule + "/internal/autocert", imported: "golang.org/x/net/idna"},
		{name: "OpenAPI compiler may accept a filesystem", from: serverModule + "/internal/openapidoc", imported: "io/fs"},
		{name: "OpenAPI compiler may validate OpenAPI", from: serverModule + "/internal/openapidoc", imported: "github.com/getkin/kin-openapi/openapi3"},
		{name: "ptool may import the OpenAPI compiler", from: serverModule + "/cmd/ptool/commands", imported: serverModule + "/internal/openapidoc"},
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

func TestProductionImportsMatchDependencyPolicy(t *testing.T) {
	violations := productionImportViolations(t)
	keys := make([]string, 0, len(violations))
	for key := range violations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		violation := violations[key]
		t.Errorf("forbidden production import: %s imports %s", violation.file, violation.importPath)
	}
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
