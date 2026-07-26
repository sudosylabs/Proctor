package mail

import (
	"context"
	"time"
)

const (
	// DefaultMaxMessageBytes is the encoded message limit used when a sender
	// does not configure one explicitly.
	DefaultMaxMessageBytes int64 = 25 << 20
	// DefaultMaxRecipients matches a common SMTP server recipient limit.
	DefaultMaxRecipients = 100
)

// Capabilities describes optional message features and sender limits.
type Capabilities struct {
	HTML              bool
	Attachments       bool
	InlineAttachments bool
	CustomHeaders     bool
	MaxMessageBytes   int64
	MaxRecipients     int
}

// Sender delivers a transactional message.
type Sender interface {
	Capabilities() Capabilities
	Send(ctx context.Context, message Message) (Receipt, error)
}

// Tester is an optional transport connectivity check.
type Tester interface {
	Test(ctx context.Context) error
}

// Receipt identifies a successfully accepted message and its SMTP envelope
// recipients.
type Receipt struct {
	MessageID  string
	Recipients []string
}

// Attachment is an encoded file part. Inline attachments require a ContentID
// referenced by the HTML body, for example "cid:school-logo".
type Attachment struct {
	Filename    string
	ContentType string
	ContentID   string
	Inline      bool
	Data        []byte
}

// Message is a transport-neutral transactional email.
type Message struct {
	From         Address
	EnvelopeFrom string
	To           []Address
	CC           []Address
	BCC          []Address
	ReplyTo      []Address

	Subject string
	Text    string
	HTML    string

	Headers     map[string][]string
	Attachments []Attachment

	MessageID  string
	InReplyTo  string
	References []string
	Date       time.Time
}

// Delivery is a fully composed RFC 5322 message and SMTP envelope. Data is
// copied by the composer and by test senders when retained.
type Delivery struct {
	EnvelopeFrom string
	Recipients   []string
	MessageID    string
	Data         []byte
}
