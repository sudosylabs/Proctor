// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package retrylayer

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// The methods in this file are the complete handwritten retry allowlist.
// Methods absent here remain promoted from the generated embedded store and
// are forwarded exactly once, including every mutation and transaction.

func (s *institutionStore) Get(ctx context.Context, id string) (*model.Institution, error) {
	return retryCall1(ctx, s.layer, func() (*model.Institution, error) {
		return s.InstitutionStore.Get(ctx, id)
	})
}

func (s *institutionStore) GetSingleton(ctx context.Context) (*model.Institution, error) {
	return retryCall1(ctx, s.layer, func() (*model.Institution, error) {
		return s.InstitutionStore.GetSingleton(ctx)
	})
}

func (s *academicUnitStore) Get(ctx context.Context, id string) (*model.AcademicUnit, error) {
	return retryCall1(ctx, s.layer, func() (*model.AcademicUnit, error) { return s.AcademicUnitStore.Get(ctx, id) })
}
func (s *academicUnitStore) ListChildren(ctx context.Context, institutionID, parentID string) ([]*model.AcademicUnit, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicUnit, error) {
		return s.AcademicUnitStore.ListChildren(ctx, institutionID, parentID)
	})
}
func (s *academicUnitStore) ListAncestors(ctx context.Context, id string) ([]*model.AcademicUnit, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicUnit, error) {
		return s.AcademicUnitStore.ListAncestors(ctx, id)
	})
}
func (s *academicUnitStore) Search(ctx context.Context, institutionID, query string, limit int) ([]*model.AcademicUnit, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicUnit, error) {
		return s.AcademicUnitStore.Search(ctx, institutionID, query, limit)
	})
}
func (s *academicUnitStore) CreateIdempotently(ctx context.Context, input *store.AcademicUnitCreation, command *store.CommandIdempotency) (*store.AcademicUnitCommandResult, error) {
	if command == nil {
		return s.AcademicUnitStore.CreateIdempotently(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.AcademicUnitCommandResult, error) {
		return s.AcademicUnitStore.CreateIdempotently(ctx, input, command)
	})
}

func (s *programmeStore) Get(ctx context.Context, id string) (*model.Programme, error) {
	return retryCall1(ctx, s.layer, func() (*model.Programme, error) { return s.ProgrammeStore.Get(ctx, id) })
}
func (s *programmeStore) GetByName(ctx context.Context, academicUnitID, name string) (*model.Programme, error) {
	return retryCall1(ctx, s.layer, func() (*model.Programme, error) {
		return s.ProgrammeStore.GetByName(ctx, academicUnitID, name)
	})
}
func (s *programmeStore) ListByAcademicUnit(ctx context.Context, academicUnitID string) ([]*model.Programme, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Programme, error) {
		return s.ProgrammeStore.ListByAcademicUnit(ctx, academicUnitID)
	})
}
func (s *programmeStore) SearchByAcademicUnit(ctx context.Context, academicUnitID, query string, limit int) ([]*model.Programme, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Programme, error) {
		return s.ProgrammeStore.SearchByAcademicUnit(ctx, academicUnitID, query, limit)
	})
}

func (s *programmeLevelStore) Get(ctx context.Context, id string) (*model.ProgrammeLevel, error) {
	return retryCall1(ctx, s.layer, func() (*model.ProgrammeLevel, error) { return s.ProgrammeLevelStore.Get(ctx, id) })
}
func (s *programmeLevelStore) GetByName(ctx context.Context, programmeID, name string) (*model.ProgrammeLevel, error) {
	return retryCall1(ctx, s.layer, func() (*model.ProgrammeLevel, error) {
		return s.ProgrammeLevelStore.GetByName(ctx, programmeID, name)
	})
}
func (s *programmeLevelStore) ListByProgramme(ctx context.Context, programmeID string) ([]*model.ProgrammeLevel, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.ProgrammeLevel, error) {
		return s.ProgrammeLevelStore.ListByProgramme(ctx, programmeID)
	})
}
func (s *programmeLevelStore) SearchByProgramme(ctx context.Context, programmeID, query string, limit int) ([]*model.ProgrammeLevel, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.ProgrammeLevel, error) {
		return s.ProgrammeLevelStore.SearchByProgramme(ctx, programmeID, query, limit)
	})
}

