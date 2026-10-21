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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TimeAuthorityIdentitySQLProbe moves persisted node-clock instants after
// PostgreSQL time, as a creating node whose clock leads the database does.
type TimeAuthorityIdentitySQLProbe struct {
	LeadInvitation func(*testing.T, context.Context, model.InvitationID)
	LeadUser       func(*testing.T, context.Context, model.UserID)
}

// timeAuthorityNodeLead is how far a creating node's clock leads PostgreSQL
// in these probes; well inside the seven-day Invitation lifetime.
const timeAuthorityNodeLead = 5 * time.Minute

func timeAuthorityStudentInvitation(t *testing.T, ctx context.Context, ss store.Store, probe TimeAuthorityIdentitySQLProbe,
	suffix string, targetEmail string,
) (*model.Invitation, *model.Class, *model.User) {
	t.Helper()
	fixture, class, inviter, issuedAt := invitationAdministrationFixture(t, ctx, ss, "ta-"+suffix)
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	if targetEmail == "" {
		targetEmail = "time-authority-" + model.NewId() + "@example.edu"
	}
	issue.Invitation.TargetEmail = targetEmail
	issue.Invitation.Suggestions.Username = "time-authority-" + model.NewId()
	requireNoError(t, issue.Invitation.Validate())
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	probe.LeadInvitation(t, ctx, invitation.ID)
	invitation, err = ss.Invitation().Get(ctx, invitation.ID)
	requireNoError(t, err)
	if !invitation.CreatedAt.After(model.NowUTC()) {
		t.Fatalf("led Invitation creation %v does not lead the clock", invitation.CreatedAt)
	}
	return invitation, class, inviter
}

func requireTimeAuthorityInvitationPersisted(t *testing.T, ctx context.Context, ss store.Store, returned *model.Invitation, superseded *model.Invitation, state model.InvitationState) {
	t.Helper()
	if returned.State != state || returned.UpdatedAt.Before(returned.CreatedAt) || returned.UpdatedAt.Before(superseded.UpdatedAt) ||
		(returned.AcceptedAt.Valid && (returned.AcceptedAt.Time.Before(returned.CreatedAt) || !returned.AcceptedAt.Time.Equal(returned.UpdatedAt))) {
		t.Fatalf("Invitation transition = %#v; superseded %#v", returned, superseded)
	}
	persisted, err := ss.Invitation().Get(ctx, returned.ID)
	requireNoError(t, err)
	if persisted.State != returned.State || persisted.Revision != returned.Revision || !persisted.CreatedAt.Equal(returned.CreatedAt) ||
		!persisted.UpdatedAt.Equal(returned.UpdatedAt) || persisted.AcceptedAt.Valid != returned.AcceptedAt.Valid ||
		(persisted.AcceptedAt.Valid && !persisted.AcceptedAt.Time.Equal(returned.AcceptedAt.Time)) || persisted.ClaimHash != returned.ClaimHash {
		t.Fatalf("persisted Invitation = %#v; returned %#v", persisted, returned)
	}
}

