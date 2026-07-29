// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return 0
	}

	_, _ = fmt.Fprintln(os.Stderr, "proctor:", err)
	var usageError *UsageError
	if errors.As(err, &usageError) {
		return 2
	}
	return 1
}

type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if stdout == nil || stderr == nil {
		return errors.New("stdout and stderr are required")
	}
	if len(args) == 0 {
		writeUsage(stderr)
		return &UsageError{Message: "a command is required"}
	}

	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], stderr)
	case "config":
		return runConfig(ctx, args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(ctx, args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		writeUsage(stdout)
		return nil
	default:
		writeUsage(stderr)
		return &UsageError{Message: fmt.Sprintf("unknown command %q", args[0])}
	}
}

func runMigrate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "up" && args[0] != "status") {
		return &UsageError{Message: "usage: proctor migrate <up|status> [--config path]"}
	}
	action := args[0]
	flags := newFlagSet("migrate "+action, stderr)
	path := flags.String("config", "", "path to a JSON configuration file")
	if err := flags.Parse(args[1:]); err != nil {
		return &UsageError{Message: err.Error()}
	}
	if flags.NArg() != 0 {
		return &UsageError{Message: "migrate does not accept positional arguments after its action"}
	}

	var backing config.BackingStore = config.NewMemoryStore(nil)
	if *path != "" {
		fileStore, err := config.NewFileStore(*path)
		if err != nil {
			return err
		}
		backing = fileStore
	}
	configStore, err := config.NewStore(ctx, backing, config.StoreOptions{})
	if err != nil {
		return err
	}
	defer configStore.Close()

	migrator, err := sqlstore.NewMigrator(
		ctx,
		sqlstore.SettingsFromConfig(configStore.Get().Database),
	)
	if err != nil {
		return err
	}
	defer migrator.Close()

	switch action {
	case "up":
		if err := migrator.Up(); err != nil {
			return err
		}
		version, err := migrator.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "database schema migrated to version %d\n", version)
		return err
	default:
		current, err := migrator.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		local, err := sqlstore.LocalSchemaVersion()
		if err != nil {
			return err
		}
		pending, err := migrator.Pending()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			stdout,
			"database schema version %d; server schema version %d; pending migrations %d\n",
			current,
			local,
			len(pending),
		)
		return err
	}
}

func runServe(ctx context.Context, args []string, stderr io.Writer) error {
	flags := newFlagSet("serve", stderr)
	path := flags.String("config", "", "path to a JSON configuration file")
	if err := flags.Parse(args); err != nil {
		return &UsageError{Message: err.Error()}
	}
	if flags.NArg() != 0 {
		return &UsageError{Message: "serve does not accept positional arguments"}
	}

	var options []app.Option
	if *path != "" {
		options = append(options, app.WithConfigPath(*path))
	}
	server, err := app.NewServer(ctx, options...)
	if err != nil {
		return err
	}
	return server.Start(ctx)
}

func runConfig(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "validate" {
		return &UsageError{Message: "usage: proctor config validate [--config path]"}
	}

	flags := newFlagSet("config validate", stderr)
	path := flags.String("config", "", "path to a JSON configuration file")
	if err := flags.Parse(args[1:]); err != nil {
		return &UsageError{Message: err.Error()}
	}
	if flags.NArg() != 0 {
		return &UsageError{Message: "config validate does not accept positional arguments"}
	}

	var backing config.BackingStore = config.NewMemoryStore(nil)
	if *path != "" {
		fileStore, err := config.NewFileStore(*path)
		if err != nil {
			return err
		}
		backing = fileStore
	}
	store, err := config.NewStore(ctx, backing, config.StoreOptions{})
	if err != nil {
		return err
	}
	if err := store.Close(); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "configuration is valid")
	return err
}

func runVersion(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("version", stderr)
	asJSON := flags.Bool("json", false, "write build information as JSON")
	if err := flags.Parse(args); err != nil {
		return &UsageError{Message: err.Error()}
	}
	if flags.NArg() != 0 {
		return &UsageError{Message: "version does not accept positional arguments"}
	}

	info := app.CurrentBuildInfo()
	if *asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	}
	_, err := fmt.Fprintf(
		stdout,
		"proctor %s (commit %s, built %s, %s)\n",
		info.Version,
		info.Commit,
		info.BuildTime,
		info.GoVersion,
	)
	return err
}

func newFlagSet(name string, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	return flags
}

func writeUsage(writer io.Writer) {
	var usage bytes.Buffer
	usage.WriteString("Usage:\n")
	usage.WriteString("  proctor serve [--config path]\n")
	usage.WriteString("  proctor config validate [--config path]\n")
	usage.WriteString("  proctor migrate <up|status> [--config path]\n")
	usage.WriteString("  proctor version [--json]\n")
	usage.WriteString("  proctor help\n")
	_, _ = io.Copy(writer, &usage)
}
