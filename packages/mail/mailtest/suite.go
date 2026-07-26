// Package mailtest contains a reusable Sender conformance suite.
package mailtest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/sudosylabs/proctor/packages/mail"
)

// Factory returns a sender isolated enough for one conformance subtest.
type Factory func(t *testing.T) mail.Sender

// Run exercises the portable Sender contract and advertised features.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("plain message", func(t *testing.T) {
		sender := factory(t)
		receipt, err := sender.Send(context.Background(), basicMessage("plain"))
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if receipt.MessageID == "" {
			t.Fatal("Send() returned an empty message ID")
		}
		if len(receipt.Recipients) != 1 || receipt.Recipients[0] != "student@example.test" {
			t.Fatalf("Send() recipients = %v", receipt.Recipients)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		sender := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := sender.Send(ctx, basicMessage("cancelled"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Send() error = %v, want context.Canceled", err)
		}
	})

	t.Run("invalid message", func(t *testing.T) {
		sender := factory(t)
		message := basicMessage("invalid")
		message.To = nil
		_, err := sender.Send(context.Background(), message)
		if !errors.Is(err, mail.ErrInvalidMessage) {
			t.Fatalf("Send() error = %v, want ErrInvalidMessage", err)
		}
	})

	t.Run("rich message", func(t *testing.T) {
		sender := factory(t)
		capabilities := sender.Capabilities()
		if !capabilities.HTML || !capabilities.Attachments ||
			!capabilities.InlineAttachments || !capabilities.CustomHeaders {
			t.Skip("sender does not advertise the full rich-message contract")
		}
		message := basicMessage("rich")
		message.Text = "Plain fallback"
		message.HTML = `<p>Hello <img src="cid:logo"></p>`
		message.CC = []mail.Address{{Name: "Teacher", Address: "teacher@example.test"}}
		message.BCC = []mail.Address{{Address: "audit@example.test"}}
		message.ReplyTo = []mail.Address{{Address: "support@example.test"}}
		message.Headers = map[string][]string{"X-Category": {"conformance"}}
		message.Attachments = []mail.Attachment{
			{
				Filename:    "logo.png",
				ContentType: "image/png",
				ContentID:   "logo",
				Inline:      true,
				Data:        []byte("not-a-real-png"),
			},
			{
				Filename:    "instructions.txt",
				ContentType: "text/plain; charset=utf-8",
				Data:        []byte("Read carefully."),
			},
		}
		receipt, err := sender.Send(context.Background(), message)
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(receipt.Recipients) != 3 {
			t.Fatalf("Send() recipients = %v, want 3 envelope recipients", receipt.Recipients)
		}
	})

	t.Run("concurrent sends", func(t *testing.T) {
		sender := factory(t)
		const workers = 12
		var wait sync.WaitGroup
		errorsChannel := make(chan error, workers)
		for index := range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, err := sender.Send(context.Background(), basicMessage("parallel-"+strconv.Itoa(index)))
				errorsChannel <- err
			}()
		}
		wait.Wait()
		close(errorsChannel)
		for err := range errorsChannel {
			if err != nil {
				t.Fatalf("parallel Send() error = %v", err)
			}
		}
	})
}

func basicMessage(suffix string) mail.Message {
	return mail.Message{
		From:    mail.Address{Name: "Proctor", Address: "noreply@example.test"},
		To:      []mail.Address{{Name: "Student", Address: "student@example.test"}},
		Subject: "Conformance " + suffix,
		Text:    "This is a conformance message.",
	}
}
