// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/storetest/user_store.go.

package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type UserStoreSQLProbe struct {
	SetPublicRegistration func(*testing.T, bool, bool)
	DatabaseNow           func(*testing.T) time.Time
}

func TestUserStore(t *testing.T, ss store.Store, probes ...UserStoreSQLProbe) {
	t.Run("CreateAndGet", func(t *testing.T) { testUserStoreCreateAndGet(t, ss) })
	t.Run("CreationAndDefaultJobAreAtomic", func(t *testing.T) { testUserCreationAndDefaultJobAreAtomic(t, ss) })
	if len(probes) > 0 && probes[0].SetPublicRegistration != nil {
		t.Run("PublicRegistrationIsAtomicPolicyFencedAndConcurrent", func(t *testing.T) {
			testPublicLocalUserRegistration(t, ss, probes[0])
		})
	}
	t.Run("NormalizedLookups", func(t *testing.T) { testUserStoreNormalizedLookups(t, ss) })
	t.Run("Update", func(t *testing.T) { testUserStoreUpdate(t, ss) })
	t.Run("UpdateLastLogin", func(t *testing.T) { testUserStoreUpdateLastLogin(t, ss) })
	t.Run("Uniqueness", func(t *testing.T) { testUserStoreUniqueness(t, ss) })
	t.Run("ListAndDisable", func(t *testing.T) { testUserStoreListAndDisable(t, ss) })
	t.Run("EnablementRevocationAndAuditAreAtomic", func(t *testing.T) {
		testUserStoreEnablementRevocationAndAuditAreAtomic(t, ss)
	})
	t.Run("DisabledMailRecordsTerminalAccountNotice", func(t *testing.T) {
		testUserStoreDisabledMailRecordsTerminalAccountNotice(t, ss)
	})
	t.Run("ProtectLastAdministrator", func(t *testing.T) {
		testUserStoreProtectLastAdministrator(t, ss)
	})
}

