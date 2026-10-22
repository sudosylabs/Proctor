// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/user_store.go.

package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type UserStoreSQLProbe struct {
	SetPublicRegistration func(*testing.T, bool, bool)
	DatabaseNow           func(*testing.T) time.Time
}

func TestUserStore(t *testing.T, ss store.Store, probes ...UserStoreSQLProbe) {
	t.Run("CurrentContext", func(t *testing.T) { testUserStoreCurrentContext(t, ss) })
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
	t.Run("VisibilityIntervalPrecision", func(t *testing.T) { testUserVisibilityIntervalPrecision(t, ss) })
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
	result, err := ss.User().RegisterLocal(ctx, disabled)
	require.Nil(t, result, "disabled public registration")
	require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled, "disabled public registration")
	_, err = ss.User().Get(ctx, disabled.User.ID)
	require.True(t, store.IsNotFound(err), "disabled registration persisted User")

	localDisabled := publicLocalUserRegistrationFixture(t, institution.ID, newUser())
	probe.SetPublicRegistration(t, false, false)
	result, err = ss.User().RegisterLocal(ctx, localDisabled)
	require.Nil(t, result, "disabled local enrollment")
	require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled, "disabled local enrollment")
	_, err = ss.User().Get(ctx, localDisabled.User.ID)
	require.True(t, store.IsNotFound(err), "disabled local enrollment persisted User")

	probe.SetPublicRegistration(t, true, true)
	accepted := publicLocalUserRegistrationFixture(t, institution.ID, newUser())
	result, err = ss.User().RegisterLocal(ctx, accepted)
	require.NoError(t, err)
	require.Equal(t, accepted.User.ID, result.User.ID)
	require.Equal(t, accepted.VerificationToken.ID, result.Token.ID)
	require.False(t, result.User.EmailVerified)
	_, err = ss.PasswordCredential().GetByUser(ctx, accepted.User.ID)
	require.NoError(t, err)
	_, err = ss.UserSettings().Get(ctx, accepted.User.ID)
	require.NoError(t, err)
	_, err = ss.UserToken().GetByHash(ctx, accepted.VerificationToken.TokenHash, model.UserTokenEmailVerification)
	require.NoError(t, err)
	_, err = ss.Mail().GetDelivery(ctx, accepted.VerificationDelivery.ID)
	require.NoError(t, err)
	_, err = ss.Job().Get(ctx, accepted.DefaultProfilePictureJob.ID)
	require.NoError(t, err)
	_, err = ss.Job().Get(ctx, accepted.VerificationJob.ID)
	require.NoError(t, err)

	if probe.DatabaseNow != nil {
		databaseNow := probe.DatabaseNow(t)
		behindNodeAt := databaseNow.Add(-2 * time.Hour)
		skewed := publicLocalUserRegistrationFixtureAt(t, institution.ID, newUser(), behindNodeAt)
		originalUserAt, originalTokenExpiry := skewed.User.CreatedAt, skewed.VerificationToken.ExpiresAt
		skewedResult, registerErr := ss.User().RegisterLocal(ctx, skewed)
		require.NoError(t, registerErr)
		require.Equal(t, originalUserAt, skewed.User.CreatedAt, "RegisterLocal mutated caller-owned candidates while rebasing timestamps")
		require.Equal(t, originalTokenExpiry, skewed.VerificationToken.ExpiresAt, "RegisterLocal mutated caller-owned candidates while rebasing timestamps")
		require.False(t, skewedResult.User.CreatedAt.Before(databaseNow), "registration did not use PostgreSQL time: user=%s token=%s..%s database", skewedResult.User.CreatedAt, skewedResult.Token.CreatedAt, skewedResult.Token.ExpiresAt)
		require.False(t, skewedResult.Token.CreatedAt.Before(databaseNow), "registration did not use PostgreSQL time: user=%s token=%s..%s database", skewedResult.User.CreatedAt, skewedResult.Token.CreatedAt, skewedResult.Token.ExpiresAt)
		require.True(t, skewedResult.Token.ExpiresAt.Equal(skewedResult.Token.CreatedAt.Add(skewed.TokenLifetime)), "registration did not use PostgreSQL time: user=%s token=%s..%s database", skewedResult.User.CreatedAt, skewedResult.Token.CreatedAt, skewedResult.Token.ExpiresAt)
		storedSettings, settingsErr := ss.UserSettings().Get(ctx, skewed.User.ID)
		require.NoError(t, settingsErr)
		storedCredential, credentialErr := ss.PasswordCredential().GetByUser(ctx, skewed.User.ID)
		require.NoError(t, credentialErr)
		storedDelivery, deliveryErr := ss.Mail().GetDelivery(ctx, skewed.VerificationDelivery.ID)
		require.NoError(t, deliveryErr)
		storedDefaultJob, defaultJobErr := ss.Job().Get(ctx, skewed.DefaultProfilePictureJob.ID)
		require.NoError(t, defaultJobErr)
		storedDeliveryJob, deliveryJobErr := ss.Job().Get(ctx, skewed.VerificationJob.ID)
		require.NoError(t, deliveryJobErr)
		storedAudit := skewedResult.AuditEvent
		require.NotNil(t, storedAudit, "registration result omitted its committed audit")
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
			require.True(t, timestamp.Equal(skewedResult.User.CreatedAt), "%s=%s, transaction time", field, timestamp)
		}
		require.True(t, storedDelivery.Deadline.Equal(storedDelivery.CreatedAt.Add(skewed.MailLifetime)), "delivery deadline=%s, created=%s lifetime=%s", storedDelivery.Deadline, storedDelivery.CreatedAt, skewed.MailLifetime)

		aheadNodeAt := databaseNow.Add(2 * time.Hour)
		ahead := publicLocalUserRegistrationFixtureAt(t, institution.ID, newUser(), aheadNodeAt)
		originalAheadAt := ahead.User.CreatedAt
		aheadResult, registerAheadErr := ss.User().RegisterLocal(ctx, ahead)
		require.NoError(t, registerAheadErr)
		require.Equal(t, originalAheadAt, ahead.User.CreatedAt, "RegisterLocal mutated the ahead-node candidate while rebasing timestamps")
		require.False(t, aheadResult.User.CreatedAt.Before(databaseNow), "ahead-node registration did not use PostgreSQL time: user=%s token=%s..%s database=%s node", aheadResult.User.CreatedAt, aheadResult.Token.CreatedAt, aheadResult.Token.ExpiresAt, databaseNow)
		require.True(t, aheadResult.User.CreatedAt.Before(aheadNodeAt), "ahead-node registration did not use PostgreSQL time: user=%s token=%s..%s database=%s node", aheadResult.User.CreatedAt, aheadResult.Token.CreatedAt, aheadResult.Token.ExpiresAt, databaseNow)
		require.True(t, aheadResult.Token.ExpiresAt.Equal(aheadResult.Token.CreatedAt.Add(ahead.TokenLifetime)), "ahead-node registration did not use PostgreSQL time: user=%s token=%s..%s database=%s node", aheadResult.User.CreatedAt, aheadResult.Token.CreatedAt, aheadResult.Token.ExpiresAt, databaseNow)
	}

	rollback := publicLocalUserRegistrationFixture(t, institution.ID, newUser())
	queued, _, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: rollback.VerificationJob})
	require.NoError(t, err)
	require.Equal(t, rollback.VerificationJob.ID, queued.ID)
	_, err = ss.User().RegisterLocal(ctx, rollback)
	require.Error(t, err, "registration accepted colliding delivery Job")
	_, err = ss.User().Get(ctx, rollback.User.ID)
	require.True(t, store.IsNotFound(err), "failed registration persisted User")
	_, err = ss.UserToken().GetByHash(ctx, rollback.VerificationToken.TokenHash, model.UserTokenEmailVerification)
	require.True(t, store.IsNotFound(err), "failed registration persisted token")
	_, err = ss.Mail().GetDelivery(ctx, rollback.VerificationDelivery.ID)
	require.True(t, store.IsNotFound(err), "failed registration persisted delivery")

	sharedEmail := model.NewID() + "@public-registration.example.edu"
	contenders := []*store.PublicLocalUserRegistration{
		publicLocalUserRegistrationFixture(t, institution.ID, &model.User{Username: "registration-a-" + model.NewID(), Email: sharedEmail}),
		publicLocalUserRegistrationFixture(t, institution.ID, &model.User{Username: "registration-b-" + model.NewID(), Email: sharedEmail}),
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
			require.FailNow(t, fmt.Sprintf("concurrent registration error = %v", registerErr))
		}
	}
	require.Equal(t, 1, successes, "concurrent registration successes")
	require.Equal(t, 1, conflicts, "concurrent registration successes")
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
	at := model.NowUTC().Add(100 * time.Millisecond)
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	command := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: attempt.ID, AuditAt: at,
	})
	command.Delivery, command.DeliveryJob = suppressSecurityNoticeForDisabledMail(t, command.Delivery, command.DeliveryJob)
	_, err := ss.User().SetDisabled(ctx, command)
	require.NoError(t, err)
	delivery, err := ss.Mail().GetDelivery(ctx, command.Delivery.ID)
	require.NoError(t, err)
	job, err := ss.Job().Get(ctx, command.DeliveryJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, delivery.State, "disabled-mail account notice delivery/job = %#v / %#v", delivery, job)
	require.Equal(t, model.MailDeliveryDisabledCode, delivery.PublicFailureCode, "disabled-mail account notice delivery/job = %#v / %#v", delivery, job)
	require.Empty(t, delivery.EncryptedPayload, "disabled-mail account notice delivery/job = %#v / %#v", delivery, job)
	require.Equal(t, model.JobStatusCanceled, job.Status, "disabled-mail account notice delivery/job = %#v / %#v", delivery, job)
}

func testUserCreationAndDefaultJobAreAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	mismatched := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	mismatched.DefaultProfilePictureJob.Command = defaultProfilePictureCommand(model.NewUserID())
	_, err := ss.User().Create(ctx, mismatched)
	require.Error(t, err, "Create() accepted a default-picture Job targeting another User")
	_, err = ss.User().Get(ctx, mismatched.User.ID)
	require.True(t, store.IsNotFound(err), "user survived mismatched Job rollback")
	_, err = ss.PasswordCredential().GetByUser(ctx, mismatched.User.ID)
	require.True(t, store.IsNotFound(err), "credential survived mismatched Job rollback")
	_, err = ss.Job().Get(ctx, mismatched.DefaultProfilePictureJob.ID)
	require.True(t, store.IsNotFound(err), "mismatched Job was persisted")
	_, err = ss.UserSettings().Get(ctx, mismatched.User.ID)
	require.True(t, store.IsNotFound(err), "settings survived mismatched Job rollback")
	mismatchedSettings := testUserCreation(newUser(), nil)
	mismatchedSettings.Settings.UserID = model.NewUserID()
	_, err = ss.User().Create(ctx, mismatchedSettings)
	require.Error(t, err, "Create() accepted settings targeting another User")
	_, err = ss.User().Get(ctx, mismatchedSettings.User.ID)
	require.True(t, store.IsNotFound(err), "user survived mismatched settings rollback")
	permanent := testUserCreation(newUser(), nil)
	permanent.DefaultProfilePictureJob.DedupePolicy = model.JobDedupePermanent
	_, err = ss.User().Create(ctx, permanent)
	require.Error(t, err, "Create() accepted a permanent-dedupe default-picture intent")

	first := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	result, err := ss.User().Create(ctx, first)
	require.NoError(t, err)
	queued, err := ss.Job().Get(ctx, first.DefaultProfilePictureJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusQueued, queued.Status, "Create() result/job = %#v / %#v", result, queued)
	require.Equal(t, result.User.ID.String(), queued.DedupeKey, "Create() result/job = %#v / %#v", result, queued)
	require.NotNil(t, result.PasswordCredential, "Create() result/job = %#v / %#v", result, queued)
	require.Equal(t, result.User.ID, result.PasswordCredential.UserID, "Create() result/job = %#v / %#v", result, queued)
	settings, err := ss.UserSettings().Get(ctx, result.User.ID)
	require.NoError(t, err)
	require.Equal(t, model.UserSettingsInitialSource, settings.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, settings.FormatVersion)
	require.Equal(t, result.User.ID, settings.UserID)

	second := testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"})
	second.DefaultProfilePictureJob.ID = first.DefaultProfilePictureJob.ID
	_, err = ss.User().Create(ctx, second)
	require.Error(t, err, "Create() succeeded when its Job insert failed")
	_, err = ss.User().Get(ctx, second.User.ID)
	require.True(t, store.IsNotFound(err), "user survived Job rollback")
	_, err = ss.PasswordCredential().GetByUser(ctx, second.User.ID)
	require.True(t, store.IsNotFound(err), "credential survived Job rollback")
	_, err = ss.UserSettings().Get(ctx, second.User.ID)
	require.True(t, store.IsNotFound(err), "settings survived Job rollback")
}

