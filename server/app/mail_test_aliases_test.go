// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

// These aliases keep focused tests readable while production code refers to
// the owning mail package directly. They are not part of the application
// package's production interface.
import (
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
)

type FrozenMailContent = appmail.FrozenContent
type DirectMailPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	Kind         model.MailOccurrenceKind
	TemplateKey  model.MailTemplateKey
	ActionURL    string
	At           time.Time
	Deadline     time.Time
	JobType      model.JobType
}
type AccountTokenMailPreparation = appmail.AccountTokenPreparation
type NoticeMailPreparation = appmail.NoticePreparation
type MFAMailNoticeKind = appmail.MFANoticeKind
type MailAddress = appmail.Address
type OutboundMail = appmail.Outbound
type MailTransportOutcome = appmail.TransportOutcome
type PersonalAccessTokenMailDetails = appmail.PersonalAccessTokenDetails
type ExamManagerMailDetails = appmail.ExamManagerDetails
type ClassTransitionMailDetails = appmail.ClassTransitionDetails
type SubmissionReceiptMailDetails = appmail.SubmissionReceiptDetails
type ResultReleaseMailDetails = appmail.ResultReleaseDetails
type MailRenderRequest = appmail.RenderRequest
type DirectMailTemplateRenderer = appmail.Renderer
type MailDeliverySender = appmail.Sender
type frozenMailPayloadV1 = appmail.FrozenPayloadV1
type directMailPreparer = appmail.Composer
type ClassTransitionMailPreparation = appmail.ClassTransitionPreparation
type ResultReleaseDirectMailPreparation = appmail.ResultReleasePreparation
type SubmissionReceiptMailPreparation = appmail.SubmissionReceiptPreparation
type personalAccessTokenSecurityNoticePreparation = appmail.PersonalAccessTokenPreparation
type relationshipTransitionMailPreparation = appmail.RelationshipTransitionPreparation
type SittingScheduleMailDetails = appmail.SittingScheduleDetails
type SittingMailTemplateRenderer = appmail.SittingRenderer
type sittingMailPreparer = appmail.SittingComposer

const (
	MailTransportUnknown                  = appmail.TransportUnknown
	MailTransportTemporary                = appmail.TransportTemporary
	MailTransportPermanent                = appmail.TransportPermanent
	MailTransportAcceptanceUncertain      = appmail.TransportAcceptanceUncertain
	MFAMailNoticeEnabled                  = appmail.MFANoticeEnabled
	MFAMailNoticeDisabled                 = appmail.MFANoticeDisabled
	MFAMailNoticeRecoveryCodesRegenerated = appmail.MFANoticeRecoveryCodesRegenerated
)
