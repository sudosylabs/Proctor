// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package executionhost adapts the reusable execenv remote client to the
// application-owned execution ports and owns all host connection lifecycle.
package executionhost

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/sudosylabs/execenv"
	"github.com/sudosylabs/execenv/remote"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
)

type HostConfig struct {
	ID                    string
	Address               string
	Security              string
	Token                 string
	ServerName            string
	CAFile                string
	ClientCertificateFile string
	ClientKeyFile         string
}

type Settings struct {
	Enabled          bool
	DialTimeout      time.Duration
	OperationTimeout time.Duration
	Hosts            []HostConfig
}

type Directory struct {
	enabled bool
	hosts   map[string]*hostClient
	ids     []string
	close   sync.Once
}

type hostClient struct {
	id     string
	config remote.Config
	mu     sync.Mutex
	client *remote.Client
}

func New(settings Settings) (*Directory, error) {
	directory := &Directory{enabled: settings.Enabled, hosts: make(map[string]*hostClient, len(settings.Hosts))}
	if !settings.Enabled {
		return directory, nil
	}
	for _, configured := range settings.Hosts {
		remoteConfig, err := remoteConfig(configured, settings.DialTimeout, settings.OperationTimeout)
		if err != nil {
			_ = directory.Close()
			return nil, fmt.Errorf("configure execution host %q: %w", configured.ID, err)
		}
		directory.ids = append(directory.ids, configured.ID)
		directory.hosts[configured.ID] = &hostClient{id: configured.ID, config: remoteConfig}
	}
	sort.Strings(directory.ids)
	return directory, nil
}

func (directory *Directory) Catalog(ctx context.Context) ([]appexecution.HostStatus, error) {
	if directory == nil || !directory.enabled {
		return []appexecution.HostStatus{}, nil
	}
	result := make([]appexecution.HostStatus, len(directory.ids))
	var wait sync.WaitGroup
	for index, id := range directory.ids {
		wait.Add(1)
		go func() {
			defer wait.Done()
			report, err := directory.hosts[id].ready(ctx)
			if err != nil {
				result[index] = appexecution.HostStatus{ID: id}
				return
			}
			result[index] = report
		}()
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (directory *Directory) Ensure(ctx context.Context, hostID string, spec appexecution.Spec) (appexecution.Environment, error) {
	host := directory.hosts[hostID]
	if !directory.enabled || host == nil {
		return nil, appexecution.ErrUnavailable
	}
	native := execenv.Spec{ID: execenv.ID(spec.ID), Image: execenv.Image(spec.Image), Network: nativeNetwork(spec.Network)}
	if _, err := host.ensure(ctx, native); err != nil {
		return nil, err
	}
	return &environment{host: host, spec: native}, nil
}

func (directory *Directory) Revoke(ctx context.Context, hostID, grantID string) error {
	host := directory.hosts[hostID]
	if !directory.enabled || host == nil {
		return appexecution.ErrUnavailable
	}
	return host.revoke(ctx, execenv.ID(grantID))
}

// Check fails closed when execution is enabled without at least one usable,
// isolated host. A disabled directory is a healthy inert dependency.
func (directory *Directory) Check(ctx context.Context) error {
	_, err := directory.CheckCatalog(ctx)
	return err
}

// CheckCatalog performs the health check and returns the exact catalog used
// for the decision. Runtime decorators use this to publish a consistent host
// snapshot without issuing a second remote probe.
func (directory *Directory) CheckCatalog(ctx context.Context) ([]appexecution.HostStatus, error) {
	if directory == nil || !directory.enabled {
		return []appexecution.HostStatus{}, nil
	}
	catalog, err := directory.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	for _, host := range catalog {
		if host.Usable && host.Isolated {
			return catalog, nil
		}
	}
	return catalog, errors.New("no usable isolated execution host")
}

func (directory *Directory) Close() error {
	if directory == nil {
		return nil
	}
	var joined error
	directory.close.Do(func() {
		for _, id := range directory.ids {
			joined = errors.Join(joined, directory.hosts[id].close())
		}
	})
	return joined
}

func (host *hostClient) ready(ctx context.Context) (appexecution.HostStatus, error) {
	var report execenv.Report
	err := host.call(ctx, func(client *remote.Client) error {
		var err error
		report, err = client.Ready(ctx)
		return err
	})
	if err != nil {
		return appexecution.HostStatus{}, err
	}
	capabilities := host.capabilities()
	status := appexecution.HostStatus{ID: host.id, Usable: report.Usable, Isolated: capabilities.Isolated,
		Freeze: capabilities.Freeze, Slots: report.Slots, Release: report.Release}
	for _, image := range report.Images {
		status.Images = append(status.Images, string(image))
	}
	for _, network := range report.Networks {
		status.Networks = append(status.Networks, applicationNetwork(network))
	}
	return status, nil
}

func (host *hostClient) ensure(ctx context.Context, spec execenv.Spec) (execenv.Env, error) {
	var result execenv.Env
	err := host.call(ctx, func(client *remote.Client) error {
		var err error
		result, err = client.Ensure(ctx, spec)
		return err
	})
	return result, err
}

func (host *hostClient) revoke(ctx context.Context, id execenv.ID) error {
	return host.call(ctx, func(client *remote.Client) error { return client.Revoke(ctx, id) })
}

func (host *hostClient) capabilities() execenv.Capabilities {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.client == nil {
		return execenv.Capabilities{Isolated: true, Freeze: true}
	}
	return host.client.Capabilities()
}

func (host *hostClient) call(ctx context.Context, operation func(*remote.Client) error) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if host.client == nil {
			client, err := remote.New(host.config)
			if err != nil {
				return translate(err)
			}
			host.client = client
		}
		err := operation(host.client)
		if !errors.Is(err, execenv.ErrConnection) {
			return translate(err)
		}
		_ = host.client.Close()
		host.client = nil
	}
	return appexecution.ErrUnavailable
}

