// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
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

func (r BrowserSourceResetReason) IsValid() bool {
	return r == BrowserSourceResetCoordinatorRestarted || r == BrowserSourceResetSpoolUnavailable || r == BrowserSourceResetSourceCorrupt
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

func (r BrowserActivityBlockReason) IsValid() bool {
	switch r {
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

func (e BrowserActivityEvent) ValidateClientRecord() error {
	if e.Sequence < 1 || !securitySafeInt(e.Sequence) || !e.PolicyRevisionID.IsValid() || !securityInstant(e.ClientOccurredAt) || !e.ReceivedAt.IsZero() {
		return invalidModelError("BrowserActivityEvent.ValidateClientRecord", "browser_activity_event", "", "invalid Browser Activity event metadata", "")
	}
	if e.RedirectFromSequence != nil && (*e.RedirectFromSequence < 1 || *e.RedirectFromSequence >= e.Sequence) {
		return ErrDeliveryInvalid
	}
	if (e.Kind == BrowserActivityTopRedirect || e.BlockReason != nil && *e.BlockReason == BrowserBlockRedirectNotAllowed) && e.RedirectFromSequence == nil {
		return ErrDeliveryInvalid
	}
	if e.RedirectFromSequence != nil && e.Kind != BrowserActivityTopRedirect && e.Kind != BrowserActivityBlockedNavigation {
		return ErrDeliveryInvalid
	}
	switch e.Kind {
	case BrowserActivityOpened, BrowserActivityClosed:
		if e.Location != nil || e.MatchedRuleID != nil || e.BlockReason != nil {
			return invalidModelError("BrowserActivityEvent.ValidateClientRecord", "browser_activity_event", "", "browser open/close event contains navigation fields", "")
		}
	case BrowserActivityTopNavigation, BrowserActivityTopRedirect:
		if e.Location == nil || e.MatchedRuleID == nil || !validBrowserPolicyRuleID(*e.MatchedRuleID) || e.BlockReason != nil {
			return invalidModelError("BrowserActivityEvent.ValidateClientRecord", "browser_activity_event", "", "successful Browser Activity navigation is incomplete", "")
		}
		if err := e.Location.Validate(); err != nil {
			return err
		}
	case BrowserActivityBlockedNavigation:
		if e.Location == nil || e.BlockReason == nil || !e.BlockReason.IsValid() ||
			e.MatchedRuleID != nil && !validBrowserPolicyRuleID(*e.MatchedRuleID) {
			return invalidModelError("BrowserActivityEvent.ValidateClientRecord", "browser_activity_event", "", "blocked Browser Activity navigation is incomplete", "")
		}
		if (*e.BlockReason == BrowserBlockSchemeNotAllowed || *e.BlockReason == BrowserBlockInvalidURL) && e.MatchedRuleID != nil {
			return invalidModelError("BrowserActivityEvent.ValidateClientRecord", "browser_activity_event", "", "blocked Browser Activity location cannot claim a matched rule", "")
		}
		if err := e.Location.ValidateBlocked(*e.BlockReason); err != nil {
			return err
		}
	default:
		return invalidModelError("BrowserActivityEvent.ValidateClientRecord", "browser_activity_event", "", "invalid Browser Activity kind", "")
	}
	return nil
}

func (l BrowserLocation) Validate() error {
	if l.Scheme == "http" {
		return l.validateNetwork("http", "80")
	}
	return l.validateNetwork("https", "443")
}

// ValidateBlocked accepts only the reason-specific minimized location shapes.
// HTTPS policy failures retain their canonical network location. Plain HTTP
// retains the same bounded components with its default port removed. Other
// denied schemes retain the scheme only so file paths, script/data payloads,
// and custom-protocol data never cross the wire. An invalid URL has no safely
// parsed components and is represented by the all-empty location value.
func (l BrowserLocation) ValidateBlocked(reason BrowserActivityBlockReason) error {
	switch reason {
	case BrowserBlockInvalidURL:
		if l != (BrowserLocation{}) {
			return invalidModelError("BrowserLocation.ValidateBlocked", "browser_location", "", "invalid URL Browser Activity location retained components", "")
		}
		return nil
	case BrowserBlockSchemeNotAllowed:
		if len(l.Scheme) < 1 || len(l.Scheme) > BrowserActivityLocationSchemeMaximumBytes ||
			!browserActivitySchemePattern.MatchString(l.Scheme) || l.Scheme == "https" {
			return invalidModelError("BrowserLocation.ValidateBlocked", "browser_location", "", "invalid blocked Browser Activity scheme", "")
		}
		if l.Scheme == "http" {
			return l.validateNetwork("http", "80")
		}
		if l.Host != "" || l.Port != "" || l.Path != "" {
			return invalidModelError("BrowserLocation.ValidateBlocked", "browser_location", "", "blocked non-HTTP scheme retained unsafe location components", "")
		}
		return nil
	case BrowserBlockOriginNotAllowed, BrowserBlockPathNotAllowed, BrowserBlockRedirectNotAllowed:
		return l.Validate()
	default:
		return invalidModelError("BrowserLocation.ValidateBlocked", "browser_location", "", "invalid Browser Activity block reason", "")
	}
}

func (l BrowserLocation) validateNetwork(scheme, defaultPort string) error {
	if l.Scheme != scheme || l.Host == "" || strings.ToLower(l.Host) != l.Host ||
		strings.ContainsAny(l.Host, "\\/?#@") || len(l.Path) > browserLocationMaximumPathBytes {
		return invalidModelError("BrowserLocation.validateNetwork", "browser_location", "", "invalid minimized Browser Activity location", "")
	}
	parsed, err := url.Parse(scheme + "://" + browserHostPort(l.Host, l.Port))
	if err != nil {
		return invalidModelError("BrowserLocation.validateNetwork", "browser_location", "", "invalid minimized Browser Activity authority", "")
	}
	host, port, err := canonicalBrowserHostPortWithDefault(parsed, defaultPort)
	if err != nil || host != l.Host || port != l.Port {
		return invalidModelError("BrowserLocation.validateNetwork", "browser_location", "", "non-canonical Browser Activity authority", "")
	}
	canonicalPath, err := canonicalBrowserPath(l.Path)
	if err != nil || canonicalPath != l.Path {
		return invalidModelError("BrowserLocation.validateNetwork", "browser_location", "", "non-canonical Browser Activity path", "")
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

func (e BrowserActivityEvent) MarshalJSON() ([]byte, error) {
	if e.ValidateClientRecord() != nil {
		return nil, ErrDeliveryInvalid
	}
	return json.Marshal(browserActivityEventWire{Sequence: e.Sequence, Kind: e.Kind, PolicyRevisionID: e.PolicyRevisionID, ClientOccurredAt: securityJSONInstant{Time: e.ClientOccurredAt, spelling: e.clientOccurredAtSpelling}, Location: e.Location, MatchedRuleID: e.MatchedRuleID, BlockReason: e.BlockReason, RedirectFromSequence: e.RedirectFromSequence})
}
func (e *BrowserActivityEvent) UnmarshalJSON(raw []byte) error {
	var value browserActivityEventWire
	if e == nil || decodeClosedDeliveryDeclaration(raw, &value, BrowserActivityAppendMaximumBytes) != nil {
		return ErrDeliveryInvalid
	}
	candidate := BrowserActivityEvent{Sequence: value.Sequence, Kind: value.Kind, PolicyRevisionID: value.PolicyRevisionID, ClientOccurredAt: value.ClientOccurredAt.Time, clientOccurredAtSpelling: value.ClientOccurredAt.spelling, Location: value.Location, MatchedRuleID: value.MatchedRuleID, BlockReason: value.BlockReason, RedirectFromSequence: value.RedirectFromSequence}
	if candidate.ValidateClientRecord() != nil {
		return ErrDeliveryInvalid
	}
	*e = candidate
	return nil
}
func (e BrowserActivityEvent) Canonical() ([]byte, error) {
	if e.ValidateClientRecord() != nil {
		return nil, ErrDeliveryInvalid
	}
	return encodeCanonicalExamDocument(e)
}
func (e BrowserActivityEvent) Fingerprint() (string, error) {
	encoded, err := e.Canonical()
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
