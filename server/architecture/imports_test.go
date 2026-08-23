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

// initialDependencyDebt is the immutable pre-migration ceiling. The active
// debt file may remove these entries but may not add or replace them.
const initialDependencyDebt = `app/academic_administration.go	net/http
app/account_recovery.go	github.com/sudosylabs/proctor/packages/mail
app/account_recovery.go	github.com/sudosylabs/proctor/server/mlog
app/account_recovery.go	github.com/sudosylabs/proctor/server/platform
app/account_recovery.go	net/http
app/api/api.go	github.com/sudosylabs/proctor/server/mlog
app/api/authentication.go	github.com/sudosylabs/proctor/server/mlog
app/api/handler.go	github.com/sudosylabs/proctor/server/mlog
app/api/middleware.go	github.com/sudosylabs/proctor/server/mlog
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
app/external_authentication.go	github.com/sudosylabs/proctor/server/mlog
app/external_authentication.go	github.com/sudosylabs/proctor/server/platform
app/external_authentication.go	github.com/sudosylabs/proctor/server/platform/externalauth
app/external_authentication.go	net/http
app/mfa.go	github.com/sudosylabs/proctor/server/config
app/mfa.go	github.com/sudosylabs/proctor/server/platform
app/mfa.go	net/http
app/password.go	github.com/sudosylabs/proctor/server/config
app/personal_access_token.go	net/http
app/realtime.go	github.com/sudosylabs/proctor/server/mlog
app/realtime.go	github.com/sudosylabs/proctor/server/platform
app/realtime.go	net/http
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
		{name: "identity-provider constraints cannot import domain", from: serverModule + "/identityprovider", imported: serverModule + "/model"},
		{name: "identity-provider constraints cannot import configuration", from: serverModule + "/identityprovider", imported: serverModule + "/config"},
		{name: "model build tools cannot import server code", from: serverModule + "/model/internal/idgen", imported: serverModule + "/app"},
		{name: "model build tools cannot import third-party code", from: serverModule + "/model/internal/idgen", imported: "golang.org/x/tools/go/packages"},
		{name: "ACME adapter cannot import application", from: serverModule + "/internal/autocert", imported: serverModule + "/app"},
		{name: "ACME adapter cannot import unrelated third-party code", from: serverModule + "/internal/autocert", imported: "github.com/gorilla/mux"},
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
	case from == serverModule+"/identityprovider":
		return thirdPartyImport(imported) || strings.HasPrefix(imported, repositoryModule+"/")
	case packageOrBelow(from, serverModule+"/model/internal/idgen"):
		return thirdPartyImport(imported) || strings.HasPrefix(imported, repositoryModule+"/")
	case from == serverModule+"/internal/autocert":
		return strings.HasPrefix(imported, repositoryModule+"/") ||
			(thirdPartyImport(imported) && imported != "golang.org/x/crypto/acme" && imported != "golang.org/x/net/idna")
	case from == serverModule+"/model":
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			(strings.HasPrefix(imported, repositoryModule+"/") && imported != serverModule+"/identityprovider")
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
	case packageOrBelow(from, serverModule+"/app/jobs"):
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported,
				serverModule+"/model", serverModule+"/store", serverModule+"/secretseal",
				serverModule+"/app/job", serverModule+"/app/mail", serverModule+"/app/exam/attempt")
	case packageOrBelow(from, serverModule+"/app/job"):
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported, serverModule+"/model", serverModule+"/store")
	case packageOrBelow(from, serverModule+"/app/realtime"):
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported, serverModule+"/model")
	case packageOrBelow(from, serverModule+"/app/execution"):
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported, serverModule+"/model", serverModule+"/store")
	case packageOrBelow(from, serverModule+"/app/mail"):
		return standardInfrastructureImport(imported) && imported != "io/fs" || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported, serverModule+"/model", serverModule+"/store",
				serverModule+"/secretseal", serverModule+"/app/exam", serverModule+"/localization")
	case packageOrBelow(from, serverModule+"/app/exam"):
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			forbiddenProjectImportExcept(imported, serverModule+"/model", serverModule+"/store",
				serverModule+"/app/exam/safemarkdown")
	case packageOrBelow(from, serverModule+"/filecontent"):
		return standardInfrastructureImport(imported) && imported != "os" ||
			(thirdPartyImport(imported) && !fileContentCodecImport(imported)) ||
			forbiddenProjectImportExcept(
				imported,
				serverModule+"/app",
				serverModule+"/model",
				repositoryModule+"/packages/vfs",
			)
	case from == serverModule+"/secretseal":
		return standardInfrastructureImport(imported) || thirdPartyImport(imported) ||
			strings.HasPrefix(imported, repositoryModule+"/")
	case from == serverModule+"/localization":
		return standardInfrastructureImport(imported) && imported != "io/fs" ||
			(thirdPartyImport(imported) && !packageOrBelow(imported, "github.com/nicksnyder/go-i18n/v2/i18n") && imported != "golang.org/x/text/language") ||
			strings.HasPrefix(imported, repositoryModule+"/")
	case applicationPackage(from):
		return standardInfrastructureImport(imported) ||
			(thirdPartyImport(imported) && imported != "golang.org/x/crypto/argon2") ||
			(strings.HasPrefix(imported, repositoryModule+"/") &&
				imported != serverModule+"/model" && imported != serverModule+"/store" &&
				imported != serverModule+"/app/job" && imported != serverModule+"/app/jobs" && imported != serverModule+"/app/realtime" &&
				imported != serverModule+"/app/mail" &&
				imported != serverModule+"/app/execution" &&
				imported != serverModule+"/secretseal" &&
				!packageOrBelow(imported, serverModule+"/app/exam"))
	case httpOrWebSocketPackage(from):
		return standardInfrastructureImportExceptHTTP(imported) ||
			(thirdPartyImport(imported) && !strings.HasPrefix(imported, "github.com/gorilla/")) ||
			(strings.HasPrefix(imported, repositoryModule+"/") &&
				imported != serverModule+"/app" && imported != serverModule+"/app/realtime" &&
				imported != serverModule+"/model" && imported != serverModule+"/localization")
	case packageOrBelow(from, serverModule+"/cluster"):
		if from == serverModule+"/cluster" {
			return thirdPartyImport(imported) || strings.HasPrefix(imported, repositoryModule+"/")
		}
		return forbiddenProjectImportExcept(imported, serverModule+"/cluster")
	case from == serverModule+"/metrics":
		return (thirdPartyImport(imported) && !packageOrBelow(imported, "github.com/prometheus/client_golang")) ||
			forbiddenProjectImportExcept(imported,
				serverModule+"/config", serverModule+"/store/timerlayer", serverModule+"/store/localcachelayer")
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
			packageOrBelow(imported, serverModule+"/httpapi") ||
			packageOrBelow(imported, serverModule+"/websocket")
	case from == serverModule+"/executionhost":
		return (thirdPartyImport(imported) && !packageOrBelow(imported, "github.com/sudosylabs/execenv")) ||
			forbiddenProjectImportExcept(imported, serverModule+"/app/execution")
	case from == serverModule+"/cmd/proctor":
		return thirdPartyImport(imported) ||
			(strings.HasPrefix(imported, repositoryModule+"/") && imported != serverModule+"/cmd/proctor/commands")
	case packageOrBelow(from, serverModule+"/cmd/proctor/commands"):
		return commandInfrastructureImport(imported) ||
			(thirdPartyImport(imported) && imported != "github.com/spf13/cobra") ||
			(strings.HasPrefix(imported, repositoryModule+"/") &&
				imported != serverModule && imported != serverModule+"/localization")
	case from == serverModule+"/cmd/ptool":
		return thirdPartyImport(imported) ||
			(strings.HasPrefix(imported, repositoryModule+"/") && imported != serverModule+"/cmd/ptool/commands")
	case packageOrBelow(from, serverModule+"/cmd/ptool/commands"):
		return (thirdPartyImport(imported) && imported != "github.com/spf13/cobra") ||
			forbiddenProjectImportExcept(imported,
				serverModule+"/app/mail", serverModule+"/cmd/proctor/commands", serverModule+"/httpapi",
				serverModule+"/localization", serverModule+"/websocket")
	case from == serverModule+"/cmd/mailpreview":
		return thirdPartyImport(imported) ||
			(strings.HasPrefix(imported, repositoryModule+"/") &&
				imported != serverModule+"/app/mail" && imported != serverModule+"/localization" &&
				imported != serverModule+"/model")
	case strings.HasPrefix(from, serverModule+"/cmd/"):
		return true
	case from == serverModule+"/config":
		return strings.HasPrefix(imported, repositoryModule+"/") && imported != serverModule+"/identityprovider"
	case from == serverModule+"/logging", from == serverModule+"/migrations":
		return strings.HasPrefix(imported, repositoryModule+"/")
	default:
		return true
	}
}

func fileContentCodecImport(importPath string) bool {
	return importPath == "github.com/HugoSmits86/nativewebp" ||
		importPath == "github.com/disintegration/imaging" ||
		strings.HasPrefix(importPath, "github.com/pdfcpu/pdfcpu/") ||
		importPath == "golang.org/x/image/webp"
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
	return packageOrBelow(packagePath, serverModule+"/app")
}

func httpOrWebSocketPackage(packagePath string) bool {
	return packageOrBelow(packagePath, serverModule+"/httpapi") ||
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
	if packagePath == serverModule || packagePath == serverModule+"/identityprovider" || packagePath == serverModule+"/model" ||
		packageOrBelow(packagePath, serverModule+"/model/internal/idgen") ||
		packagePath == serverModule+"/internal/autocert" ||
		packagePath == serverModule+"/store" || packagePath == serverModule+"/config" ||
		packagePath == serverModule+"/logging" || packagePath == serverModule+"/metrics" || packagePath == serverModule+"/migrations" ||
		packagePath == serverModule+"/secretseal" || packagePath == serverModule+"/localization" ||
		packagePath == serverModule+"/executionhost" ||
		packagePath == serverModule+"/platform" || packagePath == serverModule+"/cmd/proctor" ||
		packageOrBelow(packagePath, serverModule+"/cmd/proctor/commands") ||
		packageOrBelow(packagePath, serverModule+"/cmd/ptool") ||
		packagePath == serverModule+"/cmd/mailpreview" {
		return true
	}
	return applicationPackage(packagePath) || httpOrWebSocketPackage(packagePath) ||
		packageOrBelow(packagePath, serverModule+"/store/sqlstore") ||
		packageOrBelow(packagePath, serverModule+"/store/localcachelayer") ||
		packageOrBelow(packagePath, serverModule+"/store/timerlayer") ||
		packageOrBelow(packagePath, serverModule+"/store/retrylayer") ||
		packageOrBelow(packagePath, serverModule+"/filecontent") ||
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
