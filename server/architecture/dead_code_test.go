// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// testSupportPackages exist only for tests to import, so nothing in a
// production binary reaches them.
var testSupportPackages = []string{"config/configtest", "internal/errorflow", "store/storetest", "testlib"}

// reachableOnlyFromTests names every production function that no server
// binary reaches, keyed as <package relative to the module>.<[Receiver.]Name>
// ("server" is the module root). Each is reachable from tests and stays in
// production code for the stated reason; anything else unreachable is dead
// and must be deleted, or moved into a _test.go file when only its own
// package's tests use it.
var reachableOnlyFromTests = map[string]string{
	// Cross-package test seams: other packages' tests construct real values
	// through them, so they cannot move into a _test.go file.
	"server.NewForTesting":                                                "the testlib entry into the real server.New graph",
	"server.testingWebappFiles":                                           "the webapp assets NewForTesting serves",
	"config.NewMemoryStore":                                               "the in-memory configuration backing other packages' tests load",
	"config.MemoryStore.Load":                                             "MemoryStore implements the configuration backing store",
	"config.MemoryStore.Save":                                             "MemoryStore implements the configuration backing store",
	"config.MemoryStore.String":                                           "MemoryStore implements the configuration backing store",
	"config.MemoryStore.Close":                                            "MemoryStore implements the configuration backing store",
	"logging.Buffer.Write":                                                "the captured log sink other packages' tests assert through",
	"logging.Buffer.String":                                               "the captured log sink other packages' tests assert through",
	"logging.Buffer.Bytes":                                                "the captured log sink other packages' tests assert through",
	"logging.Buffer.Reset":                                                "the captured log sink other packages' tests assert through",
	"logging.Buffer.flushPending":                                         "Buffer.Write's line assembly",
	"app/jobhandler.NewMailCleanupDescriptor":                             "app tests run the real descriptor in a test engine",
	"app/jobhandler.NewMailDeliveryDescriptor":                            "app tests run the real descriptor in a test engine",
	"app/jobhandler.NewOnboardingImportExecuteDescriptor":                 "app tests run the real descriptor in a test engine",
	"app/jobhandler.NewDefaultProfilePictureDescriptor":                   "app tests run the real descriptor in a test engine",
	"filecontent.IsNotFound":                                              "the root adapter tests classify File Content failures through it",
	"filecontent.IsConflict":                                              "the root adapter tests classify File Content failures through it",
	"filecontent.IsUnavailable":                                           "the root adapter tests classify File Content failures through it",
	"filecontent.hasKind":                                                 "the File Content failure classifiers' shared check",
	"model.NewAcademicPeriod":                                             "validated fixture constructor used by app tests",
	"model.NewAcademicUnit":                                               "validated fixture constructor used by app tests",
	"model.NewClass":                                                      "validated fixture constructor used by app tests",
	"model.NewProgrammeLevel":                                             "validated fixture constructor used by app, httpapi, and sqlstore tests",
	"model.NewAttemptParticipation":                                       "validated fixture constructor used by app and realtime tests",
	"model.NewAttemptConnection":                                          "validated fixture constructor used by app, websocket, and realtime tests",
	"model.NewAttemptConfigurationManifest":                               "builds the empty Attempt Configuration manifest",
	"model.EmptyAttemptConfigurationManifest":                             "the empty Desktop manifest other packages' tests admit builds with",
	"model.CurrentAttemptConfigurationManifestFingerprint":                "the empty Desktop manifest fingerprint other packages' tests pin",
	"model.GetMillis":                                                     "the millisecond clock fixtures in every package's tests read",
	"model.GetMillisForTime":                                              "the conversion model.GetMillis applies",
	"model.AuditActions":                                                  "the audit action catalog the architecture test reads",
	"store/localcachelayer.NopInvalidationFanout.RegisterAcademicPeriod":  "the no-peer fan-out sqlstore conformance tests compose",
	"store/localcachelayer.NopInvalidationFanout.BroadcastAcademicPeriod": "the no-peer fan-out sqlstore conformance tests compose",
	"store/timerlayer.RecorderFunc.Observe":                               "adapts a test function to the timer layer's recorder",
	// Redaction methods fmt, slog, and encoding/json would call if a Settings
	// value ever reached them; they keep secret material out of any such path.
	"secretseal.Settings.String":      "redacts Settings for fmt",
	"secretseal.Settings.GoString":    "redacts Settings for fmt %#v",
	"secretseal.Settings.LogValue":    "redacts Settings for slog",
	"secretseal.Settings.MarshalJSON": "redacts Settings for encoding/json",
	"secretseal.Settings.Auditable":   "redacts Settings for audit details",
	"secretseal.redacted":             "the shared redacted Settings projection",
	// Null application objects in httpapi; the HTTP wire refactor owns them.
	"httpapi.unavailableAcademicAdministrationBatchApplication.RunAcademicAdministrationBatch": "httpapi test null object",
	"httpapi.unavailableAccessPolicyApplication.GetAccessPolicy":                               "httpapi test null object",
	"httpapi.unavailableAccessPolicyApplication.PreflightAccessPolicy":                         "httpapi test null object",
	"httpapi.unavailableAccessPolicyApplication.ReplaceAccessPolicy":                           "httpapi test null object",
	"httpapi.unavailableAccessPolicyApplication.DiscoverAccess":                                "httpapi test null object",
	"httpapi.unavailableBrowserInvitationApplication.StartBrowserInvitation":                   "httpapi test null object",
	"httpapi.unavailableBrowserInvitationApplication.AcceptBrowserInvitation":                  "httpapi test null object",
	"httpapi.unavailableBrowserInvitationApplication.AcceptBrowserInvitationWithSession":       "httpapi test null object",
	"httpapi.unavailableDesktopCompatibilityApplication.EvaluateDesktopCompatibility":          "httpapi test null object",
	"httpapi.unavailableDesktopCompatibilityApplication.GetDesktopCompatibilityPolicy":         "httpapi test null object",
	"httpapi.unavailableDesktopCompatibilityApplication.ReplaceDesktopCompatibilityPolicy":     "httpapi test null object",
	"httpapi.unavailableInvitationApplication.IssueStudentClassInvitation":                     "httpapi test null object",
	"httpapi.unavailableInvitationApplication.AcceptStudentClassInvitation":                    "httpapi test null object",
	"httpapi.unavailableInvitationApplication.IssueTeacherAcademicUnitInvitation":              "httpapi test null object",
	"httpapi.unavailableInvitationApplication.AcceptTeacherAcademicUnitInvitation":             "httpapi test null object",
	"httpapi.unavailableInvitationApplication.IssueAcademicUnitRoleInvitation":                 "httpapi test null object",
	"httpapi.unavailableInvitationApplication.AcceptAcademicUnitRoleInvitation":                "httpapi test null object",
	"httpapi.unavailableInvitationApplication.IssueInstitutionRoleInvitation":                  "httpapi test null object",
	"httpapi.unavailableInvitationApplication.AcceptInstitutionRoleInvitation":                 "httpapi test null object",
	"httpapi.unavailableInvitationApplication.ListInvitations":                                 "httpapi test null object",
	"httpapi.unavailableInvitationApplication.GetInvitation":                                   "httpapi test null object",
	"httpapi.unavailableInvitationApplication.ResendInvitation":                                "httpapi test null object",
	"httpapi.unavailableInvitationApplication.RevokeInvitation":                                "httpapi test null object",
	"httpapi.unavailableInvitationApplication.ReplaceInvitation":                               "httpapi test null object",
	"httpapi.unavailableInvitationApplication.RunInvitationBatch":                              "httpapi test null object",
	"httpapi.unavailableOnboardingImportApplication.UploadOnboardingImport":                    "httpapi test null object",
	"httpapi.unavailableOnboardingImportApplication.GetOnboardingImport":                       "httpapi test null object",
	"httpapi.unavailableOnboardingImportApplication.CommitOnboardingImport":                    "httpapi test null object",
	"httpapi.unavailableOnboardingImportApplication.CancelOnboardingImport":                    "httpapi test null object",
	"httpapi.unavailableOnboardingImportApplication.OnboardingImportReport":                    "httpapi test null object",
	"httpapi.unavailableStudentProgressionApplication.DryRunStudentProgression":                "httpapi test null object",
	"httpapi.unavailableStudentProgressionApplication.GetStudentProgression":                   "httpapi test null object",
	"httpapi.unavailableStudentProgressionApplication.CommitStudentProgression":                "httpapi test null object",
	"httpapi.unavailableStudentProgressionApplication.CancelStudentProgression":                "httpapi test null object",
	"httpapi.unavailableStudentProgressionApplication.StudentProgressionReport":                "httpapi test null object",
}

