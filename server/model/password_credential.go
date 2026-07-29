// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

const PasswordHashMaxLength = 1024

// PasswordCredential contains only an established password hasher's encoded
// output. Plaintext passwords must never be assigned to this model. Keeping it
// separate permits external-only accounts and future credential replacement
// without exposing password material through User serialization.
type PasswordCredential struct {
	Id                string `json:"id"`
	CreateAt          int64  `json:"create_at"`
	UpdateAt          int64  `json:"update_at"`
	DeleteAt          int64  `json:"delete_at"`
	UserId            string `json:"user_id"`
	PasswordHash      string `json:"-"`
	PasswordChangedAt int64  `json:"password_changed_at"`
}

func (pc *PasswordCredential) PreSave() {
	preSave(&pc.Id, &pc.CreateAt, &pc.UpdateAt)
	if pc.PasswordChangedAt == 0 {
		pc.PasswordChangedAt = pc.CreateAt
	}
}

func (pc *PasswordCredential) PreUpdate() {
	preUpdate(&pc.UpdateAt)
}

func (pc *PasswordCredential) IsValid() *AppError {
	const where = "PasswordCredential.IsValid"
	if appErr := validatePersistentFields(
		where,
		"password_credential",
		pc.Id,
		pc.CreateAt,
		pc.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + pc.Id
	if !IsValidId(pc.UserId) {
		return invalidModelError(where, "password_credential", "user_id", "must be a valid identifier", details)
	}
	if len(pc.PasswordHash) == 0 || len(pc.PasswordHash) > PasswordHashMaxLength {
		return invalidModelError(where, "password_credential", "password_hash", "has an invalid length", details)
	}
	if pc.PasswordChangedAt < pc.CreateAt {
		return invalidModelError(
			where,
			"password_credential",
			"password_changed_at",
			"must not precede create_at",
			details,
		)
	}
	return nil
}

func (pc *PasswordCredential) Auditable() map[string]any {
	fields := auditFields(pc.Id, pc.CreateAt, pc.UpdateAt, pc.DeleteAt)
	fields["user_id"] = pc.UserId
	fields["password_changed_at"] = pc.PasswordChangedAt
	return fields
}

var _ Auditable = (*PasswordCredential)(nil)
