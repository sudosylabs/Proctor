// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/client4.go. Proctor currently
// exposes one stable API generation and keeps its active URL suffix in one
// client-facing contract.

package model

const (
	APIURLSuffixV1 = "/api/v1"
	APIURLSuffix   = APIURLSuffixV1
)