// TestProductionFunctionsAreReachable fails on a production function that no
// server binary can call. Reachability is rapid type analysis from every
// main package's main and init functions over the default build, as
// golang.org/x/tools/cmd/deadcode computes it; generated files and empty
// marker methods are not reported. Only the reasoned reachableOnlyFromTests
// entries are exempt, and an entry that is reachable again or no longer
// exists must be removed.
func TestProductionFunctionsAreReachable(t *testing.T) {
	t.Parallel()
	unreachable, err := unreachableFunctions("..", serverModule, testSupportPackages, serverModule+"/...")
	require.NoError(t, err)
	var violations []string
	found := make(map[string]bool, len(unreachable))
	for _, name := range unreachable {
		found[name] = true
		if _, allowed := reachableOnlyFromTests[name]; !allowed {
			violations = append(violations, name+" is unreachable from every server binary; delete it, or move it into a _test.go file if only its package's tests use it")
		}
	}
	for name := range reachableOnlyFromTests {
		if !found[name] {
			violations = append(violations, name+" is listed in reachableOnlyFromTests but is reachable or gone; remove the entry")
		}
	}
	sort.Strings(violations)
	require.Empty(t, violations)
}

func TestDeadCodeRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module fixture\n\ngo 1.25\n",
		"cmd/tool/main.go": `package main

import "fixture/lib"

func main() { lib.Used(); lib.Value{}.Called() }
`,
		"lib/lib.go": `package lib

type Value struct{}

func (Value) Called()   {}
func (Value) Uncalled() {}
func (Value) marker()   {}

func Used()   { helper() }
func helper() {}
func Unused() {}
`,
		"lib/support/support.go": `package support

func OnlyForTests() {}
`,
	}
	for name, contents := range files {
		require.NoError(t, os.MkdirAll(root+"/"+name[:max(strings.LastIndex(name, "/"), 0)], 0o750))
		require.NoError(t, os.WriteFile(root+"/"+name, []byte(contents), 0o600))
	}
	unreachable, err := unreachableFunctionsWithEnv(root, append(os.Environ(), "GOWORK=off"), "fixture", []string{"lib/support"}, "./...")
	require.NoError(t, err)
	require.Equal(t, []string{"lib.Unused", "lib.Value.Uncalled"}, unreachable)
}