func testPublicLocalUserRegistration(t *testing.T, ss store.Store, probe UserStoreSQLProbe) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	probe.SetPublicRegistration(t, false, true)
	disabled := publicLocalUserRegistrationFixture(t, institution.ID, newUser())
	if result, err := ss.User().RegisterLocal(ctx, disabled); result != nil || !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("disabled public registration = %#v, %v", result, err)
	}
	if _, err := ss.User().Get(ctx, disabled.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("disabled registration persisted User: %v", err)
	}

	localDisabled := publicLocalUserRegistrationFixture(t, institution.ID, newUser())
	probe.SetPublicRegistration(t, false, false)
	if result, err := ss.User().RegisterLocal(ctx, localDisabled); result != nil || !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
		t.Fatalf("disabled local enrollment = %#v, %v", result, err)
	}
	if _, err := ss.User().Get(ctx, localDisabled.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("disabled local enrollment persisted User: %v", err)
	}

	probe.SetPublicRegistration(t, true, true)
	accepted := publicLocalUserRegistrationFixture(t, institution.ID, newUser())
	result, err := ss.User().RegisterLocal(ctx, accepted)
	requireNoError(t, err)
	if result.User.ID != accepted.User.ID || result.Token.ID != accepted.VerificationToken.ID || result.User.EmailVerified {
		t.Fatalf("public registration result = %#v", result)
	}
	if _, err = ss.PasswordCredential().GetByUser(ctx, accepted.User.ID.String()); err != nil {
		t.Fatalf("registration password = %v", err)
	}
	if _, err = ss.UserSettings().Get(ctx, accepted.User.ID); err != nil {
		t.Fatalf("registration settings = %v", err)
	}
	if _, err = ss.UserToken().GetByHash(ctx, accepted.VerificationToken.TokenHash, model.UserTokenEmailVerification); err != nil {
		t.Fatalf("registration token = %v", err)
	}
	if _, err = ss.Mail().GetDelivery(ctx, accepted.VerificationDelivery.ID); err != nil {
		t.Fatalf("registration delivery = %v", err)
	}
	if _, err = ss.Job().Get(ctx, accepted.DefaultProfilePictureJob.ID); err != nil {
		t.Fatalf("registration profile Job = %v", err)
	}
	if _, err = ss.Job().Get(ctx, accepted.VerificationJob.ID); err != nil {
		t.Fatalf("registration delivery Job = %v", err)
	}

	if probe.DatabaseNow != nil {
		databaseNow := probe.DatabaseNow(t)
		behindNodeAt := databaseNow.Add(-2 * time.Hour)
		skewed := publicLocalUserRegistrationFixtureAt(t, institution.ID, newUser(), behindNodeAt)
		originalUserAt, originalTokenExpiry := skewed.User.CreatedAt, skewed.VerificationToken.ExpiresAt
		skewedResult, registerErr := ss.User().RegisterLocal(ctx, skewed)
		requireNoError(t, registerErr)
		if skewed.User.CreatedAt != originalUserAt || skewed.VerificationToken.ExpiresAt != originalTokenExpiry {
			t.Fatal("RegisterLocal mutated caller-owned candidates while rebasing timestamps")
		}
		if skewedResult.User.CreatedAt.Before(databaseNow) || skewedResult.Token.CreatedAt.Before(databaseNow) ||
			!skewedResult.Token.ExpiresAt.Equal(skewedResult.Token.CreatedAt.Add(skewed.TokenLifetime)) {
			t.Fatalf("registration did not use PostgreSQL time: user=%s token=%s..%s database=%s", skewedResult.User.CreatedAt, skewedResult.Token.CreatedAt, skewedResult.Token.ExpiresAt, databaseNow)
		}
		storedSettings, settingsErr := ss.UserSettings().Get(ctx, skewed.User.ID)
		requireNoError(t, settingsErr)
		storedCredential, credentialErr := ss.PasswordCredential().GetByUser(ctx, skewed.User.ID.String())
		requireNoError(t, credentialErr)
		storedDelivery, deliveryErr := ss.Mail().GetDelivery(ctx, skewed.VerificationDelivery.ID)
		requireNoError(t, deliveryErr)
		storedDefaultJob, defaultJobErr := ss.Job().Get(ctx, skewed.DefaultProfilePictureJob.ID)
		requireNoError(t, defaultJobErr)
		storedDeliveryJob, deliveryJobErr := ss.Job().Get(ctx, skewed.VerificationJob.ID)
		requireNoError(t, deliveryJobErr)
		storedAudit := skewedResult.AuditEvent
		if storedAudit == nil {
			t.Fatal("registration result omitted its committed audit")
		}
		for field, timestamp := range map[string]time.Time{
			"settings.created_at":     storedSettings.CreatedAt,
			"password.created_at":     storedCredential.CreatedAt,
			"password.changed_at":     storedCredential.PasswordChangedAt,
			"delivery.created_at":     storedDelivery.CreatedAt,
			"delivery.message_date":   storedDelivery.MessageDate,
			"profile_job.created_at":  storedDefaultJob.CreatedAt,
			"delivery_job.created_at": storedDeliveryJob.CreatedAt,
			"audit.created_at":        storedAudit.CreatedAt,
			"audit.updated_at":        storedAudit.UpdatedAt,
		} {
			if !timestamp.Equal(skewedResult.User.CreatedAt) {
				t.Fatalf("%s=%s, transaction time=%s", field, timestamp, skewedResult.User.CreatedAt)
			}
		}
		if !storedDelivery.Deadline.Equal(storedDelivery.CreatedAt.Add(skewed.MailLifetime)) {
			t.Fatalf("delivery deadline=%s, created=%s lifetime=%s", storedDelivery.Deadline, storedDelivery.CreatedAt, skewed.MailLifetime)
		}

		aheadNodeAt := databaseNow.Add(2 * time.Hour)
		ahead := publicLocalUserRegistrationFixtureAt(t, institution.ID, newUser(), aheadNodeAt)
		originalAheadAt := ahead.User.CreatedAt
		aheadResult, registerAheadErr := ss.User().RegisterLocal(ctx, ahead)
		requireNoError(t, registerAheadErr)
		if ahead.User.CreatedAt != originalAheadAt {
			t.Fatal("RegisterLocal mutated the ahead-node candidate while rebasing timestamps")
		}
		if aheadResult.User.CreatedAt.Before(databaseNow) || !aheadResult.User.CreatedAt.Before(aheadNodeAt) ||
			!aheadResult.Token.ExpiresAt.Equal(aheadResult.Token.CreatedAt.Add(ahead.TokenLifetime)) {
			t.Fatalf("ahead-node registration did not use PostgreSQL time: user=%s token=%s..%s database=%s node=%s", aheadResult.User.CreatedAt, aheadResult.Token.CreatedAt, aheadResult.Token.ExpiresAt, databaseNow, aheadNodeAt)
		}
	}

	rollback := publicLocalUserRegistrationFixture(t, institution.ID, newUser())
	queued, _, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: rollback.VerificationJob})
	requireNoError(t, err)
	if queued.ID != rollback.VerificationJob.ID {
		t.Fatalf("collision Job = %#v", queued)
	}
	if _, err = ss.User().RegisterLocal(ctx, rollback); err == nil {
		t.Fatal("registration accepted colliding delivery Job")
	}
	if _, err = ss.User().Get(ctx, rollback.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("failed registration persisted User: %v", err)
	}
	if _, err = ss.UserToken().GetByHash(ctx, rollback.VerificationToken.TokenHash, model.UserTokenEmailVerification); !store.IsNotFound(err) {
		t.Fatalf("failed registration persisted token: %v", err)
	}
	if _, err = ss.Mail().GetDelivery(ctx, rollback.VerificationDelivery.ID); !store.IsNotFound(err) {
		t.Fatalf("failed registration persisted delivery: %v", err)
	}

	sharedEmail := model.NewId() + "@public-registration.example.edu"
	contenders := []*store.PublicLocalUserRegistration{
		publicLocalUserRegistrationFixture(t, institution.ID, &model.User{Username: "registration-a-" + model.NewId(), Email: sharedEmail}),
		publicLocalUserRegistrationFixture(t, institution.ID, &model.User{Username: "registration-b-" + model.NewId(), Email: sharedEmail}),
	}
	start := make(chan struct{})
	errs := make(chan error, len(contenders))
	var wait sync.WaitGroup
	for _, contender := range contenders {
		contender := contender
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, registerErr := ss.User().RegisterLocal(ctx, contender)
			errs <- registerErr
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for registerErr := range errs {
		switch {
		case registerErr == nil:
			successes++
		case store.IsConflict(registerErr):
			conflicts++
		default:
			t.Fatalf("concurrent registration error = %v", registerErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent registration successes=%d conflicts=%d", successes, conflicts)
	}
}

func publicLocalUserRegistrationFixture(t *testing.T, institutionID model.InstitutionID, candidate *model.User) *store.PublicLocalUserRegistration {
	return publicLocalUserRegistrationFixtureAt(t, institutionID, candidate, time.Now())
}

func publicLocalUserRegistrationFixtureAt(t *testing.T, institutionID model.InstitutionID, candidate *model.User, candidateAt time.Time) *store.PublicLocalUserRegistration {
	t.Helper()
	user := *candidate
	if user.ID.IsZero() {
		user.PrepareCreate(model.NewUserID(), candidateAt)
	}
	creation := testUserCreation(&user, &model.PasswordCredential{PasswordHash: "encoded-registration-password"})
	at := creation.User.CreatedAt
	token := &model.UserToken{UserID: creation.User.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: creation.User.Email, ExpiresAt: at.Add(time.Hour)}
	token.PrepareCreate(model.NewUserTokenID(), at)
	occurrence, delivery, job := userTokenMailFixture(t, creation.User.ID, model.MailOccurrenceID(token.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityVerifyEmail, model.JobTypeMailDeliverCredential,
		at, token.ExpiresAt)
	audit := userTokenAudit("authentication.public_registration", creation.User.ID.String(), institutionID.String())
	audit.AuthMethod = "anonymous"
	return &store.PublicLocalUserRegistration{
		User: creation.User, Settings: creation.Settings, PasswordCredential: creation.PasswordCredential,
		DefaultProfilePictureJob: creation.DefaultProfilePictureJob, VerificationToken: token,
		TokenLifetime: time.Hour, MailLifetime: time.Hour,
		VerificationOccurrence: occurrence, VerificationDelivery: delivery, VerificationJob: job, AuditEvent: audit,
	}
}

func testUserStoreDisabledMailRecordsTerminalAccountNotice(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	at := model.GetMillis() + 100
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	command := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.ID.String(), AuditAt: at,
	})
	command.Delivery, command.DeliveryJob = suppressSecurityNoticeForDisabledMail(t, command.Delivery, command.DeliveryJob)
	if _, err := ss.User().SetDisabledWithAudit(ctx, command); err != nil {
		t.Fatal(err)
	}
	delivery, err := ss.Mail().GetDelivery(ctx, command.Delivery.ID)
	requireNoError(t, err)
	job, err := ss.Job().Get(ctx, command.DeliveryJob.ID)
	requireNoError(t, err)
	if delivery.State != model.MailDeliverySuppressed || delivery.PublicFailureCode != model.MailDeliveryDisabledCode ||
		len(delivery.EncryptedPayload) != 0 || job.Status != model.JobStatusCanceled {
		t.Fatalf("disabled-mail account notice delivery/job = %#v / %#v", delivery, job)
	}
}

func testUserCreationAndDefaultJobAreAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	mismatched := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	mismatched.DefaultProfilePictureJob.Command = defaultProfilePictureCommand(model.NewUserID())
	if _, err := ss.User().Create(ctx, mismatched); err == nil {
		t.Fatal("Create() accepted a default-picture Job targeting another User")
	}
	if _, err := ss.User().Get(ctx, mismatched.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("user survived mismatched Job rollback: %v", err)
	}
	if _, err := ss.PasswordCredential().GetByUser(ctx, mismatched.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("credential survived mismatched Job rollback: %v", err)
	}
	if _, err := ss.Job().Get(ctx, mismatched.DefaultProfilePictureJob.ID); !store.IsNotFound(err) {
		t.Fatalf("mismatched Job was persisted: %v", err)
	}
	if _, err := ss.UserSettings().Get(ctx, mismatched.User.ID); !store.IsNotFound(err) {
		t.Fatalf("settings survived mismatched Job rollback: %v", err)
	}
	mismatchedSettings := testUserCreation(newUser(), nil)
	mismatchedSettings.Settings.UserID = model.NewUserID()
	if _, err := ss.User().Create(ctx, mismatchedSettings); err == nil {
		t.Fatal("Create() accepted settings targeting another User")
	}
	if _, err := ss.User().Get(ctx, mismatchedSettings.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("user survived mismatched settings rollback: %v", err)
	}
	permanent := testUserCreation(newUser(), nil)
	permanent.DefaultProfilePictureJob.DedupePolicy = model.JobDedupePermanent
	if _, err := ss.User().Create(ctx, permanent); err == nil {
		t.Fatal("Create() accepted a permanent-dedupe default-picture intent")
	}

	first := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	result, err := ss.User().Create(ctx, first)
	requireNoError(t, err)
	queued, err := ss.Job().Get(ctx, first.DefaultProfilePictureJob.ID)
	requireNoError(t, err)
	if queued.Status != model.JobStatusQueued || queued.DedupeKey != result.User.ID.String() ||
		result.PasswordCredential == nil || result.PasswordCredential.UserID != result.User.ID {
		t.Fatalf("Create() result/job = %#v / %#v", result, queued)
	}
	settings, err := ss.UserSettings().Get(ctx, result.User.ID)
	requireNoError(t, err)
	if settings.Source != model.UserSettingsInitialSource || settings.FormatVersion != model.UserSettingsFormatVersion1 || settings.UserID != result.User.ID {
		t.Fatalf("created settings = %#v", settings)
	}

	second := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	second.DefaultProfilePictureJob.ID = first.DefaultProfilePictureJob.ID
	if _, err = ss.User().Create(ctx, second); err == nil {
		t.Fatal("Create() succeeded when its Job insert failed")
	}
	if _, err = ss.User().Get(ctx, second.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("user survived Job rollback: %v", err)
	}
	if _, err = ss.PasswordCredential().GetByUser(ctx, second.User.ID.String()); !store.IsNotFound(err) {
		t.Fatalf("credential survived Job rollback: %v", err)
	}
	if _, err = ss.UserSettings().Get(ctx, second.User.ID); !store.IsNotFound(err) {
		t.Fatalf("settings survived Job rollback: %v", err)
	}
}

