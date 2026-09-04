// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type DryRunStudentProgressionCommand struct {
	SourcePeriodID      string
	SourceClassID       string
	DestinationPeriodID string
	DestinationClassID  string
	EffectiveAt         int64
}

type CommitStudentProgressionCommand struct {
	ID               string
	ExpectedRevision int64
	PreviewDigest    string
	IdempotencyKey   string
}

func (a *App) DryRunStudentProgression(ctx context.Context, invocation Invocation, command DryRunStudentProgressionCommand) (OnboardingImportView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, NewError("student_progression.unavailable")
	}
	return a.onboardingImports.dryRunProgression(ctx, invocation, command)
}

func (a *App) GetStudentProgression(ctx context.Context, invocation Invocation, id string) (OnboardingImportView, []OnboardingImportRowView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, nil, NewError("student_progression.unavailable")
	}
	view, rows, err := a.onboardingImports.get(ctx, invocation, id, model.OnboardingImportStudentProgression)
	if err != nil {
		return OnboardingImportView{}, nil, studentProgressionError(err)
	}
	return view, rows, nil
}

func (a *App) CommitStudentProgression(ctx context.Context, invocation Invocation, command CommitStudentProgressionCommand) (OnboardingImportView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, NewError("student_progression.unavailable")
	}
	view, err := a.onboardingImports.commit(ctx, invocation, CommitOnboardingImportCommand{ID: command.ID, ExpectedRevision: command.ExpectedRevision,
		PreviewDigest: command.PreviewDigest, Policy: model.OnboardingImportValidRowsOnly, IdempotencyKey: command.IdempotencyKey}, model.OnboardingImportStudentProgression)
	return view, studentProgressionError(err)
}

func (a *App) CancelStudentProgression(ctx context.Context, invocation Invocation, id string) (OnboardingImportView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, NewError("student_progression.unavailable")
	}
	view, err := a.onboardingImports.cancel(ctx, invocation, id, model.OnboardingImportStudentProgression)
	return view, studentProgressionError(err)
}

func (a *App) StudentProgressionReport(ctx context.Context, invocation Invocation, id string, output io.Writer) error {
	if a == nil || a.onboardingImports == nil {
		return NewError("student_progression.unavailable")
	}
	return studentProgressionError(a.onboardingImports.report(ctx, invocation, id, output, model.OnboardingImportStudentProgression))
}

