// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/model"
)

func queryActiveAt(w http.ResponseWriter, r *http.Request) (int64, bool) {
	activeAt, err := parseActiveAt(r)
	if err != nil {
		WriteError(w, r, err)
		return 0, false
	}
	return activeAt, true
}

func parseActiveAt(r *http.Request) (int64, error) {
	history := r.URL.Query().Get("history")
	if history != "" {
		if history != "true" && history != "false" {
			return 0, invalidRequestError("history", nil)
		}
		if history == "true" {
			return 0, nil
		}
	}
	value := r.URL.Query().Get("active_at")
	if value == "" {
		return model.GetMillis(), nil
	}
	activeAt, err := strconv.ParseInt(value, 10, 64)
	if err != nil || activeAt <= 0 {
		return 0, invalidRequestError("active_at", err)
	}
	return activeAt, nil
}
