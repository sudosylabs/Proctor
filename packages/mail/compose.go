package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/textproto"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ComposerConfig controls portable message limits and generated identifiers.
type ComposerConfig struct {
	MessageIDDomain string
	MaxMessageBytes int64
	MaxRecipients   int
}

// Composer validates messages and renders RFC 5322/MIME deliveries.
type Composer struct {
	domain        string
	maxBytes      int64
	maxRecipients int
}

// NewComposer constructs a reusable, concurrency-safe composer.
func NewComposer(config ComposerConfig) (*Composer, error) {
	if err := validateMessageIDDomain(config.MessageIDDomain); err != nil {
		return nil, err
	}
	if config.MaxMessageBytes == 0 {
		config.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if config.MaxMessageBytes < 1 {
		return nil, fmt.Errorf("mail composer: max message bytes must be positive")
	}
	if config.MaxRecipients == 0 {
		config.MaxRecipients = DefaultMaxRecipients
	}
	if config.MaxRecipients < 1 {
		return nil, fmt.Errorf("mail composer: max recipients must be positive")
	}
	return &Composer{
		domain:        config.MessageIDDomain,
		maxBytes:      config.MaxMessageBytes,
		maxRecipients: config.MaxRecipients,
	}, nil
}

// Capabilities reports the features implemented by the MIME composer.
func (c *Composer) Capabilities() Capabilities {
	return Capabilities{
		HTML:              true,
		Attachments:       true,
		InlineAttachments: true,
		CustomHeaders:     true,
		MaxMessageBytes:   c.maxBytes,
		MaxRecipients:     c.maxRecipients,
	}
}

// Compose validates and renders a message. The returned delivery owns its byte
// slice and may be safely retained.
func (c *Composer) Compose(message Message) (Delivery, error) {
	if message.Date.IsZero() {
		message.Date = time.Now()
	}
	if message.MessageID == "" {
		messageID, err := newMessageID(c.domain)
		if err != nil {
			return Delivery{}, Error("compose", err)
		}
		message.MessageID = messageID
	}
	if err := message.validate(c.maxRecipients, c.maxBytes); err != nil {
		return Delivery{}, Error("compose", err)
	}

	entity, err := buildRootEntity(message)
	if err != nil {
		return Delivery{}, Error("compose", err)
	}

	var output bytes.Buffer
	writeHeader(&output, "Date", message.Date.Format(time.RFC1123Z))
	writeHeader(&output, "From", message.From.String())
	if len(message.To) > 0 {
		writeHeader(&output, "To", formatAddresses(message.To))
	}
	if len(message.CC) > 0 {
		writeHeader(&output, "Cc", formatAddresses(message.CC))
	}
	if len(message.ReplyTo) > 0 {
		writeHeader(&output, "Reply-To", formatAddresses(message.ReplyTo))
	}
	writeHeader(&output, "Subject", encodeHeaderWord(message.Subject))
	writeHeader(&output, "Message-ID", message.MessageID)
	if message.InReplyTo != "" {
		writeHeader(&output, "In-Reply-To", message.InReplyTo)
	}
	if len(message.References) > 0 {
		writeHeader(&output, "References", strings.Join(message.References, " "))
	}
	writeHeader(&output, "MIME-Version", "1.0")

	headerNames := make([]string, 0, len(message.Headers))
	for name := range message.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Slice(headerNames, func(i, j int) bool {
		return strings.ToLower(headerNames[i]) < strings.ToLower(headerNames[j])
	})
	for _, name := range headerNames {
		for _, value := range message.Headers[name] {
			writeHeader(&output, name, value)
		}
	}
	writeEntityHeaders(&output, entity.header)
	output.WriteString("\r\n")
	output.Write(entity.body)

	if int64(output.Len()) > c.maxBytes {
		return Delivery{}, Error("compose", ErrMessageTooLarge)
	}
	envelopeFrom := message.EnvelopeFrom
	if envelopeFrom == "" {
		envelopeFrom = message.From.Address
	}
	return Delivery{
		EnvelopeFrom: envelopeFrom,
		Recipients:   uniqueRecipients(message),
		MessageID:    message.MessageID,
		Data:         append([]byte(nil), output.Bytes()...),
	}, nil
}

type mimeEntity struct {
	header textproto.MIMEHeader
	body   []byte
}

func buildRootEntity(message Message) (mimeEntity, error) {
	inline := make([]Attachment, 0)
	regular := make([]Attachment, 0)
	for _, attachment := range message.Attachments {
		if attachment.Inline {
			inline = append(inline, attachment)
		} else {
			regular = append(regular, attachment)
		}
	}

	body, err := buildBodyEntity(message.Text, message.HTML, inline)
	if err != nil {
		return mimeEntity{}, err
	}
	if len(regular) == 0 {
		return body, nil
	}

	parts := make([]mimeEntity, 0, len(regular)+1)
	parts = append(parts, body)
	for _, attachment := range regular {
		parts = append(parts, buildAttachmentEntity(attachment))
	}
	return buildMultipart("mixed", parts)
}

func buildBodyEntity(text, html string, inline []Attachment) (mimeEntity, error) {
	var htmlEntity mimeEntity
	if html != "" {
		htmlEntity = buildTextEntity("text/html; charset=UTF-8", html)
		if len(inline) > 0 {
			related := make([]mimeEntity, 0, len(inline)+1)
			related = append(related, htmlEntity)
			for _, attachment := range inline {
				related = append(related, buildAttachmentEntity(attachment))
			}
			var err error
			htmlEntity, err = buildMultipart("related", related)
			if err != nil {
				return mimeEntity{}, err
			}
		}
	}

	if text != "" && html != "" {
		return buildMultipart("alternative", []mimeEntity{
			buildTextEntity("text/plain; charset=UTF-8", text),
			htmlEntity,
		})
	}
	if html != "" {
		return htmlEntity, nil
	}
	return buildTextEntity("text/plain; charset=UTF-8", text), nil
}

func buildTextEntity(contentType, body string) mimeEntity {
	var encoded bytes.Buffer
	writer := quotedprintable.NewWriter(&encoded)
	_, _ = writer.Write([]byte(normalizeNewlines(body)))
	_ = writer.Close()
	return mimeEntity{
		header: textproto.MIMEHeader{
			"Content-Type":              {contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		},
		body: encoded.Bytes(),
	}
}

func buildAttachmentEntity(attachment Attachment) mimeEntity {
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(attachment.Filename))
	}
	if contentType == "" {
		contentType = http.DetectContentType(attachment.Data)
	}
	mediaType, parameters, _ := mime.ParseMediaType(contentType)
	if parameters == nil {
		parameters = make(map[string]string)
	}
	parameters["name"] = attachment.Filename

	disposition := "attachment"
	if attachment.Inline {
		disposition = "inline"
	}
	header := textproto.MIMEHeader{
		"Content-Type": {
			mime.FormatMediaType(mediaType, parameters),
		},
		"Content-Disposition": {
			mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Filename}),
		},
		"Content-Transfer-Encoding": {"base64"},
	}
	if attachment.Inline {
		header.Set("Content-ID", "<"+attachment.ContentID+">")
	}
	return mimeEntity{header: header, body: encodeBase64(attachment.Data)}
}

