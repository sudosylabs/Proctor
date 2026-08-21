// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package localization validates and resolves server-owned translations.
// Catalog storage is supplied by the composition root.
package localization

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"
)

const (
	EnglishLocale           = "en"
	maxTranslationBytes     = 16 << 10
	maxRenderedMessageBytes = 64 << 10
)

var (
	keyPattern         = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	localePattern      = regexp.MustCompile(`^[A-Za-z]{2,8}(?:[-_][A-Za-z0-9]{1,8})*$`)
	placeholderPattern = regexp.MustCompile(`\{\{\s*\.([A-Za-z][A-Za-z0-9_]*)\s*\}\}`)
)

type entry struct {
	ID          string `json:"id"`
	Translation string `json:"translation"`
}

type message struct {
	text         string
	template     *template.Template
	placeholders string
}

// Translation records the locale which supplied a resolved message.
type Translation struct {
	Locale string
	Text   string
}

// Localizer is an immutable, concurrently safe set of validated locale catalogs.
type Localizer struct {
	defaultLocale string
	catalogs      map[string]map[string]message
	locales       []string
}

// New validates top-level JSON catalogs and constructs a localizer.
func New(source fs.FS, defaultLocale string) (*Localizer, error) {
	if source == nil {
		return nil, errors.New("i18n catalog filesystem is nil")
	}
	defaultLocale, ok := normalizeLocale(defaultLocale)
	if !ok {
		return nil, fmt.Errorf("invalid default locale %q", defaultLocale)
	}
	files, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, fmt.Errorf("read i18n catalogs: %w", err)
	}
	catalogs := make(map[string]map[string]message)
	for _, file := range files {
		if file.IsDir() || path.Ext(file.Name()) != ".json" {
			continue
		}
		locale, valid := normalizeLocale(strings.TrimSuffix(file.Name(), path.Ext(file.Name())))
		if !valid {
			return nil, fmt.Errorf("i18n catalog %q has an invalid locale filename", file.Name())
		}
		if _, exists := catalogs[locale]; exists {
			return nil, fmt.Errorf("duplicate normalized i18n locale %q", locale)
		}
		entries, readErr := readEntries(source, file.Name())
		if readErr != nil {
			return nil, readErr
		}
		messages := make(map[string]message, len(entries))
		previous := ""
		for index, entry := range entries {
			if !keyPattern.MatchString(string(entry.ID)) {
				return nil, fmt.Errorf("i18n catalog %q entry %d has invalid id %q", file.Name(), index, entry.ID)
			}
			if index > 0 && previous >= entry.ID {
				return nil, fmt.Errorf("i18n catalog %q ids are not unique and lexically sorted at %q", file.Name(), entry.ID)
			}
			compiled, compileErr := compileMessage(entry.ID, entry.Translation)
			if compileErr != nil {
				return nil, fmt.Errorf("i18n catalog %q id %q: %w", file.Name(), entry.ID, compileErr)
			}
			messages[entry.ID] = compiled
			previous = entry.ID
		}
		if len(messages) == 0 {
			return nil, fmt.Errorf("i18n catalog %q is empty", file.Name())
		}
		catalogs[locale] = messages
	}
	english, ok := catalogs[EnglishLocale]
	if !ok {
		return nil, errors.New("i18n catalogs require en.json")
	}
	for locale, messages := range catalogs {
		if locale == EnglishLocale {
			continue
		}
		for key, translated := range messages {
			canonical, exists := english[key]
			if !exists {
				return nil, fmt.Errorf("i18n locale %q contains unknown id %q", locale, key)
			}
			if translated.placeholders != canonical.placeholders {
				return nil, fmt.Errorf("i18n locale %q id %q has placeholders %q, want %q", locale, key, translated.placeholders, canonical.placeholders)
			}
		}
	}
	if !hasCatalogCandidate(catalogs, defaultLocale) {
		return nil, fmt.Errorf("default locale %q is not represented by an i18n catalog", defaultLocale)
	}
	locales := make([]string, 0, len(catalogs))
	for locale := range catalogs {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	return &Localizer{defaultLocale: defaultLocale, catalogs: catalogs, locales: locales}, nil
}