func (s *academicPeriodStore) Get(ctx context.Context, id string) (*model.AcademicPeriod, error) {
	return retryCall1(ctx, s.layer, func() (*model.AcademicPeriod, error) { return s.AcademicPeriodStore.Get(ctx, id) })
}
func (s *academicPeriodStore) GetByName(ctx context.Context, institutionID, name string) (*model.AcademicPeriod, error) {
	return retryCall1(ctx, s.layer, func() (*model.AcademicPeriod, error) {
		return s.AcademicPeriodStore.GetByName(ctx, institutionID, name)
	})
}
func (s *academicPeriodStore) ListByInstitution(ctx context.Context, institutionID string) ([]*model.AcademicPeriod, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicPeriod, error) {
		return s.AcademicPeriodStore.ListByInstitution(ctx, institutionID)
	})
}
func (s *academicPeriodStore) SearchByInstitution(ctx context.Context, institutionID, query string, limit int) ([]*model.AcademicPeriod, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicPeriod, error) {
		return s.AcademicPeriodStore.SearchByInstitution(ctx, institutionID, query, limit)
	})
}
func (s *academicPeriodStore) CreateIdempotently(ctx context.Context, input *store.AcademicPeriodCreation, command *store.CommandIdempotency) (*store.AcademicPeriodCommandResult, error) {
	if command == nil {
		return s.AcademicPeriodStore.CreateIdempotently(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.AcademicPeriodCommandResult, error) {
		return s.AcademicPeriodStore.CreateIdempotently(ctx, input, command)
	})
}

func (s *examAuthoringStore) Create(ctx context.Context, input *store.ExamAuthoringCreation, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	if command == nil {
		return s.ExamAuthoringStore.Create(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamAuthoringCommandResult, error) {
		return s.ExamAuthoringStore.Create(ctx, input, command)
	})
}

func (s *examAuthoringStore) UpdateDraftText(ctx context.Context, input *store.ExamDraftTextUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	if command == nil {
		return s.ExamAuthoringStore.UpdateDraftText(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamAuthoringCommandResult, error) {
		return s.ExamAuthoringStore.UpdateDraftText(ctx, input, command)
	})
}

func (s *examAuthoringStore) UpdateDraftFocusLoss(ctx context.Context, input *store.ExamDraftFocusLossUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	if command == nil {
		return s.ExamAuthoringStore.UpdateDraftFocusLoss(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamAuthoringCommandResult, error) {
		return s.ExamAuthoringStore.UpdateDraftFocusLoss(ctx, input, command)
	})
}

func (s *examAuthoringStore) List(ctx context.Context, options store.ExamListOptions) ([]store.ExamSummary, error) {
	return retryCall1(ctx, s.layer, func() ([]store.ExamSummary, error) {
		return s.ExamAuthoringStore.List(ctx, options)
	})
}

func (s *examAuthoringStore) Archive(ctx context.Context, input *store.ExamArchive, command *store.CommandIdempotency) (*store.ExamArchiveCommandResult, error) {
	if command == nil {
		return s.ExamAuthoringStore.Archive(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamArchiveCommandResult, error) {
		return s.ExamAuthoringStore.Archive(ctx, input, command)
	})
}

func (s *examAuthoringStore) Access(ctx context.Context, examID model.ExamID, actorID model.UserID) (*store.ExamAccessSnapshot, error) {
	return retryCall1(ctx, s.layer, func() (*store.ExamAccessSnapshot, error) {
		return s.ExamAuthoringStore.Access(ctx, examID, actorID)
	})
}

func (s *examAuthoringStore) Get(ctx context.Context, examID model.ExamID, actorID model.UserID) (*store.ExamAuthoringSnapshot, error) {
	return retryCall1(ctx, s.layer, func() (*store.ExamAuthoringSnapshot, error) {
		return s.ExamAuthoringStore.Get(ctx, examID, actorID)
	})
}