func testUserStoreProtectLastAdministrator(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	role, err := ss.Role().Save(ctx, &model.Role{
		Name:        model.SystemAdministratorRoleName,
		DisplayName: "System Administrator",
		BuiltIn:     true,
		Permissions: model.AllActions(),
	})
	requireNoError(t, err)
	first := saveUserWithPassword(t, ctx, ss)
	firstBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: first.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	requireNoError(t, err)
	at := model.GetMillis()
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.ID.String())
	_, err = ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: first.ID.String(), ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.ID.String(), AuditAt: at,
	}))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) ||
		conflict.Constraint != "users_last_system_admin" {
		t.Fatalf("disable last administrator error = %v", err)
	}
	second := saveUserWithPassword(t, ctx, ss)
	secondBinding, err := ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: second.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(at - 100),
	})
	requireNoError(t, err)
	_, err = ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: first.ID.String(), ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.ID.String(), AuditAt: at,
	}))
	requireNoError(t, err)
	if _, err = ss.RoleBinding().End(ctx, secondBinding.ID.String(), at+1); !errors.As(err, &conflict) {
		t.Fatalf("end only enabled administrator binding error = %v", err)
	}
	if _, err = ss.RoleBinding().End(ctx, firstBinding.ID.String(), at+1); err != nil {
		t.Fatalf("ending disabled administrator binding should be allowed: %v", err)
	}
}