func unreachableFunctions(directory, module string, excluded []string, patterns ...string) ([]string, error) {
	return unreachableFunctionsWithEnv(directory, nil, module, excluded, patterns...)
}

// unreachableFunctionsWithEnv loads patterns with complete syntax, builds SSA
// for the whole program, and returns the named functions and methods of
// module that rapid type analysis cannot reach from any main package.
func unreachableFunctionsWithEnv(directory string, env []string, module string, excluded []string, patterns ...string) ([]string, error) {
	loaded, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: directory, Env: env}, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load %v: %w", patterns, err)
	}
	if packages.PrintErrors(loaded) > 0 {
		return nil, fmt.Errorf("load %v: packages contain errors", patterns)
	}
	program, ssaPackages := ssautil.AllPackages(loaded, ssa.InstantiateGenerics)
	program.Build()
	var roots []*ssa.Function
	for _, pkg := range ssaPackages {
		if pkg != nil && pkg.Pkg.Name() == "main" {
			if main := pkg.Func("main"); main != nil {
				roots = append(roots, main)
			}
			if initializer := pkg.Func("init"); initializer != nil {
				roots = append(roots, initializer)
			}
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("load %v: no main packages", patterns)
	}
	// A generic function's instances share its declaration's position, so a
	// declaration is reachable when any function at its position is.
	reachable := make(map[token.Position]bool)
	for function := range rta.Analyze(roots, false).Reachable {
		reachable[program.Fset.Position(function.Pos())] = true
	}
	var unreachable []string
	for _, pkg := range loaded {
		relative, ok := moduleRelativePackage(pkg.PkgPath, module)
		if !ok || isExcludedPackage(relative, excluded) {
			continue
		}
		for _, file := range pkg.Syntax {
			// A generator emits a complete family (every typed ID's
			// constructor, say), so unused members of generated code are not
			// dead declarations anyone wrote.
			if ast.IsGenerated(file) {
				continue
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Name.Name == "init" || pkg.Name == "main" && function.Name.Name == "main" ||
					isMarkerMethod(function) || reachable[program.Fset.Position(function.Name.Pos())] {
					continue
				}
				name := function.Name.Name
				if object, ok := pkg.TypesInfo.Defs[function.Name].(*types.Func); ok {
					if receiver := object.Type().(*types.Signature).Recv(); receiver != nil {
						name = ssaReceiverName(receiver.Type()) + "." + name
					}
				}
				unreachable = append(unreachable, relative+"."+name)
			}
		}
	}
	sort.Strings(unreachable)
	return unreachable, nil
}

// isMarkerMethod reports an unexported, empty method without parameters or
// results, which exists only to make a type satisfy a closed interface.
func isMarkerMethod(function *ast.FuncDecl) bool {
	return function.Recv != nil && !ast.IsExported(function.Name.Name) && function.Body != nil &&
		len(function.Body.List) == 0 && function.Type.Params.NumFields() == 0 && function.Type.Results.NumFields() == 0
}

func moduleRelativePackage(path, module string) (string, bool) {
	if path == module {
		return module[strings.LastIndex(module, "/")+1:], true
	}
	relative, ok := strings.CutPrefix(path, module+"/")
	return relative, ok
}

func isExcludedPackage(relative string, excluded []string) bool {
	for _, prefix := range excluded {
		if relative == prefix || strings.HasPrefix(relative, prefix+"/") {
			return true
		}
	}
	return false
}

func ssaReceiverName(receiver interface{ String() string }) string {
	name := receiver.String()
	name = strings.TrimPrefix(name, "*")
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	if index := strings.Index(name, "["); index >= 0 {
		name = name[:index]
	}
	return name
}