func (s *onboardingImportService) dryRunProgression(ctx context.Context, invocation Invocation, command DryRunStudentProgressionCommand) (OnboardingImportView, error) {
	sourcePeriodID, sourceClassID := strings.TrimSpace(command.SourcePeriodID), strings.TrimSpace(command.SourceClassID)
	destinationPeriodID, destinationClassID := strings.TrimSpace(command.DestinationPeriodID), strings.TrimSpace(command.DestinationClassID)
	if !model.IsValidId(sourcePeriodID) || !model.IsValidId(sourceClassID) || !model.IsValidId(destinationPeriodID) || !model.IsValidId(destinationClassID) ||
		sourceClassID == destinationClassID || command.EffectiveAt <= 0 {
		return OnboardingImportView{}, NewError("request.invalid")
	}
	for _, classID := range []string{sourceClassID, destinationClassID} {
		resource := model.Resource{Type: model.ResourceClass, ID: classID}
		for _, action := range []model.Action{model.ActionAcademicProgressionManage, model.ActionClassMembersManage} {
			if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return OnboardingImportView{}, err
			}
		}
	}
	sourcePeriod, err := s.periods.Get(ctx, sourcePeriodID)
	if err != nil {
		return OnboardingImportView{}, studentProgressionError(err)
	}
	destinationPeriod, err := s.periods.Get(ctx, destinationPeriodID)
	if err != nil {
		return OnboardingImportView{}, studentProgressionError(err)
	}
	sourceClass, err := s.classes.Get(ctx, sourceClassID)
	if err != nil {
		return OnboardingImportView{}, studentProgressionError(err)
	}
	destinationClass, err := s.classes.Get(ctx, destinationClassID)
	if err != nil {
		return OnboardingImportView{}, studentProgressionError(err)
	}
	if sourceClass.AcademicPeriodID != sourcePeriod.ID || destinationClass.AcademicPeriodID != destinationPeriod.ID || sourceClass.IsArchived() || destinationClass.IsArchived() {
		return OnboardingImportView{}, NewError("student_progression.target_conflict")
	}
	sourceLevel, err := s.levels.Get(ctx, sourceClass.ProgrammeLevelID.String())
	if err != nil {
		return OnboardingImportView{}, studentProgressionError(err)
	}
	destinationLevel, err := s.levels.Get(ctx, destinationClass.ProgrammeLevelID.String())
	if err != nil {
		return OnboardingImportView{}, studentProgressionError(err)
	}
	if sourceLevel.ProgrammeID != destinationLevel.ProgrammeID {
		return OnboardingImportView{}, NewError("student_progression.lineage_conflict")
	}
	effectiveAt := model.TimeFromMillis(command.EffectiveAt)
	if effectiveAt.Before(destinationPeriod.StartsAt) || !effectiveAt.Before(destinationPeriod.EndsAt) {
		return OnboardingImportView{}, NewError("student_progression.effective_date_conflict")
	}
	if err = s.validatePrincipal(ctx, invocation.Principal()); err != nil {
		return OnboardingImportView{}, err
	}
	at := model.TimeUTC(s.now())
	id := model.NewOnboardingImportID()
	job, err := newOnboardingImportJob(model.JobTypeStudentProgressionPreview, id, at)
	if err != nil {
		return OnboardingImportView{}, NewError("student_progression.unavailable").Wrap(err)
	}
	value := &store.OnboardingImport{ID: id, Mode: model.OnboardingImportStudentProgression, State: model.OnboardingImportParsing,
		ScopeType: model.RoleScopeClass, ScopeID: destinationClass.ID.String(), ActorUserID: invocation.Principal().UserID, Principal: invocation.Principal(),
		SourcePeriodID: sourcePeriod.ID, SourceClassID: sourceClass.ID, DestinationPeriodID: destinationPeriod.ID, DestinationClassID: destinationClass.ID,
		SourcePeriodRevision: sourcePeriod.Revision, SourceClassRevision: sourceClass.Revision, DestinationPeriodRevision: destinationPeriod.Revision,
		DestinationClassRevision: destinationClass.Revision, EffectiveAt: effectiveAt, ParseJobID: job.ID, CreatedAt: at, UpdatedAt: at,
		ExpiresAt: at.Add(onboardingImportRetention), Revision: 1}
	created, err := runStudentProgressionAuditedMutation(ctx, s.audit, invocation, "student_progression.dry_run", id.String(),
		sourceClass.ID, destinationClass.ID, at, func(ctx context.Context, destinationReference, sourceReference mutationAttemptReference) (*store.OnboardingImport, error) {
			return s.imports.CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: value, ParseJob: job,
				AuditEventID: destinationReference.ID, SourceAuditEventID: sourceReference.ID, AuditAt: destinationReference.MutationAtMillis})
		}, studentProgressionError)
	if err != nil {
		return OnboardingImportView{}, err
	}
	if s.wake != nil {
		s.wake()
	}
	return onboardingImportView(created), nil
}

