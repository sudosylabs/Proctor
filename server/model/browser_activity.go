// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	BrowserActivitySchemaVersion              = 1
	BrowserActivityAppendMaximumEvents        = 64
	BrowserActivityAppendMaximumBytes         = 256 * 1024
	BrowserActivityMaximumReorderWindow       = 4096
	BrowserActivityMaximumMissingRanges       = 32
	BrowserSourceMaximumPerParticipation      = 49
	BrowserActivityLocationSchemeMaximumBytes = 32
)

type BrowserSourceSessionID string

var (
	browserSourceSessionPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	browserActivitySchemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
)

func (id BrowserSourceSessionID) IsValid() bool {
	return browserSourceSessionPattern.MatchString(string(id))
}

type BrowserSourceResetReason string

const (
	BrowserSourceResetCoordinatorRestarted BrowserSourceResetReason = "coordinator_restarted"
	BrowserSourceResetSpoolUnavailable     BrowserSourceResetReason = "spool_unavailable"
	BrowserSourceResetSourceCorrupt        BrowserSourceResetReason = "source_corrupt"
)

func (reason BrowserSourceResetReason) IsValid() bool {
	return reason == BrowserSourceResetCoordinatorRestarted || reason == BrowserSourceResetSpoolUnavailable || reason == BrowserSourceResetSourceCorrupt
}

type BrowserActivityKind string

const (
	BrowserActivityOpened            BrowserActivityKind = "browser_opened"
	BrowserActivityClosed            BrowserActivityKind = "browser_closed"
	BrowserActivityTopNavigation     BrowserActivityKind = "top_level_navigation"
	BrowserActivityTopRedirect       BrowserActivityKind = "top_level_redirect"
	BrowserActivityBlockedNavigation BrowserActivityKind = "blocked_top_level_navigation"
)

type BrowserActivityBlockReason string

const (
	BrowserBlockSchemeNotAllowed   BrowserActivityBlockReason = "scheme_not_allowed"
	BrowserBlockOriginNotAllowed   BrowserActivityBlockReason = "origin_not_allowed"
	BrowserBlockPathNotAllowed     BrowserActivityBlockReason = "path_not_allowed"
	BrowserBlockRedirectNotAllowed BrowserActivityBlockReason = "redirect_not_allowed"
	BrowserBlockInvalidURL         BrowserActivityBlockReason = "invalid_url"
)

func (reason BrowserActivityBlockReason) IsValid() bool {
	switch reason {
	case BrowserBlockSchemeNotAllowed, BrowserBlockOriginNotAllowed, BrowserBlockPathNotAllowed, BrowserBlockRedirectNotAllowed, BrowserBlockInvalidURL:
		return true
	default:
		return false
	}
}

type BrowserActivityEvent struct {
	clientOccurredAtSpelling string
	RedirectFromSequence     *int64
	Sequence                 int64
	Kind                     BrowserActivityKind
	PolicyRevisionID         ExamRevisionID
	ClientOccurredAt         time.Time
	Location                 *BrowserLocation
	MatchedRuleID            *string
	BlockReason              *BrowserActivityBlockReason
	ReceivedAt               time.Time
}

func (event BrowserActivityEvent) ValidateClientRecord() error {
	if event.Sequence < 1 || !securitySafeInt(event.Sequence) || !event.PolicyRevisionID.IsValid() || !securityInstant(event.ClientOccurredAt) || !event.ReceivedAt.IsZero() {
		return errors.New("model: invalid Browser Activity event metadata")
	}
	if event.RedirectFromSequence != nil && (*event.RedirectFromSequence < 1 || *event.RedirectFromSequence >= event.Sequence) {
		return ErrDeliveryInvalid
	}
	if (event.Kind == BrowserActivityTopRedirect || event.BlockReason != nil && *event.BlockReason == BrowserBlockRedirectNotAllowed) && event.RedirectFromSequence == nil {
		return ErrDeliveryInvalid
	}
	if event.RedirectFromSequence != nil && event.Kind != BrowserActivityTopRedirect && event.Kind != BrowserActivityBlockedNavigation {
		return ErrDeliveryInvalid
	}
	switch event.Kind {
	case BrowserActivityOpened, BrowserActivityClosed:
		if event.Location != nil || event.MatchedRuleID != nil || event.BlockReason != nil {
			return errors.New("model: browser open/close event contains navigation fields")
		}
	case BrowserActivityTopNavigation, BrowserActivityTopRedirect:
		if event.Location == nil || event.MatchedRuleID == nil || !validBrowserPolicyRuleID(*event.MatchedRuleID) || event.BlockReason != nil {
			return errors.New("model: successful Browser Activity navigation is incomplete")
		}
		if err := event.Location.Validate(); err != nil {
			return err
		}
	case BrowserActivityBlockedNavigation:
		if event.Location == nil || event.BlockReason == nil || !event.BlockReason.IsValid() ||
			event.MatchedRuleID != nil && !validBrowserPolicyRuleID(*event.MatchedRuleID) {
			return errors.New("model: blocked Browser Activity navigation is incomplete")
		}
		if (*event.BlockReason == BrowserBlockSchemeNotAllowed || *event.BlockReason == BrowserBlockInvalidURL) && event.MatchedRuleID != nil {
			return errors.New("model: blocked Browser Activity location cannot claim a matched rule")
		}
		if err := event.Location.ValidateBlocked(*event.BlockReason); err != nil {
			return err
		}
	default:
		return errors.New("model: invalid Browser Activity kind")
	}
	return nil
}

func (location BrowserLocation) Validate() error {
	if location.Scheme == "http" {
		return location.validateNetwork("http", "80")
	}
	return location.validateNetwork("https", "443")
}