func (s *examAuthoringStore) Resolve(ctx context.Context, examID model.ExamID) (*model.Exam, error) {
	return retryCall1(ctx, s.layer, func() (*model.Exam, error) {
		return s.ExamAuthoringStore.Resolve(ctx, examID)
	})
}

func (s *examRevisionStore) GetSummary(ctx context.Context, examID model.ExamID, revisionID model.ExamRevisionID) (*store.ExamRevisionSummary, error) {
	return retryCall1(ctx, s.layer, func() (*store.ExamRevisionSummary, error) {
		return s.ExamRevisionStore.GetSummary(ctx, examID, revisionID)
	})
}

func (s *examRevisionStore) List(ctx context.Context, options store.ExamRevisionListOptions) ([]store.ExamRevisionSummary, error) {
	return retryCall1(ctx, s.layer, func() ([]store.ExamRevisionSummary, error) {
		return s.ExamRevisionStore.List(ctx, options)
	})
}

func (s *examRevisionStore) GetSnapshot(ctx context.Context, examID model.ExamID, revisionID model.ExamRevisionID) (*model.ExamRevision, error) {
	return retryCall1(ctx, s.layer, func() (*model.ExamRevision, error) {
		return s.ExamRevisionStore.GetSnapshot(ctx, examID, revisionID)
	})
}

func (s *examResourceStore) List(ctx context.Context, examID model.ExamID) ([]store.ExamResourceRecord, error) {
	return retryCall1(ctx, s.layer, func() ([]store.ExamResourceRecord, error) {
		return s.ExamResourceStore.List(ctx, examID)
	})
}

func (s *examResourceStore) Get(ctx context.Context, examID model.ExamID, resourceID model.ExamResourceID) (*store.ExamResourceRecord, error) {
	return retryCall1(ctx, s.layer, func() (*store.ExamResourceRecord, error) {
		return s.ExamResourceStore.Get(ctx, examID, resourceID)
	})
}

func (s *examResourceStore) FinalizeUpload(ctx context.Context, input *store.ExamResourceUploadFinalization, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if command == nil {
		return s.ExamResourceStore.FinalizeUpload(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamResourceCommandResult, error) {
		return s.ExamResourceStore.FinalizeUpload(ctx, input, command)
	})
}

func (s *examResourceStore) UpdateMetadata(ctx context.Context, input *store.ExamResourceMetadataUpdate, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if command == nil {
		return s.ExamResourceStore.UpdateMetadata(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamResourceCommandResult, error) {
		return s.ExamResourceStore.UpdateMetadata(ctx, input, command)
	})
}

func (s *examResourceStore) Reorder(ctx context.Context, input *store.ExamResourceReorder, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if command == nil {
		return s.ExamResourceStore.Reorder(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamResourceCommandResult, error) {
		return s.ExamResourceStore.Reorder(ctx, input, command)
	})
}

func (s *examResourceStore) Remove(ctx context.Context, input *store.ExamResourceRemoval, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if command == nil {
		return s.ExamResourceStore.Remove(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamResourceCommandResult, error) {
		return s.ExamResourceStore.Remove(ctx, input, command)
	})
}

func (s *examStarterWorkspaceStore) List(ctx context.Context, examID model.ExamID) ([]store.ExamStarterWorkspaceItem, error) {
	return retryCall1(ctx, s.layer, func() ([]store.ExamStarterWorkspaceItem, error) {
		return s.ExamStarterWorkspaceStore.List(ctx, examID)
	})
}

func (s *examStarterWorkspaceStore) GetFile(ctx context.Context, examID model.ExamID, entryID model.StarterWorkspaceEntryID) (*store.ExamStarterWorkspaceItem, error) {
	return retryCall1(ctx, s.layer, func() (*store.ExamStarterWorkspaceItem, error) {
		return s.ExamStarterWorkspaceStore.GetFile(ctx, examID, entryID)
	})
}

func (s *examStarterWorkspaceStore) CreateDirectory(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.retryMutation(ctx, input, command, s.ExamStarterWorkspaceStore.CreateDirectory)
}

func (s *examStarterWorkspaceStore) CreateFile(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.retryMutation(ctx, input, command, s.ExamStarterWorkspaceStore.CreateFile)
}

