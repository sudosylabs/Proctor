// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"fmt"

	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
)

type templateCopy struct {
	Subject             string
	Preheader           string
	Heading             string
	Body                string
	ActionLabel         string
	Footer              string
	PersonalAccessToken *personalAccessTokenCopy
	ExamManager         *examManagerCopy
	SittingSchedule     *sittingScheduleCopy
	ClassTransition     *classTransitionCopy
	SubmissionReceipt   *submissionReceiptCopy
	ResultRelease       *resultReleaseCopy
}

type personalAccessTokenCopy struct {
	DescriptionLabel, ExpiresAtLabel, ActionAtLabel, ScopeLabel, ActionCountLabel string
	InstitutionScope, AcademicUnitScope                                           string
}

type examManagerCopy struct {
	ExamLabel, RelationshipLabel, ActionAtLabel string
	Manager, Owner, NoLongerManager             string
}

type sittingScheduleCopy struct {
	ExamLabel, ClassLabel, StartsAtLabel, EndsAtLabel, TimezoneLabel, TimezoneUTC string
}

type classTransitionCopy struct {
	ClassLabel, PreviousClassLabel, NewClassLabel, StartsAtLabel string
	EndsAtLabel, TimezoneLabel, TimezoneUTC, NoScheduledEnd      string
}

type submissionReceiptCopy struct {
	ExamLabel, SittingIDLabel, SubmissionLabel, SealedAtLabel, TimezoneLabel, TimezoneUTC string
}

type resultReleaseCopy struct {
	ExamLabel, ReleasedAtLabel, TimezoneLabel, TimezoneUTC string
}

type copyResolver struct {
	localizer      *localization.Localizer
	key            model.MailTemplateKey
	locale         string
	resolvedLocale string
}

func resolveCopy(localizer *localization.Localizer, key model.MailTemplateKey, locale string) (templateCopy, string, error) {
	r := copyResolver{localizer: localizer, key: key, locale: locale}
	copy := templateCopy{
		Subject: r.required("subject"), Preheader: r.required("preheader"),
		Heading: r.required("heading"), Body: r.required("body"),
		ActionLabel: r.optional("action_label"), Footer: r.required("footer"),
	}
	if rerr := r.err(); rerr != nil {
		return templateCopy{}, "", rerr
	}
	if isPersonalAccessTokenTemplate(key) {
		copy.PersonalAccessToken = &personalAccessTokenCopy{
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
		copy.ExamManager = &examManagerCopy{
			ExamLabel: r.required("exam_manager.exam_label"), RelationshipLabel: r.required("exam_manager.relationship_label"),
			ActionAtLabel: r.required("exam_manager.action_at_label"), Manager: r.required("exam_manager.manager"),
			Owner: r.required("exam_manager.owner"), NoLongerManager: r.required("exam_manager.no_longer_manager"),
		}
	}
	if isSittingScheduleTemplate(key) {
		copy.SittingSchedule = &sittingScheduleCopy{
			ExamLabel: r.required("sitting_schedule.exam_label"), ClassLabel: r.required("sitting_schedule.class_label"),
			StartsAtLabel: r.required("sitting_schedule.starts_at_label"), EndsAtLabel: r.required("sitting_schedule.ends_at_label"),
			TimezoneLabel: r.required("sitting_schedule.timezone_label"), TimezoneUTC: r.required("sitting_schedule.timezone_utc"),
		}
	}
	if isClassTransitionTemplate(key) {
		copy.ClassTransition = &classTransitionCopy{
			ClassLabel: r.required("class_transition.class_label"), PreviousClassLabel: r.required("class_transition.previous_class_label"),
			NewClassLabel: r.required("class_transition.new_class_label"), StartsAtLabel: r.required("class_transition.starts_at_label"),
			EndsAtLabel: r.required("class_transition.ends_at_label"), TimezoneLabel: r.required("class_transition.timezone_label"),
			TimezoneUTC: r.required("class_transition.timezone_utc"), NoScheduledEnd: r.required("class_transition.no_scheduled_end"),
		}
	}
	if isSubmissionReceiptTemplate(key) {
		copy.SubmissionReceipt = &submissionReceiptCopy{
			ExamLabel: r.required("submission_receipt.exam_label"), SittingIDLabel: r.required("submission_receipt.sitting_id_label"),
			SubmissionLabel: r.required("submission_receipt.submission_id_label"), SealedAtLabel: r.required("submission_receipt.sealed_at_label"),
			TimezoneLabel: r.required("submission_receipt.timezone_label"), TimezoneUTC: r.required("submission_receipt.timezone_utc"),
		}
	}
	if key == model.MailTemplateExamResultReleased {
		copy.ResultRelease = &resultReleaseCopy{
			ExamLabel: r.required("result_release.exam_label"), ReleasedAtLabel: r.required("result_release.released_at_label"),
			TimezoneLabel: r.required("result_release.timezone_label"), TimezoneUTC: r.required("result_release.timezone_utc"),
		}
	}
	if rerr := r.err(); rerr != nil {
		return templateCopy{}, "", rerr
	}
	return copy, r.resolvedLocale, nil
}

func (r *copyResolver) messageID(field string) string {
	return localizationID(r.key, field)
}

func (r *copyResolver) required(field string) string {
	value, err := r.localizer.Resolve(r.locale, r.messageID(field), nil)
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
	value, err := r.localizer.Resolve(r.locale, r.messageID(field), nil)
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
