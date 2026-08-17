// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"testing"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type constructionCatalogStub struct{ store.Catalog }
type constructionCacheStub struct{ authenticationCache }
type constructionMailerStub struct{ AccountMailer }
type constructionMailDeliverySenderStub struct{ MailDeliverySender }
type constructionMailTemplateRendererStub struct{ MailTemplateRenderer }
type constructionRegistryStub struct{ externalProviderSource }
type constructionFileContentStub struct{ FileContent }
type constructionAuthenticationDiagnosticsStub struct{ authenticationDiagnostics }
type constructionRealtimeDiagnosticsStub struct{ realtimeDiagnostics }
type constructionRecoveryDiagnosticsStub struct{ recoveryDiagnostics }

type activeMailPayloadKeyStoreFake struct {
	ids []string
	err error
}

func (s activeMailPayloadKeyStoreFake) ActivePayloadKeyIDs(context.Context) ([]string, error) {
	return append([]string(nil), s.ids...), s.err
}

type mailPayloadKeyRingFake map[string]bool

func (r mailPayloadKeyRingFake) HasKey(keyID string) bool { return r[keyID] }

func (constructionMailDeliverySenderStub) Enabled() bool { return false }

func TestActiveMailPayloadKeysAreValidatedBeforeWorkersStart(t *testing.T) {
	t.Parallel()
	keyID := "0123456789abcdef0123456789abcdef"
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{ids: []string{keyID}}, mailPayloadKeyRingFake{keyID: true}); err != nil {
		t.Fatalf("validateActiveMailPayloadKeys() = %v", err)
	}
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{ids: []string{keyID}}, mailPayloadKeyRingFake{}); err == nil {
		t.Fatal("missing active key was accepted")
	}
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{err: errors.New("database unavailable")}, mailPayloadKeyRingFake{}); err == nil {
		t.Fatal("persistence failure was accepted")
	}
}