// ValidateBlocked accepts only the reason-specific minimized location shapes.
// HTTPS policy failures retain their canonical network location. Plain HTTP
// retains the same bounded components with its default port removed. Other
// denied schemes retain the scheme only so file paths, script/data payloads,
// and custom-protocol data never cross the wire. An invalid URL has no safely
// parsed components and is represented by the all-empty location value.
func (location BrowserLocation) ValidateBlocked(reason BrowserActivityBlockReason) error {
	switch reason {
	case BrowserBlockInvalidURL:
		if location != (BrowserLocation{}) {
			return errors.New("model: invalid URL Browser Activity location retained components")
		}
		return nil
	case BrowserBlockSchemeNotAllowed:
		if len(location.Scheme) < 1 || len(location.Scheme) > BrowserActivityLocationSchemeMaximumBytes ||
			!browserActivitySchemePattern.MatchString(location.Scheme) || location.Scheme == "https" {
			return errors.New("model: invalid blocked Browser Activity scheme")
		}
		if location.Scheme == "http" {
			return location.validateNetwork("http", "80")
		}
		if location.Host != "" || location.Port != "" || location.Path != "" {
			return errors.New("model: blocked non-HTTP scheme retained unsafe location components")
		}
		return nil
	case BrowserBlockOriginNotAllowed, BrowserBlockPathNotAllowed, BrowserBlockRedirectNotAllowed:
		return location.Validate()
	default:
		return errors.New("model: invalid Browser Activity block reason")
	}
}

func (location BrowserLocation) validateNetwork(scheme, defaultPort string) error {
	if location.Scheme != scheme || location.Host == "" || strings.ToLower(location.Host) != location.Host ||
		strings.ContainsAny(location.Host, "\\/?#@") || len(location.Path) > browserLocationMaximumPathBytes {
		return errors.New("model: invalid minimized Browser Activity location")
	}
	parsed, err := url.Parse(scheme + "://" + browserHostPort(location.Host, location.Port))
	if err != nil {
		return errors.New("model: invalid minimized Browser Activity authority")
	}
	host, port, err := canonicalBrowserHostPortWithDefault(parsed, defaultPort)
	if err != nil || host != location.Host || port != location.Port {
		return errors.New("model: non-canonical Browser Activity authority")
	}
	canonicalPath, err := canonicalBrowserPath(location.Path)
	if err != nil || canonicalPath != location.Path {
		return errors.New("model: non-canonical Browser Activity path")
	}
	return nil
}

type browserActivityEventWire struct {
	Sequence             int64                       `json:"sequence"`
	Kind                 BrowserActivityKind         `json:"kind"`
	PolicyRevisionID     ExamRevisionID              `json:"policy_revision_id"`
	ClientOccurredAt     securityJSONInstant         `json:"client_occurred_at"`
	Location             *BrowserLocation            `json:"location,omitempty"`
	MatchedRuleID        *string                     `json:"matched_rule_id,omitempty"`
	BlockReason          *BrowserActivityBlockReason `json:"block_reason,omitempty"`
	RedirectFromSequence *int64                      `json:"redirect_from_sequence,omitempty"`
}

func (event BrowserActivityEvent) MarshalJSON() ([]byte, error) {
	if event.ValidateClientRecord() != nil {
		return nil, ErrDeliveryInvalid
	}
	return json.Marshal(browserActivityEventWire{Sequence: event.Sequence, Kind: event.Kind, PolicyRevisionID: event.PolicyRevisionID, ClientOccurredAt: securityJSONInstant{Time: event.ClientOccurredAt, spelling: event.clientOccurredAtSpelling}, Location: event.Location, MatchedRuleID: event.MatchedRuleID, BlockReason: event.BlockReason, RedirectFromSequence: event.RedirectFromSequence})
}
func (event *BrowserActivityEvent) UnmarshalJSON(raw []byte) error {
	var value browserActivityEventWire
	if event == nil || decodeClosedDeliveryDeclaration(raw, &value, BrowserActivityAppendMaximumBytes) != nil {
		return ErrDeliveryInvalid
	}
	candidate := BrowserActivityEvent{Sequence: value.Sequence, Kind: value.Kind, PolicyRevisionID: value.PolicyRevisionID, ClientOccurredAt: value.ClientOccurredAt.Time, clientOccurredAtSpelling: value.ClientOccurredAt.spelling, Location: value.Location, MatchedRuleID: value.MatchedRuleID, BlockReason: value.BlockReason, RedirectFromSequence: value.RedirectFromSequence}
	if candidate.ValidateClientRecord() != nil {
		return ErrDeliveryInvalid
	}
	*event = candidate
	return nil
}
func (event BrowserActivityEvent) Canonical() ([]byte, error) {
	if event.ValidateClientRecord() != nil {
		return nil, ErrDeliveryInvalid
	}
	return encodeCanonicalExamDocument(event)
}
func (event BrowserActivityEvent) Fingerprint() (string, error) {
	encoded, err := event.Canonical()
	if err != nil {
		return "", err
	}
	return SHA256Fingerprint(encoded), nil
}

type BrowserActivityMissingRange struct {
	First int64
	Last  int64
}

type BrowserActivityAcknowledgement struct {
	Receipts               []BrowserEventReceipt
	SettledThrough         int64
	AllocatedThrough       int64
	TerminalMissingThrough int64
	SourceSessionID        BrowserSourceSessionID
	HighestContiguous      int64
	HighestSeen            int64
	MissingRanges          []BrowserActivityMissingRange
	MissingRangesTruncated bool
	ServerTime             time.Time
	ExamID                 ExamID
	SittingID              ExamSittingID
	GapAttentionChanged    bool
}
