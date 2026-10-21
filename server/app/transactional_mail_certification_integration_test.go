//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	netmail "net/mail"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestTransactionalMailPhaseCertification(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	smtpAddress := strings.TrimSpace(os.Getenv("PROCTOR_TEST_MAIL_SMTP_ADDRESS"))
	mailpitURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PROCTOR_TEST_MAILPIT_HTTP_URL")), "/")
	require.NotEmpty(t, smtpAddress, "PROCTOR_TEST_MAIL_SMTP_ADDRESS and PROCTOR_TEST_MAILPIT_HTTP_URL must be set")
	require.NotEmpty(t, mailpitURL, "PROCTOR_TEST_MAIL_SMTP_ADDRESS and PROCTOR_TEST_MAILPIT_HTTP_URL must be set")
	clearMailpit(t, mailpitURL)

	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t,
		testlib.WithStore(persistence),
		testlib.WithConfiguredMailer(),
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.ListenAddress = "127.0.0.1:0"
			cfg.Server.PublicURL = "https://proctor.example.test"
			cfg.Mail.Enabled = true
			cfg.Mail.Backend = "smtp"
			cfg.Mail.FromAddress = "no-reply@proctor.example.test"
			cfg.Mail.FromName = "Proctor Certification"
			cfg.Mail.SMTP.Address = smtpAddress
			cfg.Mail.SMTP.Security = "none"
			cfg.Mail.SMTP.Authentication = "none"
			cfg.Mail.SMTP.MessageIDDomain = "proctor.example.test"
		}),
	)
	ctx := context.Background()
	const password = "correct horse battery staple"
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, app.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "mail-certification", InstitutionDisplayName: "Mail Certification University",
		AdministratorUsername: "mail-cert-admin", AdministratorEmail: "mail-cert-admin@example.edu",
		AdministratorDisplayName: "Mail Certification Administrator", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	require.NoError(t, appErr)
	adminLogin, appErr := helper.App.Login(ctx, app.Invocation{}, app.LoginCommand{
		LoginID: bootstrap.Administrator.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1", DeviceName: "mail-cert-admin",
	})
	require.NoError(t, appErr)
	adminPrincipal, appErr := helper.App.AuthenticateAccess(ctx, adminLogin.Tokens.AccessToken)
	require.NoError(t, appErr)
	adminInvocation := app.NewInvocation(*adminPrincipal,
		model.RequestMetadata{RequestID: "mail-cert-admin", IPAddress: "127.0.0.1"})

	credentialUser, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "mail-cert-credential", Email: "mail-cert-credential@example.edu",
		DisplayName: "Mail Credential Candidate", Locale: "en", Timezone: "UTC",
	}, Password: password})
	require.NoError(t, appErr)
	credentialLogin, appErr := helper.App.Login(ctx, app.Invocation{}, app.LoginCommand{
		LoginID: credentialUser.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1", DeviceName: "mail-cert-credential",
	})
	require.NoError(t, appErr)
	credentialPrincipal, appErr := helper.App.AuthenticateAccess(ctx, credentialLogin.Tokens.AccessToken)
	require.NoError(t, appErr)
	appErr = helper.App.RequestEmailVerification(ctx,
		app.NewInvocation(*credentialPrincipal, model.RequestMetadata{RequestID: "mail-cert-verification"}),
		app.RequestEmailVerificationCommand{Source: "127.0.0.1:1"})
	require.NoError(t, appErr)

	securityUser, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "mail-cert-security", Email: "mail-cert-security@example.edu",
		DisplayName: "Mail Security Candidate", EmailVerified: true, Locale: "en", Timezone: "UTC",
	}, Password: password})
	require.NoError(t, appErr)
	_, appErr = helper.App.SetUserEnabled(ctx, adminInvocation, app.SetUserEnabledCommand{
		ID: securityUser.ID, Enabled: false, IdempotencyKey: "mail-cert-disable",
	})
	require.NoError(t, appErr)

	candidate, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "mail-cert-candidate", Email: "mail-cert-candidate@example.edu",
		DisplayName: "Mail Fanout Candidate", EmailVerified: true, Locale: "en", Timezone: "UTC",
	}, Password: password})
	require.NoError(t, appErr)
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: bootstrap.Institution.ID, Name: "mail-cert-unit", DisplayName: "Mail Certification Unit",
	})
	require.NoError(t, err)
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{
		AcademicUnitID: unit.ID, Name: "mail-cert-programme", DisplayName: "Mail Certification Programme",
	})
	require.NoError(t, err)
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "mail-cert-level", DisplayName: "Mail Certification Level",
	})
	require.NoError(t, err)
	now := model.NowUTC()
	period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(bootstrap.Institution.ID),
		Name:  "mail-cert-period", DisplayName: "Mail Certification Period",
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	class, err := storetest.SaveClass(ctx, persistence, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "mail-cert-class", DisplayName: "Mail Certification Class",
	})
	require.NoError(t, err)
	_, err = persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: now.Add(-time.Hour),
	})
	require.NoError(t, err)
	_, err = persistence.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: candidate.ID, StartsAt: now.Add(-time.Hour),
	})
	require.NoError(t, err)
	created, appErr := helper.App.CreateExam(ctx, adminInvocation, app.CreateExamCommand{
		AcademicUnitID: unit.ID, Title: "Mail Certification Exam", IdempotencyKey: "mail-cert-exam",
	})
	require.NoError(t, appErr)
	published, appErr := helper.App.PublishExamRevision(ctx, adminInvocation, app.PublishExamRevisionCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision, IdempotencyKey: "mail-cert-publish",
	})
	require.NoError(t, appErr)
	_, appErr = helper.App.ScheduleExamSitting(ctx, adminInvocation, app.ScheduleExamSittingCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: now.Add(2 * time.Hour), ScheduledEndAt: now.Add(4 * time.Hour),
		IdempotencyKey: "mail-cert-sitting",
	})
	require.NoError(t, appErr)

	startIntegrationServer(t, helper)
	expected := map[model.MailTemplateKey]string{
		model.MailTemplateIdentityVerifyEmail:     credentialUser.Email,
		model.MailTemplateIdentityAccountDisabled: securityUser.Email,
		model.MailTemplateExamSittingScheduled:    candidate.Email,
	}
	deliveries := waitForAcceptedCertificationMail(t, ctx, persistence, expected)
	messages := waitForMailpitMessages(t, mailpitURL, len(expected))
	require.Len(t, messages, len(expected), "Mailpit messages")
	byMessageID := make(map[string]certifiedMailDelivery, len(deliveries))
	for _, delivery := range deliveries {
		byMessageID[delivery.MessageID] = certifiedMailDelivery{
			delivery: delivery, recipient: expected[delivery.TemplateKey],
		}
	}
	for _, summary := range messages {
		raw := readMailpitRaw(t, mailpitURL, summary.ID)
		assertCertifiedSMTPMessage(t, raw, byMessageID)
	}
}

