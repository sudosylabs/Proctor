// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"testing"

	"github.com/sudosylabs/proctor/server/store"
)

type constructionCatalogStub struct{ store.Catalog }
type constructionCacheStub struct{ authenticationCache }
type constructionMailerStub struct{ AccountMailer }
type constructionRegistryStub struct{ externalProviderSource }
type constructionFileContentStub struct{ FileContent }
type constructionAuthenticationDiagnosticsStub struct{ authenticationDiagnostics }
type constructionRealtimeDiagnosticsStub struct{ realtimeDiagnostics }
type constructionRecoveryDiagnosticsStub struct{ recoveryDiagnostics }

func TestApplicationDependencyValidationIsFailFastAndOrdered(t *testing.T) {
	t.Parallel()

	valid := Dependencies{
		Store:                     constructionCatalogStub{},
		Cache:                     constructionCacheStub{},
		Mailer:                    constructionMailerStub{},
		Registry:                  constructionRegistryStub{},
		FileContent:               constructionFileContentStub{},
		NodeID:                    "node-a",
		AuthenticationDiagnostics: constructionAuthenticationDiagnosticsStub{},
		RealtimeDiagnostics:       constructionRealtimeDiagnosticsStub{},
		RecoveryDiagnostics:       constructionRecoveryDiagnosticsStub{},
	}
	if err := validateApplicationDependencies(valid); err != nil {
		t.Fatalf("validate complete dependencies: %v", err)
	}

	tests := []struct {
		name    string
		missing func(*Dependencies)
		want    string
	}{
		{name: "store", missing: func(deps *Dependencies) { deps.Store = nil }, want: "store is required"},
		{name: "cache", missing: func(deps *Dependencies) { deps.Cache = nil }, want: "cache is required"},
		{name: "mailer", missing: func(deps *Dependencies) { deps.Mailer = nil }, want: "mailer is required"},
		{name: "provider registry", missing: func(deps *Dependencies) { deps.Registry = nil }, want: "external provider registry is required"},
		{name: "file content", missing: func(deps *Dependencies) { deps.FileContent = nil }, want: "file content is required"},
		{name: "node ID", missing: func(deps *Dependencies) { deps.NodeID = "" }, want: "node ID is required"},
		{name: "authentication diagnostics", missing: func(deps *Dependencies) { deps.AuthenticationDiagnostics = nil }, want: "authentication diagnostics is required"},
		{name: "realtime diagnostics", missing: func(deps *Dependencies) { deps.RealtimeDiagnostics = nil }, want: "realtime diagnostics is required"},
		{name: "recovery diagnostics", missing: func(deps *Dependencies) { deps.RecoveryDiagnostics = nil }, want: "recovery diagnostics is required"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps := valid
			test.missing(&deps)
			_, err := New(deps)
			if err == nil || err.Error() != test.want {
				t.Fatalf("New validation error = %v, want %q", err, test.want)
			}
		})
	}

	_, err := New(Dependencies{})
	if err == nil || err.Error() != "store is required" {
		t.Fatalf("New with every dependency missing = %v, want store precedence", err)
	}
	deps := valid
	deps.Cache = nil
	deps.Mailer = nil
	_, err = New(deps)
	if err == nil || err.Error() != "cache is required" {
		t.Fatalf("New with cache and mailer missing = %v, want cache precedence", err)
	}
}

type constructionCatalogWithoutJobs struct{ store.Catalog }

func (constructionCatalogWithoutJobs) Job() store.JobStore { return nil }

