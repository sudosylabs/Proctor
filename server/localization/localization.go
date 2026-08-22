// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package localization validates and resolves server-owned translations.
// Catalog storage is supplied by the composition root.
package localization

import (
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

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	goi18ntemplate "github.com/nicksnyder/go-i18n/v2/i18n/template"
	"golang.org/x/text/language"
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
	ID          string             `json:"id"`
	Translation catalogTranslation `json:"translation"`
}

type message struct {
	placeholders string
	plural       bool
	compiled     *goi18n.Message
}

type catalogTranslation struct {
	forms map[string]string
}

func (translation *catalogTranslation) UnmarshalJSON(data []byte) error {
	var singular string
	if err := json.Unmarshal(data, &singular); err == nil {
		translation.forms = map[string]string{"other": singular}
		return nil
	}
	var forms map[string]string
	if err := json.Unmarshal(data, &forms); err != nil {
		return errors.New("translation must be a string or plural-form object")
	}
	if len(forms) == 0 {
		return errors.New("plural translation is empty")
	}
	for form := range forms {
		switch form {
		case "zero", "one", "two", "few", "many", "other":
		default:
			return fmt.Errorf("unknown plural form %q", form)
		}
	}
	translation.forms = forms
	return nil
}

// Translation records the locale which supplied a resolved message.
type Translation struct {
	Locale string
	Text   string
}

// Request describes one resolution. PluralCount activates CLDR plural
// selection; TemplateData remains caller-owned and is never retained.
type Request struct {
	ID           string
	TemplateData any
	PluralCount  any
}

// Localizer is an immutable, concurrently safe set of validated locale catalogs.
type Localizer struct {
	defaultLocale string
	catalogs      map[string]map[string]message
	locales       []string
	bundle        *goi18n.Bundle
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
	bundle := goi18n.NewBundle(language.English)
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
		compiled := make([]*goi18n.Message, 0, len(messages))
		for _, message := range messages {
			compiled = append(compiled, message.compiled)
		}
		if err := bundle.AddMessages(language.Make(locale), compiled...); err != nil {
			return nil, fmt.Errorf("register i18n catalog %q: %w", file.Name(), err)
		}
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
			if translated.plural != canonical.plural {
				return nil, fmt.Errorf("i18n locale %q id %q changes singular/plural shape", locale, key)
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
	return &Localizer{defaultLocale: defaultLocale, catalogs: catalogs, locales: locales, bundle: bundle}, nil
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

func compileMessage(key string, value catalogTranslation) (message, error) {
	if len(value.forms) == 0 {
		return message{}, errors.New("translation is empty")
	}
	if len(value.forms) > 1 || value.forms["other"] == "" {
		if _, exists := value.forms["other"]; !exists {
			return message{}, errors.New("plural translation requires an other form")
		}
	}
	placeholders := ""
	firstForm := true
	for form, text := range value.forms {
		compiledPlaceholders, err := validateMessageText(key+"."+form, text)
		if err != nil {
			return message{}, fmt.Errorf("%s form: %w", form, err)
		}
		if firstForm {
			placeholders = compiledPlaceholders
			firstForm = false
		} else if placeholders != compiledPlaceholders {
			return message{}, fmt.Errorf("plural forms use different placeholders %q and %q", placeholders, compiledPlaceholders)
		}
	}
	compiled := &goi18n.Message{ID: key}
	compiled.Zero = value.forms["zero"]
	compiled.One = value.forms["one"]
	compiled.Two = value.forms["two"]
	compiled.Few = value.forms["few"]
	compiled.Many = value.forms["many"]
	compiled.Other = value.forms["other"]
	return message{placeholders: placeholders, plural: len(value.forms) > 1, compiled: compiled}, nil
}

func validateMessageText(key, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("translation is empty")
	}
	if !utf8.ValidString(value) || len(value) > maxTranslationBytes {
		return "", fmt.Errorf("translation is not bounded valid UTF-8")
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return "", errors.New("translation contains a control character")
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
		return "", errors.New("translation uses unsupported template syntax")
	}
	_, err := template.New(string(key)).Option("missingkey=error").Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse translation: %w", err)
	}
	return strings.Join(names, ","), nil
}

// Translate resolves one message through requested-locale, installation
// default, then English fallback and performs bounded interpolation.
func (l *Localizer) Resolve(requestedLocale, id string, args any) (Translation, error) {
	return l.ResolveRequest(requestedLocale, Request{ID: id, TemplateData: args})
}

// ResolveRequest resolves singular or plural text through requested-locale,
// installation-default, then English fallback.
func (l *Localizer) ResolveRequest(requestedLocale string, request Request) (Translation, error) {
	if l == nil {
		return Translation{}, errors.New("localizer is nil")
	}
	canonical, exists := l.catalogs[EnglishLocale][request.ID]
	if !exists {
		return Translation{}, fmt.Errorf("unknown localization id %q", request.ID)
	}
	if canonical.plural && request.PluralCount == nil {
		return Translation{}, fmt.Errorf("localization id %q requires a plural count", request.ID)
	}
	selectedLocale := ""
	for _, candidate := range localeCandidates(requestedLocale, l.defaultLocale, EnglishLocale) {
		if catalog, exists := l.catalogs[candidate]; exists {
			if _, exists := catalog[request.ID]; exists {
				selectedLocale = candidate
				break
			}
		}
	}
	if selectedLocale == "" {
		return Translation{}, fmt.Errorf("localization id %q is unavailable", request.ID)
	}
	localizer := goi18n.NewLocalizer(l.bundle, selectedLocale)
	text, tag, err := localizer.LocalizeWithTag(&goi18n.LocalizeConfig{
		MessageID: request.ID, TemplateData: request.TemplateData, PluralCount: request.PluralCount,
		TemplateParser: &goi18ntemplate.TextParser{Option: "missingkey=error"},
	})
	if err != nil {
		return Translation{}, fmt.Errorf("render localization id %q: %w", request.ID, err)
	}
	if len(text) > maxRenderedMessageBytes {
		return Translation{}, fmt.Errorf("rendered localization id %q exceeds %d bytes", request.ID, maxRenderedMessageBytes)
	}
	locale, ok := normalizeLocale(tag.String())
	if !ok {
		locale = tag.String()
	}
	return Translation{Locale: locale, Text: text}, nil
}

// Translate resolves text for callers that do not need the supplying locale.
func (l *Localizer) Translate(requestedLocale, id string, args any) (string, error) {
	translation, err := l.Resolve(requestedLocale, id, args)
	return translation.Text, err
}

// TranslateRequest resolves text for plural-aware callers that do not need the
// supplying locale.
func (l *Localizer) TranslateRequest(requestedLocale string, request Request) (string, error) {
	translation, err := l.ResolveRequest(requestedLocale, request)
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
