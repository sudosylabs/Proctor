// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/exam/sitting"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
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
	require.NoError(t, validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: validState}, mailPayloadKeyRingFake{primary: keyID, keys: map[string]bool{keyID: true}}))
	err := validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: validState}, mailPayloadKeyRingFake{primary: keyID, keys: map[string]bool{}})
	require.Error(t, err, "missing active key was accepted")
	require.Contains(t, err.Error(), keyID, "missing active key was accepted")
	require.Contains(t, err.Error(), "7", "missing active key was accepted")
	require.Error(t, validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{err: errors.New("database unavailable")}, mailPayloadKeyRingFake{}), "persistence failure was accepted")
	otherPrimary := "11111111111111111111111111111111"
	err = validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: validState}, mailPayloadKeyRingFake{primary: otherPrimary, keys: map[string]bool{keyID: true, otherPrimary: true}})
	require.Error(t, err, "stale configured primary was accepted")
	require.Contains(t, err.Error(), keyID, "stale configured primary was accepted")
	require.Contains(t, err.Error(), otherPrimary, "stale configured primary was accepted")
	promotable := *validState
	promotable.PrimaryPromotionAllowed = true
	require.NoError(t, validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: &promotable}, mailPayloadKeyRingFake{primary: otherPrimary, keys: map[string]bool{keyID: true, otherPrimary: true}}))
	require.Error(t, validateActiveMailPayloadKeys(context.Background(), activeMailPayloadKeyStoreFake{state: &promotable}, mailPayloadKeyRingFake{primary: otherPrimary, keys: map[string]bool{otherPrimary: true}}), "staged next-primary restart without the required fallback was accepted")
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
	require.NoError(t, validateApplicationDependencies(valid))

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
			require.Error(t, err, "New validation error")
			require.Equal(t, test.want, err.Error(), "New validation error")
		})
	}

	_, err := New(Dependencies{})
	require.Error(t, err, "New with every dependency missing")
	require.Equal(t, "store is required", err.Error(), "New with every dependency missing")
	deps := valid
	deps.Cache = nil
	_, err = New(deps)
	require.Error(t, err, "New with cache and mailer missing")
	require.Equal(t, "cache is required", err.Error(), "New with cache and mailer missing")
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
	require.NoError(t, err)
	require.Nil(t, constructed.runtime, "constructed Jobs = %#v, want empty lifecycle-only result", constructed)
	require.Nil(t, constructed.operations, "constructed Jobs = %#v, want empty lifecycle-only result", constructed)
}

type constructionCatalogWithJobs struct {
	store.Catalog
	jobs                 store.JobStore
	users                store.UserStore
	files                store.FileStore
	institutions         store.InstitutionStore
	desktop              store.BrowserAuthenticationStore
	externalLoginStates  store.ExternalLoginStateStore
	invitations          store.InvitationStore
	onboardingImports    store.OnboardingImportStore
	personalAccessTokens store.PersonalAccessTokenStore
	examSittings         store.ExamSittingStore
}

