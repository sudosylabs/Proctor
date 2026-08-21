// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// This file contains direct-mail composition and shared frozen-payload rules.
package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	DeliverySealingPurpose = "mail.delivery"
	directMailLifetime     = 72 * time.Hour
	securityNoticeLifetime = 24 * time.Hour
)

type FrozenContent struct{ Subject, Text, HTML string }

type Address struct {
	Name    string
	Address string
}

type Outbound struct {
	From         Address
	EnvelopeFrom string
	To           Address
	Subject      string
	Text         string
	HTML         string
	Headers      map[string][]string
	MessageID    string
	Date         time.Time
}

type TransportOutcome string

const (
	TransportUnknown             TransportOutcome = "unknown"
	TransportTemporary           TransportOutcome = "temporary"
	TransportPermanent           TransportOutcome = "permanent"
	TransportAcceptanceUncertain TransportOutcome = "acceptance_uncertain"
)

type Sender interface {
	Enabled() bool
	From() Address
	Send(context.Context, Outbound) (TransportOutcome, error)
}

type PersonalAccessTokenDetails struct {
	Description        string
	ExpiresAt          time.Time
	ActionAt           time.Time
	ActionCount        int
	AcademicUnitScoped bool
}

type ExamManagerDetails struct {
	Title        string
	Relationship string
	ActionAt     time.Time
}

type ClassTransitionDetails struct {
	PreviousClassDisplayName string
	ClassDisplayName         string
	StartsAt                 time.Time
	EndsAt                   time.Time
}

type SubmissionReceiptDetails struct {
	ExamTitle    string
	SittingID    model.ExamSittingID
	SubmissionID model.SubmissionID
	SealedAt     time.Time
}

type ResultReleaseDetails struct {
	ExamTitle  string
	ReleasedAt time.Time
}

type Renderer interface {
	Render(model.MailTemplateKey, string, string) (FrozenContent, error)
	RenderPersonalAccessTokenSecurityNotice(model.MailTemplateKey, string, PersonalAccessTokenDetails) (FrozenContent, error)
	RenderExamManagerNotice(model.MailTemplateKey, string, ExamManagerDetails) (FrozenContent, error)
	RenderClassTransitionNotice(model.MailTemplateKey, string, ClassTransitionDetails) (FrozenContent, error)
	RenderSubmissionReceipt(model.MailTemplateKey, string, SubmissionReceiptDetails) (FrozenContent, error)
	RenderResultRelease(model.MailTemplateKey, string, ResultReleaseDetails) (FrozenContent, error)
}

type details interface {
	render(Renderer, model.MailTemplateKey, string) (FrozenContent, error)
}

func (value PersonalAccessTokenDetails) render(r Renderer, key model.MailTemplateKey, locale string) (FrozenContent, error) {
	return r.RenderPersonalAccessTokenSecurityNotice(key, locale, value)
}
func (value ExamManagerDetails) render(r Renderer, key model.MailTemplateKey, locale string) (FrozenContent, error) {
	return r.RenderExamManagerNotice(key, locale, value)
}
func (value ClassTransitionDetails) render(r Renderer, key model.MailTemplateKey, locale string) (FrozenContent, error) {
	return r.RenderClassTransitionNotice(key, locale, value)
}
func (value SubmissionReceiptDetails) render(r Renderer, key model.MailTemplateKey, locale string) (FrozenContent, error) {
	return r.RenderSubmissionReceipt(key, locale, value)
}
func (value ResultReleaseDetails) render(r Renderer, key model.MailTemplateKey, locale string) (FrozenContent, error) {
	return r.RenderResultRelease(key, locale, value)
}

type FrozenPayloadV1 struct {
	Version              int    `json:"version"`
	RecipientName        string `json:"recipient_name"`
	RecipientAddress     string `json:"recipient_address"`
	FromName             string `json:"from_name"`
	FromAddress          string `json:"from_address"`
	Subject              string `json:"subject"`
	Text                 string `json:"text"`
	HTML                 string `json:"html"`
	AutoSubmitted        string `json:"auto_submitted"`
	AutoResponseSuppress string `json:"auto_response_suppress"`
}

type DirectPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	Kind         model.MailOccurrenceKind
	TemplateKey  model.MailTemplateKey
	ActionURL    string
	At           time.Time
	Deadline     time.Time
	JobType      model.JobType
}

type SecurityNoticePreparation struct {
	Recipient   *model.User
	TemplateKey model.MailTemplateKey
	At          time.Time
}

type PersonalAccessTokenPreparation struct {
	Recipient          *model.User
	TemplateKey        model.MailTemplateKey
	Description        string
	ExpiresAt          time.Time
	ActionAt           time.Time
	ActionCount        int
	AcademicUnitScoped bool
}

type ClassTransitionPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	Details      ClassTransitionDetails
	ActionAt     time.Time
}

type SubmissionReceiptPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	Details      SubmissionReceiptDetails
	ActionAt     time.Time
}

type ResultReleasePreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	Details      ResultReleaseDetails
	ReleasedAt   time.Time
}

// RelationshipTransitionPreparation covers the generic Academic Unit
// membership and Role Binding notices. Their templates intentionally contain
// no privileged scope or role detail.
type RelationshipTransitionPreparation struct {
	Recipient    *model.User
	OccurrenceID model.MailOccurrenceID
	TemplateKey  model.MailTemplateKey
	ActionAt     time.Time
}

type Composer struct {
	renderer Renderer
	sender   Sender
	sealer   *secretseal.Sealer
}

func NewComposer(renderer Renderer, sender Sender, sealer *secretseal.Sealer) (*Composer, error) {
	if renderer == nil || sender == nil || sender.Enabled() && sealer == nil {
		return nil, errors.New("mail composer dependencies are invalid")
	}
	return &Composer{renderer: renderer, sender: sender, sealer: sealer}, nil
}

func (p *Composer) Enabled() bool {
	return p != nil && p.sender != nil && p.sender.Enabled() && p.sealer != nil
}

func (p *Composer) PrepareDirect(request DirectPreparation) (*store.PreparedMail, error) {
	user := request.Recipient
	if p == nil || p.sender == nil || user == nil || user.Validate() != nil || !user.IsActive() ||
		!request.OccurrenceID.IsValid() || !request.TemplateKey.IsValid() || request.At.IsZero() ||
		!request.Deadline.After(request.At) || request.JobType != model.JobTypeMailDeliver && request.JobType != model.JobTypeMailDeliverCredential {
		return nil, errors.New("direct mail input is invalid")
	}
	return p.prepareRecipient(user.DisplayName, user.Email, user.Locale, user.ID, user.ID, "", request.OccurrenceID,
		request.Kind, request.TemplateKey, request.ActionURL, request.At, request.Deadline, request.JobType, nil)
}

func (p *Composer) PrepareInvitation(invitation *model.Invitation, actionURL string) (*store.PreparedMail, error) {
	if !p.Enabled() || invitation == nil || invitation.Validate() != nil || invitation.State != model.InvitationPending {
		return nil, errors.New("invitation mail input is invalid")
	}
	key, err := InvitationTemplateKey(invitation.Purpose)
	if err != nil {
		return nil, err
	}
	return p.prepareRecipient(invitation.Suggestions.DisplayName, invitation.TargetEmail, invitation.Suggestions.Locale,
		invitation.InviterUserID, "", invitation.ID, model.MailOccurrenceID(invitation.ID.String()), model.MailOccurrenceInvitation,
		key, actionURL, invitation.CreatedAt, invitation.ExpiresAt, model.JobTypeMailDeliverCredential, nil)
}