func TestApplicationDependencyValidationIsFailFastAndOrdered(t *testing.T) {
	t.Parallel()

	valid := Dependencies{
		Store:                     constructionCatalogStub{},
		Cache:                     constructionCacheStub{},
		Mailer:                    constructionMailerStub{},
		MailDeliverySender:        constructionMailDeliverySenderStub{},
		MailTemplateRenderer:      constructionMailTemplateRendererStub{},
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
		{name: "mail delivery sender", missing: func(deps *Dependencies) { deps.MailDeliverySender = nil }, want: "mail delivery sender is required"},
		{name: "mail template renderer", missing: func(deps *Dependencies) { deps.MailTemplateRenderer = nil }, want: "mail template renderer is required"},
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
		examinationConstruction{},
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
func (constructionCatalogWithJobs) Mail() store.MailStore         { return nil }
func (catalog constructionCatalogWithJobs) User() store.UserStore { return catalog.users }
func (catalog constructionCatalogWithJobs) File() store.FileStore { return catalog.files }
func (constructionCatalogWithJobs) ExamStarterWorkspace() store.ExamStarterWorkspaceStore {
	return nil
}
func (constructionCatalogWithJobs) ExamAttemptWorkspace() store.ExamAttemptWorkspaceStore { return nil }
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
type constructionExamSittingUseCasesStub struct{ examSittingUseCases }
type constructionExamAttemptUseCasesStub struct{ examAttemptUseCases }

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
		examinationConstruction{sittings: constructionExamSittingUseCasesStub{}, attempts: constructionExamAttemptUseCasesStub{}},
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

func TestApplicationJobDefinitionsIncludeSittingLifecycleAndDailyRecovery(t *testing.T) {
	t.Parallel()
	profiles := profileFileConstruction{profilePictures: &profilePictureService{reads: &profilePictureReadService{}}}
	definitions := buildApplicationJobDefinitions(
		Dependencies{
			Store: constructionCatalogWithJobs{
				jobs: constructionJobStoreStub{}, users: constructionUserStoreStub{},
				files: constructionFileStoreStub{}, institutions: constructionInstitutionStoreStub{},
			},
			FileContent: constructionFileContentStub{},
		},
		examinationConstruction{sittings: constructionExamSittingUseCasesStub{}, attempts: constructionExamAttemptUseCasesStub{}}, profiles,
		&defaultProfilePictureJobProposer{jobs: constructionJobStoreStub{}},
	)
	if _, err := jobengine.NewRegistry(definitions.descriptors); err != nil {
		t.Fatalf("descriptor registry: %v", err)
	}
	descriptors := make(map[model.JobType]jobengine.Descriptor, len(definitions.descriptors))
	for _, descriptor := range definitions.descriptors {
		descriptors[descriptor.Type] = descriptor
	}
	lifecycle, exists := descriptors[model.JobTypeExamSittingLifecycle]
	if !exists {
		t.Fatal("Exam Sitting lifecycle descriptor is absent from the application Job graph")
	}
	if handler, ok := lifecycle.Handler.(examSittingLifecycleHandler); !ok || handler.reconciler == nil {
		t.Fatalf("lifecycle handler = %#v", lifecycle.Handler)
	}
	sealing, exists := descriptors[model.JobTypeExamSittingSealing]
	if !exists {
		t.Fatal("Exam Sitting sealing descriptor is absent from the application Job graph")
	}
	handler, ok := sealing.Handler.(examSittingSealingHandler)
	if !ok || handler.service == nil {
		t.Fatalf("sealing handler = %#v", sealing.Handler)
	}
	sealingUseCases, ok := handler.service.(examSittingSealingJobUseCases)
	if !ok || sealingUseCases.sittings == nil || sealingUseCases.attempts == nil || sealingUseCases.jobs == nil ||
		sealingUseCases.now == nil || sealingUseCases.newID == nil {
		t.Fatalf("sealing use cases = %#v", handler.service)
	}
	recovery, exists := descriptors[model.JobTypeExamSittingLifecycleRecovery]
	if !exists {
		t.Fatal("Exam Sitting lifecycle recovery descriptor is absent from the application Job graph")
	}
	if handler, ok := recovery.Handler.(examSittingLifecycleRecoveryHandler); !ok || handler.service == nil {
		t.Fatalf("recovery handler = %#v", recovery.Handler)
	}

	recurrenceCount := 0
	for _, recurrence := range definitions.recurrences {
		if recurrence.Name != "exam-sitting-lifecycle-recovery" {
			continue
		}
		recurrenceCount++
		proposer, ok := recurrence.Proposer.(examSittingLifecycleRecoveryProposer)
		if !ok || proposer.jobs == nil || proposer.now == nil {
			t.Fatalf("recovery proposer = %#v", recurrence.Proposer)
		}
	}
	if recurrenceCount != 1 {
		t.Fatalf("lifecycle recovery recurrence count = %d", recurrenceCount)
	}
	if len(definitions.periodicTasks) != 1 {
		t.Fatalf("periodic task count = %d, want 1", len(definitions.periodicTasks))
	}
	periodic := definitions.periodicTasks[0]
	if periodic.Name != examAttemptExpiryPeriodicTaskName || periodic.Interval != examAttemptExpiryScanInterval {
		t.Fatalf("Attempt expiry periodic task = %#v", periodic)
	}
	if runner, ok := periodic.Runner.(examAttemptExpiryPeriodicRunner); !ok || runner.attempts == nil {
		t.Fatalf("Attempt expiry periodic runner = %#v", periodic.Runner)
	}

	cleanup, exists := descriptors[model.JobTypeCleanup]
	if !exists {
		t.Fatal("Job cleanup descriptor is absent")
	}
	cleanupHandler, ok := cleanup.Handler.(jobHistoryCleanupHandler)
	if !ok {
		t.Fatalf("cleanup handler = %#v", cleanup.Handler)
	}
	retained := map[model.JobType]bool{}
	for _, policy := range cleanupHandler.policies {
		retained[policy.Type] = true
	}
	if !retained[model.JobTypeExamSittingLifecycle] || !retained[model.JobTypeExamSittingLifecycleRecovery] ||
		!retained[model.JobTypeExamSittingSealing] {
		t.Fatalf("cleanup retention types = %#v", retained)
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