func TestJobRecipePreservesLifecycleOnlyGraphs(t *testing.T) {
	t.Parallel()

	constructed, err := constructJobs(
		Dependencies{Store: constructionCatalogWithoutJobs{}},
		applicationFoundation{},
		accessAcademicConstruction{},
		profileFileConstruction{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if constructed.runtime != nil || constructed.operations != nil {
		t.Fatalf("constructed Jobs = %#v, want empty lifecycle-only result", constructed)
	}
}

type constructionCatalogWithJobs struct {
	store.Catalog
	jobs         store.JobStore
	users        store.UserStore
	files        store.FileStore
	institutions store.InstitutionStore
}

func (catalog constructionCatalogWithJobs) Job() store.JobStore   { return catalog.jobs }
func (catalog constructionCatalogWithJobs) User() store.UserStore { return catalog.users }
func (catalog constructionCatalogWithJobs) File() store.FileStore { return catalog.files }
func (constructionCatalogWithJobs) ExamStarterWorkspace() store.ExamStarterWorkspaceStore {
	return nil
}
func (catalog constructionCatalogWithJobs) Institution() store.InstitutionStore {
	return catalog.institutions
}
func (catalog constructionCatalogWithJobs) CommandOutcome() store.CommandOutcomeStore {
	return constructionCommandOutcomeStoreStub{}
}

type constructionJobStoreStub struct{ store.JobStore }
type constructionUserStoreStub struct{ store.UserStore }
type constructionFileStoreStub struct{ store.FileStore }
type constructionInstitutionStoreStub struct{ store.InstitutionStore }
type constructionCommandOutcomeStoreStub struct{}

func (constructionCommandOutcomeStoreStub) DeleteExpired(context.Context, int) (int64, error) {
	return 0, nil
}

func TestJobRecipeConnectsRuntimeOperationsAndProfileWake(t *testing.T) {
	t.Parallel()

	profiles := profileFileConstruction{profilePictures: &profilePictureService{reads: &profilePictureReadService{}}}
	jobs, err := constructJobs(
		Dependencies{
			Store: constructionCatalogWithJobs{
				jobs: constructionJobStoreStub{}, users: constructionUserStoreStub{},
				files: constructionFileStoreStub{}, institutions: constructionInstitutionStoreStub{},
			},
			NodeID: "node-a", RecoveryDiagnostics: constructionRecoveryDiagnosticsStub{},
		},
		applicationFoundation{},
		accessAcademicConstruction{},
		profiles,
	)
	if err != nil {
		t.Fatal(err)
	}
	if jobs.runtime == nil || jobs.operations == nil {
		t.Fatalf("constructed Jobs = %#v, want runtime and operations", jobs)
	}
	proposer, ok := profiles.profilePictures.reads.defaultJobs.(*defaultProfilePictureJobProposer)
	if !ok || proposer.wake == nil {
		t.Fatalf("profile default Jobs = %#v, want attached proposer with wake backlink", profiles.profilePictures.reads.defaultJobs)
	}
	application := assembleApplication(
		Dependencies{}, applicationFoundation{}, identityConstruction{}, accessAcademicConstruction{},
		examinationConstruction{}, profiles, jobs, administrationConstruction{},
	)
	if application.jobs != jobs.runtime || application.jobOperations != jobs.operations {
		t.Fatal("assembled application did not retain the constructed Job runtime and operations")
	}
}

func TestApplicationAssemblyNamesEveryFacadeField(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	parsedApp, err := parser.ParseFile(token.NewFileSet(), "app.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := structFieldNames(t, parsedApp, "App")

	source, err = os.ReadFile("construction.go")
	if err != nil {
		t.Fatal(err)
	}
	parsedConstruction, err := parser.ParseFile(token.NewFileSet(), "construction.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := compositeLiteralFieldNames(t, parsedConstruction, "App")
	if len(got) != len(want) {
		t.Fatalf("assembled App fields = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("assembled App fields = %v, want %v", got, want)
		}
	}
}

func structFieldNames(t *testing.T, file *ast.File, typeName string) []string {
	t.Helper()
	var names []string
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != typeName {
			return true
		}
		definition, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			t.Fatalf("%s is not a struct", typeName)
		}
		for _, field := range definition.Fields.List {
			for _, name := range field.Names {
				names = append(names, name.Name)
			}
		}
		return false
	})
	sort.Strings(names)
	return names
}

func compositeLiteralFieldNames(t *testing.T, file *ast.File, typeName string) []string {
	t.Helper()
	var names []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		identifier, ok := literal.Type.(*ast.Ident)
		if !ok || identifier.Name != typeName {
			return true
		}
		for _, element := range literal.Elts {
			keyed, ok := element.(*ast.KeyValueExpr)
			if !ok {
				t.Fatalf("%s assembly uses an unkeyed field", typeName)
			}
			key, ok := keyed.Key.(*ast.Ident)
			if !ok {
				t.Fatalf("%s assembly has a non-identifier field", typeName)
			}
			names = append(names, key.Name)
		}
		return false
	})
	sort.Strings(names)
	return names
}
