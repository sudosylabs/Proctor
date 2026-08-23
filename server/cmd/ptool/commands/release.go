// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type archiveOptions struct {
	source string
	output string
	prefix string
	epoch  int64
}

func newReleaseCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "release",
		Short: "Build reproducible Proctor release artifacts",
	}
	command.AddCommand(newReleaseArchiveCommand())
	return command
}

func newReleaseArchiveCommand() *cobra.Command {
	options := &archiveOptions{}
	command := &cobra.Command{
		Use:   "archive",
		Short: "Create a deterministic tar.gz archive from a package directory",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			modifiedAt := time.Unix(options.epoch, 0).UTC()
			if err := writeDeterministicArchive(options.source, options.output, options.prefix, modifiedAt); err != nil {
				return err
			}
			_, err := fmt.Fprintln(command.OutOrStdout(), options.output)
			return err
		},
	}
	command.Flags().StringVar(&options.source, "source", "", "package directory to archive")
	command.Flags().StringVar(&options.output, "output", "", "archive path to create")
	command.Flags().StringVar(&options.prefix, "prefix", "", "top-level directory name in the archive")
	command.Flags().Int64Var(&options.epoch, "epoch", 0, "Unix timestamp used for every archive entry")
	_ = command.MarkFlagRequired("source")
	_ = command.MarkFlagRequired("output")
	_ = command.MarkFlagRequired("prefix")
	return command
}

func writeDeterministicArchive(source, output, prefix string, modifiedAt time.Time) (resultErr error) {
	source = filepath.Clean(source)
	output = filepath.Clean(output)
	prefix, err := cleanArchivePrefix(prefix)
	if err != nil {
		return err
	}
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("archive output %q already exists", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect archive output %q: %w", output, err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("inspect archive source %q: %w", source, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("archive source %q is not a directory", source)
	}

	entries := make([]string, 0)
	if err := filepath.WalkDir(source, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive source contains symbolic link %q", name)
		}
		if entry.IsDir() || entry.Type().IsRegular() {
			entries = append(entries, name)
			return nil
		}
		return fmt.Errorf("archive source contains unsupported file %q", name)
	}); err != nil {
		return fmt.Errorf("walk archive source %q: %w", source, err)
	}
	sort.Strings(entries)

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create archive output directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".proctor-release-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temporary archive: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if resultErr != nil {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("set archive permissions: %w", err)
	}

	gzipWriter, err := gzip.NewWriterLevel(temporary, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	gzipWriter.Header.ModTime = modifiedAt
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range entries {
		if err := writeArchiveEntry(tarWriter, source, name, prefix, modifiedAt); err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		return fmt.Errorf("close tar archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("close gzip archive: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	if err := os.Rename(temporaryName, output); err != nil {
		return fmt.Errorf("publish archive %q: %w", output, err)
	}
	return nil
}

func cleanArchivePrefix(prefix string) (string, error) {
	prefix = strings.TrimSpace(strings.ReplaceAll(prefix, "\\", "/"))
	cleaned := path.Clean(prefix)
	if prefix == "" || cleaned == "." || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive prefix %q must be a relative directory name", prefix)
	}
	return strings.TrimSuffix(cleaned, "/"), nil
}

func writeArchiveEntry(writer *tar.Writer, source, name, prefix string, modifiedAt time.Time) error {
	info, err := os.Stat(name)
	if err != nil {
		return fmt.Errorf("inspect archive entry %q: %w", name, err)
	}
	relative, err := filepath.Rel(source, name)
	if err != nil {
		return fmt.Errorf("resolve archive entry %q: %w", name, err)
	}
	archiveName := prefix
	if relative != "." {
		archiveName = path.Join(prefix, filepath.ToSlash(relative))
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("create archive header for %q: %w", name, err)
	}
	header.Name = archiveName
	header.Uid = 0
	header.Gid = 0
	header.Uname = "root"
	header.Gname = "root"
	header.ModTime = modifiedAt
	header.AccessTime = time.Time{}
	header.ChangeTime = time.Time{}
	header.PAXRecords = nil
	header.Xattrs = nil
	header.Format = tar.FormatPAX
	if info.IsDir() {
		header.Name += "/"
		header.Mode = 0o755
	} else if info.Mode()&0o111 != 0 {
		header.Mode = 0o755
	} else {
		header.Mode = 0o644
	}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write archive header for %q: %w", name, err)
	}
	if info.IsDir() {
		return nil
	}
	file, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("open archive entry %q: %w", name, err)
	}
	defer file.Close()
	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("write archive entry %q: %w", name, err)
	}
	return nil
}
