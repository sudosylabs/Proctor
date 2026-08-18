// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const StudentClassInvitationLifetime = 7 * 24 * time.Hour

const invitationClaimHashDomain = "proctor/invitation-claim/v1\x00"

// InvitationPurpose identifies the immutable relationship package carried by
// an Invitation. Each purpose is implemented as its own application command;
// there is no empty or arbitrary package.
type InvitationPurpose string

const (
	InvitationPurposeStudentClass        InvitationPurpose = "student_class"
	InvitationPurposeTeacherAcademicUnit InvitationPurpose = "teacher_academic_unit"
	InvitationPurposeAcademicUnitRole    InvitationPurpose = "academic_unit_role"
	InvitationPurposeInstitutionRole     InvitationPurpose = "institution_role"
)

// InvitationState is the closed lifecycle of a pre-User Invitation.
type InvitationState string

const (
	InvitationPending    InvitationState = "pending"
	InvitationAccepted   InvitationState = "accepted"
	InvitationRevoked    InvitationState = "revoked"
	InvitationExpired    InvitationState = "expired"
	InvitationSuperseded InvitationState = "superseded"
)

// InvitationProfileSuggestions carries bounded, non-authoritative profile
// defaults. Acceptance may use them when creating an account, but they never
// identify a person or grant a relationship by themselves.
type InvitationProfileSuggestions struct {
	Username    string
	DisplayName string
	FirstName   string
	LastName    string
	Locale      string
}

// Invitation is the durable pre-User credential and frozen relationship
// package. ClaimHash is deliberately omitted from JSON; raw claims never enter
// this model or durable state.
type Invitation struct {
	ID                           InvitationID
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	Revision                     int64
	Purpose                      InvitationPurpose
	State                        InvitationState
	TargetEmail                  string
	ClassID                      ClassID
	AcademicPeriodID             AcademicPeriodID
	AcademicUnitID               AcademicUnitID
	RoleID                       RoleID
	RoleActions                  []string
	IntendedStartsAt             time.Time
	IntendedEndsAt               OptionalTime
	Suggestions                  InvitationProfileSuggestions
	InviterUserID                UserID
	ScopeType                    RoleScopeType
	ScopeID                      string
	ClaimHash                    string `json:"-"`
	ExpiresAt                    time.Time
	AcceptedAt                   OptionalTime
	AcceptedUserID               UserID
	AcceptedAffiliationID        AffiliationID
	AcceptedClassMemberID        ClassMemberID
	AcceptedAcademicUnitMemberID AcademicUnitMemberID
	AcceptedRoleBindingID        RoleBindingID
}

// StudentClassInvitationInput is the complete immutable package selected by
// the application after authorization and authoritative Class resolution.
type StudentClassInvitationInput struct {
	ID               InvitationID
	TargetEmail      string
	ClassID          ClassID
	AcademicPeriodID AcademicPeriodID
	IntendedStartsAt time.Time
	IntendedEndsAt   OptionalTime
	Suggestions      InvitationProfileSuggestions
	InviterUserID    UserID
	ScopeType        RoleScopeType
	ScopeID          string
	ClaimHash        string
	IssuedAt         time.Time
}

// TeacherAcademicUnitInvitationInput freezes the exact organizational and
// delegable Role package selected by an authorized inviter.
type TeacherAcademicUnitInvitationInput struct {
	ID               InvitationID
	TargetEmail      string
	AcademicUnitID   AcademicUnitID
	RoleID           RoleID
	RoleActions      []string
	IntendedStartsAt time.Time
	IntendedEndsAt   OptionalTime
	Suggestions      InvitationProfileSuggestions
	InviterUserID    UserID
	ScopeType        RoleScopeType
	ScopeID          string
	ClaimHash        string
	IssuedAt         time.Time
}

