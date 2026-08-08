// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package externalauth

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
)

type ProfileMapper struct {
	descriptor    model.ExternalAuthenticationProvider
	autoProvision bool
	claims        config.ExternalClaimMapping
}

func NewProfileMapper(
	settings config.ExternalAuthenticationProvider,
) *ProfileMapper {
	return &ProfileMapper{
		descriptor: model.ExternalAuthenticationProvider{
			Id: settings.ID, DisplayName: settings.DisplayName,
			Type: settings.Type,
		},
		autoProvision: settings.AutoProvision,
		claims:        settings.Claims,
	}
}

func (m *ProfileMapper) Descriptor() model.ExternalAuthenticationProvider {
	return m.descriptor
}

func (m *ProfileMapper) AutoProvision() bool {
	return m.autoProvision
}

func (m *ProfileMapper) Claims() config.ExternalClaimMapping {
	return m.claims
}

func (m *ProfileMapper) Assertion(
	values map[string][]string,
	emailVerified bool,
	authenticatedAt int64,
) (*model.ExternalAuthenticationAssertion, error) {
	first := func(name string) string {
		if items := values[name]; len(items) != 0 {
			return strings.TrimSpace(items[0])
		}
		return ""
	}
	subject := first(m.claims.Subject)
	if len(subject) == 0 ||
		utf8.RuneCountInString(subject) > model.IdentitySubjectMaxRunes ||
		!utf8.ValidString(subject) ||
		model.SanitizeUnicode(subject) != subject {
		return nil, InvalidResponse(
			"map external identity",
			fmt.Errorf("provider returned an invalid subject"),
		)
	}
	homeOrganization := strings.ToLower(
		strings.TrimSpace(first(m.claims.HomeOrganization)),
	)
	if !allowedHomeOrganization(
		homeOrganization,
		m.claims.AllowedHomeOrganizations,
	) {
		return nil, Rejected(
			"map external identity",
			fmt.Errorf("provider returned an unauthorized home organization"),
		)
	}
	strength := model.AuthenticationSingleFactor
	if providerClaimMatches(
		values[m.claims.MultiFactorAttribute],
		m.claims.MultiFactorValues,
	) {
		strength = model.AuthenticationMultiFactor
	}
	assertion := &model.ExternalAuthenticationAssertion{
		ProviderId:       m.descriptor.Id,
		Subject:          subject,
		Username:         first(m.claims.Username),
		Email:            first(m.claims.Email),
		EmailVerified:    emailVerified,
		DisplayName:      first(m.claims.DisplayName),
		FirstName:        first(m.claims.FirstName),
		LastName:         first(m.claims.LastName),
		HomeOrganization: homeOrganization,
		Affiliations: normalizedProviderValues(
			values[m.claims.Affiliation],
			64,
			128,
		),
		AuthenticationStrength: strength,
		AuthenticatedAt:        authenticatedAt,
	}
	if m.autoProvision {
		candidate := &model.User{
			Username: assertion.Username, Email: assertion.Email,
			EmailVerified: assertion.EmailVerified,
			DisplayName:   assertion.DisplayName,
			FirstName:     assertion.FirstName,
			LastName:      assertion.LastName,
		}
		// Validate profile fields only; ID/timestamps are assigned at provision time.
		candidate.PrepareCreate(model.NewUserID(), model.NowUTC())
		if err := candidate.Validate(); err != nil {
			return nil, InvalidResponse(
				"map external identity",
				fmt.Errorf("provider returned invalid provisioning claims"),
			)
		}
	}
	return assertion, nil
}

func allowedHomeOrganization(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(value, strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func providerClaimMatches(values []string, accepted []string) bool {
	if len(values) == 0 || len(accepted) == 0 {
		return false
	}
	for _, value := range values {
		for _, candidate := range accepted {
			if value == candidate {
				return true
			}
		}
	}
	return false
}

func normalizedProviderValues(
	values []string,
	maximum int,
	maximumLength int,
) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, min(len(values), maximum))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || len(value) > maximumLength {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == maximum {
			break
		}
	}
	sort.Strings(result)
	return result
}