func testUserStoreProtectLastAdministrator(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	role, err := SaveRole(ctx, ss, &model.Role{
		Name:        model.SystemAdministratorRoleName,
		DisplayName: "System Administrator",
		BuiltIn:     true,
		Permissions: model.AllActions(),
	})
	require.NoError(t, err)
	first := saveUserWithPassword(t, ctx, ss)
	firstBinding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: first.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	require.NoError(t, err)
	at := model.NowUTC()
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.ID.String())
	_, err = ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: first.ID, ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: attempt.ID, AuditAt: at,
	}))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "users_last_system_admin", conflict.Constraint)
	second := saveUserWithPassword(t, ctx, ss)
	secondBinding, err := SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: second.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeUTC(at.Add(-100 * time.Millisecond)),
	})
	require.NoError(t, err)
	_, err = ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: first.ID, ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: attempt.ID, AuditAt: at,
	}))
	require.NoError(t, err)
	_, err = EndRoleBinding(ctx, ss, secondBinding.ID, at.Add(1*time.Millisecond))
	require.ErrorAs(t, err, &conflict)
	_, err = EndRoleBinding(ctx, ss, firstBinding.ID, at.Add(1*time.Millisecond))
	require.NoError(t, err)
}

func testUserStoreListAndDisable(t *testing.T, ss store.Store) {
	ctx := context.Background()
	first := newUser()
	first.Username = "aaa-" + model.NewID()
	first.DisplayName = "Searchable Alpha 50%_!"
	first.FirstName = "Literal_Given"
	first.LastName = "Literal%Family!"
	first, err := createUser(t, ctx, ss, first)
	require.NoError(t, err)
	second := newUser()
	second.Username = "bbb-" + model.NewID()
	second, err = createUser(t, ctx, ss, second)
	require.NoError(t, err)
	unitMember := newUser()
	unitMember.Username = "unit-member-" + model.NewID()
	unitMember, err = createUser(t, ctx, ss, unitMember)
	require.NoError(t, err)
	unitRoleHolder := newUser()
	unitRoleHolder.Username = "unit-role-" + model.NewID()
	unitRoleHolder, err = createUser(t, ctx, ss, unitRoleHolder)
	require.NoError(t, err)
	archivedRoleHolder := newUser()
	archivedRoleHolder.Username = "zzz-archived-role-" + model.NewID()
	archivedRoleHolder, err = createUser(t, ctx, ss, archivedRoleHolder)
	require.NoError(t, err)
	otherClassMember := newUser()
	otherClassMember.Username = "yyy-class-member-" + model.NewID()
	otherClassMember, err = createUser(t, ctx, ss, otherClassMember)
	require.NoError(t, err)
	activeAt := model.NowUTC().Add(100 * time.Millisecond)
	fixture := saveClassFixture(t, ctx, ss)
	visibleClass := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "user-search-visible")
	siblingUnit := saveAcademicUnit(t, ctx, ss, fixture.institution.ID.String(), "", "visibility-sibling-"+model.NewID())
	siblingProgramme := saveProgramme(t, ctx, ss, siblingUnit.ID.String(), "visibility-sibling-programme-"+model.NewID())
	siblingLevel := saveProgrammeLevel(t, ctx, ss, siblingProgramme.ID.String(), "visibility-sibling-level-"+model.NewID())
	siblingClass := saveClass(t, ctx, ss, siblingLevel.ID.String(), fixture.period.ID.String(), "visibility-sibling-class-"+model.NewID())
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: first.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	firstMembership, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: visibleClass.ID, UserID: first.ID, StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnd(ctx, &store.ClassMemberSeedEnd{ID: firstMembership.Membership.ID, ExpectedRevision: firstMembership.Membership.Revision, EndAt: activeAt.Add(100 * time.Millisecond)})
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: second.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: visibleClass.ID, UserID: second.ID, StartsAt: model.TimeUTC(activeAt.Add(10 * time.Millisecond)),
	})
	require.NoError(t, err)
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserID: otherClassMember.ID, Kind: model.AffiliationStudent,
		StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: siblingClass.ID, UserID: otherClassMember.ID, StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	_, err = ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: fixture.programme.AcademicUnitID, UserID: unitMember.ID,
		StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	unitRole, err := SaveRole(ctx, ss, &model.Role{
		Name: "unit-directory-" + model.NewID(), DisplayName: "Unit Directory",
		Permissions: []string{string(model.ActionUserView)},
	})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: unitRoleHolder.ID, RoleID: unitRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	archivedRole, err := SaveRole(ctx, ss, &model.Role{
		Name: "archived-directory-" + model.NewID(), DisplayName: "Archived Directory",
		Permissions: []string{string(model.ActionUserView)},
	})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: archivedRoleHolder.ID, RoleID: archivedRole.ID,
		ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String(),
		StartsAt: model.TimeUTC(activeAt.Add(-10 * time.Millisecond)),
	})
	require.NoError(t, err)
	_, err = ArchiveRole(ctx, ss, archivedRole.ID, model.NowUTC())
	require.NoError(t, err)
	denied, err := ss.User().List(ctx, store.UserListOptions{Limit: 10})
	require.NoError(t, err)
	require.Empty(t, denied, "List(without visibility)")
	classVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []model.ClassID{visibleClass.ID}, ActiveAt: model.TimeUTC(activeAt)}, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, classVisible, 1, "List(class visibility)")
	require.Equal(t, first.ID, classVisible[0].ID, "List(class visibility)")
	byHiddenEmail, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []model.ClassID{visibleClass.ID}, ActiveAt: model.TimeUTC(activeAt)},
		Query:      first.Email, Limit: 10,
	})
	require.NoError(t, err)
	require.Empty(t, byHiddenEmail, "List(scoped email search)")
	unitVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{AcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}, ActiveAt: model.TimeUTC(activeAt)}, Limit: 10,
	})
	require.NoError(t, err)
	visibleIDs := map[model.UserID]bool{}
	for _, user := range unitVisible {
		visibleIDs[user.ID] = true
	}
	require.Len(t, unitVisible, 3, "List(academic-unit visibility)")
	require.True(t, visibleIDs[first.ID], "List(academic-unit visibility)")
	require.True(t, visibleIDs[unitMember.ID], "List(academic-unit visibility)")
	require.True(t, visibleIDs[unitRoleHolder.ID], "List(academic-unit visibility)")
	require.False(t, visibleIDs[archivedRoleHolder.ID], "List(academic-unit visibility) included holder of archived Role %s", archivedRoleHolder.ID)
	classMemberVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{
			ClassMemberAcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}, ActiveAt: model.TimeUTC(activeAt),
		},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, classMemberVisible, 1, "List(class-member unit visibility)")
	require.Equal(t, first.ID, classMemberVisible[0].ID, "List(class-member unit visibility)")
	allClassMembers, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassMemberInstitutionWide: true, ActiveAt: model.TimeUTC(activeAt)}, Limit: 10,
	})
	require.NoError(t, err)
	allClassMemberIDs := map[model.UserID]bool{}
	for _, user := range allClassMembers {
		allClassMemberIDs[user.ID] = true
	}
	require.Len(t, allClassMembers, 2, "List(institution class-member visibility) = %#v, want current Class members %s and %s", allClassMembers, first.ID, otherClassMember.ID)
	require.True(t, allClassMemberIDs[first.ID], "List(institution class-member visibility) = %#v, want current Class members %s and %s", allClassMembers, first.ID, otherClassMember.ID)
	require.True(t, allClassMemberIDs[otherClassMember.ID], "List(institution class-member visibility) = %#v, want current Class members %s and %s", allClassMembers, first.ID, otherClassMember.ID)
	unionVisible, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{
			AcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID},
			ClassIDs:            []model.ClassID{siblingClass.ID},
			ActiveAt:            model.TimeUTC(activeAt),
		},
		Limit: 10,
	})
	require.NoError(t, err)
	unionIDs := map[model.UserID]bool{}
	for _, user := range unionVisible {
		unionIDs[user.ID] = true
	}
	require.Len(t, unionVisible, 4, "List(union visibility)")
	require.True(t, unionIDs[first.ID], "List(union visibility)")
	require.True(t, unionIDs[unitMember.ID], "List(union visibility)")
	require.True(t, unionIDs[unitRoleHolder.ID], "List(union visibility)")
	require.True(t, unionIDs[otherClassMember.ID], "List(union visibility)")
	match, err := ss.User().MatchVisibility(ctx, first.ID, store.UserVisibilityScope{
		ClassIDs: []model.ClassID{visibleClass.ID},
		AcademicUnitRootIDs: []model.AcademicUnitID{
			siblingUnit.ID, fixture.programme.AcademicUnitID,
		},
		ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, model.RoleScopeClass, match.ScopeType, "MatchVisibility(class) = %#v, want exact Class", match)
	require.Equal(t, visibleClass.ID.String(), match.ScopeID, "MatchVisibility(class) = %#v, want exact Class", match)
	match, err = ss.User().MatchVisibility(ctx, unitRoleHolder.ID, store.UserVisibilityScope{
		AcademicUnitRootIDs: []model.AcademicUnitID{
			siblingUnit.ID, fixture.programme.AcademicUnitID,
		},
		ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, model.RoleScopeAcademicUnit, match.ScopeType, "MatchVisibility(unit) = %#v, want matching root", match)
	require.Equal(t, fixture.programme.AcademicUnitID.String(), match.ScopeID, "MatchVisibility(unit) = %#v, want matching root", match)
	match, err = ss.User().MatchVisibility(ctx, unitMember.ID, store.UserVisibilityScope{
		ClassMemberAcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}, ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, store.UserVisibilityMatch{}, match, "MatchVisibility(class-member unit nonmember)")
	match, err = ss.User().MatchVisibility(ctx, first.ID, store.UserVisibilityScope{
		ClassMemberAcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}, ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, model.RoleScopeAcademicUnit, match.ScopeType, "MatchVisibility(class-member unit member) = %#v, want matching roster root", match)
	require.Equal(t, fixture.programme.AcademicUnitID.String(), match.ScopeID, "MatchVisibility(class-member unit member) = %#v, want matching roster root", match)
	match, err = ss.User().MatchVisibility(ctx, first.ID, store.UserVisibilityScope{
		ClassMemberInstitutionWide: true, ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, model.RoleScopeClass, match.ScopeType, "MatchVisibility(institution class member) = %#v, want actual Class", match)
	require.Equal(t, visibleClass.ID.String(), match.ScopeID, "MatchVisibility(institution class member) = %#v, want actual Class", match)
	match, err = ss.User().MatchVisibility(ctx, archivedRoleHolder.ID, store.UserVisibilityScope{
		AcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}, ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, store.UserVisibilityMatch{}, match, "MatchVisibility(archived Role holder)")
	match, err = ss.User().MatchVisibility(ctx, second.ID, store.UserVisibilityScope{
		AcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}, ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, store.UserVisibilityMatch{}, match, "MatchVisibility(future relation)")
	_, err = ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []model.ClassID{"malformed"}, ActiveAt: model.TimeUTC(activeAt)}, Limit: 10,
	})
	require.Error(t, err, "List accepted a malformed visibility ID")

	found, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{InstitutionWide: true},
		Query:      "Searchable Alpha", Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, first.ID, found[0].ID)
	for _, query := range []string{first.Username, first.Email, "LITERAL_GIVEN", "Literal%Family!", "%", "_", "!"} {
		found, err = ss.User().List(ctx, store.UserListOptions{
			Visibility: store.UserVisibilityScope{InstitutionWide: true}, Query: query, Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, found, 1, "List(search %q)", query)
		require.Equal(t, first.ID, found[0].ID, "List(search %q)", query)
	}
	scopedLiteral, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []model.ClassID{visibleClass.ID}, ActiveAt: model.TimeUTC(activeAt)},
		Query:      "Literal%Family!", Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, scopedLiteral, 1, "List(scoped literal public-field search)")
	require.Equal(t, first.ID, scopedLiteral[0].ID, "List(scoped literal public-field search)")
	page, err := ss.User().List(ctx, store.UserListOptions{
		Visibility:    store.UserVisibilityScope{InstitutionWide: true},
		AfterUsername: first.Username, AfterID: first.ID, Limit: 10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, page)
	require.Equal(t, second.ID, page[0].ID)
	at := model.NowUTC().Add(100 * time.Millisecond)
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, first.ID.String())
	result, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: first.ID, ExpectedRevision: first.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: attempt.ID, AuditAt: at,
	}))
	require.NoError(t, err)
	disabled := result.User
	require.True(t, at.Equal(disabled.DisabledAt.Time))
	require.Equal(t, first.Revision+1, disabled.Revision)
	scopedDefault, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []model.ClassID{visibleClass.ID}, ActiveAt: model.TimeUTC(activeAt)},
		Limit:      10,
	})
	require.NoError(t, err)
	scopedInclusive, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassIDs: []model.ClassID{visibleClass.ID}, ActiveAt: model.TimeUTC(activeAt)},
		Limit:      10, IncludeDisabled: true,
	})
	require.NoError(t, err)
	require.Empty(t, scopedDefault, "scoped disabled visibility default")
	require.Empty(t, scopedInclusive, "scoped disabled visibility default")
	institutionClassMembersDefault, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{ClassMemberInstitutionWide: true, ActiveAt: model.TimeUTC(activeAt)}, Limit: 10,
	})
	require.NoError(t, err)
	institutionClassMembersInclusive, err := ss.User().List(ctx, store.UserListOptions{
		Visibility:      store.UserVisibilityScope{ClassMemberInstitutionWide: true, ActiveAt: model.TimeUTC(activeAt)},
		Limit:           10,
		IncludeDisabled: true,
	})
	require.NoError(t, err)
	for label, users := range map[string][]*model.User{
		"default": institutionClassMembersDefault, "inclusive": institutionClassMembersInclusive,
	} {
		require.Len(t, users, 1, "institution class-member disabled visibility %s", label)
		require.Equal(t, otherClassMember.ID, users[0].ID, "institution class-member disabled visibility %s", label)
	}
	match, err = ss.User().MatchVisibility(ctx, first.ID, store.UserVisibilityScope{
		ClassIDs: []model.ClassID{visibleClass.ID}, ActiveAt: model.TimeUTC(activeAt),
	})
	require.NoError(t, err)
	require.Equal(t, store.UserVisibilityMatch{}, match, "MatchVisibility(disabled user)")
	active, err := ss.User().List(ctx, store.UserListOptions{Limit: 10, Visibility: store.UserVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err)
	for _, user := range active {
		require.NotEqual(t, first.ID, user.ID, "disabled user returned in active list: %#v", active)
	}
	all, err := ss.User().List(ctx, store.UserListOptions{
		Visibility: store.UserVisibilityScope{InstitutionWide: true},
		Limit:      10, IncludeDisabled: true,
	})
	require.NoError(t, err)
	seen := false
	for _, user := range all {
		seen = seen || user.ID == first.ID
	}
	require.True(t, seen, "disabled user missing from inclusive list: %#v", all)
}

