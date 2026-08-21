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
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/i18n"
)

const maxRenderedMessageBytes = 1 << 20

// Properties is the complete typed model visible to HTML and text templates.
// It intentionally contains no arbitrary map and offers no template helpers.
type Properties struct {
	Copy                Copy
	ActionURL           string
	PersonalAccessToken *PersonalAccessTokenProperties
	ExamManager         *ExamManagerProperties
	SittingSchedule     *SittingScheduleProperties
	ClassTransition     *ClassTransitionProperties
	SubmissionReceipt   *SubmissionReceiptProperties
	ResultRelease       *ResultReleaseProperties
}

// PersonalAccessTokenDetails is the bounded, scope-safe dynamic input for a
// PAT transition notice. It deliberately has no credential, hash, or actions.
type PersonalAccessTokenDetails struct {
	Description        string
	ExpiresAt          time.Time
	ActionAt           time.Time
	ActionCount        int
	AcademicUnitScoped bool
}

// PersonalAccessTokenProperties is the formatted template-visible PAT model.
type PersonalAccessTokenProperties struct {
	Description string
	ExpiresAt   string
	ActionAt    string
	Scope       string
	ActionCount int
}

type ExamManagerRelationship string

const (
	ExamManagerRelationshipManager         ExamManagerRelationship = "manager"
	ExamManagerRelationshipOwner           ExamManagerRelationship = "owner"
	ExamManagerRelationshipNoLongerManager ExamManagerRelationship = "no_longer_manager"
)

// ExamManagerDetails is the bounded dynamic input for one Exam management
// relationship notice. Actor identity and authorization detail are absent by
// construction.
type ExamManagerDetails struct {
	Title        string
	Relationship ExamManagerRelationship
	ActionAt     time.Time
}

type ExamManagerProperties struct {
	Title        string
	Relationship string
	ActionAt     string
}

// SittingScheduleDetails is the complete safe fact set available to Sitting
// mail. Instructions, resources, policy, roster, and actor identity have no
// representation here.
type SittingScheduleDetails struct {
	ExamTitle        string
	ClassDisplayName string
	StartsAt         time.Time
	EndsAt           time.Time
}

type SittingScheduleProperties struct {
	ExamTitle        string
	ClassDisplayName string
	StartsAt         string
	EndsAt           string
	Timezone         string
}

// ClassTransitionDetails is the complete safe dynamic fact set for one
// enrollment, ending, or transfer notice.
type ClassTransitionDetails struct {
	PreviousClassDisplayName string
	ClassDisplayName         string
	StartsAt                 time.Time
	EndsAt                   time.Time
}

type ClassTransitionProperties struct {
	PreviousClassDisplayName string
	ClassDisplayName         string
	StartsAt                 string
	EndsAt                   string
	Timezone                 string
}

// SubmissionReceiptDetails is the complete candidate-safe receipt input.
// Manifest data, Workspace content, answers, paths, and integrity state have
// no representation in this type.
type SubmissionReceiptDetails struct {
	ExamTitle    string
	SittingID    string
	SubmissionID string
	SealedAt     time.Time
}

type SubmissionReceiptProperties struct {
	ExamTitle    string
	SittingID    string
	SubmissionID string
	SealedAt     string
	Timezone     string
}

// ResultReleaseDetails is the entire inbox-safe result availability fact.
// Scores, outcomes, remarks, evidence, rationale, and Submission data are
// absent by construction.
type ResultReleaseDetails struct {
	ExamTitle  string
	ReleasedAt time.Time
}

type ResultReleaseProperties struct {
	ExamTitle  string
	ReleasedAt string
	Timezone   string
}

// Request selects localized copy and the already constructed optional action.
type Request struct {
	Key                 Key
	RecipientLocale     string
	InstallationLocale  string
	ActionURL           string
	PersonalAccessToken *PersonalAccessTokenDetails
	ExamManager         *ExamManagerDetails
	SittingSchedule     *SittingScheduleDetails
	ClassTransition     *ClassTransitionDetails
	SubmissionReceipt   *SubmissionReceiptDetails
	ResultRelease       *ResultReleaseDetails
}