func runStudentProgressionAuditedMutation[T any](ctx context.Context, audit mutationAuditor, invocation Invocation, operation, progressionID string,
	sourceClassID, destinationClassID model.ClassID, at time.Time,
	mutate func(context.Context, mutationAttemptReference, mutationAttemptReference) (T, error), mapError func(error) error,
) (T, error) {
	destination := mutationAttempt{Invocation: invocation, Action: model.ActionAcademicProgressionManage,
		Resource: model.Resource{Type: model.ResourceClass, ID: destinationClassID.String()}, ScopeType: model.RoleScopeClass,
		ScopeID: destinationClassID.String(), Operation: operation,
		Value: map[string]any{"student_progression_id": progressionID, "class_id": destinationClassID.String()}}
	return runAuditedMutation(ctx, audit, destination, func() time.Time { return at },
		func(ctx context.Context, destinationReference mutationAttemptReference) (T, error) {
			source := mutationAttempt{Invocation: invocation, Action: model.ActionAcademicProgressionManage,
				Resource: model.Resource{Type: model.ResourceClass, ID: sourceClassID.String()}, ScopeType: model.RoleScopeClass,
				ScopeID: sourceClassID.String(), Operation: operation,
				Value: map[string]any{"student_progression_id": progressionID, "class_id": sourceClassID.String()}}
			return runAuditedMutation(ctx, audit, source, func() time.Time { return model.TimeFromMillis(destinationReference.MutationAtMillis) },
				func(ctx context.Context, sourceReference mutationAttemptReference) (T, error) {
					return mutate(ctx, destinationReference, sourceReference)
				}, mapError)
		}, mapError)
}

func (s *onboardingImportService) previewProgression(ctx context.Context, id model.OnboardingImportID) error {
	current, err := s.imports.GetOnboardingImport(ctx, id)
	if err != nil {
		return err
	}
	if current.Mode == model.OnboardingImportStudentProgression &&
		(current.State == model.OnboardingImportPreviewReady || current.State == model.OnboardingImportCanceled || current.State == model.OnboardingImportFailed) {
		return nil
	}
	if current.Mode != model.OnboardingImportStudentProgression || current.State != model.OnboardingImportParsing {
		return store.NewErrConflict("student_progression", "state", nil)
	}
	invocation := NewInvocation(current.Principal, model.RequestMetadata{})
	if err = s.validatePrincipal(ctx, current.Principal); err != nil {
		return err
	}
	for _, classID := range []model.ClassID{current.SourceClassID, current.DestinationClassID} {
		resource := model.Resource{Type: model.ResourceClass, ID: classID.String()}
		for _, action := range []model.Action{model.ActionAcademicProgressionManage, model.ActionClassMembersManage} {
			if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
				return err
			}
		}
	}
	sourcePeriod, err := s.periods.Get(ctx, current.SourcePeriodID.String())
	if err != nil {
		return err
	}
	if sourcePeriod.Revision != current.SourcePeriodRevision {
		return NewError("student_progression.target_conflict")
	}
	destinationPeriod, err := s.periods.Get(ctx, current.DestinationPeriodID.String())
	if err != nil {
		return err
	}
	if destinationPeriod.Revision != current.DestinationPeriodRevision {
		return NewError("student_progression.target_conflict")
	}
	sourceClass, err := s.classes.Get(ctx, current.SourceClassID.String())
	if err != nil {
		return err
	}
	if sourceClass.Revision != current.SourceClassRevision {
		return NewError("student_progression.target_conflict")
	}
	destinationClass, err := s.classes.Get(ctx, current.DestinationClassID.String())
	if err != nil {
		return err
	}
	if destinationClass.Revision != current.DestinationClassRevision {
		return NewError("student_progression.target_conflict")
	}
	rosterAt := studentProgressionRosterInstant(current, sourcePeriod)
	members, err := s.imports.ListStudentProgressionRoster(ctx, sourceClass.ID, rosterAt, store.OnboardingImportMaximumRows+1)
	if err != nil {
		return err
	}
	if len(members) > store.OnboardingImportMaximumRows {
		return NewError("student_progression.roster_too_large")
	}
	rows := make([]store.OnboardingImportRow, 0, len(members))
	for index, member := range members {
		row := store.OnboardingImportRow{ImportID: id, RowNumber: index + 1, Reference: member.ID.String(), ScopeType: model.RoleScopeClass,
			ScopeID: destinationClass.ID.String(), TargetRevision: destinationClass.Revision, UserID: member.UserID, RelationshipID: member.ID.String(),
			RelationshipRevision: member.Revision, StartsAt: current.EffectiveAt.UnixMilli(), PreviewStatus: model.OnboardingImportRowValid,
			Status: model.OnboardingImportRowValid, UpdatedAt: current.CreatedAt}
		if current.SourcePeriodID == current.DestinationPeriodID {
			row.Operation = string(AcademicAdministrationClassTransfer)
			if studentProgressionTransferEffectiveDateConflict(member, current.EffectiveAt) {
				row.PreviewStatus, row.Status, row.PreviewCode = model.OnboardingImportRowInvalid, model.OnboardingImportRowInvalid, "student_progression.effective_date_conflict"
				rows = append(rows, row)
				continue
			}
		} else {
			row.Operation = string(AcademicAdministrationClassEnroll)
		}
		user, userErr := s.users.Get(ctx, member.UserID.String())
		if userErr != nil {
			return userErr
		}
		if !user.IsActive() {
			row.PreviewStatus, row.Status, row.PreviewCode = model.OnboardingImportRowInvalid, model.OnboardingImportRowInvalid, "student_progression.ineligible_target"
			rows = append(rows, row)
			continue
		}
		affiliations, affiliationErr := s.affiliations.ListActiveByUser(ctx, member.UserID.String(), current.EffectiveAt.UnixMilli())
		if affiliationErr != nil {
			return affiliationErr
		}
		if !studentProgressionEligibleAffiliation(affiliations) {
			row.PreviewStatus, row.Status, row.PreviewCode = model.OnboardingImportRowInvalid, model.OnboardingImportRowInvalid, "student_progression.ineligible_target"
			rows = append(rows, row)
			continue
		}
		active, activeErr := s.classMembers.ListActiveByUser(ctx, member.UserID.String(), current.EffectiveAt.UnixMilli())
		if activeErr != nil {
			return activeErr
		}
		for _, existing := range active {
			if existing.AcademicPeriodID != current.DestinationPeriodID {
				continue
			}
			if existing.ClassID == current.DestinationClassID {
				row.Operation = string(AcademicAdministrationClassEnroll)
				row.PreviewCode = "student_progression.destination_exists"
				row.DestinationRelationshipID = existing.ID.String()
				row.DestinationRelationshipRevision = existing.Revision
			} else if existing.ID != member.ID {
				row.PreviewStatus, row.Status, row.PreviewCode = model.OnboardingImportRowInvalid, model.OnboardingImportRowInvalid, "student_progression.destination_conflict"
			}
			break
		}
		rows = append(rows, row)
	}
	document, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(document)
	_, err = s.imports.CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: id, ExpectedRevision: current.Revision,
		Digest: hex.EncodeToString(digest[:]), Rows: rows, At: model.TimeUTC(s.now())})
	return err
}