func (s *examStarterWorkspaceStore) MoveEntry(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.retryMutation(ctx, input, command, s.ExamStarterWorkspaceStore.MoveEntry)
}

func (s *examStarterWorkspaceStore) ReplaceFile(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.retryMutation(ctx, input, command, s.ExamStarterWorkspaceStore.ReplaceFile)
}

func (s *examStarterWorkspaceStore) RemoveEntry(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.retryMutation(ctx, input, command, s.ExamStarterWorkspaceStore.RemoveEntry)
}

func (s *examStarterWorkspaceStore) retryMutation(
	ctx context.Context,
	input *store.ExamStarterWorkspaceMutation,
	command *store.CommandIdempotency,
	operation func(context.Context, *store.ExamStarterWorkspaceMutation, *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error),
) (*store.ExamStarterWorkspaceMutationResult, error) {
	if command == nil {
		return operation(ctx, input, command)
	}
	return retryCall1(ctx, s.layer, func() (*store.ExamStarterWorkspaceMutationResult, error) {
		return operation(ctx, input, command)
	})
}

func (s *classStore) Get(ctx context.Context, id string) (*model.Class, error) {
	return retryCall1(ctx, s.layer, func() (*model.Class, error) { return s.ClassStore.Get(ctx, id) })
}
func (s *classStore) GetByName(ctx context.Context, programmeLevelID, academicPeriodID, name string) (*model.Class, error) {
	return retryCall1(ctx, s.layer, func() (*model.Class, error) {
		return s.ClassStore.GetByName(ctx, programmeLevelID, academicPeriodID, name)
	})
}
func (s *classStore) ListByProgrammeLevel(ctx context.Context, programmeLevelID string) ([]*model.Class, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Class, error) {
		return s.ClassStore.ListByProgrammeLevel(ctx, programmeLevelID)
	})
}
func (s *classStore) ListByAcademicPeriod(ctx context.Context, academicPeriodID string) ([]*model.Class, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Class, error) {
		return s.ClassStore.ListByAcademicPeriod(ctx, academicPeriodID)
	})
}
func (s *classStore) SearchByAcademicUnit(ctx context.Context, academicUnitID, query string, limit int) ([]*model.Class, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Class, error) {
		return s.ClassStore.SearchByAcademicUnit(ctx, academicUnitID, query, limit)
	})
}
func (s *classStore) GetAcademicUnitId(ctx context.Context, id string) (string, error) {
	return retryCall1(ctx, s.layer, func() (string, error) { return s.ClassStore.GetAcademicUnitId(ctx, id) })
}

func (s *userStore) Get(ctx context.Context, id string) (*model.User, error) {
	return retryCall1(ctx, s.layer, func() (*model.User, error) { return s.UserStore.Get(ctx, id) })
}
func (s *userStore) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	return retryCall1(ctx, s.layer, func() (*model.User, error) { return s.UserStore.GetByUsername(ctx, username) })
}
func (s *userStore) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	return retryCall1(ctx, s.layer, func() (*model.User, error) { return s.UserStore.GetByEmail(ctx, email) })
}
func (s *userStore) List(ctx context.Context, options store.UserListOptions) ([]*model.User, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.User, error) { return s.UserStore.List(ctx, options) })
}

func (s *externalIdentityStore) Get(ctx context.Context, id string) (*model.ExternalIdentity, error) {
	return retryCall1(ctx, s.layer, func() (*model.ExternalIdentity, error) { return s.ExternalIdentityStore.Get(ctx, id) })
}
func (s *externalIdentityStore) GetByProviderSubject(ctx context.Context, providerID, subject string) (*model.ExternalIdentity, error) {
	return retryCall1(ctx, s.layer, func() (*model.ExternalIdentity, error) {
		return s.ExternalIdentityStore.GetByProviderSubject(ctx, providerID, subject)
	})
}
func (s *externalIdentityStore) ListByUser(ctx context.Context, userID string) ([]*model.ExternalIdentity, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.ExternalIdentity, error) {
		return s.ExternalIdentityStore.ListByUser(ctx, userID)
	})
}