type mailpitMessageSummary struct {
	ID string `json:"ID"`
}

type certifiedMailDelivery struct {
	delivery  *model.MailDelivery
	recipient string
}

var mailpitHTTPClient = &http.Client{Timeout: 5 * time.Second}

func clearMailpit(t *testing.T, baseURL string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/messages", strings.NewReader(`{}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := mailpitHTTPClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		require.FailNow(t, fmt.Sprintf("clear Mailpit status = %d: %s", response.StatusCode, body))
	}
}

func waitForAcceptedCertificationMail(t *testing.T, ctx context.Context, persistence store.Store,
	expected map[model.MailTemplateKey]string,
) []*model.MailDelivery {
	t.Helper()
	keys := make([]model.MailTemplateKey, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
			TemplateKeys: keys, Limit: 20,
		})
		require.NoError(t, err)
		accepted := len(deliveries) == len(expected)
		for _, delivery := range deliveries {
			accepted = accepted && delivery.State == model.MailDeliveryAccepted && delivery.AcceptedAt.Valid &&
				len(delivery.EncryptedPayload) == 0 && expected[delivery.TemplateKey] != ""
		}
		if accepted {
			return deliveries
		}
		require.False(t, time.Now().After(deadline), "certification deliveries did not become accepted: %#v", deliveries)
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForMailpitMessages(t *testing.T, baseURL string, count int) []mailpitMessageSummary {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := mailpitHTTPClient.Get(fmt.Sprintf("%s/api/v1/messages?limit=%d", baseURL, count+5))
		require.NoError(t, err)
		var page struct {
			Messages []mailpitMessageSummary `json:"messages"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&page)
		response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode, "list Mailpit messages status")
		require.NoError(t, decodeErr, "list Mailpit messages status")
		if len(page.Messages) >= count {
			return page.Messages
		}
		require.False(t, time.Now().After(deadline), "Mailpit messages = %d, want %d", len(page.Messages), count)
		time.Sleep(20 * time.Millisecond)
	}
}

func readMailpitRaw(t *testing.T, baseURL, id string) []byte {
	t.Helper()
	response, err := mailpitHTTPClient.Get(baseURL + "/api/v1/message/" + id + "/raw")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode, "read Mailpit message %q status", id)
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	require.NoError(t, err)
	return raw
}

func assertCertifiedSMTPMessage(t *testing.T, raw []byte, deliveries map[string]certifiedMailDelivery) {
	t.Helper()
	message, err := netmail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	messageID := message.Header.Get("Message-ID")
	certified, ok := deliveries[messageID]
	require.True(t, ok, "SMTP Message-ID")
	require.NotNil(t, certified.delivery, "SMTP Message-ID")
	require.Equal(t, messageID, certified.delivery.MessageID, "SMTP Message-ID")
	recipients, err := message.Header.AddressList("To")
	require.NoError(t, err, "accepted recipient header")
	require.Len(t, recipients, 1, "accepted recipient header")
	require.Equal(t, certified.recipient, recipients[0].Address, "accepted recipient header")
	require.Equal(t, "auto-generated", message.Header.Get("Auto-Submitted"), "automation headers = Auto-Submitted")
	require.Equal(t, "All", message.Header.Get("X-Auto-Response-Suppress"), "automation headers = Auto-Submitted")
	require.Empty(t, message.Header.Get("Bcc"), "message exposed group recipients: Cc")
	require.Empty(t, message.Header.Get("Cc"), "message exposed group recipients: Cc")
	mediaType, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	require.NoError(t, err, "Content-Type")
	require.Equal(t, "multipart/alternative", mediaType, "Content-Type")
	require.NotEmpty(t, parameters["boundary"], "Content-Type")
	reader := multipart.NewReader(message.Body, parameters["boundary"])
	var partTypes []string
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		partType, _, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		require.NoError(t, parseErr)
		content, readErr := io.ReadAll(io.LimitReader(part, 1<<20))
		require.NoError(t, readErr, "multipart %s content is empty", partType)
		require.NotEmpty(t, bytes.TrimSpace(content), "multipart %s content is empty", partType)
		if partType == "multipart/related" {
			assertCertifiedInlineLogo(t, part.Header.Get("Content-Type"), content)
		}
		partTypes = append(partTypes, partType)
	}
	require.Len(t, partTypes, 2)
	require.Equal(t, "text/plain", partTypes[0])
	require.Equal(t, "multipart/related", partTypes[1])
}

func assertCertifiedInlineLogo(t *testing.T, contentType string, body []byte) {
	t.Helper()
	_, parameters, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.NotEmpty(t, parameters["boundary"])
	reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
	htmlPart, err := reader.NextPart()
	require.NoError(t, err)
	kind, _, err := mime.ParseMediaType(htmlPart.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "text/html", kind)
	htmlBody, err := io.ReadAll(io.LimitReader(htmlPart, 1<<20))
	require.NoError(t, err)
	imagePart, err := reader.NextPart()
	require.NoError(t, err)
	kind, _, err = mime.ParseMediaType(imagePart.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "image/png", kind)
	require.Equal(t, "base64", imagePart.Header.Get("Content-Transfer-Encoding"))
	disposition, _, err := mime.ParseMediaType(imagePart.Header.Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "inline", disposition)
	imageBytes, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, imagePart), 1<<20))
	require.NoError(t, err)
	config, err := png.DecodeConfig(bytes.NewReader(imageBytes))
	require.NoError(t, err)
	require.Equal(t, 600, config.Width)
	require.Equal(t, 118, config.Height)
	digest := sha256.Sum256(imageBytes)
	id := "proctor-lockup-" + hex.EncodeToString(digest[:]) + "@proctor"
	require.Equal(t, "<"+id+">", imagePart.Header.Get("Content-ID"), "logo Content-ID does not match its bytes and HTML reference")
	require.True(t, bytes.Contains(htmlBody, []byte(`src="cid:`+id+`"`)), "logo Content-ID does not match its bytes and HTML reference")
	_, err = reader.NextPart()
	require.Equal(t, io.EOF, err)
}