func studentProgressionTransferEffectiveDateConflict(member *model.ClassMember, effectiveAt time.Time) bool {
	return member == nil || member.EndsAt.Valid || !effectiveAt.After(member.StartsAt)
}

func studentProgressionEligibleAffiliation(affiliations []*model.Affiliation) bool {
	for _, affiliation := range affiliations {
		if affiliation != nil && affiliation.Kind == model.AffiliationStudent && !affiliation.EndsAt.Valid {
			return true
		}
	}
	return false
}

func studentProgressionRosterInstant(value *store.OnboardingImport, sourcePeriod *model.AcademicPeriod) time.Time {
	if value != nil && sourcePeriod != nil && value.SourcePeriodID == value.DestinationPeriodID {
		return value.EffectiveAt
	}
	if sourcePeriod == nil {
		return time.Time{}
	}
	return sourcePeriod.EndsAt.Add(-time.Millisecond)
}

func studentProgressionError(err error) error {
	if err == nil {
		return nil
	}
	if failure, ok := As(err); ok {
		switch failure.Code() {
		case "onboarding_import.conflict":
			return NewError("student_progression.conflict").Wrap(err)
		case "onboarding_import.unavailable":
			return NewError("student_progression.unavailable").Wrap(err)
		}
		return failure
	}
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "student_progression").Wrap(err)
	case store.IsConflict(err):
		return NewError("student_progression.conflict").Wrap(err)
	default:
		return NewError("student_progression.unavailable").Wrap(err)
	}
}
