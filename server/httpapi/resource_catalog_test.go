// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type recordingOnboardingImportApplication struct {
	getID string
}

func (*recordingOnboardingImportApplication) UploadOnboardingImport(context.Context, application.Invocation, application.UploadOnboardingImportCommand) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, nil
}

func (candidate *recordingOnboardingImportApplication) GetOnboardingImport(_ context.Context, _ application.Invocation, id string) (application.OnboardingImportView, []application.OnboardingImportRowView, error) {
	candidate.getID = id
	return application.OnboardingImportView{}, nil, nil
}

func (*recordingOnboardingImportApplication) CommitOnboardingImport(context.Context, application.Invocation, application.CommitOnboardingImportCommand) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, nil
}

func (*recordingOnboardingImportApplication) CancelOnboardingImport(context.Context, application.Invocation, string) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, nil
}

func (*recordingOnboardingImportApplication) OnboardingImportReport(context.Context, application.Invocation, string, io.Writer) error {
	return nil
}

func TestResolveResourceApplicationsRequiresCompleteGraph(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message string
		remove  func(*Options)
	}{
		{"application", "application is required", func(options *Options) { options.Application = nil }},
		{"academic units", "academic unit reads are required", func(options *Options) { options.AcademicUnits = nil }},
		{"institutions", "institution application is required", func(options *Options) { options.Institutions = nil }},
		{"programmes", "programme application is required", func(options *Options) { options.Programmes = nil }},
		{"programme levels", "programme level application is required", func(options *Options) { options.ProgrammeLevels = nil }},
		{"academic periods", "academic period application is required", func(options *Options) { options.AcademicPeriods = nil }},
		{"classes", "class application is required", func(options *Options) { options.Classes = nil }},
		{"affiliations", "affiliation application is required", func(options *Options) { options.Affiliations = nil }},
		{"academic unit members", "academic unit member application is required", func(options *Options) { options.AcademicUnitMembers = nil }},
		{"class members", "class member application is required", func(options *Options) { options.ClassMembers = nil }},
		{"invitations", "invitation application is required", func(options *Options) { options.Invitations = nil }},
		{"browser invitations", "browser invitation application is required", func(options *Options) { options.BrowserInvitations = nil }},
		{"onboarding imports", "onboarding import application is required", func(options *Options) { options.OnboardingImports = nil }},
		{"student progressions", "student progression application is required", func(options *Options) { options.StudentProgressions = nil }},
		{"academic administration batches", "academic administration batch application is required", func(options *Options) { options.AcademicAdministrationBatches = nil }},
		{"user profiles", "user profile application is required", func(options *Options) { options.UserProfiles = nil }},
		{"user settings", "user settings application is required", func(options *Options) { options.UserSettings = nil }},
		{"account states", "account state application is required", func(options *Options) { options.AccountStates = nil }},
		{"session administration", "session administration application is required", func(options *Options) { options.SessionAdministrations = nil }},
		{"roles", "role application is required", func(options *Options) { options.Roles = nil }},
		{"role bindings", "role binding application is required", func(options *Options) { options.RoleBindings = nil }},
		{"audit", "audit listing application is required", func(options *Options) { options.AuditListings = nil }},
		{"bootstrap", "bootstrap application is required", func(options *Options) { options.Bootstrap = nil }},
		{"access policy", "access policy application is required", func(options *Options) { options.AccessPolicy = nil }},
		{"mail", "mail application is required", func(options *Options) { options.Mail = nil }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := validHTTPOptions(t)
			test.remove(&options)
			_, err := resolveResourceApplications(options)
			if err == nil || err.Error() != test.message {
				t.Fatalf("resolve error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestNewRequiresExplicitWebSocketTransport(t *testing.T) {
	t.Parallel()
	options := validHTTPOptions(t)
	options.WebSocket = nil
	if _, err := New(options); err == nil || err.Error() != "websocket transport is required" {
		t.Fatalf("New error = %v", err)
	}
}

func TestResolveResourceApplicationsUsesExplicitCapabilities(t *testing.T) {
	t.Parallel()
	options := validHTTPOptions(t)
	invitations := &invitationHTTPApplication{}
	browserInvitations := &browserInvitationHTTPApplication{}
	onboardingImports := unavailableOnboardingImportApplication{}
	studentProgressions := &studentProgressionApplicationFake{}
	batches := &academicAdministrationBatchHTTPApplication{}
	options.Invitations = invitations
	options.BrowserInvitations = browserInvitations
	options.OnboardingImports = onboardingImports
	options.StudentProgressions = studentProgressions
	options.AcademicAdministrationBatches = batches

	resolved, err := resolveResourceApplications(options)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.invitations != invitations || resolved.browserInvitations != browserInvitations ||
		resolved.onboardingImports != onboardingImports || resolved.studentProgressions != studentProgressions ||
		resolved.academicAdministrationBatches != batches {
		t.Fatal("resolver did not preserve explicit resource capabilities")
	}
	if resolved.authenticator != options.Application {
		t.Fatal("resolver did not project the configured Application authenticator")
	}
}

func TestNewInvokesEveryExplicitFormerFallbackCapability(t *testing.T) {
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now().UTC(), MFACompletedAt: model.OptionalTimeFrom(time.Now().UTC()), ClientType: model.SessionClientWeb,
	}

	tests := []struct {
		name      string
		configure func(*Options) func(*testing.T)
		request   func() *http.Request
	}{
		{
			name: "access policy",
			configure: func(options *Options) func(*testing.T) {
				fake := &replayingAccessPolicyHTTPApplication{}
				options.AccessPolicy = fake
				options.Application.(*testHTTPApplication).principal = &principal
				return func(t *testing.T) {
					if fake.discoveries != 1 {
						t.Fatalf("DiscoverAccess calls = %d; want 1", fake.discoveries)
					}
				}
			},
			request: func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/v1/discovery", nil) },
		},
		{
			name: "invitations",
			configure: func(options *Options) func(*testing.T) {
				fake := &invitationHTTPApplication{}
				options.Invitations = fake
				options.Application.(*testHTTPApplication).principal = &principal
				return func(t *testing.T) {
					if fake.list.Limit == 0 {
						t.Fatal("ListInvitations was not invoked")
					}
				}
			},
			request: func() *http.Request { return authenticatedCatalogRequest(http.MethodGet, "/api/v1/invitations") },
		},
		{
			name: "browser invitations",
			configure: func(options *Options) func(*testing.T) {
				fake := &browserInvitationHTTPApplication{}
				options.BrowserInvitations = fake
				options.Application.(*testHTTPApplication).principal = &principal
				return func(t *testing.T) {
					if fake.start.Claim == "" {
						t.Fatal("StartBrowserInvitation was not invoked")
					}
				}
			},
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/browser/invitations", strings.NewReader(`{"claim":"`+model.NewCredentialToken()+`"}`))
				request.Header.Set("Content-Type", "application/json")
				return request
			},
		},
		{
			name: "onboarding imports",
			configure: func(options *Options) func(*testing.T) {
				fake := &recordingOnboardingImportApplication{}
				options.OnboardingImports = fake
				options.Application.(*testHTTPApplication).principal = &principal
				return func(t *testing.T) {
					if fake.getID == "" {
						t.Fatal("GetOnboardingImport was not invoked")
					}
				}
			},
			request: func() *http.Request {
				return authenticatedCatalogRequest(http.MethodGet, "/api/v1/onboarding-imports/"+model.NewOnboardingImportID().String())
			},
		},
		{
			name: "student progressions",
			configure: func(options *Options) func(*testing.T) {
				fake := &studentProgressionApplicationFake{}
				options.StudentProgressions = fake
				options.Application.(*testHTTPApplication).principal = &principal
				return func(t *testing.T) {
					if fake.getID == "" {
						t.Fatal("GetStudentProgression was not invoked")
					}
				}
			},
			request: func() *http.Request {
				return authenticatedCatalogRequest(http.MethodGet, "/api/v1/student-progressions/"+model.NewOnboardingImportID().String())
			},
		},
		{
			name: "academic administration batches",
			configure: func(options *Options) func(*testing.T) {
				fake := &academicAdministrationBatchHTTPApplication{}
				options.AcademicAdministrationBatches = fake
				options.Application.(*testHTTPApplication).principal = &principal
				return func(t *testing.T) {
					if fake.command.IdempotencyKey != "catalog-explicit-batch" {
						t.Fatalf("RunAcademicAdministrationBatch command = %#v", fake.command)
					}
				}
			},
			request: func() *http.Request {
				request := authenticatedCatalogRequest(http.MethodPost, "/api/v1/academic-administration-batches")
				request.Body = io.NopCloser(strings.NewReader(`{"operation":"class.end","scope_type":"class","scope_id":"` + model.NewClassID().String() + `","items":[]}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", "catalog-explicit-batch")
				return request
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validHTTPOptions(t)
			assertCall := test.configure(&options)
			httpAPI, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, test.request())
			if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
				t.Fatalf("route response = %d: %s", response.Code, response.Body.String())
			}
			assertCall(t)
		})
	}
}

func authenticatedCatalogRequest(method string, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer explicit-capability")
	return request
}

func TestResourceCatalogContainsNoRuntimeApplicationAssertions(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("resource_catalog.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "options.Application.(") {
		t.Fatal("resource catalog performs runtime capability discovery")
	}
}
