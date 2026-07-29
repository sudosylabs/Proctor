// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

// Auditable is implemented by model values that can produce a deliberately
// safe audit representation. Implementations must omit secrets, credentials,
// tokens, and unbounded user-controlled content.
type Auditable interface {
	Auditable() map[string]any
}

func auditFields(id string, createAt, updateAt, deleteAt int64) map[string]any {
	return map[string]any{
		"id":        id,
		"create_at": createAt,
		"update_at": updateAt,
		"delete_at": deleteAt,
	}
}

func preSave(id *string, createAt, updateAt *int64) {
	if *id == "" {
		*id = NewId()
	}
	now := GetMillis()
	*createAt = now
	*updateAt = now
}

func preUpdate(updateAt *int64) {
	*updateAt = GetMillis()
}

func validatePersistentFields(where, modelName, id string, createAt, updateAt int64) *AppError {
	if !IsValidId(id) {
		return invalidModelError(where, modelName, "id", "must be a valid identifier", "")
	}
	details := "id=" + id
	if createAt <= 0 {
		return invalidModelError(where, modelName, "create_at", "must be set", details)
	}
	if updateAt <= 0 {
		return invalidModelError(where, modelName, "update_at", "must be set", details)
	}
	if updateAt < createAt {
		return invalidModelError(where, modelName, "update_at", "must not precede create_at", details)
	}
	return nil
}
