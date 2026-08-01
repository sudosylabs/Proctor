// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"runtime"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
	GoVersion string
}

func CurrentBuildInfo() BuildInfo {
	return BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	}
}