// NewStudentClassInvitation constructs the one exact package implemented by
// the first Invitation slice. Expiry is fixed here so callers cannot widen the
// credential lifetime accidentally.
func NewStudentClassInvitation(input StudentClassInvitationInput) (*Invitation, error) {
	issuedAt := TimeUTC(input.IssuedAt)
	invitation := &Invitation{
		ID: input.ID, CreatedAt: issuedAt, UpdatedAt: issuedAt, Revision: 1,
		Purpose: InvitationPurposeStudentClass, State: InvitationPending,
		TargetEmail: normalizeInvitationEmail(input.TargetEmail),
		ClassID:     input.ClassID, AcademicPeriodID: input.AcademicPeriodID,
		IntendedStartsAt: TimeUTC(input.IntendedStartsAt),
		IntendedEndsAt:   normalizeOptionalInvitationTime(input.IntendedEndsAt),
		Suggestions:      normalizeInvitationSuggestions(input.Suggestions),
		InviterUserID:    input.InviterUserID, ScopeType: input.ScopeType,
		ScopeID: input.ScopeID, ClaimHash: input.ClaimHash,
		ExpiresAt: issuedAt.Add(StudentClassInvitationLifetime),
	}
	if err := invitation.Validate(); err != nil {
		return nil, err
	}
	return invitation, nil
}

// NewTeacherAcademicUnitInvitation constructs one immutable teacher
// relationship package. RoleActions is an owned, canonical snapshot so later
// Role changes cannot silently broaden an unclaimed Invitation.
func NewTeacherAcademicUnitInvitation(input TeacherAcademicUnitInvitationInput) (*Invitation, error) {
	issuedAt := TimeUTC(input.IssuedAt)
	invitation := &Invitation{
		ID: input.ID, CreatedAt: issuedAt, UpdatedAt: issuedAt, Revision: 1,
		Purpose: InvitationPurposeTeacherAcademicUnit, State: InvitationPending,
		TargetEmail: normalizeInvitationEmail(input.TargetEmail), AcademicUnitID: input.AcademicUnitID,
		RoleID: input.RoleID, RoleActions: canonicalInvitationRoleActions(input.RoleActions),
		IntendedStartsAt: TimeUTC(input.IntendedStartsAt),
		IntendedEndsAt:   normalizeOptionalInvitationTime(input.IntendedEndsAt),
		Suggestions:      normalizeInvitationSuggestions(input.Suggestions),
		InviterUserID:    input.InviterUserID, ScopeType: input.ScopeType,
		ScopeID: input.ScopeID, ClaimHash: input.ClaimHash,
		ExpiresAt: issuedAt.Add(StudentClassInvitationLifetime),
	}
	if err := invitation.Validate(); err != nil {
		return nil, err
	}
	return invitation, nil
}

// HashInvitationClaim returns the domain-separated persistent digest of a
// random Invitation claim. Human-entered secrets must not use this function.
func HashInvitationClaim(rawClaim string) string {
	sum := sha256.Sum256([]byte(invitationClaimHashDomain + rawClaim))
	return hex.EncodeToString(sum[:])
}