func testUserVisibilityIntervalPrecision(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := saveClassFixture(t, ctx, ss)
	class := saveClass(t, ctx, ss, fixture.level.ID.String(), fixture.period.ID.String(), "precise-visibility-class")
	start := fixture.period.StartsAt.Add(time.Hour + 123200*time.Microsecond)
	end := start.Add(time.Second + 500*time.Microsecond)
	for _, anchor := range []string{"academic unit member", "class member", "role binding"} {
		t.Run(anchor, func(t *testing.T) {
			user := saveUser(t, ctx, ss)
			visibility := store.UserVisibilityScope{AcademicUnitRootIDs: []model.AcademicUnitID{fixture.programme.AcademicUnitID}}
			wantMatch := store.UserVisibilityMatch{ScopeType: model.RoleScopeAcademicUnit, ScopeID: fixture.programme.AcademicUnitID.String()}
			switch anchor {
			case "academic unit member":
				_, err := ss.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
					AcademicUnitID: fixture.programme.AcademicUnitID, UserID: user.ID, StartsAt: start, EndsAt: model.OptionalTimeFrom(end),
				})
				require.NoError(t, err)
			case "class member":
				_, err := ss.Affiliation().Save(ctx, &model.Affiliation{
					UserID: user.ID, Kind: model.AffiliationStudent, StartsAt: start.Add(-time.Hour),
				})
				require.NoError(t, err)
				_, err = ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{
					ClassID: class.ID, UserID: user.ID, StartsAt: start, EndsAt: model.OptionalTimeFrom(end),
				})
				require.NoError(t, err)
				visibility = store.UserVisibilityScope{ClassIDs: []model.ClassID{class.ID}}
				wantMatch = store.UserVisibilityMatch{ScopeType: model.RoleScopeClass, ScopeID: class.ID.String()}
			case "role binding":
				role, err := SaveRole(ctx, ss, &model.Role{
					Name: "precise-visibility-reader", DisplayName: "Precise Visibility Reader",
					Permissions: []string{string(model.ActionClassView)},
				})
				require.NoError(t, err)
				_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
					UserID: user.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit,
					ScopeID: fixture.programme.AcademicUnitID.String(), StartsAt: start, EndsAt: model.OptionalTimeFrom(end),
				})
				require.NoError(t, err)
			}
			for _, test := range []struct {
				name   string
				at     time.Time
				active bool
			}{
				{"before start", start.Add(-time.Microsecond), false},
				{"at start", start, true},
				{"before end", end.Add(-time.Microsecond), true},
				{"submicrosecond before end", end.Add(-time.Nanosecond), true},
				{"at end", end, false},
				{"after end in same millisecond", end.Add(time.Microsecond), false},
			} {
				t.Run(test.name, func(t *testing.T) {
					visibility.ActiveAt = test.at.In(time.FixedZone("offset", 3600))
					listed, err := ss.User().List(ctx, store.UserListOptions{ID: user.ID, Limit: 1, Visibility: visibility})
					require.NoError(t, err)
					match, err := ss.User().MatchVisibility(ctx, user.ID, visibility)
					require.NoError(t, err)
					if test.active {
						require.Len(t, listed, 1, "visibility at %v = %#v", test.at, listed)
						require.Equal(t, user.ID, listed[0].ID, "visibility at %v = %#v", test.at, listed)
						require.Equal(t, wantMatch, match, "visibility at %v = %#v", test.at, listed)
					} else {
						require.Empty(t, listed, "visibility at %v, want absent", test.at)
						require.Zero(t, match, "visibility at %v, want absent", test.at)
					}
				})
			}
			visibility.ActiveAt = time.Time{}
			var invalid *store.ErrInvalidInput
			_, err := ss.User().List(ctx, store.UserListOptions{ID: user.ID, Limit: 1, Visibility: visibility})
			require.ErrorAs(t, err, &invalid, "List without visibility decision time")
			_, err = ss.User().MatchVisibility(ctx, user.ID, visibility)
			require.ErrorAs(t, err, &invalid, "MatchVisibility without decision time")
		})
	}
}