func testUserStoreListAndDisable(t *testing.T, ss store.Store) {
	ctx := context.Background()
	first := newUser()
	first.Username = "aaa-" + model.NewId()
	first.DisplayName = "Searchable Alpha"
	first, err := createUser(t, ctx, ss, first)
	requireNoError(t, err)
	second := newUser()
	second.Username = "bbb-" + model.NewId()
	second, err = createUser(t, ctx, ss, second)
	requireNoError(t, err)
	unitMember := newUser()
	unitMember.Username = "unit-member-" + model.NewId()
	unitMember, err = createUser(t, ctx, ss, unitMember)
	requireNoError(t, err)
	unitRoleHolder := newUser()
	unitRoleHolder.Username = "unit-role-" + model.NewId()
	unitRoleHolder, err = createUser(t, ctx, ss, unitRoleHolder)
	requireNoError(t, err)
	archivedRoleHolder := newUser()
	archivedRoleHolder.Username = "zzz-archived-role-" + model.NewId()
	archivedRoleHolder, err = createUser(t, ctx, ss, archivedRoleHolder)
	requireNoError(t, err)
	otherClassMember := newUser()
	otherClassMember.Username = "yyy-class-member-" + model.NewId()
	otherClassMember, err = createUser(t, ctx, ss, otherClassMember)
	requireNoError(t, err)
	activeAt := model.GetMillis() + 100
	fixture := saveClassFixture(t, ctx, ss)
	visibleClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "user-search-visible")
	siblingUnit := saveAcademicUnit(t, ctx, ss, fixture.institution.ID.String(), "", "visibility-sibling-"+model.NewId())
	siblingProgramme := saveProgramme(t, ctx, ss, siblingUnit.ID.String(), "visibility-sibling-programme-"+model.NewId())
	siblingLevel := saveProgrammeLevel(t, ctx, ss, siblingProgramme.ID.String(), "visibility-sibling-level-"+model.NewId())
	siblingClass := saveClass(t, ctx, ss, siblingLevel.ID.String(), fixture.period.ID.String(), "visibility-sibling-class-"+model.NewId())
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: first.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	firstMembership, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: visibleClass.ID, UserID: first.ID, StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	_, err = ss.ClassMember().End(ctx, firstMembership.Membership.ID.String(), firstMembership.Membership.Revision, activeAt+100)
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: second.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: visibleClass.ID, UserID: second.ID, StartsAt: model.TimeFromMillis(activeAt + 10),
	})
	requireNoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: otherClassMember.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	_, err = ss.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: siblingClass.ID, UserID: otherClassMember.ID, StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: fixture.programme.AcademicUnitID, UserID: unitMember.ID,
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	unitRole, err := ss.Role().Save(ctx, &model.Role{
		Name: "unit-directory-" + model.NewId(), DisplayName: "Unit Directory",
		Permissions: []string{string(model.ActionUserView)},
	})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: unitRoleHolder.ID, RoleID: unitRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	archivedRole, err := ss.Role().Save(ctx, &model.Role{
		Name: "archived-directory-" + model.NewId(), DisplayName: "Archived Directory",
		Permissions: []string{string(model.ActionUserView)},
	})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: archivedRoleHolder.ID, RoleID: archivedRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		StartsAt: model.TimeFromMillis(activeAt - 10),
	})
	requireNoError(t, err)
	_, err = ss.Role().Archive(ctx, archivedRole.ID.String(), model.GetMillis())
	requireNoError(t, err)
	denied, err := ss.User().List(ctx, store.UserListOptions{Limit: 10})
	requireNoError(t, err)
	if len(denied) != 0 {
		t.Fatalf("List(without visibility) = %#v, want empty", denied)
	}
	classVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []string{visibleClass.ID.String()}, ActiveAt: activeAt}, Limit: 10,
	})
	requireNoError(t, err)
	if len(classVisible) != 1 || classVisible[0].ID != first.ID {
		t.Fatalf("List(class visibility) = %#v, want only %s", classVisible, first.ID)
	}
	byHiddenEmail, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []string{visibleClass.ID.String()}, ActiveAt: activeAt},
		Query:      first.Email, Limit: 10,
	})
	requireNoError(t, err)
	if len(byHiddenEmail) != 0 {
		t.Fatalf("List(scoped email search) = %#v, want no email oracle", byHiddenEmail)
	}
	unitVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{AcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}, ActiveAt: activeAt}, Limit: 10,
	})
	requireNoError(t, err)
	visibleIDs := map[model.UserID]bool{}
	for _, user := range unitVisible {
		visibleIDs[user.ID] = true
	}
	if len(unitVisible) != 3 || !visibleIDs[first.ID] || !visibleIDs[unitMember.ID] || !visibleIDs[unitRoleHolder.ID] {
		t.Fatalf("List(academic-unit visibility) = %#v, want Class, unit-membership, and Role-Binding Users", unitVisible)
	}
	if visibleIDs[archivedRoleHolder.ID] {
		t.Fatalf("List(academic-unit visibility) included holder of archived Role %s", archivedRoleHolder.ID)
	}
	classMemberVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{
			ClassMemberAcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}, ActiveAt: activeAt,
		},
		Limit: 10,
	})
	requireNoError(t, err)
	if len(classMemberVisible) != 1 || classMemberVisible[0].ID != first.ID {
		t.Fatalf("List(class-member unit visibility) = %#v, want only current Class member %s", classMemberVisible, first.ID)
	}
	allClassMembers, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassMemberInstitutionWide: true, ActiveAt: activeAt}, Limit: 10,
	})
	requireNoError(t, err)
	allClassMemberIDs := map[model.UserID]bool{}
	for _, user := range allClassMembers {
		allClassMemberIDs[user.ID] = true
	}
	if len(allClassMembers) != 2 || !allClassMemberIDs[first.ID] || !allClassMemberIDs[otherClassMember.ID] {
		t.Fatalf("List(institution class-member visibility) = %#v, want current Class members %s and %s", allClassMembers, first.ID, otherClassMember.ID)
	}
	unionVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{
			AcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()},
			ClassIDs:            []string{siblingClass.ID.String()},
			ActiveAt:            activeAt,
		},
		Limit: 10,
	})
	requireNoError(t, err)
	unionIDs := map[model.UserID]bool{}
	for _, user := range unionVisible {
		unionIDs[user.ID] = true
	}
	if len(unionVisible) != 4 || !unionIDs[first.ID] || !unionIDs[unitMember.ID] ||
		!unionIDs[unitRoleHolder.ID] || !unionIDs[otherClassMember.ID] {
		t.Fatalf("List(union visibility) = %#v, want relationship subtree plus sibling Class roster", unionVisible)
	}
	match, err := ss.User().MatchVisibility(ctx, first.ID.String(), store.UserVisibilityScope{
		ClassIDs: []string{visibleClass.ID.String()},
		AcademicUnitRootIDs: []string{
			siblingUnit.ID.String(), fixture.programme.AcademicUnitID.String(),
		},
		ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match.ScopeType != model.RoleScopeClass || match.ScopeID != visibleClass.ID.String() {
		t.Fatalf("MatchVisibility(class) = %#v, want exact Class", match)
	}
	match, err = ss.User().MatchVisibility(ctx, unitRoleHolder.ID.String(), store.UserVisibilityScope{
		AcademicUnitRootIDs: []string{
			siblingUnit.ID.String(), fixture.programme.AcademicUnitID.String(),
		},
		ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match.ScopeType != model.RoleScopeAcademicUnit || match.ScopeID != fixture.programme.AcademicUnitID.String() {
		t.Fatalf("MatchVisibility(unit) = %#v, want matching root", match)
	}
	match, err = ss.User().MatchVisibility(ctx, unitMember.ID.String(), store.UserVisibilityScope{
		ClassMemberAcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}, ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match != (store.UserVisibilityMatch{}) {
		t.Fatalf("MatchVisibility(class-member unit nonmember) = %#v, want no match", match)
	}
	match, err = ss.User().MatchVisibility(ctx, first.ID.String(), store.UserVisibilityScope{
		ClassMemberAcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}, ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match.ScopeType != model.RoleScopeAcademicUnit || match.ScopeID != fixture.programme.AcademicUnitID.String() {
		t.Fatalf("MatchVisibility(class-member unit member) = %#v, want matching roster root", match)
	}
	match, err = ss.User().MatchVisibility(ctx, first.ID.String(), store.UserVisibilityScope{
		ClassMemberInstitutionWide: true, ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match.ScopeType != model.RoleScopeClass || match.ScopeID != visibleClass.ID.String() {
		t.Fatalf("MatchVisibility(institution class member) = %#v, want actual Class", match)
	}
	match, err = ss.User().MatchVisibility(ctx, archivedRoleHolder.ID.String(), store.UserVisibilityScope{
		AcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}, ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match != (store.UserVisibilityMatch{}) {
		t.Fatalf("MatchVisibility(archived Role holder) = %#v, want no match", match)
	}
	match, err = ss.User().MatchVisibility(ctx, second.ID.String(), store.UserVisibilityScope{
		AcademicUnitRootIDs: []string{fixture.programme.AcademicUnitID.String()}, ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match != (store.UserVisibilityMatch{}) {
		t.Fatalf("MatchVisibility(future relation) = %#v, want no match", match)
	}
	if _, err = ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []string{"malformed"}, ActiveAt: activeAt}, Limit: 10,
	}); err == nil {
		t.Fatal("List accepted a malformed visibility ID")
	}

	found, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{InstitutionWide: true},
		Query:      "Searchable Alpha", Limit: 10,
	})
	requireNoError(t, err)
	if len(found) != 1 || found[0].ID != first.ID {
		t.Fatalf("List(search) = %#v", found)
	}
	page, err := ss.User().List(ctx, store.UserListOptions{
		Visibility:    store.UserVisibilityScope{InstitutionWide: true},
		AfterUsername: first.Username, AfterId: first.ID.String(), Limit: 10,
	})
	requireNoError(t, err)
	if len(page) == 0 || page[0].ID != second.ID {
		t.Fatalf("List(after) = %#v", page)
	}
	at := model.GetMillis() + 100
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.ID.String())
	result, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: first.ID.String(), ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: attempt.ID.String(), AuditAt: at,
	}))
	requireNoError(t, err)
	disabled := result.User
	if disabled.DisabledAt.Millis() != at || disabled.Revision != first.Revision+1 {
		t.Fatalf("SetDisabledWithAudit() = %#v", result)
	}
	scopedDefault, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []string{visibleClass.ID.String()}, ActiveAt: activeAt},
		Limit:      10,
	})
	requireNoError(t, err)
	scopedInclusive, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []string{visibleClass.ID.String()}, ActiveAt: activeAt},
		Limit:      10, IncludeDisabled: true,
	})
	requireNoError(t, err)
	if len(scopedDefault) != 0 || len(scopedInclusive) != 0 {
		t.Fatalf("scoped disabled visibility default=%#v inclusive=%#v, want both empty", scopedDefault, scopedInclusive)
	}
	institutionClassMembersDefault, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassMemberInstitutionWide: true, ActiveAt: activeAt}, Limit: 10,
	})
	requireNoError(t, err)
	institutionClassMembersInclusive, err := ss.User().List(ctx, store.UserListOptions{
		Visibility:      store.UserVisibilityScope{ClassMemberInstitutionWide: true, ActiveAt: activeAt},
		Limit:           10,
		IncludeDisabled: true,
	})
	requireNoError(t, err)
	for label, users := range map[string][]*model.User{
		"default": institutionClassMembersDefault, "inclusive": institutionClassMembersInclusive,
	} {
		if len(users) != 1 || users[0].ID != otherClassMember.ID {
			t.Fatalf("institution class-member disabled visibility %s=%#v, want only enabled member %s", label, users, otherClassMember.ID)
		}
	}
	match, err = ss.User().MatchVisibility(ctx, first.ID.String(), store.UserVisibilityScope{
		ClassIDs: []string{visibleClass.ID.String()}, ActiveAt: activeAt,
	})
	requireNoError(t, err)
	if match != (store.UserVisibilityMatch{}) {
		t.Fatalf("MatchVisibility(disabled user) = %#v, want no match", match)
	}
	active, err := ss.User().List(ctx, store.UserListOptions{Limit: 10, Visibility: store.UserVisibilityScope{InstitutionWide: true}})
	requireNoError(t, err)
	for _, user := range active {
		if user.ID == first.ID {
			t.Fatalf("disabled user returned in active list: %#v", active)
		}
	}
	all, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{InstitutionWide: true},
		Limit:      10, IncludeDisabled: true,
	})
	requireNoError(t, err)
	seen := false
	for _, user := range all {
		seen = seen || user.ID == first.ID
	}
	if !seen {
		t.Fatalf("disabled user missing from inclusive list: %#v", all)
	}
}

