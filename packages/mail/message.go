package mail

import (
	"fmt"
	"mime"
	"net/textproto"
	"path/filepath"
	"strings"
)

const maximumCustomHeaders = 50
const maximumHeaderValueBytes = 900

var reservedHeaders = map[string]struct{}{
	"Bcc":                       {},
	"Cc":                        {},
	"Content-Disposition":       {},
	"Content-Id":                {},
	"Content-Transfer-Encoding": {},
	"Content-Type":              {},
	"Date":                      {},
	"From":                      {},
	"In-Reply-To":               {},
	"Message-Id":                {},
	"Mime-Version":              {},
	"References":                {},
	"Reply-To":                  {},
	"Subject":                   {},
	"To":                        {},
}

// Validate checks a message using the package's default limits.
func (m Message) Validate() error {
	return m.validate(DefaultMaxRecipients, DefaultMaxMessageBytes)
}

func (m Message) validate(maxRecipients int, maxBytes int64) error {
	if err := m.From.Validate(); err != nil {
		return fmt.Errorf("%w: from: %w", ErrInvalidMessage, err)
	}
	if m.EnvelopeFrom != "" {
		if err := (Address{Address: m.EnvelopeFrom}).Validate(); err != nil {
			return fmt.Errorf("%w: envelope from: %w", ErrInvalidMessage, err)
		}
	}
	if len(m.To)+len(m.CC)+len(m.BCC) == 0 {
		return fmt.Errorf("%w: at least one recipient is required", ErrInvalidMessage)
	}
	if err := validateAddresses("to", m.To); err != nil {
		return err
	}
	if err := validateAddresses("cc", m.CC); err != nil {
		return err
	}
	if err := validateAddresses("bcc", m.BCC); err != nil {
		return err
	}
	if err := validateAddresses("reply-to", m.ReplyTo); err != nil {
		return err
	}

	recipients := uniqueRecipients(m)
	if maxRecipients < 1 {
		return fmt.Errorf("%w: recipient limit must be positive", ErrInvalidMessage)
	}
	if len(recipients) > maxRecipients {
		return fmt.Errorf("%w: %d recipients exceed limit %d", ErrInvalidMessage, len(recipients), maxRecipients)
	}
	if strings.TrimSpace(m.Subject) == "" || unsafeHeaderValue(m.Subject) {
		return fmt.Errorf("%w: subject is empty or unsafe", ErrInvalidMessage)
	}
	if len(m.Subject) > 255 {
		return fmt.Errorf("%w: subject exceeds 255 bytes", ErrInvalidMessage)
	}
	if m.Text == "" && m.HTML == "" {
		return fmt.Errorf("%w: text or HTML body is required", ErrInvalidMessage)
	}

	if err := validateMessageID("message-id", m.MessageID, true); err != nil {
		return err
	}
	if err := validateMessageID("in-reply-to", m.InReplyTo, true); err != nil {
		return err
	}
	for _, reference := range m.References {
		if err := validateMessageID("reference", reference, false); err != nil {
			return err
		}
	}
	if err := validateHeaders(m.Headers); err != nil {
		return err
	}
	if err := validateAttachments(m.Attachments, m.HTML != ""); err != nil {
		return err
	}

	if maxBytes < 1 {
		return fmt.Errorf("%w: message size limit must be positive", ErrInvalidMessage)
	}
	var inputBytes int64
	for _, value := range []string{m.Subject, m.Text, m.HTML} {
		if int64(len(value)) > maxBytes-inputBytes {
			return ErrMessageTooLarge
		}
		inputBytes += int64(len(value))
	}
	for _, attachment := range m.Attachments {
		if int64(len(attachment.Data)) > maxBytes-inputBytes {
			return ErrMessageTooLarge
		}
		inputBytes += int64(len(attachment.Data))
	}
	return nil
}

func validateAddresses(field string, addresses []Address) error {
	for i, address := range addresses {
		if err := address.Validate(); err != nil {
			return fmt.Errorf("%w: %s[%d]: %w", ErrInvalidMessage, field, i, err)
		}
	}
	return nil
}

