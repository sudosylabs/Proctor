// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package localization

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Definition declares one message owned by a localization consumer. Origin is
// a stable package or subsystem label used by tooling; Variables are the exact
// template data names accepted by every locale.
type Definition struct {
	ID             string
	Origin         string
	Variables      []string
	PluralVariable string
}

// MergeDefinitions validates, copies, and lexically orders consumer-owned
// definitions. Duplicate IDs are rejected even when their declarations are
// identical so that every message has one unambiguous owner.
func MergeDefinitions(groups ...[]Definition) ([]Definition, error) {
	count := 0
	for _, group := range groups {
		count += len(group)
	}
	definitions := make([]Definition, 0, count)
	owners := make(map[string]string, count)
	for _, group := range groups {
		for _, candidate := range group {
			definition, err := normalizeDefinition(candidate)
			if err != nil {
				return nil, err
			}
			if owner, exists := owners[definition.ID]; exists {
				return nil, fmt.Errorf("localization id %q is declared by both %q and %q", definition.ID, owner, definition.Origin)
			}
			owners[definition.ID] = definition.Origin
			definitions = append(definitions, definition)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	return definitions, nil
}

func normalizeDefinition(definition Definition) (Definition, error) {
	if !keyPattern.MatchString(definition.ID) {
		return Definition{}, fmt.Errorf("invalid localization id %q", definition.ID)
	}
	definition.Origin = strings.TrimSpace(definition.Origin)
	if definition.Origin == "" {
		return Definition{}, fmt.Errorf("localization id %q has no origin", definition.ID)
	}
	definition.Variables = append([]string(nil), definition.Variables...)
	if definition.PluralVariable != "" && !validVariable(definition.PluralVariable) {
		return Definition{}, fmt.Errorf("localization id %q has invalid plural variable %q", definition.ID, definition.PluralVariable)
	}
	sort.Strings(definition.Variables)
	previous := ""
	for _, variable := range definition.Variables {
		if !validVariable(variable) {
			return Definition{}, fmt.Errorf("localization id %q has invalid variable %q", definition.ID, variable)
		}
		if variable == previous {
			return Definition{}, fmt.Errorf("localization id %q repeats variable %q", definition.ID, variable)
		}
		previous = variable
	}
	return definition, nil
}

func validVariable(variable string) bool {
	if variable == "" {
		return false
	}
	for index, character := range variable {
		if index == 0 && !unicodeLetter(character) {
			return false
		}
		if index > 0 && !unicodeLetter(character) && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func unicodeLetter(character rune) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}

// ValidateDefinitions proves that English contains exactly the declared IDs
// and that each declaration describes the template variables in the catalog.
func (l *Localizer) ValidateDefinitions(definitions []Definition) error {
	if l == nil {
		return errors.New("localizer is nil")
	}
	merged, err := MergeDefinitions(definitions)
	if err != nil {
		return err
	}
	english := l.catalogs[EnglishLocale]
	declared := make(map[string]Definition, len(merged))
	for _, definition := range merged {
		declared[definition.ID] = definition
		message, exists := english[definition.ID]
		if !exists {
			return fmt.Errorf("declared localization id %q from %q is missing from en.json", definition.ID, definition.Origin)
		}
		if message.placeholders != strings.Join(definition.Variables, ",") {
			return fmt.Errorf("localization id %q declares variables %q but en.json uses %q", definition.ID, strings.Join(definition.Variables, ","), message.placeholders)
		}
		if message.plural != (definition.PluralVariable != "") {
			return fmt.Errorf("localization id %q plural declaration does not match en.json", definition.ID)
		}
	}
	for id := range english {
		if _, exists := declared[id]; !exists {
			return fmt.Errorf("en.json contains orphan localization id %q", id)
		}
	}
	return nil
}

// MissingDefinitions returns declared IDs absent from a specific locale. A
// partial non-English catalog is valid at runtime because resolution falls
// back, while tooling can use this report to measure translation coverage.
func (l *Localizer) MissingDefinitions(locale string, definitions []Definition) ([]Definition, error) {
	if l == nil {
		return nil, errors.New("localizer is nil")
	}
	rawLocale := locale
	locale, ok := normalizeLocale(locale)
	if !ok {
		return nil, fmt.Errorf("invalid locale %q", rawLocale)
	}
	catalog, exists := l.catalogs[locale]
	if !exists {
		return nil, fmt.Errorf("locale %q has no catalog", locale)
	}
	merged, err := MergeDefinitions(definitions)
	if err != nil {
		return nil, err
	}
	missing := make([]Definition, 0)
	for _, definition := range merged {
		if _, exists := catalog[definition.ID]; !exists {
			missing = append(missing, definition)
		}
	}
	return missing, nil
}