func (p *Composer) PrepareInvitationResend(invitation *model.Invitation, actionURL string, actor model.UserID, at time.Time) (*store.PreparedMail, error) {
	if !p.Enabled() || invitation == nil || invitation.Validate() != nil || invitation.State != model.InvitationPending ||
		!actor.IsValid() || at.IsZero() || !model.TimeUTC(at).Before(invitation.ExpiresAt) {
		return nil, errors.New("invitation resend mail input is invalid")
	}
	key, err := InvitationTemplateKey(invitation.Purpose)
	if err != nil {
		return nil, err
	}
	return p.prepareRecipient(invitation.Suggestions.DisplayName, invitation.TargetEmail, invitation.Suggestions.Locale,
		actor, "", invitation.ID, model.NewMailOccurrenceID(), model.MailOccurrenceInvitation, key, actionURL, at,
		invitation.ExpiresAt, model.JobTypeMailDeliverCredential, nil)
}

func (p *Composer) PrepareInvitationRevocation(invitation *model.Invitation, actor model.UserID, at time.Time) (*store.PreparedMail, error) {
	if p == nil || invitation == nil || invitation.Validate() != nil || invitation.State != model.InvitationPending || !actor.IsValid() || at.IsZero() {
		return nil, errors.New("invitation revocation mail input is invalid")
	}
	return p.prepareRecipient(invitation.Suggestions.DisplayName, invitation.TargetEmail, invitation.Suggestions.Locale,
		actor, "", invitation.ID, model.NewMailOccurrenceID(), model.MailOccurrenceInvitation,
		model.MailTemplateAccessInvitationRevoked, "", at, model.TimeUTC(at).Add(24*time.Hour), model.JobTypeMailDeliver, nil)
}

func InvitationTemplateKey(purpose model.InvitationPurpose) (model.MailTemplateKey, error) {
	switch purpose {
	case model.InvitationPurposeStudentClass:
		return model.MailTemplateAccessStudentClassInvitation, nil
	case model.InvitationPurposeTeacherAcademicUnit:
		return model.MailTemplateAccessTeacherAcademicUnitInvitation, nil
	case model.InvitationPurposeAcademicUnitRole:
		return model.MailTemplateAccessAcademicUnitRoleInvitation, nil
	case model.InvitationPurposeInstitutionRole:
		return model.MailTemplateAccessInstitutionRoleInvitation, nil
	default:
		return "", errors.New("invitation mail purpose is not implemented")
	}
}

func (p *Composer) PrepareSecurityNotice(request SecurityNoticePreparation) (*store.PreparedMail, error) {
	return p.PrepareDirect(DirectPreparation{Recipient: request.Recipient, OccurrenceID: model.NewMailOccurrenceID(),
		Kind: model.MailOccurrenceSecurityNotice, TemplateKey: request.TemplateKey, At: request.At,
		Deadline: request.At.Add(securityNoticeLifetime), JobType: model.JobTypeMailDeliver})
}

func (p *Composer) PreparePersonalAccessTokenSecurityNotice(request PersonalAccessTokenPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.Recipient.IsActive() || !isPATTemplate(request.TemplateKey) {
		return nil, errors.New("personal access token mail template is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice,
		request.TemplateKey, "", at, at.Add(securityNoticeLifetime), model.JobTypeMailDeliver,
		PersonalAccessTokenDetails{Description: request.Description, ExpiresAt: request.ExpiresAt, ActionAt: at,
			ActionCount: request.ActionCount, AcademicUnitScoped: request.AcademicUnitScoped})
}

func isPATTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateIdentityPersonalAccessTokenCreated, model.MailTemplateIdentityPersonalAccessTokenEnabled,
		model.MailTemplateIdentityPersonalAccessTokenDisabled, model.MailTemplateIdentityPersonalAccessTokenRevoked:
		return true
	default:
		return false
	}
}

