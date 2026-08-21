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

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
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
	catalogs := flags.String("catalogs", "i18n", "directory containing locale catalogs")
	templates := flags.String("templates", "templates", "directory containing generated HTML and text templates")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*output) == "" {
		return errors.New("usage: mailpreview -output <directory>")
	}
	localizer, err := localization.New(os.DirFS(*catalogs), localization.EnglishLocale)
	if err != nil {
		return fmt.Errorf("construct localizer: %w", err)
	}
	renderer, err := appmail.NewRenderer(os.DirFS(*templates), localizer)
	if err != nil {
		return fmt.Errorf("construct mail renderer: %w", err)
	}
	sittingRenderer, ok := renderer.(appmail.SittingRenderer)
	if !ok {
		return errors.New("mail renderer does not support Sitting templates")
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return fmt.Errorf("create preview directory: %w", err)
	}

	var index strings.Builder
	index.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>Proctor mail previews</title></head><body>\n")
	index.WriteString("<h1>Proctor transactional-mail previews</h1>\n<ul>\n")
	for _, key := range model.AllMailTemplateKeys() {
		var message appmail.FrozenContent
		var renderErr error
		switch key {
		case model.MailTemplateIdentityPersonalAccessTokenCreated,
			model.MailTemplateIdentityPersonalAccessTokenEnabled,
			model.MailTemplateIdentityPersonalAccessTokenDisabled,
			model.MailTemplateIdentityPersonalAccessTokenRevoked:
			message, renderErr = renderer.RenderPersonalAccessTokenSecurityNotice(key, "en", appmail.PersonalAccessTokenDetails{
				Description: "Representative automation", ExpiresAt: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
				ActionAt: time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC), ActionCount: 2,
			})
		case model.MailTemplateExamManagerAdded,
			model.MailTemplateExamManagerRemoved,
			model.MailTemplateExamOwnershipTransferredToYou,
			model.MailTemplateExamOwnershipTransferredFromYou:
			relationship := "manager"
			if key == model.MailTemplateExamManagerRemoved {
				relationship = "no_longer_manager"
			} else if key == model.MailTemplateExamOwnershipTransferredToYou {
				relationship = "owner"
			}
			message, renderErr = renderer.RenderExamManagerNotice(key, "en", appmail.ExamManagerDetails{
				Title: "Representative programming exam", Relationship: relationship,
				ActionAt: time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC),
			})
		case model.MailTemplateExamSittingScheduled,
			model.MailTemplateExamSittingRescheduled,
			model.MailTemplateExamSittingCancelled,
			model.MailTemplateExamSittingAssignmentRemoved:
			message, renderErr = sittingRenderer.RenderSittingScheduleNotice(key, "en", appmail.SittingScheduleDetails{
				ExamTitle: "Representative programming exam", ClassDisplayName: "Year 2 · Class A",
				StartsAt: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
				EndsAt:   time.Date(2026, 9, 20, 11, 30, 0, 0, time.UTC),
			})
		case model.MailTemplateAcademicClassEnrolled,
			model.MailTemplateAcademicClassEnrollmentEnded,
			model.MailTemplateAcademicClassTransferred:
			details := appmail.ClassTransitionDetails{
				ClassDisplayName: "Year 2 · Class B",
				StartsAt:         time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
			}
			if key == model.MailTemplateAcademicClassEnrollmentEnded {
				details.EndsAt = time.Date(2026, 10, 20, 16, 30, 0, 0, time.UTC)
			} else if key == model.MailTemplateAcademicClassTransferred {
				details.PreviousClassDisplayName = "Year 2 · Class A"
			}
			message, renderErr = renderer.RenderClassTransitionNotice(key, "en", details)
		case model.MailTemplateExamSubmissionReceived, model.MailTemplateExamSubmissionAutomaticallySealed:
			message, renderErr = renderer.RenderSubmissionReceipt(key, "en", appmail.SubmissionReceiptDetails{
				ExamTitle: "Representative programming exam", SittingID: model.ExamSittingID(strings.Repeat("y", model.IdLength)),
				SubmissionID: model.SubmissionID(strings.Repeat("b", model.IdLength)),
				SealedAt:     time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)})
		case model.MailTemplateExamResultReleased:
			message, renderErr = renderer.RenderResultRelease(key, "en", appmail.ResultReleaseDetails{ExamTitle: "Representative programming exam",
				ReleasedAt: time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC)})
		default:
			message, renderErr = renderer.Render(key, "en", "https://proctor.example.test/representative#token=not-a-credential")
		}
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
