// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"io"

	"github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/localization"
)

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, execute executors) int {
	return runWithText(ctx, args, stdin, stdout, stderr, execute, englishCommandText())
}

func englishCommandText() commandText {
	localizer, _ := server.NewEmbeddedLocalizer()
	return commandText{localizer: localizer, locale: localization.EnglishLocale}
}