func buildMultipart(subtype string, parts []mimeEntity) (mimeEntity, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		partWriter, err := writer.CreatePart(part.header)
		if err != nil {
			return mimeEntity{}, err
		}
		if _, err := partWriter.Write(part.body); err != nil {
			return mimeEntity{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return mimeEntity{}, err
	}
	return mimeEntity{
		header: textproto.MIMEHeader{
			"Content-Type": {
				mime.FormatMediaType("multipart/"+subtype, map[string]string{"boundary": writer.Boundary()}),
			},
		},
		body: body.Bytes(),
	}, nil
}

func writeEntityHeaders(output *bytes.Buffer, headers textproto.MIMEHeader) {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range headers[name] {
			writeHeader(output, name, value)
		}
	}
}

func writeHeader(output *bytes.Buffer, name, value string) {
	const preferredLineLength = 78
	prefix := name + ": "
	if len(prefix)+len(value) <= preferredLineLength {
		output.WriteString(prefix)
		output.WriteString(value)
		output.WriteString("\r\n")
		return
	}
	lineLength := len(prefix)
	output.WriteString(prefix)

	tokens := headerTokens(value)
	for index, token := range tokens {
		separator := ""
		if index > 0 {
			separator = " "
		}
		if lineLength+len(separator)+len(token) > preferredLineLength && lineLength > 1 {
			output.WriteString("\r\n\t")
			lineLength = 1
			separator = ""
		}
		output.WriteString(separator)
		output.WriteString(token)
		lineLength += len(separator) + len(token)
	}
	output.WriteString("\r\n")
}

func headerTokens(value string) []string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return []string{""}
	}
	return fields
}

func formatAddresses(addresses []Address) string {
	formatted := make([]string, len(addresses))
	for i, address := range addresses {
		formatted[i] = address.String()
	}
	return strings.Join(formatted, ", ")
}

func encodeHeaderWord(value string) string {
	if isASCII(value) {
		return value
	}
	return mime.QEncoding.Encode("UTF-8", value)
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func encodeBase64(data []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(data)
	var output bytes.Buffer
	for len(encoded) > 76 {
		output.WriteString(encoded[:76])
		output.WriteString("\r\n")
		encoded = encoded[76:]
	}
	output.WriteString(encoded)
	output.WriteString("\r\n")
	return output.Bytes()
}

func newMessageID(domain string) (string, error) {
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate message ID: %w", err)
	}
	return "<" + base64.RawURLEncoding.EncodeToString(random) + "@" + domain + ">", nil
}

func validateMessageIDDomain(domain string) error {
	if !validMessageIDDomain(domain) {
		return fmt.Errorf("mail composer: invalid message ID domain")
	}
	return nil
}

func validMessageIDDomain(domain string) bool {
	if domain == "" || !isASCII(domain) || unsafeHeaderValue(domain) ||
		len(domain) > 253 || strings.ContainsAny(domain, "<> \t@") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				character != '-' {
				return false
			}
		}
	}
	return true
}
