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
	userSettings, err := newUserSettingsService(userSettingsServiceDependencies{Settings: deps.Store.UserSettings(), Audit: userSettingsAuditAdapter{audit: foundation.audit}, Effects: userSettingsRealtimeEffects{realtime: foundation.realtime}, EffectFailures: userSettingsEffectReporter{realtime: foundation.realtime}, Now: time.Now})
	if err != nil {
		return profileFileConstruction{}, err
	}
	return profileFileConstruction{
		userProfiles:    newUserProfileService(userProfileServiceDependencies{Users: deps.Store.User(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Now: time.Now}),
		profilePictures: newProfilePictureService(profilePictureServiceDependencies{Users: deps.Store.User(), Files: deps.Store.File(), Uploads: deps.FileContent, Reads: deps.FileContent, Fallback: deps.FileContent, Defaults: deps.FileContent, Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: profilePictureRealtimeEffects{realtime: foundation.realtime}, EffectFailures: profilePictureEffectReporter{realtime: foundation.realtime}, DefaultJobs: nil, Now: time.Now}),
		userSettings:    userSettings,
	}, nil
}