func (s *externalLoginStateStore) GetByStateHash(ctx context.Context, hash string) (*model.ExternalLoginState, error) {
	return retryCall1(ctx, s.layer, func() (*model.ExternalLoginState, error) {
		return s.ExternalLoginStateStore.GetByStateHash(ctx, hash)
	})
}

func (s *userTokenStore) GetByHash(ctx context.Context, hash string, purpose model.UserTokenPurpose) (*model.UserToken, error) {
	return retryCall1(ctx, s.layer, func() (*model.UserToken, error) {
		return s.UserTokenStore.GetByHash(ctx, hash, purpose)
	})
}

func (s *personalAccessTokenStore) Get(ctx context.Context, id string) (*model.PersonalAccessToken, error) {
	return retryCall1(ctx, s.layer, func() (*model.PersonalAccessToken, error) {
		return s.PersonalAccessTokenStore.Get(ctx, id)
	})
}
func (s *personalAccessTokenStore) ListByUser(ctx context.Context, userID string) ([]*model.PersonalAccessToken, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.PersonalAccessToken, error) {
		return s.PersonalAccessTokenStore.ListByUser(ctx, userID)
	})
}

func (s *mfaStore) GetByUser(ctx context.Context, userID string) (*model.MFACredential, error) {
	return retryCall1(ctx, s.layer, func() (*model.MFACredential, error) { return s.MFAStore.GetByUser(ctx, userID) })
}
func (s *mfaStore) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	return retryCall1(ctx, s.layer, func() (int, error) { return s.MFAStore.CountRecoveryCodes(ctx, userID) })
}

func (s *affiliationStore) Get(ctx context.Context, id string) (*model.Affiliation, error) {
	return retryCall1(ctx, s.layer, func() (*model.Affiliation, error) { return s.AffiliationStore.Get(ctx, id) })
}
func (s *affiliationStore) ListByUser(ctx context.Context, userID string) ([]*model.Affiliation, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Affiliation, error) { return s.AffiliationStore.ListByUser(ctx, userID) })
}
func (s *affiliationStore) ListActiveByUser(ctx context.Context, userID string, at int64) ([]*model.Affiliation, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Affiliation, error) {
		return s.AffiliationStore.ListActiveByUser(ctx, userID, at)
	})
}

func (s *academicUnitMemberStore) Get(ctx context.Context, id string) (*model.AcademicUnitMember, error) {
	return retryCall1(ctx, s.layer, func() (*model.AcademicUnitMember, error) { return s.AcademicUnitMemberStore.Get(ctx, id) })
}
func (s *academicUnitMemberStore) ListByUser(ctx context.Context, userID string) ([]*model.AcademicUnitMember, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicUnitMember, error) {
		return s.AcademicUnitMemberStore.ListByUser(ctx, userID)
	})
}
func (s *academicUnitMemberStore) ListByAcademicUnit(ctx context.Context, academicUnitID string, at int64) ([]*model.AcademicUnitMember, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicUnitMember, error) {
		return s.AcademicUnitMemberStore.ListByAcademicUnit(ctx, academicUnitID, at)
	})
}
func (s *academicUnitMemberStore) ListActiveByUser(ctx context.Context, userID string, at int64) ([]*model.AcademicUnitMember, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AcademicUnitMember, error) {
		return s.AcademicUnitMemberStore.ListActiveByUser(ctx, userID, at)
	})
}

func (s *classMemberStore) Get(ctx context.Context, id string) (*model.ClassMember, error) {
	return retryCall1(ctx, s.layer, func() (*model.ClassMember, error) { return s.ClassMemberStore.Get(ctx, id) })
}
func (s *classMemberStore) ListByUser(ctx context.Context, userID string) ([]*model.ClassMember, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.ClassMember, error) { return s.ClassMemberStore.ListByUser(ctx, userID) })
}
func (s *classMemberStore) ListByClass(ctx context.Context, classID string, at int64) ([]*model.ClassMember, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.ClassMember, error) {
		return s.ClassMemberStore.ListByClass(ctx, classID, at)
	})
}
func (s *classMemberStore) ListActiveByUser(ctx context.Context, userID string, at int64) ([]*model.ClassMember, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.ClassMember, error) {
		return s.ClassMemberStore.ListActiveByUser(ctx, userID, at)
	})
}

