package smtp_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/packages/mail"
	mailsmtp "github.com/sudosylabs/proctor/packages/mail/smtp"
)

func TestRecipientRejectionOutcomeIsClassified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		outcome  mail.Outcome
	}{
		{name: "temporary", response: "450 4.2.0 mailbox busy", outcome: mail.OutcomeTemporary},
		{name: "permanent", response: "550 5.1.1 recipient rejected", outcome: mail.OutcomePermanent},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			address, wait := startSMTPServer(t, func(connection net.Conn) error {
				return serveSMTP(connection, func(command string) string {
					if strings.HasPrefix(command, "RCPT TO:") {
						return test.response
					}
					return ""
				})
			})
			sender := newTestSender(t, address, mailsmtp.SecurityNone)
			_, err := sender.Send(t.Context(), testMessage())
			if !errors.Is(err, mail.ErrRejected) {
				t.Fatal("Send() error does not match ErrRejected")
			}
			if outcome := mail.Classify(err); outcome != test.outcome {
				t.Fatalf("Classify(Send() error) = %q, want %q", outcome, test.outcome)
			}
			wait()
		})
	}
}

func TestNetworkFailureBeforeTransmissionIsTemporary(t *testing.T) {
	t.Parallel()

	address, wait := startSMTPServer(t, func(net.Conn) error { return nil })
	sender := newTestSender(t, address, mailsmtp.SecurityNone)
	_, err := sender.Send(t.Context(), testMessage())
	if !errors.Is(err, mail.ErrConnection) {
		t.Fatal("Send() error does not match ErrConnection")
	}
	if outcome := mail.Classify(err); outcome != mail.OutcomeTemporary {
		t.Fatalf("Classify(Send() error) = %q, want %q", outcome, mail.OutcomeTemporary)
	}
	wait()
}

func TestConnectionLossAfterTransmissionIsAcceptanceUncertain(t *testing.T) {
	t.Parallel()

	address, wait := startSMTPServer(t, serveSMTPThenDropBeforeCommitResponse)
	sender := newTestSender(t, address, mailsmtp.SecurityNone)
	_, err := sender.Send(t.Context(), testMessage())
	if !errors.Is(err, mail.ErrConnection) {
		t.Fatal("Send() error does not match ErrConnection")
	}
	if outcome := mail.Classify(err); outcome != mail.OutcomeAcceptanceUncertain {
		t.Fatalf(
			"Classify(Send() error) = %q, want %q",
			outcome,
			mail.OutcomeAcceptanceUncertain,
		)
	}
	wait()
}

func TestSTARTTLSIsRequired(t *testing.T) {
	t.Parallel()

	address, wait := startSMTPServer(t, func(connection net.Conn) error {
		return serveSMTP(connection, nil)
	})
	sender := newTestSender(t, address, mailsmtp.SecuritySTARTTLS)
	err := sender.Test(t.Context())
	if !errors.Is(err, mail.ErrTLS) {
		t.Fatalf("Test() error = %v, want ErrTLS", err)
	}
	wait()
}

func TestGreetingHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	address, wait := startSMTPServer(t, func(connection net.Conn) error {
		_, err := bufio.NewReader(connection).ReadString('\n')
		if err == nil {
			return errors.New("client unexpectedly sent data before SMTP greeting")
		}
		return nil
	})
	sender := newTestSender(t, address, mailsmtp.SecurityNone)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := sender.Test(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Test() error = %v, want context.DeadlineExceeded", err)
	}
	wait()
}

func startSMTPServer(t *testing.T, serve func(net.Conn) error) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	result := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			result <- acceptErr
			return
		}
		defer connection.Close()
		result <- serve(connection)
	}()
	wait := func() {
		t.Helper()
		_ = listener.Close()
		select {
		case serveErr := <-result:
			if serveErr != nil && !errors.Is(serveErr, net.ErrClosed) {
				t.Fatalf("SMTP test server error = %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Fatal("SMTP test server did not stop")
		}
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	return listener.Addr().String(), wait
}

func serveSMTP(connection net.Conn, response func(command string) string) error {
	writer := bufio.NewWriter(connection)
	if _, err := writer.WriteString("220 test.example ESMTP ready\r\n"); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(connection)
	for scanner.Scan() {
		command := scanner.Text()
		if custom := responseFor(response, command); custom != "" {
			if _, err := writer.WriteString(custom + "\r\n"); err != nil {
				return err
			}
		} else {
			switch {
			case strings.HasPrefix(command, "EHLO "):
				if _, err := writer.WriteString("250-test.example\r\n250 SIZE 26214400\r\n"); err != nil {
					return err
				}
			case strings.HasPrefix(command, "HELO "),
				strings.HasPrefix(command, "MAIL FROM:"),
				strings.HasPrefix(command, "RCPT TO:"),
				command == "RSET":
				if _, err := writer.WriteString("250 2.0.0 ok\r\n"); err != nil {
					return err
				}
			case command == "QUIT":
				if _, err := writer.WriteString("221 2.0.0 bye\r\n"); err != nil {
					return err
				}
				return writer.Flush()
			default:
				return fmt.Errorf("unexpected SMTP command %q", command)
			}
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func serveSMTPThenDropBeforeCommitResponse(connection net.Conn) error {
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	if _, err := writer.WriteString("220 test.example ESMTP ready\r\n"); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		command := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case strings.HasPrefix(command, "EHLO "):
			if _, err := writer.WriteString("250-test.example\r\n250 SIZE 26214400\r\n"); err != nil {
				return err
			}
		case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
			if _, err := writer.WriteString("250 2.0.0 ok\r\n"); err != nil {
				return err
			}
		case command == "DATA":
			if _, err := writer.WriteString("354 send message\r\n"); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return err
				}
				if line == ".\r\n" {
					return nil
				}
			}
		default:
			return fmt.Errorf("unexpected SMTP command %q", command)
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func responseFor(response func(string) string, command string) string {
	if response == nil {
		return ""
	}
	return response(command)
}

func newTestSender(t *testing.T, address string, security mailsmtp.Security) *mailsmtp.Sender {
	t.Helper()
	sender, err := mailsmtp.New(mailsmtp.Config{
		Address:         address,
		Security:        security,
		Timeout:         time.Second,
		MessageIDDomain: "example.test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return sender
}

func testMessage() mail.Message {
	return mail.Message{
		From:    mail.Address{Address: "from@example.test"},
		To:      []mail.Address{{Address: "to@example.test"}},
		Subject: "Test",
		Text:    "body",
	}
}