func (host *hostClient) close() error {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.client == nil {
		return nil
	}
	err := host.client.Close()
	host.client = nil
	return err
}

type environment struct {
	host *hostClient
	spec execenv.Spec
}

func (environment *environment) withEnvironment(ctx context.Context, operation func(execenv.Env) error) error {
	return environment.host.call(ctx, func(client *remote.Client) error {
		native, err := client.Ensure(ctx, environment.spec)
		if err != nil {
			return err
		}
		return operation(native)
	})
}

func (environment *environment) ReplaceTree(ctx context.Context, tree appexecution.Tree) error {
	native := make(execenv.Tree, len(tree))
	for index, node := range tree {
		native[index] = execenv.Node{Path: node.Path, Kind: nativeNodeKind(node.Kind),
			Version: execenv.Version(node.Version), Data: append([]byte(nil), node.Data...)}
	}
	return environment.withEnvironment(ctx, func(env execenv.Env) error { return env.ReplaceTree(ctx, native) })
}

func (environment *environment) Apply(ctx context.Context, mutations []appexecution.Mutation) error {
	native := make([]execenv.Mutation, len(mutations))
	for index, mutation := range mutations {
		native[index] = execenv.Mutation{Op: execenv.Op(mutation.Operation), Path: mutation.Path, From: mutation.From,
			Kind: nativeNodeKind(mutation.Kind), Version: execenv.Version(mutation.Version), Data: append([]byte(nil), mutation.Data...)}
	}
	return environment.withEnvironment(ctx, func(env execenv.Env) error { return env.Apply(ctx, execenv.Batch{Mutations: native}) })
}