func testUserStoreEnablementRevocationAndAuditAreAtomic(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	first, _, firstRaw := saveSession(t, ctx, ss, user.ID.String(), 10)
	second, _, _ := saveSession(t, ctx, ss, user.ID.String(), 10)
	at := model.NowUTC().Add(100 * time.Millisecond)

	oversizedAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	if _, err := ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt:        at,
		RevocationReason: model.SessionRevocationReason(strings.Repeat("x", model.SessionRevocationMaxRunes+1)),
		AuditEventID:     oversizedAttempt.ID,
		AuditAt:          at,
	})); err == nil {
		require.FailNow(t, "SetDisabled() accepted an oversized revocation reason")
	} else {
		var invalid *store.ErrInvalidInput
		require.ErrorAs(t, err, &invalid, "oversized revocation reason error")
	}
	oversizedAudit, err := ss.Audit().Get(ctx, oversizedAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, oversizedAudit.Status)

	// A missing audit attempt must roll back both the user update and every
	// session/credential revocation performed before completion was attempted.
	missingAuditCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: model.NewAuditEventID(), AuditAt: at,
	})
	_, err = ss.User().SetDisabled(ctx, missingAuditCommand)
	require.Error(t, err, "SetDisabled() succeeded without its audit attempt")
	_, getErr := ss.Mail().GetDelivery(ctx, missingAuditCommand.Delivery.ID)
	require.True(t, store.IsNotFound(getErr), "account notice survived audit rollback")
	_, getErr = ss.Job().Get(ctx, missingAuditCommand.DeliveryJob.ID)
	require.True(t, store.IsNotFound(getErr), "account notice Job survived audit rollback")
	unchanged, err := ss.User().Get(ctx, user.ID)
	require.NoError(t, err)
	require.False(t, unchanged.DisabledAt.Valid)
	require.Equal(t, user.Revision, unchanged.Revision)
	unrevoked, err := ss.Session().Get(ctx, first.ID)
	require.NoError(t, err)
	credential, _, err := ss.SessionCredential().GetSessionByTokenHash(
		ctx,
		model.HashToken(firstRaw.access),
		model.SessionCredentialAccess,
	)
	require.NoError(t, err)
	require.False(t, unrevoked.RevokedAt.Valid, "session revocation survived audit rollback: session=%#v credential=%#v", unrevoked, credential)
	require.False(t, credential.RevokedAt.Valid, "session revocation survived audit rollback: session=%#v credential=%#v", unrevoked, credential)

	disableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	disableCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: true,
		ChangedAt: at, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: disableAttempt.ID, AuditAt: at,
	})
	disabled, err := ss.User().SetDisabled(ctx, disableCommand)
	require.NoError(t, err)
	require.True(t, at.Equal(disabled.User.DisabledAt.Time))
	require.Equal(t, user.Revision+1, disabled.User.Revision)
	require.Len(t, disabled.RevokedSessions, 2)
	require.Len(t, disabled.RevokedTokenHashes, 4)
	for _, sessionID := range []string{first.ID.String(), second.ID.String()} {
		revoked, sessionErr := ss.Session().Get(ctx, model.SessionID(sessionID))
		require.NoError(t, sessionErr)
		require.True(t, at.Equal(revoked.RevokedAt.Time), "session %s was not revoked with the account: %#v", sessionID, revoked)
		require.Equal(t, model.SessionRevocationAccountDisabled, revoked.RevocationReason, "session %s was not revoked with the account: %#v", sessionID, revoked)
	}
	completed, err := ss.Audit().Get(ctx, disableAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)
	require.NotEmpty(t, completed.Result)
	disabledNotice, err := ss.Mail().GetDelivery(ctx, disableCommand.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailTemplateIdentityAccountDisabled, disabledNotice.TemplateKey, "account-disable notice = %#v, want disabled template", disabledNotice)
	sessionNotices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
		TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentitySessionsRevokedByAdmin}, Limit: 10,
	})
	require.NoError(t, err)
	require.Empty(t, sessionNotices)
	replayAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	replayCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: disabled.User.Revision, Disabled: true,
		ChangedAt: at.Add(1 * time.Millisecond), RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: replayAttempt.ID, AuditAt: at.Add(1 * time.Millisecond),
	})
	_, replayErr := ss.User().SetDisabled(ctx, replayCommand)
	require.True(t, store.IsConflict(replayErr), "same-state account replay error")
	_, getErr = ss.Mail().GetDelivery(ctx, replayCommand.Delivery.ID)
	require.True(t, store.IsNotFound(getErr), "same-state account replay persisted duplicate notice")

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	_, err = ss.User().SetDisabled(ctx, userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: user.Revision, Disabled: false,
		ChangedAt: at.Add(1 * time.Millisecond), AuditEventID: staleAttempt.ID, AuditAt: at.Add(1 * time.Millisecond),
	}))
	require.True(t, store.IsConflict(err), "stale SetDisabled() error")
	staleAudit, err := ss.Audit().Get(ctx, staleAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusAttempt, staleAudit.Status)

	enableAttempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	enableCommand := userDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: disabled.User.Revision, Disabled: false,
		ChangedAt: at.Add(2 * time.Millisecond), AuditEventID: enableAttempt.ID, AuditAt: at.Add(2 * time.Millisecond),
	})
	enabled, err := ss.User().SetDisabled(ctx, enableCommand)
	require.NoError(t, err)
	require.False(t, enabled.User.DisabledAt.Valid)
	require.Equal(t, disabled.User.Revision+1, enabled.User.Revision)
	require.NotNil(t, enabled.RevokedSessions)
	require.Empty(t, enabled.RevokedSessions)
	require.NotNil(t, enabled.RevokedTokenHashes)
	require.Empty(t, enabled.RevokedTokenHashes)
	enableAudit, err := ss.Audit().Get(ctx, enableAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, enableAudit.Status)
	delivery, getErr := ss.Mail().GetDelivery(ctx, enableCommand.Delivery.ID)
	require.NoError(t, getErr, "account-enable notice = %#v", delivery)
	require.Equal(t, model.MailTemplateIdentityAccountEnabled, delivery.TemplateKey, "account-enable notice = %#v", delivery)
}

