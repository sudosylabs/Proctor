// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"strings"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// maximumAcademicPageProbe is the largest page plus the one probe row that
// tells the caller whether another page follows.
const maximumAcademicPageProbe = 201

// academicNamePage applies options to a listing of table: the optional term
// filter, the exclusive (name, id) continuation, the (name, id) order, and the
// limit. It reports false for invalid options.
func academicNamePage(query sq.SelectBuilder, table string, options store.AcademicNameListOptions) (sq.SelectBuilder, bool) {
	if options.Limit < 1 || options.Limit > maximumAcademicPageProbe ||
		(options.AfterName == "") != (options.AfterID == "") ||
		(options.AfterID != "" && !model.IsValidOpaqueID(options.AfterID)) {
		return query, false
	}
	if term := strings.TrimSpace(options.Term); term != "" {
		pattern := directorySearchPattern(term)
		query = query.Where("("+table+".name ILIKE ? ESCAPE '!' OR "+table+".display_name ILIKE ? ESCAPE '!')", pattern, pattern)
	}
	if options.AfterID != "" {
		query = query.Where("("+table+".name, "+table+".id) > (?, ?)", options.AfterName, options.AfterID)
	}
	return query.OrderBy(table+".name", table+".id").Limit(uint64(options.Limit)), true
}