// Validate checks rehydrated Invitation state and its frozen package.
func (i *Invitation) Validate() error {
	const where = "Invitation.Validate"
	if i == nil {
		return invalidModelError(where, "invitation", "value", "is required", "")
	}
	if !i.ID.IsValid() {
		return invalidModelError(where, "invitation", "id", "must be a valid identifier", "")
	}
	details := "id=" + i.ID.String()
	if i.CreatedAt.IsZero() || i.UpdatedAt.IsZero() || i.UpdatedAt.Before(i.CreatedAt) {
		return invalidModelError(where, "invitation", "timestamps", "must be ordered and set", details)
	}
	if i.Revision < 1 {
		return invalidModelError(where, "invitation", "revision", "must be positive", details)
	}
	if !i.Purpose.IsValid() {
		return invalidModelError(where, "invitation", "purpose", "has an unknown value", details)
	}
	if !i.State.IsValid() {
		return invalidModelError(where, "invitation", "state", "has an unknown value", details)
	}
	if !IsValidEmail(i.TargetEmail) || i.TargetEmail != normalizeInvitationEmail(i.TargetEmail) {
		return invalidModelError(where, "invitation", "target_email", "must be a normalized email address", details)
	}
	if !i.InviterUserID.IsValid() {
		return invalidModelError(where, "invitation", "inviter_user_id", "must be a valid identifier", details)
	}
	if !i.ScopeType.IsValid() || !IsValidId(i.ScopeID) {
		return invalidModelError(where, "invitation", "scope", "must identify a valid authorization scope", details)
	}
	if !IsValidTokenHash(i.ClaimHash) {
		return invalidModelError(where, "invitation", "claim_hash", "has an invalid format", details)
	}
	if !i.ExpiresAt.Equal(i.CreatedAt.Add(StudentClassInvitationLifetime)) {
		return invalidModelError(where, "invitation", "expires_at", "must be exactly seven days after creation", details)
	}
	if err := validateInvitationSuggestions(i.Suggestions); err != nil {
		return invalidModelError(where, "invitation", "suggestions", err.Error(), details)
	}
	if i.Purpose == InvitationPurposeStudentClass {
		if !i.ClassID.IsValid() || !i.AcademicPeriodID.IsValid() {
			return invalidModelError(where, "invitation", "student_class", "must identify a Class and Academic Period", details)
		}
		if i.ScopeType != RoleScopeClass || i.ScopeID != i.ClassID.String() {
			return invalidModelError(where, "invitation", "scope", "must identify the exact invited Class", details)
		}
		if i.IntendedStartsAt.IsZero() ||
			(i.IntendedEndsAt.Valid && !i.IntendedEndsAt.Time.After(i.IntendedStartsAt)) {
			return invalidModelError(where, "invitation", "effective_bounds", "must form a valid half-open interval", details)
		}
		if i.AcademicUnitID.IsValid() || i.RoleID.IsValid() || len(i.RoleActions) != 0 {
			return invalidModelError(where, "invitation", "student_class", "must not contain a teacher package", details)
		}
	}
	if i.Purpose == InvitationPurposeTeacherAcademicUnit {
		if !i.AcademicUnitID.IsValid() || !i.RoleID.IsValid() ||
			i.ScopeType != RoleScopeAcademicUnit || i.ScopeID != i.AcademicUnitID.String() {
			return invalidModelError(where, "invitation", "teacher_academic_unit", "must identify the exact Academic Unit and Role", details)
		}
		if len(i.RoleActions) == 0 || !slices.IsSorted(i.RoleActions) {
			return invalidModelError(where, "invitation", "role_actions", "must be a nonempty canonical action snapshot", details)
		}
		for index, action := range i.RoleActions {
			if !IsGrantableAction(action) || (index > 0 && i.RoleActions[index-1] == action) {
				return invalidModelError(where, "invitation", "role_actions", "must contain unique grantable actions", details)
			}
		}
		if i.IntendedStartsAt.IsZero() ||
			(i.IntendedEndsAt.Valid && !i.IntendedEndsAt.Time.After(i.IntendedStartsAt)) {
			return invalidModelError(where, "invitation", "effective_bounds", "must form a valid half-open interval", details)
		}
		if i.ClassID.IsValid() || i.AcademicPeriodID.IsValid() {
			return invalidModelError(where, "invitation", "teacher_academic_unit", "must not contain a student package", details)
		}
	}
	if i.State == InvitationAccepted {
		if !i.AcceptedAt.Valid || !i.AcceptedUserID.IsValid() || !i.AcceptedAffiliationID.IsValid() ||
			i.AcceptedAt.Time.Before(i.CreatedAt) || !i.AcceptedAt.Time.Before(i.ExpiresAt) {
			return invalidModelError(where, "invitation", "acceptance", "must identify a User within the invitation lifetime", details)
		}
		switch i.Purpose {
		case InvitationPurposeStudentClass:
			if !i.AcceptedClassMemberID.IsValid() || i.AcceptedAcademicUnitMemberID.IsValid() || i.AcceptedRoleBindingID.IsValid() {
				return invalidModelError(where, "invitation", "acceptance", "must identify the accepted Class package", details)
			}
		case InvitationPurposeTeacherAcademicUnit:
			if i.AcceptedClassMemberID.IsValid() || !i.AcceptedAcademicUnitMemberID.IsValid() || !i.AcceptedRoleBindingID.IsValid() {
				return invalidModelError(where, "invitation", "acceptance", "must identify the accepted Academic Unit package", details)
			}
		}
	} else if i.AcceptedAt.Valid || !i.AcceptedUserID.IsZero() || !i.AcceptedAffiliationID.IsZero() ||
		!i.AcceptedClassMemberID.IsZero() || !i.AcceptedAcademicUnitMemberID.IsZero() || !i.AcceptedRoleBindingID.IsZero() {
		return invalidModelError(where, "invitation", "acceptance", "must be empty outside accepted state", details)
	}
	return nil
}