func (environment *environment) Attach(ctx context.Context, window appexecution.Window) (appexecution.Terminal, error) {
	var result execenv.Terminal
	err := environment.withEnvironment(ctx, func(env execenv.Env) error {
		var err error
		result, err = env.Attach(ctx, execenv.Window{Cols: window.Cols, Rows: window.Rows})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &terminal{Terminal: result}, nil
}

func (environment *environment) Watch(ctx context.Context, after appexecution.Cursor) (appexecution.Observation, error) {
	var result execenv.Observation
	err := environment.withEnvironment(ctx, func(env execenv.Env) error {
		var err error
		result, err = env.Watch(ctx, execenv.Cursor(after))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &observation{Observation: result}, nil
}

func (environment *environment) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	var result io.ReadCloser
	err := environment.withEnvironment(ctx, func(env execenv.Env) error {
		var err error
		result, err = env.Open(ctx, path)
		return err
	})
	return result, err
}

func (environment *environment) Freeze(ctx context.Context) error {
	return environment.withEnvironment(ctx, func(env execenv.Env) error { return env.Freeze(ctx) })
}

func (environment *environment) Thaw(ctx context.Context) error {
	return environment.withEnvironment(ctx, func(env execenv.Env) error { return env.Thaw(ctx) })
}

type terminal struct{ execenv.Terminal }

func (terminal *terminal) Resize(ctx context.Context, window appexecution.Window) error {
	return translate(terminal.Terminal.Resize(ctx, execenv.Window{Cols: window.Cols, Rows: window.Rows}))
}

type observation struct{ execenv.Observation }

func (observation *observation) Cursor() appexecution.Cursor {
	return appexecution.Cursor(observation.Observation.Cursor())
}

func (observation *observation) Next(ctx context.Context) (appexecution.Event, error) {
	event, err := observation.Observation.Next(ctx)
	if err != nil {
		return appexecution.Event{}, translate(err)
	}
	return appexecution.Event{Cursor: appexecution.Cursor(event.Cursor), Operation: appexecution.Operation(event.Op), Path: event.Path, From: event.From}, nil
}

func remoteConfig(config HostConfig, dialTimeout, operationTimeout time.Duration) (remote.Config, error) {
	result := remote.Config{Address: config.Address, ServerName: config.ServerName, Token: []byte(config.Token),
		Timeout: dialTimeout, OperationTimeout: operationTimeout}
	switch config.Security {
	case "insecure_local":
		result.Security = remote.SecurityInsecureLocal
	case "tls":
		result.Security = remote.SecurityTLS
		tlsConfig := &tls.Config{ServerName: config.ServerName, MinVersion: tls.VersionTLS13}
		if config.CAFile != "" {
			contents, err := os.ReadFile(config.CAFile)
			if err != nil {
				return remote.Config{}, fmt.Errorf("read CA file: %w", err)
			}
			roots, err := x509.SystemCertPool()
			if err != nil || roots == nil {
				roots = x509.NewCertPool()
			}
			if !roots.AppendCertsFromPEM(contents) {
				return remote.Config{}, errors.New("CA file contains no certificates")
			}
			tlsConfig.RootCAs = roots
		}
		if config.ClientCertificateFile != "" {
			certificate, err := tls.LoadX509KeyPair(config.ClientCertificateFile, config.ClientKeyFile)
			if err != nil {
				return remote.Config{}, fmt.Errorf("load client certificate: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{certificate}
		}
		result.TLS = tlsConfig
	default:
		return remote.Config{}, errors.New("unknown security mode")
	}
	return result, nil
}

func nativeNetwork(network appexecution.Network) execenv.Network {
	if network == appexecution.NetworkAllowlist {
		return execenv.NetworkAllowlist
	}
	return execenv.NetworkNone
}

func applicationNetwork(network execenv.Network) appexecution.Network {
	if network == execenv.NetworkAllowlist {
		return appexecution.NetworkAllowlist
	}
	return appexecution.NetworkNone
}

func nativeNodeKind(kind appexecution.NodeKind) execenv.NodeKind {
	if kind == appexecution.NodeDirectory {
		return execenv.KindDirectory
	}
	return execenv.KindFile
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, execenv.ErrCapacity):
		return fmt.Errorf("%w: %v", appexecution.ErrCapacity, err)
	case errors.Is(err, execenv.ErrConflict):
		return fmt.Errorf("%w: %v", appexecution.ErrConflict, err)
	case errors.Is(err, execenv.ErrInvalid), errors.Is(err, execenv.ErrTooLarge), errors.Is(err, execenv.ErrUnknownImage), errors.Is(err, execenv.ErrNetwork):
		return fmt.Errorf("%w: %v", appexecution.ErrInvalid, err)
	case errors.Is(err, execenv.ErrRevoked):
		return fmt.Errorf("%w: %v", appexecution.ErrRevoked, err)
	case errors.Is(err, execenv.ErrNotFound):
		return fmt.Errorf("%w: %v", appexecution.ErrNotFound, err)
	case errors.Is(err, execenv.ErrLagged):
		return fmt.Errorf("%w: %v", appexecution.ErrObservationLost, err)
	case errors.Is(err, execenv.ErrUnavailable), errors.Is(err, execenv.ErrConnection), errors.Is(err, execenv.ErrClosed):
		return fmt.Errorf("%w: %v", appexecution.ErrUnavailable, err)
	default:
		return err
	}
}

var _ appexecution.HostDirectory = (*Directory)(nil)
