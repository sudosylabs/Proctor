//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/store/storetest"
)

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
