// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
)

const localizationOrigin = "app/mail"

// LocalizationDefinitions returns the closed set of copy consumed by the mail
// renderer. The IDs are derived from the same typed template registry used at
// runtime, so adding a template cannot silently escape catalog validation.
func LocalizationDefinitions() []localization.Definition {
	definitions := make([]localization.Definition, 0, len(model.AllMailTemplateKeys())*6)
	for _, key := range model.AllMailTemplateKeys() {
		for _, field := range localizationFields(key) {
			definitions = append(definitions, localization.Definition{
				ID:     localizationID(key, field),
				Origin: localizationOrigin,
			})
		}
	}
	return definitions
}

func localizationID(key model.MailTemplateKey, field string) string {
	return "mail." + string(key) + "." + field
}

func localizationFields(key model.MailTemplateKey) []string {
	fields := []string{"subject", "preheader", "heading", "body", "footer"}
	if hasActionLabel(key) {
		fields = append(fields, "action_label")
	}
	if isPersonalAccessTokenTemplate(key) {
		fields = append(fields,
			"personal_access_token.description_label",
			"personal_access_token.expires_at_label",
			"personal_access_token.action_at_label",
			"personal_access_token.scope_label",
			"personal_access_token.action_count_label",
			"personal_access_token.institution_scope",
			"personal_access_token.academic_unit_scope",
		)
	}
	if isExamManagerTemplate(key) {
		fields = append(fields,
			"exam_manager.exam_label", "exam_manager.relationship_label",
			"exam_manager.action_at_label", "exam_manager.manager",
			"exam_manager.owner", "exam_manager.no_longer_manager",
		)
	}
	if isSittingScheduleTemplate(key) {
		fields = append(fields,
			"sitting_schedule.exam_label", "sitting_schedule.class_label",
			"sitting_schedule.starts_at_label", "sitting_schedule.ends_at_label",
			"sitting_schedule.timezone_label", "sitting_schedule.timezone_utc",
		)
	}
	if isClassTransitionTemplate(key) {
		fields = append(fields,
			"class_transition.class_label", "class_transition.previous_class_label",
			"class_transition.new_class_label", "class_transition.starts_at_label",
			"class_transition.ends_at_label", "class_transition.timezone_label",
			"class_transition.timezone_utc", "class_transition.no_scheduled_end",
		)
	}
	if isSubmissionReceiptTemplate(key) {
		fields = append(fields,
			"submission_receipt.exam_label", "submission_receipt.sitting_id_label",
			"submission_receipt.submission_id_label", "submission_receipt.sealed_at_label",
			"submission_receipt.timezone_label", "submission_receipt.timezone_utc",
		)
	}
	if key == model.MailTemplateExamResultReleased {
		fields = append(fields,
			"result_release.exam_label", "result_release.released_at_label",
			"result_release.timezone_label", "result_release.timezone_utc",
		)
	}
	return fields
}

func hasActionLabel(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateIdentityVerifyEmail,
		model.MailTemplateIdentityPasswordReset,
		model.MailTemplateIdentityEmailChangeVerifyNew,
		model.MailTemplateAccessStudentClassInvitation,
		model.MailTemplateAccessTeacherAcademicUnitInvitation,
		model.MailTemplateAccessAcademicUnitRoleInvitation,
		model.MailTemplateAccessInstitutionRoleInvitation:
		return true
	default:
		return false
	}
}
