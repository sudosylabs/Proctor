// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package tlscert serves an operator-managed TLS key pair from files and
// replaces it when the files are rotated or a reload is requested. The main
// HTTP listener and the metrics listener share it, so both follow the same
// rotation and SIGHUP behavior.
package tlscert

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// CheckInterval bounds how often TLS handshakes stat the operator-managed
// certificate files for replacement.
const CheckInterval = 30 * time.Second

type certificateFileStamp struct {
	certificateModified, privateKeyModified time.Time
	certificateSize, privateKeySize         int64
}

// Static serves an operator-managed key pair and replaces it when both files
// change on disk or when Reload is requested. A replacement that fails to
// parse keeps the previously loaded certificate in service.
type Static struct {
	certificateFile string
	privateKeyFile  string
	interval        time.Duration
	now             func() time.Time
	reportFailure   func(error)

	current atomic.Pointer[tls.Certificate]

	mu        sync.Mutex
	stamp     certificateFileStamp
	checkedAt time.Time
}

// New returns a Static for the key pair files with nothing loaded yet; call
// Reload before serving. reportFailure, when set, receives each failed
// rotation check a handshake triggers.
func New(certificateFile, privateKeyFile string, reportFailure func(error)) *Static {
	return &Static{
		certificateFile: certificateFile, privateKeyFile: privateKeyFile,
		interval: CheckInterval, now: time.Now, reportFailure: reportFailure,
	}
}

// GetCertificate implements tls.Config.GetCertificate.
func (c *Static) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.refreshIfChanged()
	certificate := c.current.Load()
	if certificate == nil {
		return nil, errors.New("static TLS certificate is not loaded")
	}
	return certificate, nil
}

// Reload loads the configured key pair now. On failure the previously loaded
// certificate, if any, remains in service.
func (c *Static) Reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkedAt = c.now()
	stamp, err := c.statFiles()
	if err != nil {
		return err
	}
	return c.loadLocked(stamp)
}

func (c *Static) refreshIfChanged() {
	// A handshake never waits behind another handshake's file check.
	if !c.mu.TryLock() {
		return
	}
	defer c.mu.Unlock()
	now := c.now()
	if now.Sub(c.checkedAt) < c.interval {
		return
	}
	c.checkedAt = now
	stamp, err := c.statFiles()
	if err != nil || stamp == c.stamp {
		if err != nil && c.reportFailure != nil {
			c.reportFailure(err)
		}
		return
	}
	if err := c.loadLocked(stamp); err != nil && c.reportFailure != nil {
		c.reportFailure(err)
	}
}

func (c *Static) loadLocked(stamp certificateFileStamp) error {
	certificate, err := tls.LoadX509KeyPair(c.certificateFile, c.privateKeyFile)
	if err != nil {
		return fmt.Errorf("load static TLS certificate: %w", err)
	}
	c.current.Store(&certificate)
	c.stamp = stamp
	return nil
}

func (c *Static) statFiles() (certificateFileStamp, error) {
	certificate, err := os.Stat(c.certificateFile)
	if err != nil {
		return certificateFileStamp{}, fmt.Errorf("inspect static TLS certificate: %w", err)
	}
	privateKey, err := os.Stat(c.privateKeyFile)
	if err != nil {
		return certificateFileStamp{}, fmt.Errorf("inspect static TLS private key: %w", err)
	}
	return certificateFileStamp{
		certificateModified: certificate.ModTime(), privateKeyModified: privateKey.ModTime(),
		certificateSize: certificate.Size(), privateKeySize: privateKey.Size(),
	}, nil
}