// RunTimeAuthorityInvitationTransitions proves that PostgreSQL-timed
// Invitation transitions accept Invitations issued by a node whose clock leads
// the database and never record an instant before the issue.
func RunTimeAuthorityInvitationTransitions(t *testing.T, ss store.Store, probe TimeAuthorityIdentitySQLProbe) {
	t.Run("AcceptStudentClass", func(t *testing.T) {
		ctx := context.Background()
		invitation, _, _ := timeAuthorityStudentInvitation(t, ctx, ss, probe, "accept", "")
		acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
		accepted, err := ss.Invitation().AcceptStudentClass(ctx, acceptance)
		requireNoError(t, err)
		requireTimeAuthorityInvitationPersisted(t, ctx, ss, accepted.Invitation, invitation, model.InvitationAccepted)
	})
	t.Run("AcceptStudentClassVerifiesLeadingExistingUser", func(t *testing.T) {
		ctx := context.Background()
		existing := newUser()
		existing.Email = "time-authority-existing-" + model.NewId() + "@example.edu"
		existing.EmailVerified = false
		existing, err := createUser(t, ctx, ss, existing)
		requireNoError(t, err)
		probe.LeadUser(t, ctx, existing.ID)
		led, err := ss.User().Get(ctx, existing.ID)
		requireNoError(t, err)
		invitation, _, _ := timeAuthorityStudentInvitation(t, ctx, ss, probe, "existing", existing.Email)
		accepted, err := ss.Invitation().AcceptStudentClass(ctx, studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC()))
		requireNoError(t, err)
		if accepted.User.ID != existing.ID || !accepted.User.EmailVerified || accepted.User.UpdatedAt.Before(led.UpdatedAt) {
			t.Fatalf("accepted existing User = %#v; led %#v", accepted.User, led)
		}
		persisted, err := ss.User().Get(ctx, existing.ID)
		requireNoError(t, err)
		if !persisted.EmailVerified || !persisted.UpdatedAt.Equal(accepted.User.UpdatedAt) || persisted.Revision != accepted.User.Revision {
			t.Fatalf("persisted existing User = %#v; returned %#v", persisted, accepted.User)
		}
		requireTimeAuthorityInvitationPersisted(t, ctx, ss, accepted.Invitation, invitation, model.InvitationAccepted)
	})
	t.Run("Resend", func(t *testing.T) {
		ctx := context.Background()
		invitation, class, inviter := timeAuthorityStudentInvitation(t, ctx, ss, probe, "resend", "")
		occurrence, delivery, job := invitationLifecycleMailFixture(t, invitation.ID, inviter.ID,
			model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), invitation.ExpiresAt)
		audit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.resend")
		resent, err := ss.Invitation().Resend(ctx, &store.InvitationResend{ID: invitation.ID, ExpectedRevision: invitation.Revision,
			ClaimHash: model.HashInvitationClaim(model.NewCredentialToken()), Occurrence: occurrence, Delivery: delivery, DeliveryJob: job,
			ActorUserID: inviter.ID, AuditEventID: audit.ID, AuditAt: model.GetMillis()})
		requireNoError(t, err)
		requireTimeAuthorityInvitationPersisted(t, ctx, ss, resent.Invitation, invitation, model.InvitationPending)
	})
	t.Run("Revoke", func(t *testing.T) {
		ctx := context.Background()
		invitation, class, inviter := timeAuthorityStudentInvitation(t, ctx, ss, probe, "revoke", "")
		audit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.revoke")
		revoked, err := ss.Invitation().Revoke(ctx, &store.InvitationRevocation{ID: invitation.ID, ExpectedRevision: invitation.Revision,
			ActorUserID: inviter.ID, AuditEventID: audit.ID, AuditAt: model.GetMillis()})
		requireNoError(t, err)
		requireTimeAuthorityInvitationPersisted(t, ctx, ss, revoked.Invitation, invitation, model.InvitationRevoked)
	})
	t.Run("ReplaceSupersedes", func(t *testing.T) {
		ctx := context.Background()
		invitation, class, inviter := timeAuthorityStudentInvitation(t, ctx, ss, probe, "replace", "")
		candidate := *invitation
		candidate.ID, candidate.ClaimHash = model.NewInvitationID(), model.HashInvitationClaim(model.NewCredentialToken())
		candidate.Suggestions.DisplayName = "Replacement Student"
		candidate.CreatedAt, candidate.UpdatedAt = model.NowUTC(), model.NowUTC()
		candidate.ExpiresAt, candidate.Revision = candidate.CreatedAt.Add(model.InvitationLifetime), 1
		requireNoError(t, candidate.Validate())
		occurrence, delivery, job := invitationLifecycleMailFixture(t, candidate.ID, inviter.ID,
			model.MailTemplateAccessStudentClassInvitation, model.JobTypeMailDeliverCredential, model.NowUTC(), candidate.ExpiresAt)
		currentAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.supersede")
		replacementAudit := saveInvitationLifecycleAuditAttempt(t, ctx, ss, inviter.ID, class.ID.String(), "invitation.replacement_issue")
		_, err := ss.Invitation().Replace(ctx, &store.InvitationReplacement{CurrentID: invitation.ID,
			ExpectedCurrentRevision: invitation.Revision, Replacement: &candidate, Lifetime: model.InvitationLifetime,
			Occurrence: occurrence, Delivery: delivery, DeliveryJob: job, ActorUserID: inviter.ID,
			CurrentAuditEventID: currentAudit.ID, ReplacementAuditEventID: replacementAudit.ID, AuditAt: model.GetMillis()})
		requireNoError(t, err)
		superseded, err := ss.Invitation().Get(ctx, invitation.ID)
		requireNoError(t, err)
		requireTimeAuthorityInvitationPersisted(t, ctx, ss, superseded, invitation, model.InvitationSuperseded)
	})
}

