//go:build !dev

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and NOTICE in the server module root for
// license and attribution information.
// SPDX-License-Identifier: Apache-2.0
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost service environment selection; see NOTICE.

package server

// defaultServiceEnvironment fails closed: every build without the explicit
// dev tag, including a plain go build, enforces production behavior.
const defaultServiceEnvironment = ServiceEnvironmentProduction