func (s *passwordCredentialStore) GetByUser(ctx context.Context, userID string) (*model.PasswordCredential, error) {
	return retryCall1(ctx, s.layer, func() (*model.PasswordCredential, error) {
		return s.PasswordCredentialStore.GetByUser(ctx, userID)
	})
}

func (s *sessionStore) Get(ctx context.Context, id string) (*model.Session, error) {
	return retryCall1(ctx, s.layer, func() (*model.Session, error) { return s.SessionStore.Get(ctx, id) })
}
func (s *sessionStore) ListByUser(ctx context.Context, userID string) ([]*model.Session, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Session, error) { return s.SessionStore.ListByUser(ctx, userID) })
}
func (s *sessionStore) ListActiveByUser(ctx context.Context, userID string, at int64) ([]*model.Session, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Session, error) {
		return s.SessionStore.ListActiveByUser(ctx, userID, at)
	})
}

func (s *sessionCredentialStore) GetSessionByTokenHash(ctx context.Context, hash string, kind model.SessionCredentialKind) (*model.SessionCredential, *model.Session, error) {
	return retryCall2(ctx, s.layer, func() (*model.SessionCredential, *model.Session, error) {
		return s.SessionCredentialStore.GetSessionByTokenHash(ctx, hash, kind)
	})
}

func (s *roleStore) Get(ctx context.Context, id string) (*model.Role, error) {
	return retryCall1(ctx, s.layer, func() (*model.Role, error) { return s.RoleStore.Get(ctx, id) })
}
func (s *roleStore) GetByName(ctx context.Context, name string) (*model.Role, error) {
	return retryCall1(ctx, s.layer, func() (*model.Role, error) { return s.RoleStore.GetByName(ctx, name) })
}
func (s *roleStore) GetByIds(ctx context.Context, ids []string) ([]*model.Role, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Role, error) { return s.RoleStore.GetByIds(ctx, ids) })
}
func (s *roleStore) List(ctx context.Context) ([]*model.Role, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.Role, error) { return s.RoleStore.List(ctx) })
}

func (s *roleBindingStore) Get(ctx context.Context, id string) (*model.RoleBinding, error) {
	return retryCall1(ctx, s.layer, func() (*model.RoleBinding, error) { return s.RoleBindingStore.Get(ctx, id) })
}
func (s *roleBindingStore) ListByUser(ctx context.Context, userID string) ([]*model.RoleBinding, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.RoleBinding, error) {
		return s.RoleBindingStore.ListByUser(ctx, userID)
	})
}
func (s *roleBindingStore) ListByScope(ctx context.Context, scopeType model.RoleScopeType, scopeID string) ([]*model.RoleBinding, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.RoleBinding, error) {
		return s.RoleBindingStore.ListByScope(ctx, scopeType, scopeID)
	})
}
func (s *roleBindingStore) ListActiveByUser(ctx context.Context, userID string, at int64) ([]*model.RoleBinding, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.RoleBinding, error) {
		return s.RoleBindingStore.ListActiveByUser(ctx, userID, at)
	})
}

func (s *auditStore) Get(ctx context.Context, id string) (*model.AuditEvent, error) {
	return retryCall1(ctx, s.layer, func() (*model.AuditEvent, error) { return s.AuditStore.Get(ctx, id) })
}
func (s *auditStore) List(ctx context.Context, options store.AuditListOptions) ([]*model.AuditEvent, error) {
	return retryCall1(ctx, s.layer, func() ([]*model.AuditEvent, error) { return s.AuditStore.List(ctx, options) })
}

func (s *installationStore) Get(ctx context.Context) (*model.InstallationState, error) {
	return retryCall1(ctx, s.layer, func() (*model.InstallationState, error) { return s.InstallationStore.Get(ctx) })
}

func (s *clusterDiscoveryStore) ListLive(ctx context.Context, nowMillis int64) ([]*store.ClusterDiscoveryNode, error) {
	return retryCall1(ctx, s.layer, func() ([]*store.ClusterDiscoveryNode, error) {
		return s.ClusterDiscoveryStore.ListLive(ctx, nowMillis)
	})
}