func (p *Composer) PrepareClassTransition(request ClassTransitionPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!validClassTransition(request.TemplateKey, request.Details) || request.ActionAt.IsZero() {
		return nil, errors.New("class transition mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			model.MailOccurrenceAcademicAdministration, request.TemplateKey, at, at.Add(directMailLifetime), model.JobTypeMailDeliver,
			model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, model.MailOccurrenceAcademicAdministration,
		request.TemplateKey, "", at, at.Add(directMailLifetime), model.JobTypeMailDeliver, request.Details)
}

func validClassTransition(key model.MailTemplateKey, value ClassTransitionDetails) bool {
	if value.ClassDisplayName == "" || value.StartsAt.IsZero() || !value.EndsAt.IsZero() && !value.StartsAt.Before(value.EndsAt) {
		return false
	}
	switch key {
	case model.MailTemplateAcademicClassEnrolled:
		return value.PreviousClassDisplayName == ""
	case model.MailTemplateAcademicClassEnrollmentEnded:
		return value.PreviousClassDisplayName == "" && !value.EndsAt.IsZero()
	case model.MailTemplateAcademicClassTransferred:
		return value.PreviousClassDisplayName != ""
	default:
		return false
	}
}

func (p *Composer) PrepareRelationshipTransition(request RelationshipTransitionPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!isRelationshipTemplate(request.TemplateKey) || request.ActionAt.IsZero() {
		return nil, errors.New("relationship transition mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			model.MailOccurrenceAcademicAdministration, request.TemplateKey, at, at.Add(directMailLifetime), model.JobTypeMailDeliver,
			model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, model.MailOccurrenceAcademicAdministration,
		request.TemplateKey, "", at, at.Add(directMailLifetime), model.JobTypeMailDeliver, nil)
}

func isRelationshipTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateAcademicUnitAssigned, model.MailTemplateAcademicUnitAssignmentEnded,
		model.MailTemplateAuthorizationScopedRoleAssigned, model.MailTemplateAuthorizationScopedRoleEnded,
		model.MailTemplateAuthorizationInstitutionRoleAssigned, model.MailTemplateAuthorizationInstitutionRoleEnded:
		return true
	default:
		return false
	}
}

func (p *Composer) PrepareManagerMail(request examengine.ManagerMailPreparation) (*store.ExamManagerMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		!validManagerMeaning(request.TemplateKey, request.Relationship) || request.ActionAt.IsZero() {
		return nil, errors.New("exam manager mail input is invalid")
	}
	prepared, err := p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, model.MailOccurrenceExamManagement,
		request.TemplateKey, "", request.ActionAt, request.ActionAt.Add(directMailLifetime), model.JobTypeMailDeliver,
		ExamManagerDetails{Title: request.ExamTitle, Relationship: string(request.Relationship), ActionAt: request.ActionAt})
	if err != nil {
		return nil, err
	}
	return &store.ExamManagerMail{Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job}, nil
}

func validManagerMeaning(key model.MailTemplateKey, relationship examengine.ManagerMailRelationship) bool {
	switch key {
	case model.MailTemplateExamManagerAdded:
		return relationship == examengine.ManagerMailRelationshipManager
	case model.MailTemplateExamManagerRemoved:
		return relationship == examengine.ManagerMailRelationshipNoLongerManager
	case model.MailTemplateExamOwnershipTransferredToYou:
		return relationship == examengine.ManagerMailRelationshipOwner
	case model.MailTemplateExamOwnershipTransferredFromYou:
		return relationship == examengine.ManagerMailRelationshipManager
	default:
		return false
	}
}

func (p *Composer) PrepareSubmissionReceiptMail(request SubmissionReceiptPreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		request.Details.ExamTitle == "" || !request.Details.SittingID.IsValid() || !request.Details.SubmissionID.IsValid() ||
		request.Details.SealedAt.IsZero() || request.ActionAt.IsZero() ||
		request.TemplateKey != model.MailTemplateExamSubmissionReceived && request.TemplateKey != model.MailTemplateExamSubmissionAutomaticallySealed {
		return nil, errors.New("submission receipt mail input is invalid")
	}
	at := model.TimeUTC(request.ActionAt)
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			model.MailOccurrenceSubmissionReceipt, request.TemplateKey, at, at.Add(directMailLifetime), model.JobTypeMailDeliver,
			model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, model.MailOccurrenceSubmissionReceipt,
		request.TemplateKey, "", at, at.Add(directMailLifetime), model.JobTypeMailDeliver, request.Details)
}

