//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
	"github.com/sudosylabs/proctor/server/store/retrylayer"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

func TestLocalCacheLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	cache, err := localcachelayer.NewMemoryCache(128)
	if err != nil {
		t.Fatal(err)
	}
	cachedStore, err := localcachelayer.New(
		sqlStore,
		cache,
		localcachelayer.DefaultPolicy(),
		localcachelayer.NopRecorder{},
		localcachelayer.NopInvalidationFanout{},
	)
	if err != nil {
		t.Fatal(err)
	}
	runLayerConformance(t, sqlStore, cachedStore)
}

func TestRetryLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	retriedStore, err := retrylayer.New(sqlStore, retrylayer.DefaultPolicy(IsTransientError))
	if err != nil {
		t.Fatal(err)
	}
	runLayerConformance(t, sqlStore, retriedStore)
}

func TestTimerLayerConformance(t *testing.T) {
	sqlStore := openTestStore(t)
	timedStore, err := timerlayer.New(sqlStore, timerlayer.NopRecorder{})
	if err != nil {
		t.Fatal(err)
	}

	runLayerConformance(t, sqlStore, timedStore)
}

func runLayerConformance(t *testing.T, sqlStore *SQLStore, decorated store.Store) {
	t.Helper()
	tests := []struct {
		name string
		run  func(*testing.T, store.Store)
	}{
		{"Institution", storetest.TestInstitutionStore},
		{"AcademicUnit", storetest.TestAcademicUnitStore},
		{"Programme", storetest.TestProgrammeStore},
		{"ProgrammeLevel", storetest.TestProgrammeLevelStore},
		{"AcademicPeriod", storetest.TestAcademicPeriodStore},
		{"Class", storetest.TestClassStore},
		{"User", storetest.TestUserStore},
		{"ExternalIdentity", storetest.TestExternalIdentityStore},
		{"ExternalLoginState", storetest.TestExternalLoginStateStore},
		{"PasswordCredential", storetest.TestPasswordCredentialStore},
		{"UserToken", storetest.TestUserTokenStore},
		{"PersonalAccessToken", storetest.TestPersonalAccessTokenStore},
		{"MFA", storetest.TestMFAStore},
		{"Session", storetest.TestSessionStores},
		{"ClusterDiscovery", storetest.TestClusterDiscoveryStore},
		{"Affiliation", storetest.TestAffiliationStore},
		{"AcademicUnitMember", storetest.TestAcademicUnitMemberStore},
		{"ClassMember", storetest.TestClassMemberStore},
		{"Role", storetest.TestRoleStore},
		{"RoleBinding", storetest.TestRoleBindingStore},
		{"Audit", storetest.TestAuditStore},
		{"Installation", storetest.TestInstallationStore},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetTestStore(t, sqlStore)
			test.run(t, decorated)
		})
	}
}

func TestInstitutionStore(t *testing.T) {
	StoreTest(t, storetest.TestInstitutionStore)
}

func TestAcademicUnitStore(t *testing.T) {
	StoreTest(t, storetest.TestAcademicUnitStore)
}

func TestProgrammeStore(t *testing.T) {
	StoreTest(t, storetest.TestProgrammeStore)
}

func TestProgrammeLevelStore(t *testing.T) {
	StoreTest(t, storetest.TestProgrammeLevelStore)
}

func TestAcademicPeriodStore(t *testing.T) {
	StoreTest(t, storetest.TestAcademicPeriodStore)
}

func TestClassStore(t *testing.T) {
	StoreTest(t, storetest.TestClassStore)
}

func TestUserStore(t *testing.T) {
	StoreTest(t, storetest.TestUserStore)
}

func TestFileStore(t *testing.T) {
	StoreTest(t, storetest.TestFileStore)
}

func TestExternalIdentityStore(t *testing.T) {
	StoreTest(t, storetest.TestExternalIdentityStore)
}

func TestExternalLoginStateStore(t *testing.T) {
	StoreTest(t, storetest.TestExternalLoginStateStore)
}

func TestPasswordCredentialStore(t *testing.T) {
	StoreTest(t, storetest.TestPasswordCredentialStore)
}

func TestUserTokenStore(t *testing.T) {
	StoreTest(t, storetest.TestUserTokenStore)
}

func TestPersonalAccessTokenStore(t *testing.T) {
	StoreTest(t, storetest.TestPersonalAccessTokenStore)
}

func TestMFAStore(t *testing.T) {
	StoreTest(t, storetest.TestMFAStore)
}

func TestSessionStores(t *testing.T) {
	StoreTest(t, storetest.TestSessionStores)
}

func TestClusterDiscoveryStore(t *testing.T) {
	StoreTest(t, storetest.TestClusterDiscoveryStore)
}

func TestAffiliationStore(t *testing.T) {
	StoreTest(t, storetest.TestAffiliationStore)
}

func TestAcademicUnitMemberStore(t *testing.T) {
	StoreTest(t, storetest.TestAcademicUnitMemberStore)
}

func TestClassMemberStore(t *testing.T) {
	StoreTest(t, storetest.TestClassMemberStore)
}

func TestRoleStore(t *testing.T) {
	StoreTest(t, storetest.TestRoleStore)
}

func TestRoleBindingStore(t *testing.T) {
	StoreTest(t, storetest.TestRoleBindingStore)
}

func TestAuditStore(t *testing.T) {
	StoreTest(t, storetest.TestAuditStore)
}

func TestInstallationStore(t *testing.T) {
	StoreTest(t, storetest.TestInstallationStore)
}
