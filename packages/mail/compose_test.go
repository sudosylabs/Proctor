package mail_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/packages/mail"
)

func TestComposerRichMessage(t *testing.T) {
	t.Parallel()

	composer := mustComposer(t, mail.ComposerConfig{MessageIDDomain: "example.test"})
	message := mail.Message{
		From:         mail.Address{Name: "École Proctor", Address: "noreply@example.test"},
		EnvelopeFrom: "bounces@example.test",
		To: []mail.Address{
			{Name: "Student One", Address: "one@example.test"},
			{Name: "Duplicate", Address: "audit@example.test"},
		},
		CC:      []mail.Address{{Address: "teacher@example.test"}},
		BCC:     []mail.Address{{Address: "audit@example.test"}},
		ReplyTo: []mail.Address{{Name: "Support", Address: "support@example.test"}},
		Subject: "Résultats disponibles",
		Text:    "Hello,\nYour results are ready.",
		HTML:    `<p>Hello, <img src="cid:logo"> your results are ready.</p>`,
		Headers: map[string][]string{
			"X-Category": {"results"},
		},
		Attachments: []mail.Attachment{
			{
				Filename:    "logo.png",
				ContentType: "image/png",
				ContentID:   "logo",
				Inline:      true,
				Data:        []byte{0x89, 'P', 'N', 'G'},
			},
			{
				Filename:    "résultats.txt",
				ContentType: "text/plain; charset=utf-8",
				Data:        []byte("score: 10/10"),
			},
		},
		MessageID: "<fixed@example.test>",
		InReplyTo: "<parent@example.test>",
		References: []string{
			"<root@example.test>",
			"<parent@example.test>",
		},
		Date: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}

	delivery, err := composer.Compose(message)
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	if delivery.EnvelopeFrom != "bounces@example.test" {
		t.Fatalf("EnvelopeFrom = %q", delivery.EnvelopeFrom)
	}
	wantRecipients := []string{"one@example.test", "audit@example.test", "teacher@example.test"}
	if strings.Join(delivery.Recipients, ",") != strings.Join(wantRecipients, ",") {
		t.Fatalf("Recipients = %v, want %v", delivery.Recipients, wantRecipients)
	}
	assertCRLF(t, delivery.Data)

	parsed, err := stdmail.ReadMessage(bytes.NewReader(delivery.Data))
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if parsed.Header.Get("Bcc") != "" {
		t.Fatal("Bcc leaked into the message headers")
	}
	if parsed.Header.Get("X-Category") != "results" {
		t.Fatalf("X-Category = %q", parsed.Header.Get("X-Category"))
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != message.Subject {
		t.Fatalf("decoded subject = %q, %v", subject, err)
	}
	if parsed.Header.Get("Message-ID") != message.MessageID {
		t.Fatalf("Message-ID = %q", parsed.Header.Get("Message-ID"))
	}

	parts := collectParts(t, parsed.Header, parsed.Body)
	assertPart(t, parts, "text/plain", "", "Hello,\r\nYour results are ready.")
	assertPart(t, parts, "text/html", "", message.HTML)
	assertPart(t, parts, "image/png", "inline", string([]byte{0x89, 'P', 'N', 'G'}))
	assertPart(t, parts, "text/plain", "attachment", "score: 10/10")
}

func TestComposerGeneratesUniqueMessageIDs(t *testing.T) {
	t.Parallel()

	composer := mustComposer(t, mail.ComposerConfig{MessageIDDomain: "school.example"})
	message := basicMessage()
	first, err := composer.Compose(message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := composer.Compose(message)
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID == second.MessageID {
		t.Fatalf("generated duplicate message ID %q", first.MessageID)
	}
	for _, messageID := range []string{first.MessageID, second.MessageID} {
		if !strings.HasSuffix(messageID, "@school.example>") {
			t.Fatalf("message ID %q has the wrong domain", messageID)
		}
	}
}

func TestComposerRejectsUnsafeAndLargeMessages(t *testing.T) {
	t.Parallel()

	composer := mustComposer(t, mail.ComposerConfig{
		MessageIDDomain: "example.test",
		MaxMessageBytes: 512,
	})
	tests := []struct {
		name    string
		mutate  func(*mail.Message)
		wantErr error
	}{
		{
			name: "header injection",
			mutate: func(message *mail.Message) {
				message.Headers = map[string][]string{"X-Test": {"safe\r\nBcc: attacker@example.test"}}
			},
			wantErr: mail.ErrInvalidHeader,
		},
		{
			name: "reserved header",
			mutate: func(message *mail.Message) {
				message.Headers = map[string][]string{"Subject": {"replacement"}}
			},
			wantErr: mail.ErrInvalidHeader,
		},
		{
			name: "unsafe attachment",
			mutate: func(message *mail.Message) {
				message.Attachments = []mail.Attachment{{Filename: "../secret.txt", Data: []byte("x")}}
			},
			wantErr: mail.ErrInvalidMessage,
		},
		{
			name: "inline without html",
			mutate: func(message *mail.Message) {
				message.Attachments = []mail.Attachment{{
					Filename: "logo.png", ContentID: "logo", Inline: true,
				}}
			},
			wantErr: mail.ErrInvalidMessage,
		},
		{
			name: "encoded size",
			mutate: func(message *mail.Message) {
				message.Text = strings.Repeat("=", 500)
			},
			wantErr: mail.ErrMessageTooLarge,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			message := basicMessage()
			test.mutate(&message)
			_, err := composer.Compose(message)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Compose() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

type decodedPart struct {
	mediaType   string
	disposition string
	body        string
}

func collectParts(t *testing.T, headers stdmail.Header, body io.Reader) []decodedPart {
	t.Helper()
	contentType := headers.Get("Content-Type")
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", contentType, err)
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(body, parameters["boundary"])
		var parts []decodedPart
		for {
			part, nextErr := reader.NextPart()
			if errors.Is(nextErr, io.EOF) {
				return parts
			}
			if nextErr != nil {
				t.Fatalf("NextPart() error = %v", nextErr)
			}
			nestedHeaders := stdmail.Header(part.Header)
			parts = append(parts, collectParts(t, nestedHeaders, part)...)
		}
	}

	data, err := io.ReadAll(decodeBody(t, headers, body))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	disposition, _, _ := mime.ParseMediaType(headers.Get("Content-Disposition"))
	return []decodedPart{{
		mediaType:   mediaType,
		disposition: disposition,
		body:        string(data),
	}}
}

func decodeBody(t *testing.T, headers stdmail.Header, body io.Reader) io.Reader {
	t.Helper()
	switch strings.ToLower(headers.Get("Content-Transfer-Encoding")) {
	case "", "7bit", "8bit":
		return body
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	default:
		t.Fatalf("unknown transfer encoding %q", headers.Get("Content-Transfer-Encoding"))
		return nil
	}
}

func assertPart(t *testing.T, parts []decodedPart, mediaType, disposition, body string) {
	t.Helper()
	for _, part := range parts {
		if part.mediaType == mediaType && part.disposition == disposition && part.body == body {
			return
		}
	}
	t.Fatalf("missing part type=%q disposition=%q body=%q; got %#v", mediaType, disposition, body, parts)
}

func assertCRLF(t *testing.T, data []byte) {
	t.Helper()
	for index, value := range data {
		if value == '\n' && (index == 0 || data[index-1] != '\r') {
			t.Fatalf("message contains a lone LF at byte %d", index)
		}
	}
}

func mustComposer(t *testing.T, config mail.ComposerConfig) *mail.Composer {
	t.Helper()
	composer, err := mail.NewComposer(config)
	if err != nil {
		t.Fatalf("NewComposer() error = %v", err)
	}
	return composer
}

func basicMessage() mail.Message {
	return mail.Message{
		From:    mail.Address{Name: "Proctor", Address: "noreply@example.test"},
		To:      []mail.Address{{Address: "student@example.test"}},
		Subject: "Test message",
		Text:    "Hello",
	}
}
