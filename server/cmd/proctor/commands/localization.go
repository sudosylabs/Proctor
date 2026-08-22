// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bytes"
	"os"
	"strings"
	"text/template"

	server "github.com/sudosylabs/proctor/server"
	"github.com/sudosylabs/proctor/server/localization"
)

const commandLocalizationOrigin = "cmd/proctor/commands"

type commandLocalizer interface {
	Translate(locale, id string, args any) (string, error)
	SupportedLocales() []string
}

type commandText struct {
	localizer commandLocalizer
	locale    string
}

func (text commandText) value(id, fallback string, args any) string {
	if text.localizer != nil {
		if translated, err := text.localizer.Translate(text.locale, id, args); err == nil {
			return translated
		}
	}
	if args != nil {
		parsed, err := template.New(id).Option("missingkey=error").Parse(fallback)
		if err == nil {
			var rendered bytes.Buffer
			if err := parsed.Execute(&rendered, args); err == nil {
				return rendered.String()
			}
		}
	}
	return fallback
}

func englishCommandText() commandText {
	localizer, _ := server.NewEmbeddedLocalizer()
	return commandText{localizer: localizer, locale: localization.EnglishLocale}
}

func productionCommandText() commandText {
	localizer, _ := server.NewEmbeddedLocalizer()
	if localizer == nil {
		return commandText{}
	}
	raw := firstNonemptyEnvironment("PROCTOR_LOCALE", "LC_ALL", "LC_MESSAGES", "LANG")
	if prefix, _, found := strings.Cut(raw, "."); found {
		raw = prefix
	}
	if prefix, _, found := strings.Cut(raw, "@"); found {
		raw = prefix
	}
	return commandText{
		localizer: localizer,
		locale:    localization.PreferredLocale(raw, localizer.SupportedLocales()),
	}
}

func firstNonemptyEnvironment(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

// LocalizationDefinitions returns all prose owned by the operator CLI.
func LocalizationDefinitions() []localization.Definition {
	plain := []string{
		"cli.administrator.error.invalid_flags",
		"cli.administrator.error.no_result",
		"cli.administrator.error.requires_recover",
		"cli.administrator.error.requires_recovery_input",
		"cli.administrator.error.requires_subcommand",
		"cli.administrator.error.unexpected_arguments",
		"cli.administrator.flag.enable_local_login",
		"cli.administrator.flag.institution_id",
		"cli.administrator.flag.rotate_password",
		"cli.administrator.flag.user_id",
		"cli.administrator.recover.short",
		"cli.administrator.recover.success",
		"cli.administrator.short",
		"cli.config.error.read_flag",
		"cli.config.error.requires_subcommand",
		"cli.config.flag.path",
		"cli.config.short",
		"cli.config.validate.short",
		"cli.config.validate.success",
		"cli.migrate.error.requires_subcommand",
		"cli.migrate.short",
		"cli.migrate.status.short",
		"cli.migrate.up.short",
		"cli.password.error.inspect_input",
		"cli.password.error.invalid_length",
		"cli.password.error.read_input",
		"cli.password.error.terminal_input",
		"cli.password.error.unavailable",
		"cli.root.error.command_required",
		"cli.root.short",
		"cli.root.warning.root_user",
		"cli.serve.short",
		"cli.version.flag.json",
		"cli.version.short",
	}
	definitions := make([]localization.Definition, 0, len(plain)+3)
	for _, id := range plain {
		definitions = append(definitions, localization.Definition{ID: id, Origin: commandLocalizationOrigin})
	}
	definitions = append(definitions,
		localization.Definition{ID: "cli.migrate.up.success", Origin: commandLocalizationOrigin, Variables: []string{"Version"}},
		localization.Definition{ID: "cli.migrate.status.success", Origin: commandLocalizationOrigin, Variables: []string{"DatabaseVersion", "PendingMigrations", "ServerVersion"}},
		localization.Definition{ID: "cli.version.output", Origin: commandLocalizationOrigin, Variables: []string{"BuildTime", "Commit", "GoVersion", "Version"}},
	)
	return definitions
}
