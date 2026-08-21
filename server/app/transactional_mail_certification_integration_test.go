//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	netmail "net/mail"
	"os"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestTransactionalMailPhaseCertification(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	smtpAddress := strings.TrimSpace(os.Getenv("PROCTOR_TEST_MAIL_SMTP_ADDRESS"))
	mailpitURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PROCTOR_TEST_MAILPIT_HTTP_URL")), "/")
	if smtpAddress == "" || mailpitURL == "" {
		t.Fatal("PROCTOR_TEST_MAIL_SMTP_ADDRESS and PROCTOR_TEST_MAILPIT_HTTP_URL must be set")
	}
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
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "mail-certification", InstitutionDisplayName: "Mail Certification University",
		AdministratorUsername: "mail-cert-admin", AdministratorEmail: "mail-cert-admin@example.edu",
		AdministratorDisplayName: "Mail Certification Administrator", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	adminLogin, appErr := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: bootstrap.Administrator.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1", DeviceName: "mail-cert-admin",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	adminPrincipal, appErr := helper.App.AuthenticateAccess(ctx, adminLogin.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	adminInvocation := application.NewInvocation(*adminPrincipal,
		model.RequestMetadata{RequestID: "mail-cert-admin", IPAddress: "127.0.0.1"})

	credentialUser, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "mail-cert-credential", Email: "mail-cert-credential@example.edu",
		DisplayName: "Mail Credential Candidate", Locale: "en", Timezone: "UTC",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	credentialLogin, appErr := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: credentialUser.Username, Password: password, ClientType: model.SessionClientWeb,
		Source: "127.0.0.1:1", DeviceName: "mail-cert-credential",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	credentialPrincipal, appErr := helper.App.AuthenticateAccess(ctx, credentialLogin.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if appErr = helper.App.RequestEmailVerification(ctx,
		application.NewInvocation(*credentialPrincipal, model.RequestMetadata{RequestID: "mail-cert-verification"}),
		application.RequestEmailVerificationCommand{Source: "127.0.0.1:1"}); appErr != nil {
		t.Fatal(appErr)
	}

	securityUser, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "mail-cert-security", Email: "mail-cert-security@example.edu",
		DisplayName: "Mail Security Candidate", EmailVerified: true, Locale: "en", Timezone: "UTC",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr = helper.App.SetUserEnabled(ctx, adminInvocation, application.SetUserEnabledCommand{
		ID: securityUser.ID.String(), Enabled: false, IdempotencyKey: "mail-cert-disable",
	}); appErr != nil {
		t.Fatal(appErr)
	}

	candidate, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "mail-cert-candidate", Email: "mail-cert-candidate@example.edu",
		DisplayName: "Mail Fanout Candidate", EmailVerified: true, Locale: "en", Timezone: "UTC",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: bootstrap.Institution.ID, Name: "mail-cert-unit", DisplayName: "Mail Certification Unit",
	})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitID: unit.ID, Name: "mail-cert-programme", DisplayName: "Mail Certification Programme",
	})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "mail-cert-level", DisplayName: "Mail Certification Level",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := model.NowUTC()
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(bootstrap.Institution.ID),
		Name:  "mail-cert-period", DisplayName: "Mail Certification Period",
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	class, err := persistence.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "mail-cert-class", DisplayName: "Mail Certification Class",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: candidate.ID, StartsAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	created, appErr := helper.App.CreateExam(ctx, adminInvocation, application.CreateExamCommand{
		AcademicUnitID: unit.ID, Title: "Mail Certification Exam", IdempotencyKey: "mail-cert-exam",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	published, appErr := helper.App.PublishExamRevision(ctx, adminInvocation, application.PublishExamRevisionCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision, IdempotencyKey: "mail-cert-publish",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr = helper.App.ScheduleExamSitting(ctx, adminInvocation, application.ScheduleExamSittingCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: now.Add(2 * time.Hour), ScheduledEndAt: now.Add(4 * time.Hour),
		IdempotencyKey: "mail-cert-sitting",
	}); appErr != nil {
		t.Fatal(appErr)
	}

	startIntegrationServer(t, helper)
	expected := map[model.MailTemplateKey]string{
		model.MailTemplateIdentityVerifyEmail:     credentialUser.Email,
		model.MailTemplateIdentityAccountDisabled: securityUser.Email,
		model.MailTemplateExamSittingScheduled:    candidate.Email,
	}
	deliveries := waitForAcceptedCertificationMail(t, ctx, persistence, expected)
	messages := waitForMailpitMessages(t, mailpitURL, len(expected))
	if len(messages) != len(expected) {
		t.Fatalf("Mailpit messages = %d, want %d", len(messages), len(expected))
	}
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
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := mailpitHTTPClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("clear Mailpit status = %d: %s", response.StatusCode, body)
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
		if err != nil {
			t.Fatal(err)
		}
		accepted := len(deliveries) == len(expected)
		for _, delivery := range deliveries {
			accepted = accepted && delivery.State == model.MailDeliveryAccepted && delivery.AcceptedAt.Valid &&
				len(delivery.EncryptedPayload) == 0 && expected[delivery.TemplateKey] != ""
		}
		if accepted {
			return deliveries
		}
		if time.Now().After(deadline) {
			t.Fatalf("certification deliveries did not become accepted: %#v", deliveries)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForMailpitMessages(t *testing.T, baseURL string, count int) []mailpitMessageSummary {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := mailpitHTTPClient.Get(fmt.Sprintf("%s/api/v1/messages?limit=%d", baseURL, count+5))
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Messages []mailpitMessageSummary `json:"messages"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&page)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil {
			t.Fatalf("list Mailpit messages status=%d decode=%v", response.StatusCode, decodeErr)
		}
		if len(page.Messages) >= count {
			return page.Messages
		}
		if time.Now().After(deadline) {
			t.Fatalf("Mailpit messages = %d, want %d", len(page.Messages), count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readMailpitRaw(t *testing.T, baseURL, id string) []byte {
	t.Helper()
	response, err := mailpitHTTPClient.Get(baseURL + "/api/v1/message/" + id + "/raw")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("read Mailpit message %q status = %d", id, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertCertifiedSMTPMessage(t *testing.T, raw []byte, deliveries map[string]certifiedMailDelivery) {
	t.Helper()
	message, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	messageID := message.Header.Get("Message-ID")
	certified, ok := deliveries[messageID]
	if !ok || certified.delivery == nil || certified.delivery.MessageID != messageID {
		t.Fatalf("SMTP Message-ID %q has no matching durable delivery", messageID)
	}
	recipients, err := message.Header.AddressList("To")
	if err != nil || len(recipients) != 1 || recipients[0].Address != certified.recipient {
		t.Fatalf("accepted recipient header = %#v, want %q: %v", recipients, certified.recipient, err)
	}
	if message.Header.Get("Auto-Submitted") != "auto-generated" ||
		message.Header.Get("X-Auto-Response-Suppress") != "All" {
		t.Fatalf("automation headers = Auto-Submitted:%q X-Auto-Response-Suppress:%q",
			message.Header.Get("Auto-Submitted"), message.Header.Get("X-Auto-Response-Suppress"))
	}
	if message.Header.Get("Bcc") != "" || message.Header.Get("Cc") != "" {
		t.Fatalf("message exposed group recipients: Cc=%q Bcc=%q", message.Header.Get("Cc"), message.Header.Get("Bcc"))
	}
	mediaType, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" || parameters["boundary"] == "" {
		t.Fatalf("Content-Type = %q, %v", message.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(message.Body, parameters["boundary"])
	var partTypes []string
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		partType, _, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		content, readErr := io.ReadAll(io.LimitReader(part, 1<<20))
		if readErr != nil || len(bytes.TrimSpace(content)) == 0 {
			t.Fatalf("multipart %s content is empty: %v", partType, readErr)
		}
		partTypes = append(partTypes, partType)
	}
	if len(partTypes) != 2 || partTypes[0] != "text/plain" || partTypes[1] != "text/html" {
		t.Fatalf("multipart alternatives = %#v", partTypes)
	}
}
