// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "fmt"

type OnboardingImportID string
type OnboardingImportMode string
type OnboardingImportState string
type OnboardingImportCommitPolicy string
type OnboardingImportRowStatus string

const (
	OnboardingImportStudentClass           OnboardingImportMode = "student_class"
	OnboardingImportTeacherAcademicUnit    OnboardingImportMode = "teacher_academic_unit"
	OnboardingImportInstitution            OnboardingImportMode = "institution"
	OnboardingImportAcademicAdministration OnboardingImportMode = "academic_administration"
	OnboardingImportStudentProgression     OnboardingImportMode = "student_progression"

	OnboardingImportUploading           OnboardingImportState = "uploading"
	OnboardingImportParsing             OnboardingImportState = "parsing"
	OnboardingImportPreviewReady        OnboardingImportState = "preview_ready"
	OnboardingImportExecuting           OnboardingImportState = "executing"
	OnboardingImportCompleted           OnboardingImportState = "completed"
	OnboardingImportCompletedWithErrors OnboardingImportState = "completed_with_errors"
	OnboardingImportCanceled            OnboardingImportState = "canceled"
	OnboardingImportFailed              OnboardingImportState = "failed"

	OnboardingImportRequireAllValid OnboardingImportCommitPolicy = "require_all_valid"
	OnboardingImportValidRowsOnly   OnboardingImportCommitPolicy = "valid_rows_only"

	OnboardingImportRowValid     OnboardingImportRowStatus = "valid"
	OnboardingImportRowInvalid   OnboardingImportRowStatus = "invalid"
	OnboardingImportRowDuplicate OnboardingImportRowStatus = "duplicate"
	OnboardingImportRowPending   OnboardingImportRowStatus = "pending"
	OnboardingImportRowSucceeded OnboardingImportRowStatus = "succeeded"
	OnboardingImportRowNoOp      OnboardingImportRowStatus = "no_op"
	OnboardingImportRowFailed    OnboardingImportRowStatus = "failed"
	OnboardingImportRowSkipped   OnboardingImportRowStatus = "skipped"
	OnboardingImportRowCanceled  OnboardingImportRowStatus = "canceled"
)

func NewOnboardingImportID() OnboardingImportID { return OnboardingImportID(NewId()) }
func (id OnboardingImportID) String() string    { return string(id) }
func (id OnboardingImportID) IsValid() bool     { return IsValidId(string(id)) }
func (id OnboardingImportID) IsZero() bool      { return id == "" }

func (mode OnboardingImportMode) IsValid() bool {
	return mode == OnboardingImportStudentClass || mode == OnboardingImportTeacherAcademicUnit || mode == OnboardingImportInstitution || mode == OnboardingImportAcademicAdministration || mode == OnboardingImportStudentProgression
}

func (state OnboardingImportState) IsValid() bool {
	switch state {
	case OnboardingImportUploading, OnboardingImportParsing, OnboardingImportPreviewReady, OnboardingImportExecuting,
		OnboardingImportCompleted, OnboardingImportCompletedWithErrors, OnboardingImportCanceled, OnboardingImportFailed:
		return true
	default:
		return false
	}
}

func (policy OnboardingImportCommitPolicy) IsValid() bool {
	return policy == OnboardingImportRequireAllValid || policy == OnboardingImportValidRowsOnly
}

func (status OnboardingImportRowStatus) IsValid() bool {
	switch status {
	case OnboardingImportRowValid, OnboardingImportRowInvalid, OnboardingImportRowDuplicate, OnboardingImportRowPending,
		OnboardingImportRowSucceeded, OnboardingImportRowNoOp, OnboardingImportRowFailed, OnboardingImportRowSkipped,
		OnboardingImportRowCanceled:
		return true
	default:
		return false
	}
}

func ValidateOnboardingImportScope(mode OnboardingImportMode, scopeType RoleScopeType, scopeID string) error {
	if !mode.IsValid() || !IsValidId(scopeID) {
		return fmt.Errorf("model: invalid onboarding import scope")
	}
	if (mode == OnboardingImportStudentClass && scopeType != RoleScopeClass) ||
		(mode == OnboardingImportTeacherAcademicUnit && scopeType != RoleScopeAcademicUnit) ||
		(mode == OnboardingImportInstitution && scopeType != RoleScopeInstitution) ||
		(mode == OnboardingImportStudentProgression && scopeType != RoleScopeClass) ||
		(mode == OnboardingImportAcademicAdministration && scopeType != RoleScopeInstitution && scopeType != RoleScopeAcademicUnit && scopeType != RoleScopeClass) {
		return fmt.Errorf("model: onboarding import mode and scope do not match")
	}
	return nil
}