func readEntries(source fs.FS, name string) ([]entry, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open i18n catalog %q: %w", name, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	var entries []entry
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode i18n catalog %q: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("decode i18n catalog %q: %w", name, err)
	}
	return entries, nil
}

func compileMessage(key string, value string) (message, error) {
	if strings.TrimSpace(value) == "" {
		return message{}, errors.New("translation is empty")
	}
	if !utf8.ValidString(value) || len(value) > maxTranslationBytes {
		return message{}, fmt.Errorf("translation is not bounded valid UTF-8")
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return message{}, errors.New("translation contains a control character")
		}
	}
	matches := placeholderPattern.FindAllStringSubmatch(value, -1)
	names := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if _, exists := seen[match[1]]; exists {
			continue
		}
		seen[match[1]] = struct{}{}
		names = append(names, match[1])
	}
	sort.Strings(names)
	withoutPlaceholders := placeholderPattern.ReplaceAllString(value, "")
	if strings.Contains(withoutPlaceholders, "{{") || strings.Contains(withoutPlaceholders, "}}") {
		return message{}, errors.New("translation uses unsupported template syntax")
	}
	compiled, err := template.New(string(key)).Option("missingkey=error").Parse(value)
	if err != nil {
		return message{}, fmt.Errorf("parse translation: %w", err)
	}
	return message{text: value, template: compiled, placeholders: strings.Join(names, ",")}, nil
}

// Translate resolves one message through requested-locale, installation
// default, then English fallback and performs bounded interpolation.
func (l *Localizer) Resolve(requestedLocale, id string, args any) (Translation, error) {
	if l == nil {
		return Translation{}, errors.New("localizer is nil")
	}
	if _, exists := l.catalogs[EnglishLocale][id]; !exists {
		return Translation{}, fmt.Errorf("unknown localization id %q", id)
	}
	for _, locale := range localeCandidates(requestedLocale, l.defaultLocale, EnglishLocale) {
		catalog, exists := l.catalogs[locale]
		if !exists {
			continue
		}
		candidate, exists := catalog[id]
		if !exists {
			continue
		}
		if candidate.placeholders == "" {
			return Translation{Locale: locale, Text: candidate.text}, nil
		}
		var output bytes.Buffer
		if err := candidate.template.Execute(&output, args); err != nil {
			return Translation{}, fmt.Errorf("render localization id %q: %w", id, err)
		}
		if output.Len() > maxRenderedMessageBytes {
			return Translation{}, fmt.Errorf("rendered localization id %q exceeds %d bytes", id, maxRenderedMessageBytes)
		}
		return Translation{Locale: locale, Text: output.String()}, nil
	}
	return Translation{}, fmt.Errorf("localization id %q is unavailable", id)
}

// Translate resolves text for callers that do not need the supplying locale.
func (l *Localizer) Translate(requestedLocale, id string, args any) (string, error) {
	translation, err := l.Resolve(requestedLocale, id, args)
	return translation.Text, err
}

// SupportedLocales returns a stable copy of the locales included in the bundle.
func (l *Localizer) SupportedLocales() []string {
	if l == nil {
		return nil
	}
	return append([]string(nil), l.locales...)
}

// DefaultLocale returns the normalized installation locale used before the
// final English fallback.
func (l *Localizer) DefaultLocale() string {
	if l == nil {
		return ""
	}
	return l.defaultLocale
}

func normalizeLocale(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !localePattern.MatchString(raw) {
		return "", false
	}
	return strings.ToLower(strings.ReplaceAll(raw, "_", "-")), true
}

func localeCandidates(values ...string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values)*2)
	for _, raw := range values {
		locale, ok := normalizeLocale(raw)
		if !ok {
			continue
		}
		for _, candidate := range []string{locale, strings.SplitN(locale, "-", 2)[0]} {
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			result = append(result, candidate)
		}
	}
	return result
}

func hasCatalogCandidate(catalogs map[string]map[string]message, locale string) bool {
	for _, candidate := range localeCandidates(locale) {
		if _, exists := catalogs[candidate]; exists {
			return true
		}
	}
	return false
}