func testUserStoreCreateAndGet(t *testing.T, ss store.Store) {
	ctx := context.Background()
	input := newUser()
	saved, err := createUser(t, ctx, ss, input)
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid(), "Create() saved=%#v input=%#v", saved, input)
	require.Equal(t, int64(1), saved.Revision, "Create() saved=%#v input=%#v", saved, input)
	require.True(t, input.ID.IsZero(), "Create() saved=%#v input=%#v", saved, input)
	got, err := ss.User().Get(ctx, saved.ID)
	require.NoError(t, err)
	require.Equal(t, *saved, *got, "Get() = %#v, want %#v", got, saved)
	_, err = ss.User().Get(ctx, model.NewUserID())
	require.True(t, store.IsNotFound(err), "Get(missing) error")
	_, err = ss.User().Create(ctx, testUserCreation(saved, nil))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "second Create(saved) error")
}

func testUserStoreNormalizedLookups(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)

	byUsername, err := ss.User().GetByUsername(ctx, "  "+user.Username+"  ")
	require.NoError(t, err)
	byEmail, err := ss.User().GetByEmail(ctx, "  "+user.Email+"  ")
	require.NoError(t, err)
	require.Equal(t, user.ID, byUsername.ID, "lookups returned username=%#v email=%#v", byUsername, byEmail)
	require.Equal(t, user.ID, byEmail.ID, "lookups returned username=%#v email=%#v", byUsername, byEmail)
	_, err = ss.User().GetByEmail(ctx, "missing@example.edu")
	require.True(t, store.IsNotFound(err), "GetByEmail(missing) error")
}

