package smtp_test

import (
	"crypto/tls"
	"testing"
	"time"

	mailsmtp "github.com/sudosylabs/proctor/packages/mail/smtp"
)

func TestNewValidatesSecurityAndAuthentication(t *testing.T) {
	t.Parallel()

	valid := mailsmtp.Config{
		Address:         "127.0.0.1:1025",
		Security:        mailsmtp.SecurityNone,
		MessageIDDomain: "example.test",
	}
	if _, err := mailsmtp.New(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*mailsmtp.Config)
	}{
		{name: "missing port", mutate: func(config *mailsmtp.Config) {
			config.Address = "localhost"
		}},
		{name: "unknown security", mutate: func(config *mailsmtp.Config) {
			config.Security = "opportunistic"
		}},
		{name: "insecure auth", mutate: func(config *mailsmtp.Config) {
			config.Username = "user"
			config.Password = "secret"
		}},
		{name: "credentials disabled", mutate: func(config *mailsmtp.Config) {
			config.Security = mailsmtp.SecurityTLS
			config.Authentication = mailsmtp.AuthenticationNone
			config.Username = "user"
		}},
		{name: "negative timeout", mutate: func(config *mailsmtp.Config) {
			config.Timeout = -time.Second
		}},
		{name: "invalid domain", mutate: func(config *mailsmtp.Config) {
			config.MessageIDDomain = "bad domain"
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			if _, err := mailsmtp.New(config); err == nil {
				t.Fatal("New() accepted invalid config")
			}
		})
	}
}

func TestNewClonesTLSConfig(t *testing.T) {
	t.Parallel()

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	sender, err := mailsmtp.New(mailsmtp.Config{
		Address:         "smtp.example.test:465",
		Security:        mailsmtp.SecurityTLS,
		TLSConfig:       tlsConfig,
		MessageIDDomain: "example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig.MinVersion = tls.VersionTLS10
	if sender.Capabilities().MaxMessageBytes == 0 {
		t.Fatal("sender has invalid capabilities")
	}
}
