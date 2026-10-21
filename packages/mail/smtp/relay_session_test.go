package smtp_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/packages/mail"
	mailsmtp "github.com/sudosylabs/proctor/packages/mail/smtp"
)

// Relay session failures are outages an operator repairs, not properties of
// the message, so every one of them must be retryable.

func TestMissingSTARTTLSOfferIsTemporary(t *testing.T) {
	t.Parallel()

	address, wait := startSMTPServer(t, func(connection net.Conn) error {
		return serveSMTP(connection, nil)
	})
	sender := newTestSender(t, address, mailsmtp.SecuritySTARTTLS)
	_, err := sender.Send(t.Context(), testMessage())
	requireRelaySessionOutage(t, err, mail.ErrTLS)
	wait()
}

func TestUntrustedRelayCertificateIsTemporary(t *testing.T) {
	t.Parallel()

	serverTLS, _ := testCertificate(t)
	address, wait := startSMTPServer(t, func(connection net.Conn) error {
		// The client rejects the certificate, so the handshake fails here too.
		_ = tls.Server(connection, serverTLS.Clone()).HandshakeContext(context.Background())
		return nil
	})
	sender, err := mailsmtp.New(mailsmtp.Config{
		Address: address, ServerName: "localhost", Security: mailsmtp.SecurityTLS,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, Timeout: 2 * time.Second, MessageIDDomain: "example.test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = sender.Send(t.Context(), testMessage())
	requireRelaySessionOutage(t, err, mail.ErrTLS)
	wait()
}

func TestRejectedRelayCredentialsAreTemporary(t *testing.T) {
	t.Parallel()

	serverTLS, roots := testCertificate(t)
	address, wait := startSMTPServer(t, func(connection net.Conn) error {
		return serveSTARTTLSRejectingAuthentication(connection, serverTLS)
	})
	sender, err := mailsmtp.New(mailsmtp.Config{
		Address: address, ServerName: "localhost", Security: mailsmtp.SecuritySTARTTLS,
		TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		Username:  "user", Password: "rotated", Authentication: mailsmtp.AuthenticationPlain,
		Timeout: 2 * time.Second, MessageIDDomain: "example.test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = sender.Send(t.Context(), testMessage())
	requireRelaySessionOutage(t, err, mail.ErrAuthentication)
	wait()
}

func requireRelaySessionOutage(t *testing.T, err, sentinel error) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("Send() error = %v, want %v", err, sentinel)
	}
	if outcome := mail.Classify(err); outcome != mail.OutcomeTemporary {
		t.Fatalf("Classify(Send() error) = %q, want %q", outcome, mail.OutcomeTemporary)
	}
}

func serveSTARTTLSRejectingAuthentication(connection net.Conn, serverTLS *tls.Config) error {
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	if err := writeSMTP(writer, "220 test.example ESMTP ready"); err != nil {
		return err
	}
	if _, err := readSMTP(reader); err != nil {
		return err
	}
	if err := writeSMTP(writer, "250-test.example", "250 STARTTLS"); err != nil {
		return err
	}
	if _, err := readSMTP(reader); err != nil {
		return err
	}
	if err := writeSMTP(writer, "220 2.0.0 ready for TLS"); err != nil {
		return err
	}
	tlsConnection := tls.Server(connection, serverTLS.Clone())
	if err := tlsConnection.HandshakeContext(context.Background()); err != nil {
		return err
	}
	reader = bufio.NewReader(tlsConnection)
	writer = bufio.NewWriter(tlsConnection)
	for {
		command, err := readSMTP(reader)
		if err != nil {
			return nil
		}
		switch {
		case strings.HasPrefix(command, "EHLO "):
			err = writeSMTP(writer, "250-test.example", "250 AUTH PLAIN LOGIN")
		case strings.HasPrefix(command, "AUTH "):
			err = writeSMTP(writer, "535 5.7.8 authentication credentials invalid")
		case command == "QUIT":
			return writeSMTP(writer, "221 2.0.0 bye")
		default:
			err = writeSMTP(writer, "501 5.5.2 cancelled")
		}
		if err != nil {
			return nil
		}
	}
}
