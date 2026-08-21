// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func readPrivatePassword(input io.Reader) (string, error) {
	if input == nil {
		return "", errors.New("private password input is unavailable")
	}
	if file, ok := input.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return "", fmt.Errorf("inspect private password input: %w", err)
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return "", errors.New("private password input must be redirected from a non-terminal source")
		}
	}
	reader := bufio.NewReader(io.LimitReader(input, 4098))
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read private password: %w", err)
	}
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || len(value) > 4096 {
		return "", errors.New("private password input has invalid length")
	}
	return value, nil
}
