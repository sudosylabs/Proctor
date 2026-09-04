// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import "time"

func constructProfilesAndFiles(
	deps Dependencies,
	foundation applicationFoundation,
	access accessAcademicConstruction,
) (profileFileConstruction, error) {
	authorization := userProfileAuthorization{
		authorization: access.authorization,
		institutions:  deps.Store.Institution(),
	}
	userSettings, err := newUserSettingsService(
		deps.Store.UserSettings(),
		userSettingsAuditAdapter{audit: foundation.audit},
		userSettingsRealtimeEffects{realtime: foundation.realtime},
		userSettingsEffectReporter{realtime: foundation.realtime},
		time.Now,
	)
	if err != nil {
		return profileFileConstruction{}, err
	}
	return profileFileConstruction{
		userProfiles: newUserProfileService(
			deps.Store.User(), authorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now,
		),
		profilePictures: newProfilePictureService(
			deps.Store.User(), deps.Store.File(),
			deps.FileContent, deps.FileContent, deps.FileContent, deps.FileContent,
			authorization,
			mutationAuditAdapter{audit: foundation.audit},
			profilePictureRealtimeEffects{realtime: foundation.realtime},
			profilePictureEffectReporter{realtime: foundation.realtime},
			nil,
			time.Now,
		),
		userSettings: userSettings,
	}, nil
}
