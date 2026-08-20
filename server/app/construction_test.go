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
	"strings"
	"testing"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type constructionCatalogStub struct{ store.Catalog }
type constructionCacheStub struct{ authenticationCache }
type constructionMailDeliverySenderStub struct{ MailDeliverySender }
type constructionMailTemplateRendererStub struct{ DirectMailTemplateRenderer }
type constructionRegistryStub struct{ externalProviderSource }
type constructionFileContentStub struct{ FileContent }
type constructionAuthenticationDiagnosticsStub struct{ authenticationDiagnostics }
type constructionRealtimeDiagnosticsStub struct{ realtimeDiagnostics }
type constructionRecoveryDiagnosticsStub struct{ recoveryDiagnostics }

type activeMailPayloadKeyStoreFake struct {
	state *store.MailKeyState
	err   error
}

func (s activeMailPayloadKeyStoreFake) InspectKeyState(context.Context) (*store.MailKeyState, error) {
	return s.state, s.err
}

type mailPayloadKeyRingFake struct {
	primary string
	keys    map[string]bool
}

func (r mailPayloadKeyRingFake) HasKey(keyID string) bool { return r.keys[keyID] }
func (r mailPayloadKeyRingFake) PrimaryKeyID() string     { return r.primary }

func (constructionMailDeliverySenderStub) Enabled() bool { return false }

func TestActiveMailPayloadKeysAreValidatedBeforeWorkersStart(t *testing.T) {
	t.Parallel()
	keyID := "0123456789abcdef0123456789abcdef"
	validState := &store.MailKeyState{RequiredPrimaryKeyID: keyID,
		Active: []store.MailPayloadKeyUsage{{KeyID: keyID, ActiveReferences: 7}}}
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: validState}, mailPayloadKeyRingFake{primary: keyID, keys: map[string]bool{keyID: true}}); err != nil {
		t.Fatalf("validateActiveMailPayloadKeys() = %v", err)
	}
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: validState}, mailPayloadKeyRingFake{primary: keyID, keys: map[string]bool{}}); err == nil || !strings.Contains(err.Error(), keyID) || !strings.Contains(err.Error(), "7") {
		t.Fatal("missing active key was accepted")
	}
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{err: errors.New("database unavailable")}, mailPayloadKeyRingFake{}); err == nil {
		t.Fatal("persistence failure was accepted")
	}
	otherPrimary := "11111111111111111111111111111111"
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: validState}, mailPayloadKeyRingFake{primary: otherPrimary, keys: map[string]bool{keyID: true, otherPrimary: true}}); err == nil || !strings.Contains(err.Error(), keyID) || !strings.Contains(err.Error(), otherPrimary) {
		t.Fatal("stale configured primary was accepted")
	}
	promotable := *validState
	promotable.PrimaryPromotionAllowed = true
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: &promotable}, mailPayloadKeyRingFake{primary: otherPrimary, keys: map[string]bool{keyID: true, otherPrimary: true}}); err != nil {
		t.Fatalf("staged next-primary restart was rejected: %v", err)
	}
	if err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: &promotable}, mailPayloadKeyRingFake{primary: otherPrimary, keys: map[string]bool{otherPrimary: true}}); err == nil {
		t.Fatal("staged next-primary restart without the required fallback was accepted")
	}
}