func testUserStoreUpdate(t *testing.T, ss store.Store) {
	ctx := context.Background()
	input := newUser()
	input.EmailVerified = true
	updated, err := createUser(t, ctx, ss, input)
	require.NoError(t, err)

	activityAt := model.NowUTC().Add(100 * time.Millisecond)
	require.NoError(t, ss.User().UpdateLastLogin(ctx, &store.UserLastLoginUpdate{ID: updated.ID, At: activityAt}))
	displayName := "Audited User"
	auditAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.ID.String())
	audited, err := ss.User().UpdateProfile(ctx, &store.UserProfileUpdate{
		UserID: updated.ID, Changes: model.UserProfileChanges{DisplayName: &displayName}, ExpectedRevision: updated.Revision,
		AuditEventID: auditAttempt.ID, AuditAt: model.NowUTC(),
	})
	require.NoError(t, err)
	require.Equal(t, "Audited User", audited.DisplayName)
	require.Equal(t, updated.Revision+1, audited.Revision)
	require.Equal(t, updated.Email, audited.Email)
	require.Equal(t, updated.EmailVerified, audited.EmailVerified)
	completed, err := ss.Audit().Get(ctx, auditAttempt.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusSuccess, completed.Status)

	rolledBackName := "Must Roll Back"
	_, err = ss.User().UpdateProfile(ctx, &store.UserProfileUpdate{
		UserID: audited.ID, Changes: model.UserProfileChanges{DisplayName: &rolledBackName}, ExpectedRevision: audited.Revision,
		AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC(),
	})
	require.Error(t, err, "UpdateProfile() succeeded without its audit attempt")
	persisted, err := ss.User().Get(ctx, audited.ID)
	require.NoError(t, err)
	require.Equal(t, audited.DisplayName, persisted.DisplayName)
	require.Equal(t, audited.Revision, persisted.Revision)
	require.True(t, activityAt.Equal(persisted.LastLoginAt.Time))
	require.True(t, activityAt.Equal(persisted.LastActivityAt.Time))
	require.False(t, persisted.UpdatedAt.Before(activityAt))

	staleAttempt := saveUserProfileAuditAttempt(t, ctx, ss, updated.ID.String())
	_, err = ss.User().UpdateProfile(ctx, &store.UserProfileUpdate{
		UserID: updated.ID, Changes: model.UserProfileChanges{DisplayName: &displayName}, ExpectedRevision: updated.Revision,
		AuditEventID: staleAttempt.ID, AuditAt: model.NowUTC(),
	})
	require.True(t, store.IsConflict(err), "stale UpdateProfile() error")
	missingID := model.UserID(model.NewID())
	missingAttempt := saveUserProfileAuditAttempt(t, ctx, ss, missingID.String())
	_, err = ss.User().UpdateProfile(ctx, &store.UserProfileUpdate{
		UserID: missingID, Changes: model.UserProfileChanges{DisplayName: &displayName}, ExpectedRevision: updated.Revision,
		AuditEventID: missingAttempt.ID, AuditAt: model.NowUTC(),
	})
	require.True(t, store.IsNotFound(err), "UpdateProfile(missing) error")
}

func saveUserProfileAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID string) *model.AuditEvent {
	t.Helper()
	attempt, err := ss.Audit().Save(ctx, &model.AuditEvent{
		Action:    string(model.ActionUserManage),
		Resource:  model.Resource{Type: model.ResourceUser, ID: userID},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewID(),
		Status: model.AuditStatusAttempt, NodeID: "test-node",
	})
	require.NoError(t, err)
	return attempt
}

func testUserStoreUpdateLastLogin(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	revision := user.Revision
	at := model.NowUTC().Add(100 * time.Millisecond)
	require.NoError(t, ss.User().UpdateLastLogin(ctx, &store.UserLastLoginUpdate{ID: user.ID, At: at}))
	got, err := ss.User().Get(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, at.Equal(got.LastLoginAt.Time))
	require.True(t, at.Equal(got.LastActivityAt.Time))
	require.False(t, got.UpdatedAt.Before(at))
	require.Equal(t, revision, got.Revision)
}

func testUserStoreUniqueness(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	duplicateUsername := newUser()
	duplicateUsername.Username = user.Username
	_, err := createUser(t, ctx, ss, duplicateUsername)
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "users_username_key", conflict.Constraint)
	duplicateEmail := newUser()
	duplicateEmail.Email = user.Email
	_, err = createUser(t, ctx, ss, duplicateEmail)
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "users_email_key", conflict.Constraint)
}

func newUser() *model.User {
	id := model.NewID()
	return &model.User{
		Username:    "user-" + id,
		Email:       id + "@example.edu",
		DisplayName: "Test User",
	}
}

func saveUser(t *testing.T, ctx context.Context, ss store.Store) *model.User {
	t.Helper()
	user, err := createUser(t, ctx, ss, newUser())
	require.NoError(t, err)
	return user
}

func saveUserWithPassword(t *testing.T, ctx context.Context, ss store.Store) *model.User {
	t.Helper()
	result, err := ss.User().Create(ctx, testUserCreation(newUser(), &model.PasswordCredential{PasswordHash: "encoded-password"}))
	require.NoError(t, err)
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
		model.NewJobID(), model.JobTypeProfilePictureGenerateDefault,
		command, user.ID.String(), user.CreatedAt, user.CreatedAt, 8,
	)
	settings, _ := model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), user.CreatedAt)
	return &store.UserCreation{User: &user, Settings: settings, PasswordCredential: credential, DefaultProfilePictureJob: job}
}

func defaultProfilePictureCommand(userID model.UserID) json.RawMessage {
	document, _ := json.Marshal(map[string]string{"user_id": userID.String()})
	return document
}
