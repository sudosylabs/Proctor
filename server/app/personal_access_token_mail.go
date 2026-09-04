// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import appmail "github.com/sudosylabs/proctor/server/app/mail"

type personalAccessTokenSecurityNoticeMailPreparer interface {
	PreparePersonalAccessTokenSecurityNotice(appmail.PersonalAccessTokenPreparation) (*preparedDirectMail, error)
}