// Message is one safe, fully rendered multipart-alternative payload.
type Message struct {
	Key     Key
	Locale  string
	Subject string
	Text    string
	HTML    string
}

// Renderer owns parsed, embedded templates and the localization catalog.
type Renderer struct {
	catalog *i18n.Bundle
	html    map[Key]*htmltemplate.Template
	text    map[Key]*texttemplate.Template
}

//go:embed *.html *.txt
var templateFiles embed.FS

// DefaultRenderer constructs a renderer from the embedded English catalog.
func DefaultRenderer() (*Renderer, error) {
	catalog, err := i18n.DefaultBundle(i18n.EnglishLocale)
	if err != nil {
		return nil, err
	}
	return NewRenderer(catalog)
}

// NewRenderer parses every production template during construction.
func NewRenderer(catalog *i18n.Bundle) (*Renderer, error) {
	return newRenderer(catalog, true)
}

func newRenderer(catalog *i18n.Bundle, validateCompleteCatalog bool) (*Renderer, error) {
	if catalog == nil {
		return nil, errors.New("mail template catalog is nil")
	}
	renderer := &Renderer{
		catalog: catalog,
		html:    make(map[Key]*htmltemplate.Template),
		text:    make(map[Key]*texttemplate.Template),
	}
	for _, key := range AllKeys() {
		if validateCompleteCatalog {
			if _, _, err := resolveCopy(catalog, key, i18n.EnglishLocale, i18n.EnglishLocale); err != nil {
				return nil, fmt.Errorf("validate localized mail copy %q: %w", key, err)
			}
		}
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
	copy, locale, err := resolveCopy(r.catalog, request.Key, request.RecipientLocale, request.InstallationLocale)
	if err != nil {
		return Message{}, err
	}
	actionURL := strings.TrimSpace(request.ActionURL)
	if copy.ActionLabel == "" {
		actionURL = ""
	} else {
		if err := validateActionURL(actionURL); err != nil {
			return Message{}, err
		}
	}
	properties := Properties{Copy: copy, ActionURL: actionURL}
	patKey := isPersonalAccessTokenTemplate(request.Key)
	if patKey != (request.PersonalAccessToken != nil) {
		return Message{}, fmt.Errorf("mail template %q has invalid PAT details", request.Key)
	}
	if request.PersonalAccessToken != nil {
		if copy.PersonalAccessToken == nil || !validPersonalAccessTokenDetails(request.PersonalAccessToken) {
			return Message{}, fmt.Errorf("mail template %q has invalid PAT details", request.Key)
		}
		scope := copy.PersonalAccessToken.InstitutionScope
		if request.PersonalAccessToken.AcademicUnitScoped {
			scope = copy.PersonalAccessToken.AcademicUnitScope
		}
		properties.PersonalAccessToken = &PersonalAccessTokenProperties{
			Description: request.PersonalAccessToken.Description,
			ExpiresAt:   request.PersonalAccessToken.ExpiresAt.UTC().Format(time.RFC3339),
			ActionAt:    request.PersonalAccessToken.ActionAt.UTC().Format(time.RFC3339),
			Scope:       scope,
			ActionCount: request.PersonalAccessToken.ActionCount,
		}
	}
	examManagerKey := isExamManagerTemplate(request.Key)
	if examManagerKey != (request.ExamManager != nil) {
		return Message{}, fmt.Errorf("mail template %q has invalid Exam Manager details", request.Key)
	}
	if request.ExamManager != nil {
		if copy.ExamManager == nil || !validExamManagerDetails(request.ExamManager) {
			return Message{}, fmt.Errorf("mail template %q has invalid Exam Manager details", request.Key)
		}
		var relationship string
		switch request.ExamManager.Relationship {
		case ExamManagerRelationshipManager:
			relationship = copy.ExamManager.Manager
		case ExamManagerRelationshipOwner:
			relationship = copy.ExamManager.Owner
		case ExamManagerRelationshipNoLongerManager:
			relationship = copy.ExamManager.NoLongerManager
		}
		properties.ExamManager = &ExamManagerProperties{
			Title: strings.TrimSpace(request.ExamManager.Title), Relationship: relationship,
			ActionAt: request.ExamManager.ActionAt.UTC().Format(time.RFC3339),
		}
	}
	sittingKey := isSittingScheduleTemplate(request.Key)
	if sittingKey != (request.SittingSchedule != nil) {
		return Message{}, fmt.Errorf("mail template %q has invalid Sitting schedule details", request.Key)
	}
	if request.SittingSchedule != nil {
		if copy.SittingSchedule == nil || !validSittingScheduleDetails(request.SittingSchedule) {
			return Message{}, fmt.Errorf("mail template %q has invalid Sitting schedule details", request.Key)
		}
		properties.SittingSchedule = &SittingScheduleProperties{
			ExamTitle:        strings.TrimSpace(request.SittingSchedule.ExamTitle),
			ClassDisplayName: strings.TrimSpace(request.SittingSchedule.ClassDisplayName),
			StartsAt:         request.SittingSchedule.StartsAt.UTC().Format(time.RFC3339),
			EndsAt:           request.SittingSchedule.EndsAt.UTC().Format(time.RFC3339),
			Timezone:         copy.SittingSchedule.TimezoneUTC,
		}
	}
	classKey := isClassTransitionTemplate(request.Key)
	if classKey != (request.ClassTransition != nil) {
		return Message{}, fmt.Errorf("mail template %q has invalid Class transition details", request.Key)
	}
	if request.ClassTransition != nil {
		if copy.ClassTransition == nil || !validClassTransitionDetails(request.Key, request.ClassTransition) {
			return Message{}, fmt.Errorf("mail template %q has invalid Class transition details", request.Key)
		}
		endsAt := copy.ClassTransition.NoScheduledEnd
		if !request.ClassTransition.EndsAt.IsZero() {
			endsAt = request.ClassTransition.EndsAt.UTC().Format(time.RFC3339)
		}
		properties.ClassTransition = &ClassTransitionProperties{
			PreviousClassDisplayName: strings.TrimSpace(request.ClassTransition.PreviousClassDisplayName),
			ClassDisplayName:         strings.TrimSpace(request.ClassTransition.ClassDisplayName),
			StartsAt:                 request.ClassTransition.StartsAt.UTC().Format(time.RFC3339), EndsAt: endsAt,
			Timezone: copy.ClassTransition.TimezoneUTC,
		}
	}
	submissionReceiptKey := isSubmissionReceiptTemplate(request.Key)
	if submissionReceiptKey != (request.SubmissionReceipt != nil) {
		return Message{}, fmt.Errorf("mail template %q has invalid Submission receipt details", request.Key)
	}
	if request.SubmissionReceipt != nil {
		if copy.SubmissionReceipt == nil || !validSubmissionReceiptDetails(request.SubmissionReceipt) {
			return Message{}, fmt.Errorf("mail template %q has invalid Submission receipt details", request.Key)
		}
		properties.SubmissionReceipt = &SubmissionReceiptProperties{
			ExamTitle:    strings.TrimSpace(request.SubmissionReceipt.ExamTitle),
			SittingID:    strings.TrimSpace(request.SubmissionReceipt.SittingID),
			SubmissionID: strings.TrimSpace(request.SubmissionReceipt.SubmissionID),
			SealedAt:     request.SubmissionReceipt.SealedAt.UTC().Format(time.RFC3339),
			Timezone:     copy.SubmissionReceipt.TimezoneUTC,
		}
	}
	resultReleaseKey := request.Key == ExamResultReleased
	if resultReleaseKey != (request.ResultRelease != nil) {
		return Message{}, fmt.Errorf("mail template %q has invalid result release details", request.Key)
	}
	if request.ResultRelease != nil {
		if copy.ResultRelease == nil || request.ResultRelease.ReleasedAt.IsZero() ||
			!validBoundedMailLabel(request.ResultRelease.ExamTitle) {
			return Message{}, fmt.Errorf("mail template %q has invalid result release details", request.Key)
		}
		properties.ResultRelease = &ResultReleaseProperties{ExamTitle: strings.TrimSpace(request.ResultRelease.ExamTitle),
			ReleasedAt: request.ResultRelease.ReleasedAt.UTC().Format(time.RFC3339),
			Timezone:   copy.ResultRelease.TimezoneUTC}
	}

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
		Key: request.Key, Locale: locale, Subject: copy.Subject,
		Text: textOutput.String(), HTML: htmlOutput.String(),
	}, nil
}

