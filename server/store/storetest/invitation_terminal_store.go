// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInvitationTerminalizationSuppressesCredentialDelivery(t *testing.T, ss store.Store) {
	t.Run("Acceptance", func(t *testing.T) {
		ctx := context.Background()
		fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "accept-suppression")
		issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
		invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
		requireNoError(t, err)
		_, err = ss.Invitation().AcceptStudentClass(ctx, studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC()))
		requireNoError(t, err)
		assertInvitationCredentialDeliverySuppressed(t, ctx, ss, issue)
	})
	t.Run("ScheduledExpiry", func(t *testing.T) {
		ctx := context.Background()
		unit, programme := saveProgrammeParents(t, ctx, ss, "scheduled-invitation")
		level := saveProgrammeLevel(t, ctx, ss, programme.ID.String(), "scheduled-level")
		period := saveAcademicPeriod(t, ctx, ss, unit.InstitutionID.String(), "scheduled-period", model.GetMillis()-86_400_000)
		class := saveClass(t, ctx, ss, level.ID.String(), period.ID.String(), "scheduled-class")
		inviter := saveUser(t, ctx, ss)
		role, err := ss.Role().Save(ctx, &model.Role{Name: "scheduled-inviter-" + model.NewId(), DisplayName: "Scheduled Inviter",
			Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
		requireNoError(t, err)
		issuedAt := model.NowUTC().Add(-time.Minute)
		_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
			ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: issuedAt.Add(-time.Second)})
		requireNoError(t, err)
		issue := studentClassInvitationIssueFixture(t, ss, inviter, class, period, issuedAt)
		issue.Invitation.IntendedStartsAt = model.NowUTC()
		issue.Invitation.IntendedEndsAt = model.OptionalTimeFrom(model.NowUTC().Add(500 * time.Millisecond))
		invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
		requireNoError(t, err)
		time.Sleep(time.Until(issue.Invitation.IntendedEndsAt.Time) + 20*time.Millisecond)
		result, err := ss.Invitation().Maintain(ctx, 1)
		requireNoError(t, err)
		if result.Expired != 1 {
			t.Fatalf("Maintain() = %#v", result)
		}
		current, err := ss.Invitation().Get(ctx, invitation.ID)
		requireNoError(t, err)
		if current.State != model.InvitationExpired {
			t.Fatalf("scheduled Invitation state = %q", current.State)
		}
		assertInvitationCredentialDeliverySuppressed(t, ctx, ss, issue)
	})
}

func assertInvitationCredentialDeliverySuppressed(t *testing.T, ctx context.Context, ss store.Store, issue *store.StudentClassInvitationIssue) {
	t.Helper()
	delivery, err := ss.Mail().GetDelivery(ctx, issue.Delivery.ID)
	requireNoError(t, err)
	if delivery.State != model.MailDeliverySuppressed || delivery.PublicFailureCode != model.MailDeliveryObsoleteCode || len(delivery.EncryptedPayload) != 0 {
		t.Fatalf("terminal Invitation credential delivery = %#v", delivery)
	}
	job, err := ss.Job().Get(ctx, issue.DeliveryJob.ID)
	requireNoError(t, err)
	if job.Status != model.JobStatusCanceled || !job.CompletedAt.Valid {
		t.Fatalf("terminal Invitation credential Job = %#v", job)
	}
}
