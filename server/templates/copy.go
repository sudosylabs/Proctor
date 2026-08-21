// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package templates

import (
	"fmt"

	"github.com/sudosylabs/proctor/server/i18n"
)

// Copy is the complete, markup-free prose model shared by HTML and text mail.
type Copy struct {
	Subject             string
	Preheader           string
	Heading             string
	Body                string
	ActionLabel         string
	Footer              string
	PersonalAccessToken *PersonalAccessTokenCopy
	ExamManager         *ExamManagerCopy
	SittingSchedule     *SittingScheduleCopy
	ClassTransition     *ClassTransitionCopy
	SubmissionReceipt   *SubmissionReceiptCopy
	ResultRelease       *ResultReleaseCopy
}

type PersonalAccessTokenCopy struct {
	DescriptionLabel, ExpiresAtLabel, ActionAtLabel, ScopeLabel, ActionCountLabel string
	InstitutionScope, AcademicUnitScope                                           string
}

type ExamManagerCopy struct {
	ExamLabel, RelationshipLabel, ActionAtLabel string
	Manager, Owner, NoLongerManager             string
}

type SittingScheduleCopy struct {
	ExamLabel, ClassLabel, StartsAtLabel, EndsAtLabel, TimezoneLabel, TimezoneUTC string
}

type ClassTransitionCopy struct {
	ClassLabel, PreviousClassLabel, NewClassLabel, StartsAtLabel string
	EndsAtLabel, TimezoneLabel, TimezoneUTC, NoScheduledEnd      string
}

type SubmissionReceiptCopy struct {
	ExamLabel, SittingIDLabel, SubmissionLabel, SealedAtLabel, TimezoneLabel, TimezoneUTC string
}

type ResultReleaseCopy struct {
	ExamLabel, ReleasedAtLabel, TimezoneLabel, TimezoneUTC string
}

type copyResolver struct {
	bundle             *i18n.Bundle
	key                Key
	locale             string
	installationLocale string
	resolvedLocale     string
}

func resolveCopy(bundle *i18n.Bundle, key Key, locale, installationLocale string) (Copy, string, error) {
	r := copyResolver{bundle: bundle, key: key, locale: locale, installationLocale: installationLocale}
	copy := Copy{
		Subject: r.required("subject"), Preheader: r.required("preheader"),
		Heading: r.required("heading"), Body: r.required("body"),
		ActionLabel: r.optional("action_label"), Footer: r.required("footer"),
	}
	if rerr := r.err(); rerr != nil {
		return Copy{}, "", rerr
	}
	if isPersonalAccessTokenTemplate(key) {
		copy.PersonalAccessToken = &PersonalAccessTokenCopy{
			DescriptionLabel:  r.required("personal_access_token.description_label"),
			ExpiresAtLabel:    r.required("personal_access_token.expires_at_label"),
			ActionAtLabel:     r.required("personal_access_token.action_at_label"),
			ScopeLabel:        r.required("personal_access_token.scope_label"),
			ActionCountLabel:  r.required("personal_access_token.action_count_label"),
			InstitutionScope:  r.required("personal_access_token.institution_scope"),
			AcademicUnitScope: r.required("personal_access_token.academic_unit_scope"),
		}
	}
	if isExamManagerTemplate(key) {
		copy.ExamManager = &ExamManagerCopy{
			ExamLabel: r.required("exam_manager.exam_label"), RelationshipLabel: r.required("exam_manager.relationship_label"),
			ActionAtLabel: r.required("exam_manager.action_at_label"), Manager: r.required("exam_manager.manager"),
			Owner: r.required("exam_manager.owner"), NoLongerManager: r.required("exam_manager.no_longer_manager"),
		}
	}
	if isSittingScheduleTemplate(key) {
		copy.SittingSchedule = &SittingScheduleCopy{
			ExamLabel: r.required("sitting_schedule.exam_label"), ClassLabel: r.required("sitting_schedule.class_label"),
			StartsAtLabel: r.required("sitting_schedule.starts_at_label"), EndsAtLabel: r.required("sitting_schedule.ends_at_label"),
			TimezoneLabel: r.required("sitting_schedule.timezone_label"), TimezoneUTC: r.required("sitting_schedule.timezone_utc"),
		}
	}
	if isClassTransitionTemplate(key) {
		copy.ClassTransition = &ClassTransitionCopy{
			ClassLabel: r.required("class_transition.class_label"), PreviousClassLabel: r.required("class_transition.previous_class_label"),
			NewClassLabel: r.required("class_transition.new_class_label"), StartsAtLabel: r.required("class_transition.starts_at_label"),
			EndsAtLabel: r.required("class_transition.ends_at_label"), TimezoneLabel: r.required("class_transition.timezone_label"),
			TimezoneUTC: r.required("class_transition.timezone_utc"), NoScheduledEnd: r.required("class_transition.no_scheduled_end"),
		}
	}
	if isSubmissionReceiptTemplate(key) {
		copy.SubmissionReceipt = &SubmissionReceiptCopy{
			ExamLabel: r.required("submission_receipt.exam_label"), SittingIDLabel: r.required("submission_receipt.sitting_id_label"),
			SubmissionLabel: r.required("submission_receipt.submission_id_label"), SealedAtLabel: r.required("submission_receipt.sealed_at_label"),
			TimezoneLabel: r.required("submission_receipt.timezone_label"), TimezoneUTC: r.required("submission_receipt.timezone_utc"),
		}
	}
	if key == ExamResultReleased {
		copy.ResultRelease = &ResultReleaseCopy{
			ExamLabel: r.required("result_release.exam_label"), ReleasedAtLabel: r.required("result_release.released_at_label"),
			TimezoneLabel: r.required("result_release.timezone_label"), TimezoneUTC: r.required("result_release.timezone_utc"),
		}
	}
	if rerr := r.err(); rerr != nil {
		return Copy{}, "", rerr
	}
	return copy, r.resolvedLocale, nil
}

func (r *copyResolver) messageID(field string) i18n.Key {
	return i18n.Key("mail." + string(r.key) + "." + field)
}

func (r *copyResolver) required(field string) string {
	value, err := r.bundle.Translate(r.locale, r.messageID(field), nil)
	if err != nil {
		if r.installationLocale != "" && r.installationLocale != r.locale {
			value, err = r.bundle.Translate(r.installationLocale, r.messageID(field), nil)
		}
	}
	if err != nil {
		r.resolvedLocale = "!" + err.Error()
		return ""
	}
	if r.resolvedLocale == "" {
		r.resolvedLocale = value.Locale
	}
	return value.Text
}

func (r *copyResolver) optional(field string) string {
	value, err := r.bundle.Translate(r.locale, r.messageID(field), nil)
	if err != nil {
		return ""
	}
	return value.Text
}

func (r *copyResolver) err() error {
	if len(r.resolvedLocale) > 0 && r.resolvedLocale[0] == '!' {
		return fmt.Errorf("resolve mail copy %q: %s", r.key, r.resolvedLocale[1:])
	}
	return nil
}
