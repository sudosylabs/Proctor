// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Command mailpreview renders deterministic representative transactional mail.
package main

import (
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	mailtemplates "github.com/sudosylabs/proctor/server/templates"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("mailpreview", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "directory that will receive deterministic previews")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*output) == "" {
		return errors.New("usage: mailpreview -output <directory>")
	}
	renderer, err := mailtemplates.DefaultRenderer()
	if err != nil {
		return fmt.Errorf("construct mail renderer: %w", err)
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return fmt.Errorf("create preview directory: %w", err)
	}

	var index strings.Builder
	index.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>Proctor mail previews</title></head><body>\n")
	index.WriteString("<h1>Proctor transactional-mail previews</h1>\n<ul>\n")
	for _, key := range mailtemplates.AllKeys() {
		request := mailtemplates.Request{
			Key: key, RecipientLocale: "en", InstallationLocale: "en",
			ActionURL: "https://proctor.example.test/representative#token=not-a-credential",
		}
		switch key {
		case mailtemplates.IdentityPersonalAccessTokenCreated,
			mailtemplates.IdentityPersonalAccessTokenEnabled,
			mailtemplates.IdentityPersonalAccessTokenDisabled,
			mailtemplates.IdentityPersonalAccessTokenRevoked:
			request.PersonalAccessToken = &mailtemplates.PersonalAccessTokenDetails{
				Description: "Representative automation", ExpiresAt: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
				ActionAt: time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC), ActionCount: 2,
			}
		case mailtemplates.ExamManagerAdded,
			mailtemplates.ExamManagerRemoved,
			mailtemplates.ExamOwnershipTransferredToYou,
			mailtemplates.ExamOwnershipTransferredFromYou:
			relationship := mailtemplates.ExamManagerRelationshipManager
			if key == mailtemplates.ExamManagerRemoved {
				relationship = mailtemplates.ExamManagerRelationshipNoLongerManager
			} else if key == mailtemplates.ExamOwnershipTransferredToYou {
				relationship = mailtemplates.ExamManagerRelationshipOwner
			}
			request.ExamManager = &mailtemplates.ExamManagerDetails{
				Title: "Representative programming exam", Relationship: relationship,
				ActionAt: time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC),
			}
		case mailtemplates.ExamSittingScheduled,
			mailtemplates.ExamSittingRescheduled,
			mailtemplates.ExamSittingCancelled,
			mailtemplates.ExamSittingAssignmentRemoved:
			request.SittingSchedule = &mailtemplates.SittingScheduleDetails{
				ExamTitle: "Representative programming exam", ClassDisplayName: "Year 2 · Class A",
				StartsAt: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
				EndsAt:   time.Date(2026, 9, 20, 11, 30, 0, 0, time.UTC),
			}
		case mailtemplates.AcademicClassEnrolled,
			mailtemplates.AcademicClassEnrollmentEnded,
			mailtemplates.AcademicClassTransferred:
			details := &mailtemplates.ClassTransitionDetails{
				ClassDisplayName: "Year 2 · Class B",
				StartsAt:         time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
			}
			if key == mailtemplates.AcademicClassEnrollmentEnded {
				details.EndsAt = time.Date(2026, 10, 20, 16, 30, 0, 0, time.UTC)
			} else if key == mailtemplates.AcademicClassTransferred {
				details.PreviousClassDisplayName = "Year 2 · Class A"
			}
			request.ClassTransition = details
		case mailtemplates.ExamSubmissionReceived, mailtemplates.ExamSubmissionAutomaticallySealed:
			request.SubmissionReceipt = &mailtemplates.SubmissionReceiptDetails{ExamTitle: "Representative programming exam",
				SittingID: "sitting-safe-id", SubmissionID: "submission-safe-id",
				SealedAt: time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)}
		case mailtemplates.ExamResultReleased:
			request.ResultRelease = &mailtemplates.ResultReleaseDetails{ExamTitle: "Representative programming exam",
				ReleasedAt: time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC)}
		}
		message, renderErr := renderer.Render(request)
		if renderErr != nil {
			return fmt.Errorf("render preview %q: %w", key, renderErr)
		}
		base := string(key)
		if err := os.WriteFile(filepath.Join(*output, base+".html"), []byte(message.HTML), 0o644); err != nil {
			return fmt.Errorf("write HTML preview %q: %w", key, err)
		}
		if err := os.WriteFile(filepath.Join(*output, base+".txt"), []byte(message.Text), 0o644); err != nil {
			return fmt.Errorf("write text preview %q: %w", key, err)
		}
		fmt.Fprintf(&index, "<li><a href=\"%s.html\">%s</a> — <a href=\"%s.txt\">text</a></li>\n",
			html.EscapeString(base), html.EscapeString(message.Subject), html.EscapeString(base))
	}
	index.WriteString("</ul>\n</body></html>\n")
	if err := os.WriteFile(filepath.Join(*output, "index.html"), []byte(index.String()), 0o644); err != nil {
		return fmt.Errorf("write preview index: %w", err)
	}
	return nil
}