func isPersonalAccessTokenTemplate(key Key) bool {
	switch key {
	case IdentityPersonalAccessTokenCreated,
		IdentityPersonalAccessTokenEnabled,
		IdentityPersonalAccessTokenDisabled,
		IdentityPersonalAccessTokenRevoked:
		return true
	default:
		return false
	}
}

func validPersonalAccessTokenDetails(details *PersonalAccessTokenDetails) bool {
	if details == nil || strings.TrimSpace(details.Description) == "" ||
		!utf8.ValidString(details.Description) || utf8.RuneCountInString(details.Description) > 255 ||
		details.ExpiresAt.IsZero() || details.ActionAt.IsZero() ||
		details.ActionCount < 1 || details.ActionCount > 128 {
		return false
	}
	for _, character := range details.Description {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func isExamManagerTemplate(key Key) bool {
	switch key {
	case ExamManagerAdded,
		ExamManagerRemoved,
		ExamOwnershipTransferredToYou,
		ExamOwnershipTransferredFromYou:
		return true
	default:
		return false
	}
}

func validExamManagerDetails(details *ExamManagerDetails) bool {
	if details == nil {
		return false
	}
	title := strings.TrimSpace(details.Title)
	if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 255 || details.ActionAt.IsZero() {
		return false
	}
	switch details.Relationship {
	case ExamManagerRelationshipManager, ExamManagerRelationshipOwner, ExamManagerRelationshipNoLongerManager:
	default:
		return false
	}
	for _, character := range title {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func isSittingScheduleTemplate(key Key) bool {
	switch key {
	case ExamSittingScheduled, ExamSittingRescheduled, ExamSittingCancelled, ExamSittingAssignmentRemoved:
		return true
	default:
		return false
	}
}

func validSittingScheduleDetails(details *SittingScheduleDetails) bool {
	if details == nil || details.StartsAt.IsZero() || !details.StartsAt.Before(details.EndsAt) {
		return false
	}
	for _, value := range []string{details.ExamTitle, details.ClassDisplayName} {
		value = strings.TrimSpace(value)
		if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 {
			return false
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return false
			}
		}
	}
	return true
}

func isClassTransitionTemplate(key Key) bool {
	switch key {
	case AcademicClassEnrolled, AcademicClassEnrollmentEnded, AcademicClassTransferred:
		return true
	default:
		return false
	}
}

func isSubmissionReceiptTemplate(key Key) bool {
	return key == ExamSubmissionReceived || key == ExamSubmissionAutomaticallySealed
}

func validSubmissionReceiptDetails(details *SubmissionReceiptDetails) bool {
	if details == nil || details.SealedAt.IsZero() || !validBoundedMailLabel(details.ExamTitle) {
		return false
	}
	for _, value := range []string{details.SittingID, details.SubmissionID} {
		value = strings.TrimSpace(value)
		if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 {
			return false
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return false
			}
		}
	}
	return true
}

func validClassTransitionDetails(key Key, details *ClassTransitionDetails) bool {
	if details == nil || details.StartsAt.IsZero() || (!details.EndsAt.IsZero() && !details.StartsAt.Before(details.EndsAt)) {
		return false
	}
	previous := strings.TrimSpace(details.PreviousClassDisplayName)
	if (key == AcademicClassTransferred) != (previous != "") || !validBoundedMailLabel(details.ClassDisplayName) ||
		(previous != "" && !validBoundedMailLabel(previous)) {
		return false
	}
	if key == AcademicClassEnrollmentEnded && details.EndsAt.IsZero() {
		return false
	}
	return true
}

func validBoundedMailLabel(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
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
