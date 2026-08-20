// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	server "github.com/sudosylabs/proctor/server"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return reportError(runWithPrivateInput(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr), os.Stderr)
}

// reportError writes a single operational failure line and maps it to the
// process exit code: 0 for success, 2 for usage failures, 1 otherwise.
func reportError(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}

	_, _ = fmt.Fprintln(stderr, "proctor:", err)
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
	return runWithPrivateInput(ctx, args, nil, stdout, stderr)
}

func runWithPrivateInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if stdout == nil || stderr == nil {
		return errors.New("stdout and stderr are required")
	}
	if len(args) == 0 {
		writeUsage(stderr)
		return &UsageError{Message: "a command is required"}
	}

	switch args[0] {
	case "administrator":
		if len(args) == 1 || args[1] != "recover" {
			return &UsageError{Message: "usage: proctor administrator recover [options]"}
		}
		return runAdministratorRecover(ctx, args[2:], stdin, stdout, stderr, server.RecoverAdministratorAccess)
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

type administratorRecoveryExecutor func(context.Context, string, server.AdministratorRecoveryCommand) (*server.AdministratorRecoveryResult, error)

func runAdministratorRecover(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, recover administratorRecoveryExecutor) error {
	flags := newFlagSet("administrator recover", stderr)
	path := flags.String("config", "", "path to a JSON configuration file")
	institutionID := flags.String("institution-id", "", "exact Institution identifier to confirm")
	userID := flags.String("user-id", "", "active system-administrator User identifier")
	enableLocalLogin := flags.Bool("enable-local-login", false, "re-enable local login")
	rotatePassword := flags.Bool("rotate-password", false, "read and rotate the password from private input")
	if err := flags.Parse(args); err != nil {
		return &UsageError{Message: err.Error()}
	}
	if flags.NArg() != 0 {
		return &UsageError{Message: "administrator recover does not accept positional arguments or passwords"}
	}
	if strings.TrimSpace(*institutionID) == "" || strings.TrimSpace(*userID) == "" || (!*enableLocalLogin && !*rotatePassword) {
		return &UsageError{Message: "administrator recover requires --institution-id, --user-id, and at least one recovery action"}
	}
	password := ""
	if *rotatePassword {
		var err error
		password, err = readPrivatePassword(stdin, stderr)
		if err != nil {
			return err
		}
	}
	result, err := recover(ctx, *path, server.AdministratorRecoveryCommand{
		InstitutionID: strings.TrimSpace(*institutionID), UserID: strings.TrimSpace(*userID),
		EnableLocalLogin: *enableLocalLogin, Password: password,
	})
	password = ""
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("administrator recovery returned no result")
	}
	_, err = fmt.Fprintln(stdout, "administrator recovery recorded; restart Proctor normally to reconcile its audit record")
	return err
}

func readPrivatePassword(input io.Reader, _ io.Writer) (string, error) {
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

	switch action {
	case "up":
		version, err := server.MigrateUp(ctx, *path)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "database schema migrated to version %d\n", version)
		return err
	default:
		status, err := server.MigrateStatus(ctx, *path)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(
			stdout,
			"database schema version %d; server schema version %d; pending migrations %d\n",
			status.DatabaseVersion,
			status.ServerVersion,
			status.PendingMigrations,
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

	var options []server.Option
	if *path != "" {
		options = append(options, server.WithConfigPath(*path))
	}
	node, err := server.New(ctx, options...)
	if err != nil {
		return err
	}
	return node.Start(ctx)
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

	if err := server.ValidateConfig(ctx, *path); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "configuration is valid")
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

	info := server.CurrentBuildInfo()
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
	usage.WriteString("  proctor administrator recover --institution-id ID --user-id ID [--enable-local-login] [--rotate-password] [--config path]\n")
	usage.WriteString("  proctor version [--json]\n")
	usage.WriteString("  proctor help\n")
	_, _ = io.Copy(writer, &usage)
}
