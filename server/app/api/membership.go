// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/model"
)

func queryActiveAt(w http.ResponseWriter, r *http.Request) (int64, bool) {
	history := r.URL.Query().Get("history")
	if history != "" {
		if history != "true" && history != "false" {
			WriteError(w, r, invalidRequestError("history", nil))
			return 0, false
		}
		if history == "true" {
			return 0, true
		}
	}
	value := r.URL.Query().Get("active_at")
	if value == "" {
		return model.GetMillis(), true
	}
	activeAt, err := strconv.ParseInt(value, 10, 64)
	if err != nil || activeAt <= 0 {
		WriteError(w, r, invalidRequestError("active_at", err))
		return 0, false
	}
	return activeAt, true
}
