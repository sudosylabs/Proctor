// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mail

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	directMailLifetime     = 72 * time.Hour
	securityNoticeLifetime = 24 * time.Hour
)

type presentationKind uint8

const (
	presentationBase presentationKind = iota
	presentationPersonalAccessToken
	presentationExamManager
	presentationSittingSchedule
	presentationClassTransition
	presentationSubmissionReceipt
	presentationResultRelease
)

type definition struct {
	key             model.MailTemplateKey
	kind            model.MailOccurrenceKind
	jobType         model.JobType
	defaultLifetime time.Duration
	actionRequired  bool
	presentation    presentationKind
}

var mailCatalog = func() map[model.MailTemplateKey]definition {
	result := make(map[model.MailTemplateKey]definition, len(model.AllMailTemplateKeys()))
	for _, key := range model.AllMailTemplateKeys() {
		kind, ok := key.OccurrenceKind()
		if !ok {
			panic("mail catalog contains a template without occurrence meaning")
		}
		lifetime := directMailLifetime
		if kind == model.MailOccurrenceSecurityNotice || key == model.MailTemplateSystemTest ||
			key == model.MailTemplateAccessInvitationAccepted || key == model.MailTemplateAccessInvitationRevoked {
			lifetime = securityNoticeLifetime
		}
		result[key] = definition{key: key, kind: kind, jobType: model.JobTypeMailDeliver,
			defaultLifetime: lifetime, presentation: presentationBase}
	}
	setDefinitionGroup(result, []model.MailTemplateKey{
		model.MailTemplateIdentityVerifyEmail,
		model.MailTemplateIdentityPasswordReset,
		model.MailTemplateIdentityEmailChangeVerifyNew,
		model.MailTemplateAccessStudentClassInvitation,
		model.MailTemplateAccessTeacherAcademicUnitInvitation,
		model.MailTemplateAccessAcademicUnitRoleInvitation,
		model.MailTemplateAccessInstitutionRoleInvitation,
	}, func(value definition) definition {
		value.jobType = model.JobTypeMailDeliverCredential
		value.defaultLifetime = 0
		value.actionRequired = true
		return value
	})
	setPresentation(result, presentationPersonalAccessToken, []model.MailTemplateKey{
		model.MailTemplateIdentityPersonalAccessTokenCreated,
		model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenDisabled,
		model.MailTemplateIdentityPersonalAccessTokenRevoked,
	})
	setPresentation(result, presentationExamManager, []model.MailTemplateKey{
		model.MailTemplateExamManagerAdded,
		model.MailTemplateExamManagerRemoved,
		model.MailTemplateExamOwnershipTransferredToYou,
		model.MailTemplateExamOwnershipTransferredFromYou,
	})
	setPresentation(result, presentationSittingSchedule, sittingTemplateKeys())
	setPresentation(result, presentationClassTransition, []model.MailTemplateKey{
		model.MailTemplateAcademicClassEnrolled,
		model.MailTemplateAcademicClassEnrollmentEnded,
		model.MailTemplateAcademicClassTransferred,
	})
	setPresentation(result, presentationSubmissionReceipt, []model.MailTemplateKey{
		model.MailTemplateExamSubmissionReceived,
		model.MailTemplateExamSubmissionAutomaticallySealed,
	})
	setPresentation(result, presentationResultRelease, []model.MailTemplateKey{model.MailTemplateExamResultReleased})
	return result
}()

func setDefinitionGroup(catalog map[model.MailTemplateKey]definition, keys []model.MailTemplateKey,
	update func(definition) definition,
) {
	for _, key := range keys {
		catalog[key] = update(catalog[key])
	}
}

func setPresentation(catalog map[model.MailTemplateKey]definition, presentation presentationKind,
	keys []model.MailTemplateKey,
) {
	setDefinitionGroup(catalog, keys, func(value definition) definition {
		value.presentation = presentation
		return value
	})
}

func definitionFor(key model.MailTemplateKey) (definition, bool) {
	value, ok := mailCatalog[key]
	return value, ok
}

func defaultLifetimeFor(key model.MailTemplateKey) (time.Duration, bool) {
	value, ok := definitionFor(key)
	return value.defaultLifetime, ok && value.defaultLifetime > 0
}
