package memory_test

import (
	"testing"

	"github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/packages/mail/mailtest"
	"github.com/sudosylabs/proctor/packages/mail/memory"
)

func TestConformance(t *testing.T) {
	mailtest.Run(t, func(t *testing.T) mail.Sender {
		t.Helper()
		sender, err := memory.New(mail.ComposerConfig{MessageIDDomain: "example.test"})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return sender
	})
}

func TestSnapshotIsolationAndReset(t *testing.T) {
	t.Parallel()

	sender, err := memory.New(mail.ComposerConfig{MessageIDDomain: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sender.Send(t.Context(), mail.Message{
		From:    mail.Address{Address: "from@example.test"},
		To:      []mail.Address{{Address: "to@example.test"}},
		Subject: "Stored",
		Text:    "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	first := sender.Deliveries()
	first[0].Data[0] = 'X'
	second := sender.Deliveries()
	if second[0].Data[0] == 'X' {
		t.Fatal("Deliveries() returned aliased data")
	}
	sender.Reset()
	if len(sender.Deliveries()) != 0 {
		t.Fatal("Reset() retained deliveries")
	}
}
