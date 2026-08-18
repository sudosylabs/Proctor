// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestEmailChangeAuditOmitsOldAndNewMailboxes(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	user := &model.User{ID: model.NewUserID(), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), Revision: 4,
		Username: "student", Email: "old-private@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC",
		DefaultProfilePictureSeed: "0000000000000000000000000000000000000000000000000000000000000000"}
	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewAuditEventID().String()}
	tokens := &accountTokenStoreFake{}
	application := newAccountTokenTestApp(t, accountTokenTestDependencies{
		users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{}, tokens: tokens,
		institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: &emailTransitionMailerFake{}, now: func() time.Time { return now },
	})
	application.userProfiles = &userProfileService{authorization: &userProfileAuthorizerFake{events: &events}, audit: auditor}
	application.recentAuthenticationTTL = 15 * time.Minute
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now), ClientType: model.SessionClientWeb}

	if _, err := application.ChangeUserEmail(context.Background(), NewInvocation(principal, model.RequestMetadata{}),
		ChangeUserEmailCommand{UserID: user.ID.String(), Email: "new-private@example.edu"}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]any{"parameters": auditor.attempt.Value, "prior": auditor.attempt.Prior})
	if err != nil {
		t.Fatal(err)
	}
	for _, mailbox := range []string{"old-private@example.edu", "new-private@example.edu"} {
		if strings.Contains(string(encoded), mailbox) {
			t.Fatalf("audit projection exposed mailbox %q: %s", mailbox, encoded)
		}
	}
}

func TestEmailChangePreparesFrozenOldWarningAndNewTargetVerification(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	user := &model.User{ID: model.NewUserID(), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), Revision: 4,
		Username: "student", Email: "old@example.edu", EmailVerified: true, DisplayName: "Student", Locale: "en", Timezone: "UTC",
		DefaultProfilePictureSeed: "0000000000000000000000000000000000000000000000000000000000000000"}
	tokens := &accountTokenStoreFake{}
	mailer := &emailTransitionMailerFake{}
	app := newAccountTokenTestApp(t, accountTokenTestDependencies{users: &accountTokenUserStoreFake{byID: user}, passwords: &accountTokenPasswordStoreFake{},
		tokens: tokens, institution: &model.Institution{ID: model.NewInstitutionID()}, mailer: mailer, now: func() time.Time { return now }})
	result, err := app.accountTokens.changeUserEmail(context.Background(), user, "new@example.edu", now, mutationAttemptReference{ID: model.NewId(), MutationAtMillis: now.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Email != "new@example.edu" || tokens.emailChange == nil || tokens.emailChange.Token.Target != "new@example.edu" ||
		tokens.emailChange.TokenLifetime != 24*time.Hour || tokens.emailChange.WarningLifetime != 24*time.Hour {
		t.Fatalf("result/aggregate = %#v / %#v", result, tokens.emailChange)
	}
	if len(mailer.requests) != 2 || mailer.requests[0].Recipient.Email != "old@example.edu" || mailer.requests[0].TemplateKey != model.MailTemplateIdentityEmailChangeWarningOld ||
		mailer.requests[1].Recipient.Email != "new@example.edu" || mailer.requests[1].TemplateKey != model.MailTemplateIdentityEmailChangeVerifyNew || mailer.requests[1].ActionURL == "" {
		t.Fatalf("mail requests = %#v", mailer.requests)
	}
	user.Email = "later@example.edu"
	if mailer.requests[0].Recipient.Email != "old@example.edu" || mailer.requests[1].Recipient.Email != "new@example.edu" {
		t.Fatal("prepared recipients followed a later profile mutation")
	}
}

type emailTransitionMailerFake struct{ requests []DirectMailPreparation }

func (*emailTransitionMailerFake) Enabled() bool { return true }
func (m *emailTransitionMailerFake) PrepareDirect(request DirectMailPreparation) (*preparedDirectMail, error) {
	copyRecipient := *request.Recipient
	request.Recipient = &copyRecipient
	m.requests = append(m.requests, request)
	return &preparedDirectMail{Occurrence: &model.MailOccurrence{ID: request.OccurrenceID, Kind: request.Kind, TemplateKey: request.TemplateKey, ActorUserID: request.Recipient.ID, CreatedAt: request.At},
		Delivery: &model.MailDelivery{TargetUserID: request.Recipient.ID, TemplateKey: request.TemplateKey, Deadline: request.Deadline}, Job: &model.Job{Type: request.JobType}}, nil
}
