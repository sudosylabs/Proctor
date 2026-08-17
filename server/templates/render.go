// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package templates renders the server's closed transactional-mail catalog.
package templates

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"
	"unicode"

	"github.com/sudosylabs/proctor/server/i18n"
)

const maxRenderedMessageBytes = 1 << 20

// Properties is the complete typed model visible to HTML and text templates.
// It intentionally contains no arbitrary map and offers no template helpers.
type Properties struct {
	Copy      i18n.Copy
	ActionURL string
}

// Request selects localized copy and the already constructed optional action.
type Request struct {
	Key                i18n.Key
	RecipientLocale    string
	InstallationLocale string
	ActionURL          string
}

// Message is one safe, fully rendered multipart-alternative payload.
type Message struct {
	Key     i18n.Key
	Locale  string
	Subject string
	Text    string
	HTML    string
}

// Renderer owns parsed, embedded templates and the localization catalog.
type Renderer struct {
	catalog *i18n.Catalog
	html    map[i18n.Key]*htmltemplate.Template
	text    map[i18n.Key]*texttemplate.Template
}

//go:embed *.html *.txt
var templateFiles embed.FS

// DefaultRenderer constructs a renderer from the embedded English catalog.
func DefaultRenderer() (*Renderer, error) {
	catalog, err := i18n.DefaultCatalog()
	if err != nil {
		return nil, err
	}
	return NewRenderer(catalog)
}

// NewRenderer parses every production template during construction.
func NewRenderer(catalog *i18n.Catalog) (*Renderer, error) {
	if catalog == nil {
		return nil, errors.New("mail template catalog is nil")
	}
	renderer := &Renderer{
		catalog: catalog,
		html:    make(map[i18n.Key]*htmltemplate.Template),
		text:    make(map[i18n.Key]*texttemplate.Template),
	}
	for _, key := range i18n.AllKeys() {
		name := string(key)
		htmlSource, err := templateFiles.ReadFile(name + ".html")
		if err != nil {
			return nil, fmt.Errorf("read HTML mail template %q: %w", key, err)
		}
		htmlValue, err := htmltemplate.New(name).Option("missingkey=error").Parse(string(htmlSource))
		if err != nil {
			return nil, fmt.Errorf("parse HTML mail template %q: %w", key, err)
		}
		textSource, err := templateFiles.ReadFile(name + ".txt")
		if err != nil {
			return nil, fmt.Errorf("read text mail template %q: %w", key, err)
		}
		textValue, err := texttemplate.New(name).Option("missingkey=error").Parse(string(textSource))
		if err != nil {
			return nil, fmt.Errorf("parse text mail template %q: %w", key, err)
		}
		renderer.html[key] = htmlValue
		renderer.text[key] = textValue
	}
	return renderer, nil
}

// Render resolves one complete copy model and applies it to both alternatives.
func (r *Renderer) Render(request Request) (Message, error) {
	if r == nil || r.catalog == nil {
		return Message{}, errors.New("mail template renderer is nil")
	}
	resolved, err := r.catalog.Resolve(request.Key, request.RecipientLocale, request.InstallationLocale)
	if err != nil {
		return Message{}, err
	}
	actionURL := strings.TrimSpace(request.ActionURL)
	if resolved.Copy.ActionLabel == "" {
		actionURL = ""
	} else {
		if err := validateActionURL(actionURL); err != nil {
			return Message{}, err
		}
	}
	properties := Properties{Copy: resolved.Copy, ActionURL: actionURL}

	htmlValue, ok := r.html[request.Key]
	if !ok {
		return Message{}, fmt.Errorf("HTML mail template %q is unavailable", request.Key)
	}
	textValue, ok := r.text[request.Key]
	if !ok {
		return Message{}, fmt.Errorf("text mail template %q is unavailable", request.Key)
	}
	var htmlOutput bytes.Buffer
	if err := htmlValue.Execute(&htmlOutput, properties); err != nil {
		return Message{}, fmt.Errorf("render HTML mail template %q: %w", request.Key, err)
	}
	var textOutput bytes.Buffer
	if err := textValue.Execute(&textOutput, properties); err != nil {
		return Message{}, fmt.Errorf("render text mail template %q: %w", request.Key, err)
	}
	if htmlOutput.Len()+textOutput.Len() > maxRenderedMessageBytes {
		return Message{}, fmt.Errorf("rendered mail template %q exceeds %d bytes", request.Key, maxRenderedMessageBytes)
	}
	return Message{
		Key: request.Key, Locale: resolved.Locale, Subject: resolved.Copy.Subject,
		Text: textOutput.String(), HTML: htmlOutput.String(),
	}, nil
}

func validateActionURL(raw string) error {
	if raw == "" || len(raw) > 4096 {
		return errors.New("mail action URL is missing or too long")
	}
	for _, character := range raw {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return errors.New("mail action URL contains whitespace or control characters")
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("mail action URL must be an absolute HTTPS URL without user information")
	}
	return nil
}