// Accept applies the sole successful claim transition. Store transactions
// remain responsible for authoritative policy and relationship rechecks.
func (i *Invitation) Accept(userID UserID, affiliationID AffiliationID, classMemberID ClassMemberID, at time.Time) error {
	if i == nil || i.Purpose != InvitationPurposeStudentClass || i.State != InvitationPending || !userID.IsValid() || !affiliationID.IsValid() || !classMemberID.IsValid() {
		return fmt.Errorf("model: invitation cannot be accepted")
	}
	at = TimeUTC(at)
	if at.Before(i.CreatedAt) || !at.Before(i.ExpiresAt) ||
		(i.IntendedEndsAt.Valid && !at.Before(i.IntendedEndsAt.Time)) {
		return fmt.Errorf("model: invitation cannot be accepted at this time")
	}
	i.State = InvitationAccepted
	i.AcceptedAt = OptionalTimeFrom(at)
	i.AcceptedUserID = userID
	i.AcceptedAffiliationID = affiliationID
	i.AcceptedClassMemberID = classMemberID
	i.UpdatedAt = at
	i.Revision++
	if err := i.Validate(); err != nil {
		return err
	}
	return nil
}

// AcceptTeacherAcademicUnit records the exact immutable outcome of applying a
// teacher package. Replay reads these retained identities rather than mutable
// latest relationship rows.
func (i *Invitation) AcceptTeacherAcademicUnit(userID UserID, affiliationID AffiliationID, memberID AcademicUnitMemberID, bindingID RoleBindingID, at time.Time) error {
	if i == nil || i.Purpose != InvitationPurposeTeacherAcademicUnit || i.State != InvitationPending ||
		!userID.IsValid() || !affiliationID.IsValid() || !memberID.IsValid() || !bindingID.IsValid() {
		return fmt.Errorf("model: teacher academic unit invitation cannot be accepted")
	}
	at = TimeUTC(at)
	if at.Before(i.CreatedAt) || !at.Before(i.ExpiresAt) ||
		(i.IntendedEndsAt.Valid && !at.Before(i.IntendedEndsAt.Time)) {
		return fmt.Errorf("model: teacher academic unit invitation cannot be accepted at this time")
	}
	i.State = InvitationAccepted
	i.AcceptedAt = OptionalTimeFrom(at)
	i.AcceptedUserID = userID
	i.AcceptedAffiliationID = affiliationID
	i.AcceptedAcademicUnitMemberID = memberID
	i.AcceptedRoleBindingID = bindingID
	i.UpdatedAt = at
	i.Revision++
	return i.Validate()
}

// Expire terminalizes a pending Invitation only after its claim lifetime or
// packaged relationship interval has elapsed at authoritative database time.
func (i *Invitation) Expire(at time.Time) error {
	if i == nil || i.State != InvitationPending {
		return fmt.Errorf("model: invitation cannot be expired")
	}
	at = TimeUTC(at)
	elapsed := !at.Before(i.ExpiresAt) || (i.IntendedEndsAt.Valid && !at.Before(i.IntendedEndsAt.Time))
	if at.Before(i.CreatedAt) || !elapsed {
		return fmt.Errorf("model: invitation cannot be expired at this time")
	}
	i.State = InvitationExpired
	i.UpdatedAt = at
	i.Revision++
	return i.Validate()
}