func (p *Composer) PrepareResultReleaseMail(request ResultReleasePreparation) (*store.PreparedMail, error) {
	if request.Recipient == nil || request.Recipient.Validate() != nil || !request.OccurrenceID.IsValid() ||
		request.Details.ExamTitle == "" || request.Details.ReleasedAt.IsZero() || request.ReleasedAt.IsZero() ||
		!request.Details.ReleasedAt.Equal(request.ReleasedAt) {
		return nil, errors.New("result release mail input is invalid")
	}
	at := model.TimeUTC(request.ReleasedAt)
	if !request.Recipient.IsActive() {
		return p.prepareSuppressedRecipient(request.Recipient.Email, request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID,
			model.MailOccurrenceResultRelease, model.MailTemplateExamResultReleased, at, at.Add(directMailLifetime), model.JobTypeMailDeliver,
			model.MailDeliveryRecipientIneligibleCode)
	}
	return p.prepareRecipient(request.Recipient.DisplayName, request.Recipient.Email, request.Recipient.Locale,
		request.Recipient.ID, request.Recipient.ID, "", request.OccurrenceID, model.MailOccurrenceResultRelease,
		model.MailTemplateExamResultReleased, "", at, at.Add(directMailLifetime), model.JobTypeMailDeliver, request.Details)
}

func (p *Composer) PreparePublicRegistrationVerification(recipient *model.User, occurrenceID model.MailOccurrenceID,
	actionURL string, at, deadline time.Time,
) (*store.PreparedMail, error) {
	return p.PrepareDirect(DirectPreparation{Recipient: recipient, OccurrenceID: occurrenceID, Kind: model.MailOccurrenceAccountToken,
		TemplateKey: model.MailTemplateIdentityVerifyEmail, ActionURL: actionURL, At: at, Deadline: deadline,
		JobType: model.JobTypeMailDeliverCredential})
}

func (p *Composer) prepareRecipient(recipientName, recipientAddress, locale string, actorUserID, targetUserID model.UserID,
	targetInvitationID model.InvitationID, occurrenceID model.MailOccurrenceID, kind model.MailOccurrenceKind,
	key model.MailTemplateKey, actionURL string, at, deadline time.Time, jobType model.JobType, detail details,
) (*store.PreparedMail, error) {
	if p == nil || p.sender == nil || p.renderer == nil || !model.IsValidEmail(recipientAddress) || !actorUserID.IsValid() ||
		!occurrenceID.IsValid() || targetUserID.IsValid() == targetInvitationID.IsValid() || !key.IsValid() || at.IsZero() ||
		!deadline.After(at) || jobType != model.JobTypeMailDeliver && jobType != model.JobTypeMailDeliverCredential {
		return nil, errors.New("direct mail recipient is invalid")
	}
	at, deadline = model.TimeUTC(at), model.TimeUTC(deadline)
	if !p.sender.Enabled() {
		return p.prepareSuppressedRecipient(recipientAddress, actorUserID, targetUserID, targetInvitationID, occurrenceID,
			kind, key, at, deadline, jobType, model.MailDeliveryDisabledCode)
	}
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return nil, err
	}
	job, err := model.NewJob(jobID, jobType, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	if err != nil {
		return nil, err
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: kind, TemplateKey: key, ActorUserID: actorUserID, CreatedAt: at}
	var rendered FrozenContent
	if detail == nil {
		rendered, err = p.renderer.Render(key, locale, actionURL)
	} else {
		rendered, err = detail.render(p.renderer, key, locale)
	}
	if err != nil {
		return nil, err
	}
	from := p.sender.From()
	if err = ValidateAddress(from); err != nil {
		return nil, err
	}
	payload := FrozenPayloadV1{Version: 1, RecipientName: recipientName, RecipientAddress: recipientAddress,
		FromName: from.Name, FromAddress: from.Address, Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML,
		AutoSubmitted: "auto-generated", AutoResponseSuppress: "All"}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) > model.MailRenderedPayloadMaximumBytes {
		return nil, errors.New("rendered mail payload is invalid")
	}
	envelope, err := p.sealer.Seal(secretseal.Binding{Purpose: DeliverySealingPurpose, Owner: deliveryID.String()}, plaintext)
	if err != nil {
		return nil, err
	}
	encrypted, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: targetUserID,
		TargetInvitationID: targetInvitationID, TemplateKey: key, TemplateDigest: Digest(rendered.Subject, rendered.Text, rendered.HTML),
		MaskedRecipient: MaskAddress(recipientAddress), State: model.MailDeliveryQueued, CreatedAt: at, UpdatedAt: at,
		MessageDate: at, Deadline: deadline, MessageID: StableMessageID(deliveryID, from.Address), EncryptedPayload: encrypted, Revision: 1}
	if err = occurrence.Validate(); err != nil {
		return nil, err
	}
	if err = delivery.Validate(); err != nil {
		return nil, err
	}
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}, nil
}