func validateMessageID(field, value string, optional bool) error {
	if value == "" && optional {
		return nil
	}
	if len(value) < 5 || value[0] != '<' || value[len(value)-1] != '>' ||
		unsafeHeaderValue(value) ||
		strings.ContainsAny(value[1:len(value)-1], "<> \t") ||
		len(value) > maximumHeaderValueBytes {
		return fmt.Errorf("%w: invalid %s", ErrInvalidMessage, field)
	}
	identifier := value[1 : len(value)-1]
	at := strings.LastIndexByte(identifier, '@')
	if at < 1 || at != strings.IndexByte(identifier, '@') ||
		!validDotAtom(identifier[:at]) || !validMessageIDDomain(identifier[at+1:]) {
		return fmt.Errorf("%w: invalid %s", ErrInvalidMessage, field)
	}
	return nil
}

func validDotAtom(value string) bool {
	if value == "" || value[0] == '.' || value[len(value)-1] == '.' ||
		strings.Contains(value, "..") {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("!#$%&'*+-/=?^_`{|}~.", character) {
			continue
		}
		return false
	}
	return true
}

func validateHeaders(headers map[string][]string) error {
	if len(headers) > maximumCustomHeaders {
		return fmt.Errorf("%w: more than %d custom headers", ErrInvalidHeader, maximumCustomHeaders)
	}
	seen := make(map[string]struct{}, len(headers))
	for name, values := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if name == "" || len(name) > 78 || canonical == "" {
			return fmt.Errorf("%w: invalid header name %q", ErrInvalidHeader, name)
		}
		lower := strings.ToLower(canonical)
		if _, duplicate := seen[lower]; duplicate {
			return fmt.Errorf("%w: duplicate header name %q", ErrInvalidHeader, name)
		}
		seen[lower] = struct{}{}
		if _, reserved := reservedHeaders[canonical]; reserved {
			return fmt.Errorf("%w: header %q is managed by the composer", ErrInvalidHeader, name)
		}
		if len(values) == 0 {
			return fmt.Errorf("%w: header %q has no values", ErrInvalidHeader, name)
		}
		for _, value := range values {
			if value == "" || unsafeHeaderValue(value) || !isASCII(value) ||
				len(value) > maximumHeaderValueBytes {
				return fmt.Errorf("%w: unsafe value for header %q", ErrInvalidHeader, name)
			}
		}
	}
	return nil
}

func validateAttachments(attachments []Attachment, hasHTML bool) error {
	contentIDs := make(map[string]struct{})
	for i, attachment := range attachments {
		if attachment.Filename == "" || filepath.Base(attachment.Filename) != attachment.Filename ||
			strings.ContainsAny(attachment.Filename, `/\`) || unsafeHeaderValue(attachment.Filename) ||
			len(attachment.Filename) > 255 {
			return fmt.Errorf("%w: attachment[%d] has an unsafe filename", ErrInvalidMessage, i)
		}
		if attachment.ContentType != "" {
			mediaType, _, err := mime.ParseMediaType(attachment.ContentType)
			if err != nil || strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
				return fmt.Errorf("%w: attachment[%d] has an invalid content type", ErrInvalidMessage, i)
			}
		}
		if attachment.Inline {
			if !hasHTML || attachment.ContentID == "" || unsafeHeaderValue(attachment.ContentID) ||
				strings.ContainsAny(attachment.ContentID, "<> \t") {
				return fmt.Errorf("%w: attachment[%d] has invalid inline metadata", ErrInvalidMessage, i)
			}
			if _, exists := contentIDs[attachment.ContentID]; exists {
				return fmt.Errorf("%w: duplicate inline content ID %q", ErrInvalidMessage, attachment.ContentID)
			}
			contentIDs[attachment.ContentID] = struct{}{}
		} else if attachment.ContentID != "" {
			return fmt.Errorf("%w: attachment[%d] has a content ID but is not inline", ErrInvalidMessage, i)
		}
	}
	return nil
}

func uniqueRecipients(m Message) []string {
	seen := make(map[string]struct{})
	recipients := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	for _, group := range [][]Address{m.To, m.CC, m.BCC} {
		for _, address := range group {
			if _, exists := seen[address.Address]; exists {
				continue
			}
			seen[address.Address] = struct{}{}
			recipients = append(recipients, address.Address)
		}
	}
	return recipients
}
