// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
)

const maxRenderedMessageBytes = 1 << 20

// templateProperties is the complete typed model visible to presentation assets.
// It intentionally contains no arbitrary map and offers no template helpers.
type templateProperties struct {
	Copy                templateCopy
	ActionURL           string
	PersonalAccessToken *personalAccessTokenProperties
	ExamManager         *examManagerProperties
	SittingSchedule     *sittingScheduleProperties
	ClassTransition     *classTransitionProperties
	SubmissionReceipt   *submissionReceiptProperties
	ResultRelease       *resultReleaseProperties
}

type personalAccessTokenProperties struct {
	Description string
	ExpiresAt   string
	ActionAt    string
	Scope       string
	ActionCount int
}

type examManagerProperties struct {
	Title        string
	Relationship string
	ActionAt     string
}

type sittingScheduleProperties struct {
	ExamTitle        string
	ClassDisplayName string
	StartsAt         string
	EndsAt           string
	Timezone         string
}

type classTransitionProperties struct {
	PreviousClassDisplayName string
	ClassDisplayName         string
	StartsAt                 string
	EndsAt                   string
	Timezone                 string
}

type submissionReceiptProperties struct {
	ExamTitle    string
	SittingID    string
	SubmissionID string
	SealedAt     string
	Timezone     string
}

type resultReleaseProperties struct {
	ExamTitle  string
	ReleasedAt string
	Timezone   string
}

type renderRequest struct {
	Key                 model.MailTemplateKey
	RecipientLocale     string
	ActionURL           string
	PersonalAccessToken *PersonalAccessTokenDetails
	ExamManager         *ExamManagerDetails
	SittingSchedule     *SittingScheduleDetails
	ClassTransition     *ClassTransitionDetails
	SubmissionReceipt   *SubmissionReceiptDetails
	ResultRelease       *ResultReleaseDetails
}

type renderedMessage struct {
	Key     model.MailTemplateKey
	Locale  string
	Subject string
	Text    string
	HTML    string
}

type templateRenderer struct {
	localizer *localization.Localizer
	html      map[model.MailTemplateKey]*htmltemplate.Template
	text      map[model.MailTemplateKey]*texttemplate.Template
}

// NewRenderer constructs the application-owned renderer from presentation
// assets and the installation localizer.
func NewRenderer(files fs.FS, localizer *localization.Localizer) (Renderer, error) {
	return newRenderer(files, localizer, true)
}