func TestApplicationDependencyValidationIsFailFastAndOrdered(t *testing.T) {
	t.Parallel()

	valid := Dependencies{
		Store:                     constructionCatalogStub{},
		Cache:                     constructionCacheStub{},
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
		identityConstruction{},
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
	jobs                 store.JobStore
	users                store.UserStore
	files                store.FileStore
	institutions         store.InstitutionStore
	desktop              store.DesktopAuthorizationStore
	externalLoginStates  store.ExternalLoginStateStore
	invitations          store.InvitationStore
	onboardingImports    store.OnboardingImportStore
	personalAccessTokens store.PersonalAccessTokenStore
	examSittings         store.ExamSittingStore
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
func (catalog constructionCatalogWithJobs) DesktopAuthorization() store.DesktopAuthorizationStore {
	return catalog.desktop
}
func (catalog constructionCatalogWithJobs) ExternalLoginState() store.ExternalLoginStateStore {
	return catalog.externalLoginStates
}
func (catalog constructionCatalogWithJobs) Invitation() store.InvitationStore {
	return catalog.invitations
}
func (catalog constructionCatalogWithJobs) OnboardingImport() store.OnboardingImportStore {
	return catalog.onboardingImports
}
func (catalog constructionCatalogWithJobs) PersonalAccessToken() store.PersonalAccessTokenStore {
	return catalog.personalAccessTokens
}
func (catalog constructionCatalogWithJobs) ExamSitting() store.ExamSittingStore {
	return catalog.examSittings
}
func (catalog constructionCatalogWithJobs) CommandOutcome() store.CommandOutcomeStore {
	return constructionCommandOutcomeStoreStub{}
}

type constructionJobStoreStub struct{ store.JobStore }
type constructionUserStoreStub struct{ store.UserStore }
type constructionFileStoreStub struct{ store.FileStore }
type constructionInstitutionStoreStub struct{ store.InstitutionStore }
type constructionInvitationStoreStub struct{ store.InvitationStore }
type constructionOnboardingImportStoreStub struct{ store.OnboardingImportStore }
type constructionPersonalAccessTokenStoreStub struct{ store.PersonalAccessTokenStore }
type constructionExamSittingStoreStub struct{ store.ExamSittingStore }
type constructionDesktopAuthorizationStoreStub struct {
	store.DesktopAuthorizationStore
}
type constructionExternalLoginStateStoreStub struct{ store.ExternalLoginStateStore }
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
				desktop: constructionDesktopAuthorizationStoreStub{}, externalLoginStates: constructionExternalLoginStateStoreStub{}, invitations: constructionInvitationStoreStub{},
				onboardingImports:    constructionOnboardingImportStoreStub{},
				personalAccessTokens: constructionPersonalAccessTokenStoreStub{},
				examSittings:         constructionExamSittingStoreStub{},
			},
			NodeID: "node-a", RecoveryDiagnostics: constructionRecoveryDiagnosticsStub{},
		},
		applicationFoundation{},
		accessAcademicConstruction{},
		identityConstruction{},
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
				desktop: constructionDesktopAuthorizationStoreStub{}, externalLoginStates: constructionExternalLoginStateStoreStub{}, invitations: constructionInvitationStoreStub{},
				onboardingImports: constructionOnboardingImportStoreStub{}, personalAccessTokens: constructionPersonalAccessTokenStoreStub{},
			},
			FileContent: constructionFileContentStub{},
		},
		identityConstruction{onboardingImports: &onboardingImportService{}},
		examinationConstruction{sittings: constructionExamSittingUseCasesStub{}, attempts: constructionExamAttemptUseCasesStub{}}, profiles,
		&defaultProfilePictureJobProposer{jobs: constructionJobStoreStub{}},
		newMailHealth(false),
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
	invitationMaintenance, exists := descriptors[model.JobTypeInvitationMaintenance]
	if !exists {
		t.Fatal("Invitation maintenance descriptor is absent from the application Job graph")
	}
	if handler, ok := invitationMaintenance.Handler.(invitationMaintenanceHandler); !ok || handler.invitations == nil || handler.imports == nil || handler.content == nil || handler.now == nil {
		t.Fatalf("Invitation maintenance handler = %#v", invitationMaintenance.Handler)
	}
	if descriptor, ok := descriptors[model.JobTypeOnboardingImportParse]; !ok || !descriptor.Cancelable {
		t.Fatalf("Onboarding import parse descriptor = %#v", descriptor)
	}
	if descriptor, ok := descriptors[model.JobTypeOnboardingImportExecute]; !ok || !descriptor.Cancelable || len(descriptor.CheckpointVersions) != 1 {
		t.Fatalf("Onboarding import execution descriptor = %#v", descriptor)
	}

	recurrenceCount := 0
	invitationMaintenanceRecurrenceCount := 0
	for _, recurrence := range definitions.recurrences {
		if recurrence.Name == "invitation-maintenance" {
			invitationMaintenanceRecurrenceCount++
			proposer, ok := recurrence.Proposer.(invitationMaintenanceProposer)
			if !ok || proposer.jobs == nil || proposer.now == nil {
				t.Fatalf("Invitation maintenance proposer = %#v", recurrence.Proposer)
			}
		}
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
	if invitationMaintenanceRecurrenceCount != 1 {
		t.Fatalf("Invitation maintenance recurrence count = %d", invitationMaintenanceRecurrenceCount)
	}
	periodicByName := make(map[string]jobengine.PeriodicTask, len(definitions.periodicTasks))
	for _, task := range definitions.periodicTasks {
		periodicByName[task.Name] = task
	}
	periodic, exists := periodicByName[examAttemptExpiryPeriodicTaskName]
	if !exists {
		t.Fatal("Attempt expiry periodic task is absent")
	}
	if periodic.Name != examAttemptExpiryPeriodicTaskName || periodic.Interval != examAttemptExpiryScanInterval {
		t.Fatalf("Attempt expiry periodic task = %#v", periodic)
	}
	if runner, ok := periodic.Runner.(examAttemptExpiryPeriodicRunner); !ok || runner.attempts == nil {
		t.Fatalf("Attempt expiry periodic runner = %#v", periodic.Runner)
	}
	desktopPeriodic, exists := periodicByName["desktop-authorization-maintenance"]
	if !exists || desktopPeriodic.Interval != desktopAuthorizationMaintenanceInterval {
		t.Fatalf("Desktop authorization maintenance periodic task = %#v", desktopPeriodic)
	}
	if runner, ok := desktopPeriodic.Runner.(desktopAuthorizationMaintenancePeriodicRunner); !ok || runner.transactions == nil {
		t.Fatalf("Desktop authorization maintenance runner = %#v", desktopPeriodic.Runner)
	}
	externalPeriodic, exists := periodicByName["external-authentication-maintenance"]
	if !exists || externalPeriodic.Interval != externalAuthenticationMaintenanceInterval {
		t.Fatalf("External authentication maintenance periodic task = %#v", externalPeriodic)
	}
	if runner, ok := externalPeriodic.Runner.(externalAuthenticationMaintenancePeriodicRunner); !ok || runner.states == nil {
		t.Fatalf("External authentication maintenance runner = %#v", externalPeriodic.Runner)
	}
	personalAccessTokenPeriodic, exists := periodicByName["personal-access-token-maintenance"]
	if !exists || personalAccessTokenPeriodic.Interval != personalAccessTokenMaintenanceInterval {
		t.Fatalf("Personal access token maintenance periodic task = %#v", personalAccessTokenPeriodic)
	}
	if runner, ok := personalAccessTokenPeriodic.Runner.(personalAccessTokenMaintenancePeriodicRunner); !ok || runner.tokens == nil {
		t.Fatalf("Personal access token maintenance runner = %#v", personalAccessTokenPeriodic.Runner)
	}
	if _, exists = descriptors[model.JobType("authentication.desktop_authorization_maintenance")]; exists {
		t.Fatal("Desktop authorization maintenance must not create a durable Job descriptor")
	}
	if _, exists = descriptors[model.JobType("personal_access_token.mutation_maintenance")]; exists {
		t.Fatal("Personal access token maintenance must not create a durable Job descriptor")
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
		!retained[model.JobTypeExamSittingSealing] || !retained[model.JobTypeInvitationMaintenance] ||
		!retained[model.JobTypeOnboardingImportParse] || !retained[model.JobTypeOnboardingImportExecute] {
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