// EffectiveStartsAt prevents acceptance from granting a backdated
// relationship while preserving an intended future start.
func (i *Invitation) EffectiveStartsAt(acceptedAt time.Time) time.Time {
	acceptedAt = TimeUTC(acceptedAt)
	if i == nil || i.IntendedStartsAt.Before(acceptedAt) {
		return acceptedAt
	}
	return i.IntendedStartsAt
}

// IsPendingAt reports whether a claim may still be evaluated at the supplied
// authoritative time. It does not replace policy or package reauthorization.
func (i *Invitation) IsPendingAt(at time.Time) bool {
	return i != nil && i.State == InvitationPending && TimeUTC(at).Before(i.ExpiresAt)
}

// Auditable returns the safe relationship-package projection. Recipient and
// claim material are intentionally absent.
func (i *Invitation) Auditable() map[string]any {
	if i == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": i.ID.String(), "purpose": i.Purpose, "state": i.State,
		"class_id": i.ClassID.String(), "academic_period_id": i.AcademicPeriodID.String(),
		"academic_unit_id": i.AcademicUnitID.String(), "role_id": i.RoleID.String(),
		"role_actions":    append([]string(nil), i.RoleActions...),
		"inviter_user_id": i.InviterUserID.String(), "scope_type": i.ScopeType,
		"scope_id": i.ScopeID, "created_at": MillisFromTime(i.CreatedAt),
		"updated_at": MillisFromTime(i.UpdatedAt), "expires_at": MillisFromTime(i.ExpiresAt),
		"accepted_at": i.AcceptedAt.Millis(), "accepted_user_id": i.AcceptedUserID.String(),
		"accepted_affiliation_id": i.AcceptedAffiliationID.String(), "accepted_class_member_id": i.AcceptedClassMemberID.String(),
		"accepted_academic_unit_member_id": i.AcceptedAcademicUnitMemberID.String(),
		"accepted_role_binding_id":         i.AcceptedRoleBindingID.String(),
	}
}

func canonicalInvitationRoleActions(actions []string) []string {
	result := append([]string(nil), actions...)
	sort.Strings(result)
	return result
}

func (p InvitationPurpose) IsValid() bool {
	switch p {
	case InvitationPurposeStudentClass, InvitationPurposeTeacherAcademicUnit,
		InvitationPurposeAcademicUnitRole, InvitationPurposeInstitutionRole:
		return true
	default:
		return false
	}
}

func (s InvitationState) IsValid() bool {
	switch s {
	case InvitationPending, InvitationAccepted, InvitationRevoked, InvitationExpired, InvitationSuperseded:
		return true
	default:
		return false
	}
}

func normalizeInvitationEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(SanitizeUnicode(value)))
}

func normalizeOptionalInvitationTime(value OptionalTime) OptionalTime {
	if !value.Valid {
		return OptionalTime{}
	}
	return OptionalTimeFrom(value.Time)
}

func normalizeInvitationSuggestions(value InvitationProfileSuggestions) InvitationProfileSuggestions {
	value.Username = strings.ToLower(SanitizeUnicode(value.Username))
	value.DisplayName = SanitizeUnicode(value.DisplayName)
	value.FirstName = SanitizeUnicode(value.FirstName)
	value.LastName = SanitizeUnicode(value.LastName)
	value.Locale = SanitizeUnicode(value.Locale)
	return value
}

func validateInvitationSuggestions(value InvitationProfileSuggestions) error {
	if value.Username != "" && !IsValidUsername(value.Username) {
		return fmt.Errorf("username has an invalid format")
	}
	if utf8.RuneCountInString(value.DisplayName) > UserDisplayNameMaxRunes ||
		utf8.RuneCountInString(value.FirstName) > UserPersonalNameMaxRunes ||
		utf8.RuneCountInString(value.LastName) > UserPersonalNameMaxRunes {
		return fmt.Errorf("profile name is too long")
	}
	if value.Locale != "" && (len(value.Locale) > UserLocaleMaxLength || !validLocale.MatchString(value.Locale)) {
		return fmt.Errorf("locale has an invalid format")
	}
	return nil
}

var _ Auditable = (*Invitation)(nil)
