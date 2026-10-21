// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sudosylabs/proctor/server/internal/errorcatalog"
)

type errorCatalogOptions struct {
	catalog string
	mapping string
	module  string
}

func newErrorsCommand() *cobra.Command {
	options := &errorCatalogOptions{}
	command := &cobra.Command{
		Use:   "errors",
		Short: "Generate and verify the public error code catalog",
	}
	command.PersistentFlags().StringVar(&options.catalog, "catalog", "httpapi/error_catalog.yaml", "human-authored error code catalog")
	command.PersistentFlags().StringVar(&options.mapping, "mapping", "httpapi/error_mapping.go", "generated HTTP status mapping")
	command.PersistentFlags().StringVar(&options.module, "module", ".", "server module directory scanned for minted codes")
	command.AddCommand(newErrorsGenerateCommand(options), newErrorsCheckCommand(options))
	return command
}

func newErrorsGenerateCommand(options *errorCatalogOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Render the HTTP status mapping from the catalog",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			catalog, err := readErrorCatalog(options.catalog)
			if err != nil {
				return err
			}
			if err = writeFileAtomically(filepath.Clean(options.mapping), catalog.RenderHTTPMapping()); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "wrote %s from %s\n", options.mapping, options.catalog)
			return err
		},
	}
}

func newErrorsCheckCommand(options *errorCatalogOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Fail when the mapping is stale, source mints codes the catalog does not list, or a Store table maps an unproduced constraint",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			catalog, err := readErrorCatalog(options.catalog)
			if err != nil {
				return err
			}
			current, err := os.ReadFile(filepath.Clean(options.mapping))
			if err != nil {
				return fmt.Errorf("read generated error mapping %s: %w", options.mapping, err)
			}
			if !bytes.Equal(current, catalog.RenderHTTPMapping()) {
				return fmt.Errorf("generated error mapping %s is stale; run `go run ./cmd/ptool errors generate`", options.mapping)
			}
			mints, err := scanMintedErrorCodes(command.Context(), options.module)
			if err != nil {
				return err
			}
			if err = catalog.Verify(mints); err != nil {
				return err
			}
			if err = verifyStoreTableConstraints(options.module); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "verified %d catalog codes against %d minted constants\n", len(catalog.Entries), len(mints))
			return err
		},
	}
}

func readErrorCatalog(path string) (*errorcatalog.Catalog, error) {
	source, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read error catalog %s: %w", path, err)
	}
	return errorcatalog.Parse(source)
}
