// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type testHTTPApplication struct {
	Application
	principal *model.Principal
}

func (application *testHTTPApplication) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	return application.principal, nil
}

func (application *testHTTPApplication) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	return application.principal, nil
}

type testHTTPHealth struct{}

func (testHTTPHealth) Live() bool  { return true }
func (testHTTPHealth) Ready() bool { return true }

type testWebSocketTransport struct{}

func (testWebSocketTransport) Accept(http.ResponseWriter, *http.Request, model.Principal, model.RequestMetadata, string, int64, bool) error {
	return nil
}

func validHTTPOptions(t *testing.T) Options {
	t.Helper()
	logger, _ := newTestLogger(t)
	application := &testHTTPApplication{}
	return Options{
		Logger: logger, Localizer: newTestLocalizer(t), Health: testHTTPHealth{}, Application: application,
		AcademicUnits: &academicUnitHTTPApplication{}, Institutions: &institutionHTTPApplication{},
		Programmes: &programmeHTTPApplication{}, ProgrammeLevels: &programmeLevelHTTPApplication{},
		AcademicPeriods: &academicPeriodHTTPApplication{}, Classes: &classHTTPApplication{},
		Affiliations: &affiliationHTTPApplication{}, AcademicUnitMembers: &academicUnitMemberHTTPApplication{},
		ClassMembers: &classMemberHTTPApplication{}, Invitations: unavailableInvitationApplication{},
		BrowserInvitations: unavailableBrowserInvitationApplication{}, OnboardingImports: unavailableOnboardingImportApplication{},
		StudentProgressions:           unavailableStudentProgressionApplication{},
		AcademicAdministrationBatches: unavailableAcademicAdministrationBatchApplication{},
		UserProfiles:                  &userProfileHTTPApplication{}, UserSettings: &userSettingsHTTPApplication{},
		AccountStates: &accountStateHTTPApplication{}, SessionAdministrations: &sessionAdministrationHTTPApplication{},
		Roles: &roleHTTPApplication{}, RoleBindings: &roleBindingHTTPApplication{},
		AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{},
		AccessPolicy: unavailableAccessPolicyApplication{}, Mail: application,
		BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065",
		MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
		WebSocket: testWebSocketTransport{},
	}
}

// agreementResourceApplications intentionally returns a zero-valued resolved
// graph. Resource constructors only declare handlers while compiling the route
// catalog; agreement tests never invoke those handlers.
func agreementResourceApplications() resourceApplications {
	return resourceApplications{}
}
