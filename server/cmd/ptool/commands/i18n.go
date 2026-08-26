// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	proctorcommands "github.com/sudosylabs/proctor/server/cmd/proctor/commands"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/websocket"
)

type i18nOptions struct {
	catalogs string
}

type catalogEntry struct {
	ID          string          `json:"id"`
	Translation json.RawMessage `json:"translation"`
}

func newI18nCommand() *cobra.Command {
	options := &i18nOptions{}
	command := &cobra.Command{
		Use:   "i18n",
		Short: "Inspect and validate server translation catalogs",
	}
	command.PersistentFlags().StringVar(&options.catalogs, "catalogs", "i18n", "directory containing locale catalogs")
	command.AddCommand(
		newI18nCheckCommand(options),
		newI18nListCommand(options),
		newI18nMissingCommand(options),
		newI18nFormatCommand(options),
	)
	return command
}

func localizationDefinitions() ([]localization.Definition, error) {
	// This explicit aggregation is intentional. Dynamic families are expanded
	// by their owning package; irregular messages are declared beside their
	// consumer. Adding a new consumer requires one visible registration here.
	return localization.MergeDefinitions(
		appmail.LocalizationDefinitions(),
		proctorcommands.LocalizationDefinitions(),
		httpapi.LocalizationDefinitions(),
		websocket.LocalizationDefinitions(),
	)
}

func loadCatalogs(directory string) (*localization.Localizer, []localization.Definition, error) {
	directory = filepath.Clean(directory)
	localizer, err := localization.New(os.DirFS(directory), localization.EnglishLocale)
	if err != nil {
		return nil, nil, err
	}
	definitions, err := localizationDefinitions()
	if err != nil {
		return nil, nil, err
	}
	return localizer, definitions, nil
}

func newI18nCheckCommand(options *i18nOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Validate catalogs against all declared messages",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			localizer, definitions, err := loadCatalogs(options.catalogs)
			if err != nil {
				return err
			}
			// Browser source owns and exactly validates the webapp namespace in
			// webapp/scripts/generate-i18n.mjs. Server consumers retain exact
			// ownership of every other catalog entry.
			if err := localizer.ValidateDefinitionsWithDelegatedPrefixes(definitions, "webapp."); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "validated %d messages across %d locale(s)\n", len(definitions), len(localizer.SupportedLocales()))
			return err
		},
	}
}

func newI18nListCommand(options *i18nOptions) *cobra.Command {
	var origin string
	command := &cobra.Command{
		Use:   "list",
		Short: "List declared messages and their owners",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, definitions, err := loadCatalogs(options.catalogs)
			if err != nil {
				return err
			}
			for _, definition := range definitions {
				if origin != "" && definition.Origin != origin {
					continue
				}
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", definition.ID, definition.Origin); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&origin, "origin", "", "only list messages owned by this origin")
	return command
}

func newI18nMissingCommand(options *i18nOptions) *cobra.Command {
	var locale string
	command := &cobra.Command{
		Use:   "missing",
		Short: "List declared messages missing from a locale",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			localizer, definitions, err := loadCatalogs(options.catalogs)
			if err != nil {
				return err
			}
			locales := localizer.SupportedLocales()
			if locale != "" {
				locales = []string{locale}
			}
			for _, candidate := range locales {
				missing, err := localizer.MissingDefinitions(candidate, definitions)
				if err != nil {
					return err
				}
				for _, definition := range missing {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", candidate, definition.ID, definition.Origin); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&locale, "locale", "", "only report this locale")
	return command
}

func newI18nFormatCommand(options *i18nOptions) *cobra.Command {
	var check bool
	command := &cobra.Command{
		Use:   "format",
		Short: "Lexically sort and consistently format catalog files",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			files, err := filepath.Glob(filepath.Join(filepath.Clean(options.catalogs), "*.json"))
			if err != nil {
				return err
			}
			if len(files) == 0 {
				return errors.New("no localization catalogs found")
			}
			for _, name := range files {
				changed, err := formatCatalog(name, check)
				if err != nil {
					return err
				}
				if changed {
					if check {
						return fmt.Errorf("catalog %q is not formatted", name)
					}
					if _, err := fmt.Fprintln(command.OutOrStdout(), name); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	command.Flags().BoolVar(&check, "check", false, "report formatting drift without writing")
	return command
}

func formatCatalog(name string, check bool) (bool, error) {
	input, err := os.ReadFile(name)
	if err != nil {
		return false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var entries []catalogEntry
	if err := decoder.Decode(&entries); err != nil {
		return false, fmt.Errorf("decode catalog %q: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return false, fmt.Errorf("decode catalog %q: %w", name, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	for index, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" || len(entry.Translation) == 0 {
			return false, fmt.Errorf("catalog %q entry %d is incomplete", name, index)
		}
		if index > 0 && entries[index-1].ID == entry.ID {
			return false, fmt.Errorf("catalog %q repeats id %q", name, entry.ID)
		}
	}
	formatted, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return false, err
	}
	formatted = append(formatted, '\n')
	changed := !bytes.Equal(input, formatted)
	if !changed || check {
		return changed, nil
	}
	info, err := os.Stat(name)
	if err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(name), ".ptool-i18n-*")
	if err != nil {
		return false, err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		temporary.Close()
		return false, err
	}
	if _, err := temporary.Write(formatted); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryName, name); err != nil {
		return false, err
	}
	return true, nil
}