func (c constructionCatalogWithJobs) Job() store.JobStore       { return c.jobs }
func (constructionCatalogWithJobs) Mail() store.MailStore       { return nil }
func (constructionCatalogWithJobs) Audit() store.AuditStore     { return nil }
func (constructionCatalogWithJobs) Session() store.SessionStore { return nil }
func (constructionCatalogWithJobs) SessionCredential() store.SessionCredentialStore {
	return nil
}
func (constructionCatalogWithJobs) Retention() store.RetentionStore   { return nil }
func (constructionCatalogWithJobs) ExamExport() store.ExamExportStore { return nil }
func (c constructionCatalogWithJobs) User() store.UserStore           { return c.users }
func (c constructionCatalogWithJobs) File() store.FileStore           { return c.files }
func (constructionCatalogWithJobs) ExamStarterWorkspace() store.ExamStarterWorkspaceStore {
	return nil
}
func (constructionCatalogWithJobs) ExamAttemptWorkspace() store.ExamAttemptWorkspaceStore { return nil }
func (c constructionCatalogWithJobs) Institution() store.InstitutionStore {
	return c.institutions
}
func (c constructionCatalogWithJobs) BrowserAuthentication() store.BrowserAuthenticationStore {
	return c.desktop
}
func (c constructionCatalogWithJobs) ExternalLoginState() store.ExternalLoginStateStore {
	return c.externalLoginStates
}
func (c constructionCatalogWithJobs) Invitation() store.InvitationStore {
	return c.invitations
}
func (c constructionCatalogWithJobs) OnboardingImport() store.OnboardingImportStore {
	return c.onboardingImports
}
func (c constructionCatalogWithJobs) PersonalAccessToken() store.PersonalAccessTokenStore {
	return c.personalAccessTokens
}
func (c constructionCatalogWithJobs) ExamSitting() store.ExamSittingStore {
	return c.examSittings
}
func (c constructionCatalogWithJobs) CommandOutcome() store.CommandOutcomeStore {
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
type constructionBrowserAuthenticationStoreStub struct {
	store.BrowserAuthenticationStore
}
type constructionExternalLoginStateStoreStub struct{ store.ExternalLoginStateStore }
type constructionCommandOutcomeStoreStub struct{}

func (constructionCommandOutcomeStoreStub) DeleteExpired(context.Context, int) (int64, error) {
	return 0, nil
}

func (constructionCommandOutcomeStoreStub) Has(context.Context, *store.CommandIdempotency) (bool, error) {
	return false, nil
}

func TestJobRecipeConnectsRuntimeOperationsAndProfileWake(t *testing.T) {
	t.Parallel()

	profiles := profileFileConstruction{profilePictures: &profilePictureService{reads: &profilePictureReadService{}}}
	jobs, err := constructJobs(
		Dependencies{
			Store: constructionCatalogWithJobs{
				jobs: constructionJobStoreStub{}, users: constructionUserStoreStub{},
				files: constructionFileStoreStub{}, institutions: constructionInstitutionStoreStub{},
				desktop: constructionBrowserAuthenticationStoreStub{}, externalLoginStates: constructionExternalLoginStateStoreStub{}, invitations: constructionInvitationStoreStub{},
				onboardingImports:    constructionOnboardingImportStoreStub{},
				personalAccessTokens: constructionPersonalAccessTokenStoreStub{},
				examSittings:         constructionExamSittingStoreStub{},
			},
			NodeID: "node-a", RecoveryDiagnostics: constructionRecoveryDiagnosticsStub{},
		},
		applicationFoundation{},
		accessAcademicConstruction{},
		identityConstruction{},
		examinationConstruction{sittings: &sitting.Service{}, attempts: &attempt.Service{}},
		profiles,
	)
	require.NoError(t, err)
	require.NotNil(t, jobs.runtime, "constructed Jobs = %#v, want runtime and operations", jobs)
	require.NotNil(t, jobs.operations, "constructed Jobs = %#v, want runtime and operations", jobs)
	_, ok := profiles.profilePictures.reads.defaultJobs.(*jobhandler.DefaultProfilePictureJobs)
	require.True(t, ok, "profile default Jobs = %#v, want attached proposer with wake backlink", profiles.profilePictures.reads.defaultJobs)
	application := assembleApplication(
		Dependencies{}, applicationFoundation{}, identityConstruction{}, accessAcademicConstruction{},
		examinationConstruction{}, profiles, jobs, administrationConstruction{},
	)
	require.Same(t, jobs.runtime, application.jobs, "assembled application did not retain the constructed Job runtime and operations")
	require.Same(t, jobs.operations, application.jobOperationsService, "assembled application did not retain the constructed Job runtime and operations")
}

func TestApplicationJobDefinitionsIncludeSittingLifecycleAndDailyRecovery(t *testing.T) {
	t.Parallel()
	profiles := profileFileConstruction{profilePictures: &profilePictureService{reads: &profilePictureReadService{}}}
	definitions := buildApplicationJobDefinitions(
		Dependencies{
			Store: constructionCatalogWithJobs{
				jobs: constructionJobStoreStub{}, users: constructionUserStoreStub{},
				files: constructionFileStoreStub{}, institutions: constructionInstitutionStoreStub{},
				desktop: constructionBrowserAuthenticationStoreStub{}, externalLoginStates: constructionExternalLoginStateStoreStub{}, invitations: constructionInvitationStoreStub{},
				onboardingImports: constructionOnboardingImportStoreStub{}, personalAccessTokens: constructionPersonalAccessTokenStoreStub{},
			},
			FileContent: constructionFileContentStub{},
		},
		applicationFoundation{},
		identityConstruction{onboardingImports: &onboardingImportService{}},
		examinationConstruction{sittings: &sitting.Service{}, attempts: &attempt.Service{}}, profiles,
		jobhandler.NewDefaultProfilePictureJobs(jobhandler.DefaultProfilePictureJobsDependencies{Jobs: constructionJobStoreStub{}}),
		newMailHealth(false),
	)
	_, err := job.NewRegistry(definitions.descriptors)
	require.NoError(t, err)
	descriptors := make(map[model.JobType]job.Descriptor, len(definitions.descriptors))
	for _, descriptor := range definitions.descriptors {
		descriptors[descriptor.Type] = descriptor
	}
	lifecycle, exists := descriptors[model.JobTypeExamSittingLifecycle]
	require.True(t, exists, "Exam Sitting lifecycle descriptor is absent from the application Job graph")
	require.NotNil(t, lifecycle.Handler)
	sealing, exists := descriptors[model.JobTypeExamSittingSealing]
	require.True(t, exists, "Exam Sitting sealing descriptor is absent from the application Job graph")
	require.NotNil(t, sealing.Handler)
	recovery, exists := descriptors[model.JobTypeExamSittingLifecycleRecovery]
	require.True(t, exists, "Exam Sitting lifecycle recovery descriptor is absent from the application Job graph")
	require.NotNil(t, recovery.Handler)
	invitationMaintenance, exists := descriptors[model.JobTypeInvitationMaintenance]
	require.True(t, exists, "Invitation maintenance descriptor is absent from the application Job graph")
	require.NotNil(t, invitationMaintenance.Handler)
	descriptor, ok := descriptors[model.JobTypeOnboardingImportParse]
	require.True(t, ok)
	require.True(t, descriptor.Cancelable)
	descriptor, ok = descriptors[model.JobTypeOnboardingImportExecute]
	require.True(t, ok)
	require.True(t, descriptor.Cancelable)
	require.True(t, descriptor.Checkpoints)
	descriptor, ok = descriptors[model.JobTypeStudentProgressionPreview]
	require.True(t, ok)
	require.True(t, descriptor.Cancelable)

	recurrenceCount := 0
	invitationMaintenanceRecurrenceCount := 0
	for _, recurrence := range definitions.recurrences {
		if recurrence.Name == "invitation-maintenance" {
			invitationMaintenanceRecurrenceCount++
			require.NotNil(t, recurrence.Proposer)
		}
		if recurrence.Name != "exam-sitting-lifecycle-recovery" {
			continue
		}
		recurrenceCount++
		require.NotNil(t, recurrence.Proposer)
	}
	require.Equal(t, 1, recurrenceCount, "lifecycle recovery recurrence count")
	require.Equal(t, 1, invitationMaintenanceRecurrenceCount, "Invitation maintenance recurrence count")
	periodicByName := make(map[string]job.PeriodicTask, len(definitions.periodicTasks))
	for _, task := range definitions.periodicTasks {
		periodicByName[task.Name] = task
	}
	periodic, exists := periodicByName[examAttemptExpiryPeriodicTaskName]
	require.True(t, exists, "Attempt expiry periodic task is absent")
	require.Equal(t, examAttemptExpiryPeriodicTaskName, periodic.Name)
	require.Equal(t, examAttemptExpiryScanInterval, periodic.Interval)
	runner, ok := periodic.Runner.(examAttemptExpiryPeriodicRunner)
	require.True(t, ok)
	require.NotNil(t, runner.attempts)
	browserPeriodic, exists := periodicByName["browser-authentication-maintenance"]
	require.True(t, exists)
	require.Equal(t, browserAuthenticationMaintenanceInterval, browserPeriodic.Interval)
	browserRunner, ok := browserPeriodic.Runner.(browserAuthenticationMaintenancePeriodicRunner)
	require.True(t, ok, "Browser authentication maintenance runner = %#v", browserPeriodic.Runner)
	require.NotNil(t, browserRunner.transactions)
	externalPeriodic, exists := periodicByName["external-authentication-maintenance"]
	require.True(t, exists)
	require.Equal(t, externalAuthenticationMaintenanceInterval, externalPeriodic.Interval)
	externalRunner, ok := externalPeriodic.Runner.(externalAuthenticationMaintenancePeriodicRunner)
	require.True(t, ok, "External authentication maintenance runner = %#v", externalPeriodic.Runner)
	require.NotNil(t, externalRunner.states)
	personalAccessTokenPeriodic, exists := periodicByName["personal-access-token-maintenance"]
	require.True(t, exists)
	require.Equal(t, personalAccessTokenMaintenanceInterval, personalAccessTokenPeriodic.Interval)
	personalAccessTokenRunner, ok := personalAccessTokenPeriodic.Runner.(personalAccessTokenMaintenancePeriodicRunner)
	require.True(t, ok, "Personal access token maintenance runner = %#v", personalAccessTokenPeriodic.Runner)
	require.NotNil(t, personalAccessTokenRunner.tokens)
	_, exists = descriptors[model.JobType("authentication.desktop_authorization_maintenance")]
	require.False(t, exists, "Browser authentication maintenance must not create a durable Job descriptor")
	_, exists = descriptors[model.JobType("personal_access_token.mutation_maintenance")]
	require.False(t, exists, "Personal access token maintenance must not create a durable Job descriptor")

	cleanup, exists := descriptors[model.JobTypeCleanup]
	require.True(t, exists, "Job cleanup descriptor is absent")
	require.NotNil(t, cleanup.Handler)
}

func TestApplicationAssemblyNamesEveryAppField(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("app.go")
	require.NoError(t, err)
	parsedApp, err := parser.ParseFile(token.NewFileSet(), "app.go", source, 0)
	require.NoError(t, err)
	want := structFieldNames(t, parsedApp, "App")

	source, err = os.ReadFile("construction.go")
	require.NoError(t, err)
	parsedConstruction, err := parser.ParseFile(token.NewFileSet(), "construction.go", source, 0)
	require.NoError(t, err)
	got := compositeLiteralFieldNames(t, parsedConstruction, "App")
	require.Len(t, got, len(want), "assembled App fields")
	for index := range want {
		require.Equal(t, want[index], got[index], "assembled App fields = %v, want %v", got, want)
	}
}

// embeddedFieldName returns the implicit field name of an embedded field,
// which Go derives from the unqualified type name.
func embeddedFieldName(t *testing.T, expression ast.Expr) string {
	t.Helper()
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	switch typeName := expression.(type) {
	case *ast.Ident:
		return typeName.Name
	case *ast.SelectorExpr:
		return typeName.Sel.Name
	default:
		require.FailNow(t, fmt.Sprintf("unsupported embedded field type %T", expression))
		return ""
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
		require.True(t, ok, "%s is not a struct", typeName)
		for _, field := range definition.Fields.List {
			if len(field.Names) == 0 {
				names = append(names, embeddedFieldName(t, field.Type))
				continue
			}
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
			require.True(t, ok, "%s assembly uses an unkeyed field", typeName)
			key, ok := keyed.Key.(*ast.Ident)
			require.True(t, ok, "%s assembly has a non-identifier field", typeName)
			names = append(names, key.Name)
		}
		return false
	})
	sort.Strings(names)
	return names
}
