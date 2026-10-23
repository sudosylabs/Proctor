// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sudosylabs/proctor/server"
)

type configShowExecutor func(context.Context, string) (server.EffectiveConfiguration, error)

func newConfigShowCommand(show configShowExecutor, text commandText) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "show",
		Short: text.value("cli.config.show.short", "Print the effective configuration with every secret redacted", nil),
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			path, err := configPath(command, text)
			if err != nil {
				return err
			}
			shown, err := show(command.Context(), path)
			if err != nil {
				return err
			}
			output := command.OutOrStdout()
			if asJSON {
				document := bytes.TrimRight(shown.Document, "\n")
				_, err := fmt.Fprintf(output, "%s\n", document)
				return err
			}
			if len(shown.EnvironmentOverrides) > 0 {
				if _, err := fmt.Fprintln(output, text.value(
					"cli.config.show.environment_overrides",
					"# environment overrides: {{.Names}}",
					map[string]any{"Names": strings.Join(shown.EnvironmentOverrides, ", ")},
				)); err != nil {
					return err
				}
			}
			return writeConfigurationSettings(output, shown.Document)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, text.value("cli.config.show.flag.json", "write the redacted configuration document as JSON", nil))
	return command
}

// writeConfigurationSettings prints one "Path = value" line per leaf in the
// document's declaration order. Values keep their JSON spelling so strings,
// numbers, and empty collections stay unambiguous.
func writeConfigurationSettings(output io.Writer, document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := writeConfigurationValue(output, decoder, ""); err != nil {
		return fmt.Errorf("render configuration: %w", err)
	}
	return nil
}

func writeConfigurationValue(output io.Writer, decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		encoded, err := json.Marshal(token)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "%s = %s\n", path, encoded)
		return err
	}
	empty, closing := "{}", json.Delim('}')
	if delimiter == '[' {
		empty, closing = "[]", ']'
	}
	written := 0
	for decoder.More() {
		child := path + "[" + strconv.Itoa(written) + "]"
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("configuration member name is not a string")
			}
			child = name
			if path != "" {
				child = path + "." + name
			}
		}
		if err := writeConfigurationValue(output, decoder, child); err != nil {
			return err
		}
		written++
	}
	if end, err := decoder.Token(); err != nil || end != closing {
		return errors.Join(errors.New("configuration document is malformed"), err)
	}
	if written == 0 && path != "" {
		_, err := fmt.Fprintf(output, "%s = %s\n", path, empty)
		return err
	}
	return nil
}