func (p *Composer) prepareSuppressedRecipient(recipientAddress string, actorUserID, targetUserID model.UserID,
	targetInvitationID model.InvitationID, occurrenceID model.MailOccurrenceID, kind model.MailOccurrenceKind,
	key model.MailTemplateKey, at, deadline time.Time, jobType model.JobType, publicCode string,
) (*store.PreparedMail, error) {
	if p == nil || p.sender == nil || !model.IsValidEmail(recipientAddress) || !actorUserID.IsValid() ||
		targetUserID.IsValid() == targetInvitationID.IsValid() || !occurrenceID.IsValid() || !key.IsValid() || at.IsZero() ||
		!deadline.After(at) || jobType != model.JobTypeMailDeliver && jobType != model.JobTypeMailDeliverCredential ||
		publicCode != model.MailDeliveryDisabledCode && publicCode != model.MailDeliveryRecipientIneligibleCode {
		return nil, errors.New("direct mail suppression input is invalid")
	}
	at, deadline = model.TimeUTC(at), model.TimeUTC(deadline)
	deliveryID, jobID := model.NewMailDeliveryID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		return nil, err
	}
	job, err := model.NewJob(jobID, jobType, 1, command, deliveryID.String(), at, at, model.MailMaximumAttempts)
	if err != nil {
		return nil, err
	}
	job, err = job.RequestCancellation(at)
	if err != nil {
		return nil, err
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: kind, TemplateKey: key, ActorUserID: actorUserID, CreatedAt: at}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: targetUserID,
		TargetInvitationID: targetInvitationID, TemplateKey: key, TemplateDigest: Digest("", "", ""),
		MaskedRecipient: MaskAddress(recipientAddress), State: model.MailDeliverySuppressed, CreatedAt: at, UpdatedAt: at,
		MessageDate: at, Deadline: deadline, MessageID: StableMessageID(deliveryID, ""), PublicFailureCode: publicCode, Revision: 1}
	if err = occurrence.Validate(); err != nil {
		return nil, err
	}
	if err = delivery.Validate(); err != nil {
		return nil, err
	}
	return &store.PreparedMail{Occurrence: occurrence, Delivery: delivery, Job: job}, nil
}

func ValidateAddress(address Address) error {
	if address.Address == "" || strings.ContainsAny(address.Name+address.Address, "\x00\r\n") {
		return errors.New("mail address is invalid")
	}
	parsed, err := mail.ParseAddress(address.Address)
	if err != nil || parsed.Address != address.Address {
		return errors.New("mail address is invalid")
	}
	return nil
}

func StableMessageID(id model.MailDeliveryID, from string) string {
	domain := "localhost"
	if parsed, err := mail.ParseAddress(from); err == nil {
		if at := strings.LastIndexByte(parsed.Address, '@'); at >= 0 {
			domain = parsed.Address[at+1:]
		}
	}
	return "<mail." + id.String() + "@" + strings.ToLower(domain) + ">"
}

func MaskAddress(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 1 {
		return "***"
	}
	local := []rune(address[:at])
	prefix := "***"
	if len(local) > 1 {
		prefix = string(local[0]) + strings.Repeat("*", min(3, len(local)-1))
	}
	return prefix + address[at:]
}

func Digest(subject, text, html string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(subject))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(text))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(html))
	return hex.EncodeToString(hash.Sum(nil))
}
