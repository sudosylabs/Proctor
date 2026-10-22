// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and NOTICE in the server module root for
// license and attribution information.
// SPDX-License-Identifier: Apache-2.0
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/public/model/client4.go. Proctor currently
// exposes one stable API generation and keeps its active URL suffix in one
// client-facing contract.

package model

// APIURLSuffix is the path prefix of the public HTTP API. Its v1 segment is
// the client-facing API generation, not an internal version.
const APIURLSuffix = "/api/v1"