// RunTimeAuthorityOnboardingImportRowOutcome proves that a row outcome
// recorded at PostgreSQL time never moves an onboarding import, created and
// committed by a node whose clock leads the database, before its creation.
func RunTimeAuthorityOnboardingImportRowOutcome(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture, class, actor, issuedAt := invitationAdministrationFixture(t, ctx, ss, "ta-onboard")
	session, credentials, _ := saveSession(t, ctx, ss, actor.ID.String(), 10)
	principal := model.Principal{UserID: actor.ID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credentials[0].ID),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: session.ClientType, AuthenticatedAt: session.AuthenticatedAt}
	email := "time-authority-onboarding-" + model.NewId() + "@example.edu"
	importID := createExecutingStudentOnboardingImport(t, ctx, ss, actor, principal, class, model.NowUTC().Add(timeAuthorityNodeLead), email)
	executing, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	requireNoError(t, err)
	issue := studentClassInvitationIssueFixture(t, ss, actor, class, fixture.period, issuedAt)
	issue.Invitation.TargetEmail = email
	requireNoError(t, issue.Invitation.Validate())
	command := invitationTestCommand(actor.ID, "invitation.student_class.issue.v1", "time-authority-"+importID.String(), "time-authority-"+importID.String())
	command.OnboardingImportID, command.OnboardingImportRowNumber = importID, 1
	_, err = ss.Invitation().IssueStudentClassIdempotently(ctx, issue, command)
	requireNoError(t, err)
	completed, err := ss.OnboardingImport().GetOnboardingImport(ctx, importID)
	requireNoError(t, err)
	if completed.State != model.OnboardingImportCompleted || completed.SucceededRows != 1 ||
		completed.UpdatedAt.Before(completed.CreatedAt) || completed.UpdatedAt.Before(executing.UpdatedAt) {
		t.Fatalf("completed onboarding import = %#v; executing %#v", completed, executing)
	}
	page, err := ss.OnboardingImport().ListOnboardingImportRows(ctx, importID, 0, 10)
	requireNoError(t, err)
	if len(page.Rows) != 1 || page.Rows[0].Status != model.OnboardingImportRowSucceeded || page.Rows[0].UpdatedAt.Before(executing.UpdatedAt) {
		t.Fatalf("onboarding import rows = %#v; executing %#v", page, executing)
	}
}

// RunTimeAuthorityUserDisable proves that disabling a User whose lifecycle
// leads the disabling node's clock never records a modification or disable
// before the User's creation.
func RunTimeAuthorityUserDisable(t *testing.T, ss store.Store, probe TimeAuthorityIdentitySQLProbe) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	probe.LeadUser(t, ctx, user.ID)
	led, err := ss.User().Get(ctx, user.ID)
	requireNoError(t, err)
	attempt := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	result, err := ss.User().SetDisabled(ctx, UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: led.Revision, Disabled: true, ChangedAt: model.GetMillis(),
		RevocationReason: model.SessionRevocationAccountDisabled, AuditEventID: attempt.ID, AuditAt: model.MillisFromTime(attempt.CreatedAt),
		Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
	}))
	requireNoError(t, err)
	disabled := result.User
	if !disabled.DisabledAt.Valid || disabled.DisabledAt.Time.Before(disabled.CreatedAt) ||
		disabled.UpdatedAt.Before(led.UpdatedAt) || disabled.UpdatedAt.Before(disabled.CreatedAt) {
		t.Fatalf("disabled User = %#v; led %#v", disabled, led)
	}
	persisted, err := ss.User().Get(ctx, user.ID)
	requireNoError(t, err)
	if !persisted.UpdatedAt.Equal(disabled.UpdatedAt) || !persisted.DisabledAt.Time.Equal(disabled.DisabledAt.Time) || persisted.Revision != disabled.Revision {
		t.Fatalf("persisted disabled User = %#v; returned %#v", persisted, disabled)
	}
}