func newRenderer(files fs.FS, localizer *localization.Localizer, validateCompleteCatalog bool) (*templateRenderer, error) {
	if files == nil || localizer == nil {
		return nil, errors.New("mail renderer dependencies are invalid")
	}
	renderer := &templateRenderer{
		localizer: localizer,
		html:      make(map[model.MailTemplateKey]*htmltemplate.Template),
		text:      make(map[model.MailTemplateKey]*texttemplate.Template),
	}
	for _, key := range model.AllMailTemplateKeys() {
		if validateCompleteCatalog {
			if _, _, err := resolveCopy(localizer, key, localization.EnglishLocale); err != nil {
				return nil, fmt.Errorf("validate localized mail copy %q: %w", key, err)
			}
		}
		name := string(key)
		htmlSource, err := fs.ReadFile(files, name+".html")
		if err != nil {
			return nil, fmt.Errorf("read HTML mail template %q: %w", key, err)
		}
		htmlValue, err := htmltemplate.New(name).Option("missingkey=error").Parse(string(htmlSource))
		if err != nil {
			return nil, fmt.Errorf("parse HTML mail template %q: %w", key, err)
		}
		textSource, err := fs.ReadFile(files, name+".txt")
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

func (r *templateRenderer) render(request renderRequest) (renderedMessage, error) {
	if r == nil || r.localizer == nil {
		return renderedMessage{}, errors.New("mail template renderer is nil")
	}
	copy, locale, err := resolveCopy(r.localizer, request.Key, request.RecipientLocale)
	if err != nil {
		return renderedMessage{}, err
	}
	actionURL := strings.TrimSpace(request.ActionURL)
	if copy.ActionLabel == "" {
		actionURL = ""
	} else {
		if err := validateActionURL(actionURL); err != nil {
			return renderedMessage{}, err
		}
	}
	properties := templateProperties{Copy: copy, ActionURL: actionURL}
	patKey := isPersonalAccessTokenTemplate(request.Key)
	if patKey != (request.PersonalAccessToken != nil) {
		return renderedMessage{}, fmt.Errorf("mail template %q has invalid PAT details", request.Key)
	}
	if request.PersonalAccessToken != nil {
		if copy.PersonalAccessToken == nil || !validPersonalAccessTokenDetails(request.PersonalAccessToken) {
			return renderedMessage{}, fmt.Errorf("mail template %q has invalid PAT details", request.Key)
		}
		scope := copy.PersonalAccessToken.InstitutionScope
		if request.PersonalAccessToken.AcademicUnitScoped {
			scope = copy.PersonalAccessToken.AcademicUnitScope
		}
		properties.PersonalAccessToken = &personalAccessTokenProperties{
			Description: request.PersonalAccessToken.Description,
			ExpiresAt:   request.PersonalAccessToken.ExpiresAt.UTC().Format(time.RFC3339),
			ActionAt:    request.PersonalAccessToken.ActionAt.UTC().Format(time.RFC3339),
			Scope:       scope,
			ActionCount: request.PersonalAccessToken.ActionCount,
		}
	}
	examManagerKey := isExamManagerTemplate(request.Key)
	if examManagerKey != (request.ExamManager != nil) {
		return renderedMessage{}, fmt.Errorf("mail template %q has invalid Exam Manager details", request.Key)
	}
	if request.ExamManager != nil {
		if copy.ExamManager == nil || !validExamManagerDetails(request.ExamManager) {
			return renderedMessage{}, fmt.Errorf("mail template %q has invalid Exam Manager details", request.Key)
		}
		var relationship string
		switch request.ExamManager.Relationship {
		case "manager":
			relationship = copy.ExamManager.Manager
		case "owner":
			relationship = copy.ExamManager.Owner
		case "no_longer_manager":
			relationship = copy.ExamManager.NoLongerManager
		}
		properties.ExamManager = &examManagerProperties{
			Title: strings.TrimSpace(request.ExamManager.Title), Relationship: relationship,
			ActionAt: request.ExamManager.ActionAt.UTC().Format(time.RFC3339),
		}
	}
	sittingKey := isSittingScheduleTemplate(request.Key)
	if sittingKey != (request.SittingSchedule != nil) {
		return renderedMessage{}, fmt.Errorf("mail template %q has invalid Sitting schedule details", request.Key)
	}
	if request.SittingSchedule != nil {
		if copy.SittingSchedule == nil || !validSittingScheduleDetails(request.SittingSchedule) {
			return renderedMessage{}, fmt.Errorf("mail template %q has invalid Sitting schedule details", request.Key)
		}
		properties.SittingSchedule = &sittingScheduleProperties{
			ExamTitle:        strings.TrimSpace(request.SittingSchedule.ExamTitle),
			ClassDisplayName: strings.TrimSpace(request.SittingSchedule.ClassDisplayName),
			StartsAt:         request.SittingSchedule.StartsAt.UTC().Format(time.RFC3339),
			EndsAt:           request.SittingSchedule.EndsAt.UTC().Format(time.RFC3339),
			Timezone:         copy.SittingSchedule.TimezoneUTC,
		}
	}
	classKey := isClassTransitionTemplate(request.Key)
	if classKey != (request.ClassTransition != nil) {
		return renderedMessage{}, fmt.Errorf("mail template %q has invalid Class transition details", request.Key)
	}
	if request.ClassTransition != nil {
		if copy.ClassTransition == nil || !validClassTransitionDetails(request.Key, request.ClassTransition) {
			return renderedMessage{}, fmt.Errorf("mail template %q has invalid Class transition details", request.Key)
		}
		endsAt := copy.ClassTransition.NoScheduledEnd
		if !request.ClassTransition.EndsAt.IsZero() {
			endsAt = request.ClassTransition.EndsAt.UTC().Format(time.RFC3339)
		}
		properties.ClassTransition = &classTransitionProperties{
			PreviousClassDisplayName: strings.TrimSpace(request.ClassTransition.PreviousClassDisplayName),
			ClassDisplayName:         strings.TrimSpace(request.ClassTransition.ClassDisplayName),
			StartsAt:                 request.ClassTransition.StartsAt.UTC().Format(time.RFC3339), EndsAt: endsAt,
			Timezone: copy.ClassTransition.TimezoneUTC,
		}
	}
	submissionReceiptKey := isSubmissionReceiptTemplate(request.Key)
	if submissionReceiptKey != (request.SubmissionReceipt != nil) {
		return renderedMessage{}, fmt.Errorf("mail template %q has invalid Submission receipt details", request.Key)
	}
	if request.SubmissionReceipt != nil {
		if copy.SubmissionReceipt == nil || !validSubmissionReceiptDetails(request.SubmissionReceipt) {
			return renderedMessage{}, fmt.Errorf("mail template %q has invalid Submission receipt details", request.Key)
		}
		properties.SubmissionReceipt = &submissionReceiptProperties{
			ExamTitle:    strings.TrimSpace(request.SubmissionReceipt.ExamTitle),
			SittingID:    request.SubmissionReceipt.SittingID.String(),
			SubmissionID: request.SubmissionReceipt.SubmissionID.String(),
			SealedAt:     request.SubmissionReceipt.SealedAt.UTC().Format(time.RFC3339),
			Timezone:     copy.SubmissionReceipt.TimezoneUTC,
		}
	}
	resultReleaseKey := request.Key == model.MailTemplateExamResultReleased
	if resultReleaseKey != (request.ResultRelease != nil) {
		return renderedMessage{}, fmt.Errorf("mail template %q has invalid result release details", request.Key)
	}
	if request.ResultRelease != nil {
		if copy.ResultRelease == nil || request.ResultRelease.ReleasedAt.IsZero() ||
			!validBoundedMailLabel(request.ResultRelease.ExamTitle) {
			return renderedMessage{}, fmt.Errorf("mail template %q has invalid result release details", request.Key)
		}
		properties.ResultRelease = &resultReleaseProperties{ExamTitle: strings.TrimSpace(request.ResultRelease.ExamTitle),
			ReleasedAt: request.ResultRelease.ReleasedAt.UTC().Format(time.RFC3339),
			Timezone:   copy.ResultRelease.TimezoneUTC}
	}

	htmlValue, ok := r.html[request.Key]
	if !ok {
		return renderedMessage{}, fmt.Errorf("HTML mail template %q is unavailable", request.Key)
	}
	textValue, ok := r.text[request.Key]
	if !ok {
		return renderedMessage{}, fmt.Errorf("text mail template %q is unavailable", request.Key)
	}
	var htmlOutput bytes.Buffer
	if err := htmlValue.Execute(&htmlOutput, properties); err != nil {
		return renderedMessage{}, fmt.Errorf("render HTML mail template %q: %w", request.Key, err)
	}
	var textOutput bytes.Buffer
	if err := textValue.Execute(&textOutput, properties); err != nil {
		return renderedMessage{}, fmt.Errorf("render text mail template %q: %w", request.Key, err)
	}
	if htmlOutput.Len()+textOutput.Len() > maxRenderedMessageBytes {
		return renderedMessage{}, fmt.Errorf("rendered mail template %q exceeds %d bytes", request.Key, maxRenderedMessageBytes)
	}
	return renderedMessage{
		Key: request.Key, Locale: locale, Subject: copy.Subject,
		Text: textOutput.String(), HTML: htmlOutput.String(),
	}, nil
}

func (r *templateRenderer) Render(key model.MailTemplateKey, locale, actionURL string) (FrozenContent, error) {
	return r.renderContent(renderRequest{Key: key, RecipientLocale: locale, ActionURL: actionURL})
}

func (r *templateRenderer) RenderPersonalAccessTokenSecurityNotice(
	key model.MailTemplateKey,
	locale string,
	details PersonalAccessTokenDetails,
) (FrozenContent, error) {
	return r.renderContent(renderRequest{Key: key, RecipientLocale: locale, PersonalAccessToken: &details})
}

func (r *templateRenderer) RenderExamManagerNotice(
	key model.MailTemplateKey,
	locale string,
	details ExamManagerDetails,
) (FrozenContent, error) {
	return r.renderContent(renderRequest{Key: key, RecipientLocale: locale, ExamManager: &details})
}

func (r *templateRenderer) RenderClassTransitionNotice(
	key model.MailTemplateKey,
	locale string,
	details ClassTransitionDetails,
) (FrozenContent, error) {
	return r.renderContent(renderRequest{Key: key, RecipientLocale: locale, ClassTransition: &details})
}

func (r *templateRenderer) RenderSubmissionReceipt(
	key model.MailTemplateKey,
	locale string,
	details SubmissionReceiptDetails,
) (FrozenContent, error) {
	return r.renderContent(renderRequest{Key: key, RecipientLocale: locale, SubmissionReceipt: &details})
}

func (r *templateRenderer) RenderResultRelease(
	key model.MailTemplateKey,
	locale string,
	details ResultReleaseDetails,
) (FrozenContent, error) {
	return r.renderContent(renderRequest{Key: key, RecipientLocale: locale, ResultRelease: &details})
}

func (r *templateRenderer) RenderSittingScheduleNotice(
	key model.MailTemplateKey,
	locale string,
	details SittingScheduleDetails,
) (FrozenContent, error) {
	return r.renderContent(renderRequest{Key: key, RecipientLocale: locale, SittingSchedule: &details})
}

func (r *templateRenderer) renderContent(request renderRequest) (FrozenContent, error) {
	message, err := r.render(request)
	if err != nil {
		return FrozenContent{}, err
	}
	return FrozenContent{Subject: message.Subject, Text: message.Text, HTML: message.HTML}, nil
}

func isPersonalAccessTokenTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateIdentityPersonalAccessTokenCreated,
		model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenDisabled,
		model.MailTemplateIdentityPersonalAccessTokenRevoked:
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

func isExamManagerTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateExamManagerAdded,
		model.MailTemplateExamManagerRemoved,
		model.MailTemplateExamOwnershipTransferredToYou,
		model.MailTemplateExamOwnershipTransferredFromYou:
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
	case "manager", "owner", "no_longer_manager":
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

func isSittingScheduleTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateExamSittingScheduled, model.MailTemplateExamSittingRescheduled,
		model.MailTemplateExamSittingCancelled, model.MailTemplateExamSittingAssignmentRemoved:
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

func isClassTransitionTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateAcademicClassEnrolled, model.MailTemplateAcademicClassEnrollmentEnded,
		model.MailTemplateAcademicClassTransferred:
		return true
	default:
		return false
	}
}

func isSubmissionReceiptTemplate(key model.MailTemplateKey) bool {
	return key == model.MailTemplateExamSubmissionReceived || key == model.MailTemplateExamSubmissionAutomaticallySealed
}

func validSubmissionReceiptDetails(details *SubmissionReceiptDetails) bool {
	if details == nil || details.SealedAt.IsZero() || !validBoundedMailLabel(details.ExamTitle) {
		return false
	}
	return details.SittingID.IsValid() && details.SubmissionID.IsValid()
}

func validClassTransitionDetails(key model.MailTemplateKey, details *ClassTransitionDetails) bool {
	if details == nil || details.StartsAt.IsZero() || (!details.EndsAt.IsZero() && !details.StartsAt.Before(details.EndsAt)) {
		return false
	}
	previous := strings.TrimSpace(details.PreviousClassDisplayName)
	if (key == model.MailTemplateAcademicClassTransferred) != (previous != "") || !validBoundedMailLabel(details.ClassDisplayName) ||
		(previous != "" && !validBoundedMailLabel(previous)) {
		return false
	}
	if key == model.MailTemplateAcademicClassEnrollmentEnded && details.EndsAt.IsZero() {
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
