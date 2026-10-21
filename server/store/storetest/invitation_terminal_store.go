// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInvitationTerminalizationSuppressesCredentialDelivery(t *testing.T, ss store.Store) {
	t.Run("Acceptance", func(t *testing.T) {
		ctx := context.Background()
		fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "accept-suppression")
		issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
		invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
		require.NoError(t, err)
		_, err = ss.Invitation().AcceptStudentClass(ctx, studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC()))
		require.NoError(t, err)
		assertInvitationCredentialDeliverySuppressed(t, ctx, ss, issue)
	})
	t.Run("ScheduledExpiry", func(t *testing.T) {
		ctx := context.Background()
		unit, programme := saveProgrammeParents(t, ctx, ss, "scheduled-invitation")
		level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "scheduled-level")
		period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "scheduled-period", model.GetMillis()-86_400_000)
		class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "scheduled-class")
		inviter := saveUser(t, ctx, ss)
		role, err := SaveRole(ctx, ss, &model.Role{Name: "scheduled-inviter-" + model.NewID(), DisplayName: "Scheduled Inviter",
			Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
		require.NoError(t, err)
		issuedAt := model.NowUTC().Add(-time.Minute)
		_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
			ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
		require.NoError(t, err)
		issue := studentClassInvitationIssueFixture(t, ss, inviter, class, period, issuedAt)
		issue.Invitation.IntendedStartsAt = model.NowUTC()
		issue.Invitation.IntendedEndsAt = model.OptionalTimeFrom(model.NowUTC().Add(500 * time.Millisecond))
		invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
		require.NoError(t, err)
		time.Sleep(time.Until(issue.Invitation.IntendedEndsAt.Time) + 20*time.Millisecond)
		result, err := ss.Invitation().Maintain(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, 1, result.Expired)
		current, err := ss.Invitation().Get(ctx, invitation.ID)
		require.NoError(t, err)
		require.Equal(t, model.InvitationExpired, current.State)
		assertInvitationCredentialDeliverySuppressed(t, ctx, ss, issue)
	})
}

func assertInvitationCredentialDeliverySuppressed(t *testing.T, ctx context.Context, ss store.Store, issue *store.StudentClassInvitationIssue) {
	t.Helper()
	delivery, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, delivery.State)
	require.Equal(t, model.MailDeliveryObsoleteCode, delivery.PublicFailureCode)
	require.Empty(t, delivery.EncryptedPayload)
	job, err := ss.Job().Get(ctx, issue.DeliveryJob.ID)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusCanceled, job.Status)
	require.True(t, job.CompletedAt.Valid)
}