func testUserStoreEnablementRevocationAndAuditAreAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, firstRaw := saveSession(t, ctx, ss, user.ID.String(), 10)
	second, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.GetMillis() + 100

	oversizedAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	if _, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt:        at,
		RevocationReason: strings.Repeat("x", model.SessionRevocationMaxRunes+1),
		AuditEventID:     oversizedAttempt.ID.String(),
		AuditAt:          at,
	})); err == nil {
		t.Fatal("SetDisabledWithAudit() accepted an oversized revocation reason")
	} else {
		var invalid *store.ErrInvalidInput
		if !errors.As(err, &invalid) {
			t.Fatalf("oversized revocation reason error = %v, want invalid input", err)
		}
	}
	oversizedAudit, err := ss.Audit().Get(ctx, oversizedAttempt.ID.String())
	requireNoError(t, err)
	if oversizedAudit.Status != model.AuditStatusAttempt {
		t.Fatalf("invalid state change completed its audit: %#v", oversizedAudit)
	}

	// A missing audit attempt must roll back both the user update and every
	// session/credential revocation performed before completion was attempted.
	missingAuditCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: model.NewId(), AuditAt: at,
	})
	if _, err := ss.User().SetDisabledWithAudit(ctx, missingAuditCommand); err == nil {
		t.Fatal("SetDisabledWithAudit() succeeded without its audit attempt")
	}
	if _, getErr := ss.Mail().GetDelivery(ctx, missingAuditCommand.Delivery.ID); !store.IsNotFound(getErr) {
		t.Fatalf("account notice survived audit rollback: %v", getErr)
	}
	if _, getErr := ss.Job().Get(ctx, missingAuditCommand.DeliveryJob.ID); !store.IsNotFound(getErr) {
		t.Fatalf("account notice Job survived audit rollback: %v", getErr)
	}
	unchanged, err := ss.User().Get(ctx, user.ID.String())
	requireNoError(t, err)
	if unchanged.DisabledAt.Valid || unchanged.Revision != user.Revision {
		t.Fatalf("user state survived audit rollback: %#v", unchanged)
	}
	unrevoked, err := ss.Session().Get(ctx, first.ID.String())
	requireNoError(t, err)
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(firstRaw.access),
		model.SessionCredentialAccess,
	)
	requireNoError(t, err)
	if unrevoked.RevokedAt.Valid || credential.RevokedAt.Valid {
		t.Fatalf("session revocation survived audit rollback: session=%#v credential=%#v", unrevoked, credential)
	}

	disableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	disableCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: "administrator disabled account",
		AuditEventID: disableAttempt.ID.String(), AuditAt: at,
	})
	disabled, err := ss.User().SetDisabledWithAudit(ctx, disableCommand)
	requireNoError(t, err)
	if disabled.User.DisabledAt.Millis() != at || disabled.User.Revision != user.Revision+1 ||
		len(disabled.RevokedSessions) != 2 || len(disabled.RevokedTokenHashes) != 4 {
		t.Fatalf("SetDisabledWithAudit() = %#v", disabled)
	}
	for _, sessionID := range []string{first.ID.String(), second.ID.String()} {
		revoked, getErr := ss.Session().Get(ctx, sessionID)
		requireNoError(t, getErr)
		if revoked.RevokedAt.Millis() != at || revoked.RevocationReason != "administrator disabled account" {
			t.Fatalf("session %s was not revoked with the account: %#v", sessionID, revoked)
		}
	}
	completed, err := ss.Audit().Get(ctx, disableAttempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess || len(completed.Result) == 0 {
		t.Fatalf("disable audit = %#v", completed)
	}
	disabledNotice, err := ss.Mail().GetDelivery(ctx, disableCommand.Delivery.ID)
	requireNoError(t, err)
	if disabledNotice.TemplateKey != model.MailTemplateIdentityAccountDisabled {
		t.Fatalf("account-disable notice = %#v, want disabled template", disabledNotice)
	}
	sessionNotices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentitySessionsRevokedByAdmin}, Limit: 10,
	})
	requireNoError(t, err)
	if len(sessionNotices) != 0 {
		t.Fatalf("account disable emitted administrative session notices: %#v", sessionNotices)
	}
	replayAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	replayCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: disabled.User.Revision, Disabled: true,
		ChangedAt: at + 1, RevocationReason: "administrator disabled account",
		AuditEventID: replayAttempt.ID.String(), AuditAt: at + 1,
	})
	if _, replayErr := ss.User().SetDisabledWithAudit(ctx, replayCommand); !store.IsConflict(replayErr) {
		t.Fatalf("same-state account replay error = %v", replayErr)
	}
	if _, getErr := ss.Mail().GetDelivery(ctx, replayCommand.Delivery.ID); !store.IsNotFound(getErr) {
		t.Fatalf("same-state account replay persisted duplicate notice: %v", getErr)
	}

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	if _, err := ss.User().SetDisabledWithAudit(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: user.Revision, Disabled: false,
		ChangedAt: at + 1, AuditEventID: staleAttempt.ID.String(), AuditAt: at + 1,
	})); !store.IsConflict(err) {
		t.Fatalf("stale SetDisabledWithAudit() error = %v", err)
	}
	staleAudit, err := ss.Audit().Get(ctx, staleAttempt.ID.String())
	requireNoError(t, err)
	if staleAudit.Status != model.AuditStatusAttempt {
		t.Fatalf("stale state change completed its audit: %#v", staleAudit)
	}

	enableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	enableCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: disabled.User.Revision, Disabled: false,
		ChangedAt: at + 2, AuditEventID: enableAttempt.ID.String(), AuditAt: at + 2,
	})
	enabled, err := ss.User().SetDisabledWithAudit(ctx, enableCommand)
	requireNoError(t, err)
	if enabled.User.DisabledAt.Valid || enabled.User.Revision != disabled.User.Revision+1 ||
		enabled.RevokedSessions == nil || len(enabled.RevokedSessions) != 0 ||
		enabled.RevokedTokenHashes == nil || len(enabled.RevokedTokenHashes) != 0 {
		t.Fatalf("enable result = %#v", enabled)
	}
	enableAudit, err := ss.Audit().Get(ctx, enableAttempt.ID.String())
	requireNoError(t, err)
	if enableAudit.Status != model.AuditStatusSuccess {
		t.Fatalf("enable audit = %#v", enableAudit)
	}
	if delivery, getErr := ss.Mail().GetDelivery(ctx, enableCommand.Delivery.ID); getErr != nil || delivery.TemplateKey != model.MailTemplateIdentityAccountEnabled {
		t.Fatalf("account-enable notice = %#v, %v", delivery, getErr)
	}
}

func testUserStoreCreateAndGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	input := newUser()
	saved, err := createUser(t, ctx, ss, input)
	requireNoError(t, err)
	if !saved.ID.IsValid() || saved.Revision != 1 || !input.ID.IsZero() {
		t.Fatalf("Create() saved=%#v input=%#v", saved, input)
	}
	got, err := ss.User().Get(ctx, saved.ID.String())
	requireNoError(t, err)
	if *got != *saved {
		t.Fatalf("Get() = %#v, want %#v", got, saved)
	}
	if _, err := ss.User().Get(ctx, model.NewId()); !store.IsNotFound(err) {
		t.Fatalf("Get(missing) error = %v, want not found", err)
	}
	_, err = ss.User().Create(ctx, testUserCreation(saved, nil))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("second Create(saved) error = %v, want conflict", err)
	}
}

func testUserStoreNormalizedLookups(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)

	byUsername, err := ss.User().GetByUsername(ctx, "  "+user.Username+"  ")
	requireNoError(t, err)
	byEmail, err := ss.User().GetByEmail(ctx, "  "+user.Email+"  ")
	requireNoError(t, err)
	if byUsername.ID != user.ID || byEmail.ID != user.ID {
		t.Fatalf("lookups returned username=%#v email=%#v", byUsername, byEmail)
	}
	if _, err := ss.User().GetByEmail(ctx, "missing@example.edu"); !store.IsNotFound(err) {
		t.Fatalf("GetByEmail(missing) error = %v", err)
	}
}

func testUserStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	input := newUser()
	input.EmailVerified = true
	updated, err := createUser(t, ctx, ss, input)
	requireNoError(t, err)

	activityAt := model.GetMillis() + 100
	requireNoError(t, ss.User().UpdateLastLogin(ctx, updated.ID.String(), activityAt))
	displayName := "Audited User"
	auditAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.ID.String())
	audited, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		UserID: updated.ID, Changes: model.UserProfileChanges{DisplayName: &displayName}, ExpectedRevision: updated.Revision,
		AuditEventID: auditAttempt.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if audited.DisplayName != "Audited User" || audited.Revision != updated.Revision+1 ||
		audited.Email != updated.Email || audited.EmailVerified != updated.EmailVerified {
		t.Fatalf("UpdateProfileWithAudit() = %#v", audited)
	}
	completed, err := ss.Audit().Get(ctx, auditAttempt.ID.String())
	requireNoError(t, err)
	if completed.Status != model.AuditStatusSuccess {
		t.Fatalf("profile update audit = %#v", completed)
	}

	rolledBackName := "Must Roll Back"
	if _, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		UserID: audited.ID, Changes: model.UserProfileChanges{DisplayName: &rolledBackName}, ExpectedRevision: audited.Revision,
		AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	}); err == nil {
		t.Fatal("UpdateProfileWithAudit() succeeded without its audit attempt")
	}
	persisted, err := ss.User().Get(ctx, audited.ID.String())
	requireNoError(t, err)
	if persisted.DisplayName != audited.DisplayName || persisted.Revision != audited.Revision ||
		persisted.LastLoginAt.Millis() != activityAt || persisted.LastActivityAt.Millis() != activityAt || model.MillisFromTime(persisted.UpdatedAt) < activityAt {
		t.Fatalf("profile update survived audit rollback: %#v", persisted)
	}

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.ID.String())
	if _, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		UserID: updated.ID, Changes: model.UserProfileChanges{DisplayName: &displayName}, ExpectedRevision: updated.Revision,
		AuditEventID: staleAttempt.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsConflict(err) {
		t.Fatalf("stale UpdateProfileWithAudit() error = %v", err)
	}
	missingID := model.UserID(model.NewId())
	missingAttempt := saveUserProfileAuditAttempt(t, ctx, ss, missingID.String())
	if _, err := ss.User().UpdateProfileWithAudit(ctx, &store.UserProfileUpdate{
		UserID: missingID, Changes: model.UserProfileChanges{DisplayName: &displayName}, ExpectedRevision: updated.Revision,
		AuditEventID: missingAttempt.ID.String(), AuditAt: model.GetMillis(),
	}); !store.IsNotFound(err) {
		t.Fatalf("UpdateProfileWithAudit(missing) error = %v", err)
	}
}

func saveUserProfileAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionUserManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: userID},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	requireNoError(t, err)
	return attempt
}

func testUserStoreUpdateLastLogin(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	revision := user.Revision
	at := model.GetMillis() + 100
	requireNoError(t, ss.User().UpdateLastLogin(ctx, user.ID.String(), at))
	got, err := ss.User().Get(ctx, user.ID.String())
	requireNoError(t, err)
	if got.LastLoginAt.Millis() != at || got.LastActivityAt.Millis() != at || model.MillisFromTime(got.UpdatedAt) < at || got.Revision != revision {
		t.Fatalf("UpdateLastLogin() user = %#v", got)
	}
}

func testUserStoreUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	duplicateUsername := newUser()
	duplicateUsername.Username = user.Username
	_, err := createUser(t, ctx, ss, duplicateUsername)
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "users_username_key" {
		t.Fatalf("duplicate username error = %v", err)
	}
	duplicateEmail := newUser()
	duplicateEmail.Email = user.Email
	_, err = createUser(t, ctx, ss, duplicateEmail)
	if !errors.As(err, &conflict) || conflict.Constraint != "users_email_key" {
		t.Fatalf("duplicate email error = %v", err)
	}
}

func newUser() *model.User {
	id := model.NewId()
	return &model.User{
		Username:    "user-" + id,
		Email:       id + "@example.edu",
		DisplayName: "Test User",
	}
}

func saveUser(t *testing.T, ctx context.Context, ss store.Store) *model.User {
	t.Helper()
	user, err := createUser(t, ctx, ss, newUser())
	requireNoError(t, err)
	return user
}

func saveUserWithPassword(t *testing.T, ctx context.Context, ss store.Store) *model.User {
	t.Helper()
	result, err := ss.User().Create(ctx, testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"}))
	requireNoError(t, err)
	return result.User
}

func createUser(t *testing.T, ctx context.Context, ss store.Store, input *model.User) (*model.User, error) {
	t.Helper()
	result, err := ss.User().Create(ctx, testUserCreation(input, nil))
	if err != nil {
		return nil, err
	}
	return result.User, nil
}

func testUserCreation(input *model.User, credential *model.PasswordCredential) *store.UserCreation {
	user := *input
	if user.ID.IsZero() {
		user.PrepareCreate(model.NewUserID(), model.NowUTC())
	}
	if credential != nil {
		copy := *credential
		if copy.ID.IsZero() {
			copy.UserID = user.ID
			copy.PrepareCreate(model.NewPasswordCredentialID(), user.CreatedAt)
		}
		credential = &copy
	}
	command := defaultProfilePictureCommand(user.ID)
	job, _ := model.NewJob(
		model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1,
		command, user.ID.String(), user.CreatedAt, user.CreatedAt, 8,
	)
	settings, _ := model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), user.CreatedAt)
	return &store.UserCreation{User: &user, Settings: settings, PasswordCredential: credential, DefaultProfilePictureJob: job}
}

func defaultProfilePictureCommand(userID model.UserID) json.RawMessage {
	document, _ := json.Marshal(map[string]string{"user_id": userID.String()})
	return document
}
