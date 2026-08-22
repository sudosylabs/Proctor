// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package localization

import (
	"strconv"
	"strings"
)

// PreferredLocale selects the best supported locale from an HTTP
// Accept-Language value. An empty result asks Resolve to use installation and
// English fallback.
func PreferredLocale(acceptLanguage string, supported []string) string {
	type preference struct {
		locale  string
		quality float64
		order   int
	}
	type specificity struct {
		kind  int
		depth int
	}
	moreSpecific := func(left, right specificity) bool {
		return left.kind > right.kind || left.kind == right.kind && left.depth > right.depth
	}
	preferences := make([]preference, 0, 8)
	for order, raw := range strings.Split(acceptLanguage, ",") {
		parts := strings.Split(strings.TrimSpace(raw), ";")
		locale := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(parts[0]), "_", "-"))
		if locale == "" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(name, "q") {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		preferences = append(preferences, preference{locale: locale, quality: quality, order: order})
	}
	bestLocale, bestQuality, bestSpecificity, bestOrder := "", -1.0, specificity{}, len(preferences)+1
	for _, candidate := range supported {
		normalized := strings.ToLower(strings.ReplaceAll(candidate, "_", "-"))
		candidateQuality, candidateSpecificity, candidateOrder := -1.0, specificity{}, len(preferences)+1
		for _, requested := range preferences {
			matched := specificity{}
			switch {
			case requested.locale == normalized:
				matched = specificity{kind: 3, depth: len(strings.Split(requested.locale, "-"))}
			case strings.HasPrefix(requested.locale, normalized+"-") || strings.HasPrefix(normalized, requested.locale+"-"):
				matched = specificity{kind: 2, depth: len(strings.Split(requested.locale, "-"))}
			case requested.locale == "*":
				matched = specificity{kind: 1}
			}
			preferredMatch := moreSpecific(matched, candidateSpecificity) ||
				matched == candidateSpecificity && requested.order < candidateOrder
			if matched.kind > 0 && preferredMatch {
				candidateQuality, candidateSpecificity, candidateOrder = requested.quality, matched, requested.order
			}
		}
		better := candidateQuality > bestQuality ||
			candidateQuality == bestQuality && moreSpecific(candidateSpecificity, bestSpecificity) ||
			candidateQuality == bestQuality && candidateSpecificity == bestSpecificity && candidateOrder < bestOrder
		if candidateQuality > 0 && better {
			bestLocale, bestQuality, bestSpecificity, bestOrder = candidate, candidateQuality, candidateSpecificity, candidateOrder
		}
	}
	return bestLocale
}
