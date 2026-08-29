// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/sha256"
	"encoding/hex"
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
	BrowserSourceMaximumPerParticipation      = 16
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
	Sequence         int64
	Kind             BrowserActivityKind
	PolicyRevisionID ExamRevisionID
	ClientOccurredAt time.Time
	Location         *BrowserLocation
	MatchedRuleID    *string
	BlockReason      *BrowserActivityBlockReason
	ReceivedAt       time.Time
}

func (event BrowserActivityEvent) ValidateClientRecord() error {
	if event.Sequence < 1 || !event.PolicyRevisionID.IsValid() || event.ClientOccurredAt.IsZero() || !event.ReceivedAt.IsZero() {
		return errors.New("model: invalid Browser Activity event metadata")
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
	if location.Scheme != scheme || location.Host == "" || strings.ToLower(location.Host) != location.Host || strings.ContainsAny(location.Host, "\\/?#@") {
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
	canonicalPath, err := CanonicalizeBrowserPolicyPath(location.Path)
	if err != nil || canonicalPath != location.Path {
		return errors.New("model: non-canonical Browser Activity path")
	}
	return nil
}

func (event BrowserActivityEvent) Fingerprint() (string, error) {
	if err := event.ValidateClientRecord(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion    int                         `json:"schema_version"`
		Sequence         int64                       `json:"sequence"`
		Kind             BrowserActivityKind         `json:"kind"`
		PolicyRevisionID string                      `json:"policy_revision_id"`
		ClientOccurredAt string                      `json:"client_occurred_at"`
		Location         *BrowserLocation            `json:"location"`
		MatchedRuleID    *string                     `json:"matched_rule_id"`
		BlockReason      *BrowserActivityBlockReason `json:"block_reason"`
	}{BrowserActivitySchemaVersion, event.Sequence, event.Kind, event.PolicyRevisionID.String(),
		TimeUTC(event.ClientOccurredAt).Format(time.RFC3339Nano), event.Location, event.MatchedRuleID, event.BlockReason})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type BrowserActivityMissingRange struct {
	First int64
	Last  int64
}

type BrowserActivityAcknowledgement struct {
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
