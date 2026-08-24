// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/sudosylabs/proctor/server/internal/openapidoc"
)

type openAPIOptions struct {
	source string
	output string
}

func newOpenAPICommand() *cobra.Command {
	options := &openAPIOptions{}
	command := &cobra.Command{
		Use:   "openapi",
		Short: "Build and validate the public OpenAPI contract",
	}
	command.PersistentFlags().StringVar(&options.source, "source", "openapi", "human-authored OpenAPI source directory")
	command.PersistentFlags().StringVar(&options.output, "output", "openapi.json", "generated OpenAPI JSON artifact")
	command.AddCommand(newOpenAPIBuildCommand(options), newOpenAPICheckCommand(options))
	return command
}

func newOpenAPIBuildCommand(options *openAPIOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Compile YAML sources into the reviewed JSON artifact",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			compiled, err := compileOpenAPI(options.source)
			if err != nil {
				return err
			}
			if err := writeFileAtomically(filepath.Clean(options.output), compiled); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "wrote %s from %s\n", options.output, options.source)
			return err
		},
	}
}

func newOpenAPICheckCommand(options *openAPIOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Validate sources and fail when the JSON artifact is stale",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			compiled, err := compileOpenAPI(options.source)
			if err != nil {
				return err
			}
			output := filepath.Clean(options.output)
			current, err := os.ReadFile(output)
			if err != nil {
				return fmt.Errorf("read generated OpenAPI artifact %s: %w", output, err)
			}
			if !bytes.Equal(current, compiled) {
				return fmt.Errorf("generated OpenAPI artifact %s is stale; run `go run ./cmd/ptool openapi build`", output)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "validated %s against %s\n", options.output, options.source)
			return err
		},
	}
}

func compileOpenAPI(source string) ([]byte, error) {
	source = filepath.Clean(source)
	compiled, err := openapidoc.Compile(os.DirFS(source))
	if err != nil {
		return nil, fmt.Errorf("compile OpenAPI source %s: %w", source, err)
	}
	return compiled, nil
}

func writeFileAtomically(name string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(name); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect OpenAPI artifact %s: %w", name, err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(name), ".ptool-openapi-*")
	if err != nil {
		return fmt.Errorf("create temporary OpenAPI artifact: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, name); err != nil {
		return fmt.Errorf("replace OpenAPI artifact %s: %w", name, err)
	}
	return nil
}
