package smtp_test

import (
	"os"
	"testing"

	"github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/packages/mail/mailtest"
	mailsmtp "github.com/sudosylabs/proctor/packages/mail/smtp"
)

func TestIntegrationConformance(t *testing.T) {
	address := os.Getenv("MAIL_SMTP_ADDRESS")
	if address == "" {
		t.Skip("set MAIL_SMTP_ADDRESS to run SMTP integration tests")
	}
	factory := func(t *testing.T) mail.Sender {
		t.Helper()
		sender, err := mailsmtp.New(mailsmtp.Config{
			Address:         address,
			Security:        mailsmtp.SecurityNone,
			MessageIDDomain: "example.test",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if err := sender.Test(t.Context()); err != nil {
			t.Fatalf("Test() error = %v", err)
		}
		return sender
	}
	mailtest.Run(t, factory)
}
