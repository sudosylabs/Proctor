// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func readPrivatePassword(input io.Reader, text commandText) (string, error) {
	if input == nil {
		return "", errors.New(text.value("cli.password.error.unavailable", "private password input is unavailable", nil))
	}
	if file, ok := input.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return "", fmt.Errorf("%s: %w", text.value("cli.password.error.inspect_input", "inspect private password input", nil), err)
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return "", errors.New(text.value("cli.password.error.terminal_input", "private password input must be redirected from a non-terminal source", nil))
		}
	}
	reader := bufio.NewReader(io.LimitReader(input, 4098))
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%s: %w", text.value("cli.password.error.read_input", "read private password", nil), err)
	}
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || len(value) > 4096 {
		return "", errors.New(text.value("cli.password.error.invalid_length", "private password input has invalid length", nil))
	}
	return value, nil
}
