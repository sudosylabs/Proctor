// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package webui serves the immutable browser application packaged with one
// Proctor server build. It owns hosted-route fallback, cache policy, and the
// browser security response contract without depending on application state.
package webui

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	BuildManifestName = "webapp-build.json"
	buildManifestMax  = 4096
	indexMax          = 2 << 20
)

var fingerprintedAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

//go:embed hosted_routes.json
var hostedRoutesJSON []byte

var hostedRoutes = mustHostedRoutes(hostedRoutesJSON)

// HandlesPath reports whether path belongs to the packaged browser module.
// Root transport uses this exact ownership predicate so unknown server paths
// retain the API's Problem Details response instead of becoming an SPA fallback.
func HandlesPath(requestPath string) bool {
	if strings.HasPrefix(requestPath, "/assets/") {
		name := strings.TrimPrefix(requestPath, "/")
		return fs.ValidPath(name) && path.Clean(name) == name &&
			fingerprintedAsset.MatchString(path.Base(name))
	}
	_, ok := hostedRoutes[requestPath]
	return ok
}

type Options struct {
	BuildVersion string
	BuildCommit  string
}

type buildManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
}

type handler struct {
	files      fs.FS
	index      []byte
	fileServer http.Handler
}

// New validates and constructs the complete immutable webapp handler. Files
// must expose index.html, webapp-build.json, and any referenced assets at their
// distribution-root paths. Build version and commit must exactly match the Go
// server build; mismatch is a startup error rather than a mixed release.
func New(files fs.FS, options Options) (http.Handler, error) {
	if files == nil {
		return nil, errors.New("webapp files are required")
	}
	options.BuildVersion = strings.TrimSpace(options.BuildVersion)
	options.BuildCommit = strings.TrimSpace(options.BuildCommit)
	if options.BuildVersion == "" || options.BuildCommit == "" {
		return nil, errors.New("server build version and commit are required")
	}

	manifestData, err := readBoundedFile(files, BuildManifestName, buildManifestMax)
	if err != nil {
		return nil, fmt.Errorf("read webapp build manifest: %w", err)
	}
	var manifest buildManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode webapp build manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode webapp build manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return nil, fmt.Errorf("webapp build manifest schema %d is unsupported", manifest.SchemaVersion)
	}
	if manifest.Version != options.BuildVersion || manifest.Commit != options.BuildCommit {
		return nil, fmt.Errorf(
			"webapp build %q at %q does not match server build %q at %q",
			manifest.Version, manifest.Commit, options.BuildVersion, options.BuildCommit,
		)
	}

	index, err := readBoundedFile(files, "index.html", indexMax)
	if err != nil {
		return nil, fmt.Errorf("read webapp index: %w", err)
	}
	if len(bytes.TrimSpace(index)) == 0 {
		return nil, errors.New("webapp index is empty")
	}

	return &handler{files: files, index: index, fileServer: http.FileServer(http.FS(files))}, nil
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(writer.Header())
	writer.Header().Set("Cache-Control", "no-store")

	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	if strings.HasPrefix(request.URL.Path, "/assets/") {
		h.serveAsset(writer, request)
		return
	}
	if !HandlesPath(request.URL.Path) {
		http.NotFound(writer, request)
		return
	}

	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Length", strconv.Itoa(len(h.index)))
	if request.Method == http.MethodHead {
		writer.WriteHeader(http.StatusOK)
		return
	}
	_, _ = writer.Write(h.index)
}

func (h *handler) serveAsset(writer http.ResponseWriter, request *http.Request) {
	if !HandlesPath(request.URL.Path) {
		http.NotFound(writer, request)
		return
	}
	name := strings.TrimPrefix(request.URL.Path, "/")
	info, err := fs.Stat(h.files, name)
	if err != nil || info.IsDir() {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if mediaType := mime.TypeByExtension(path.Ext(name)); mediaType != "" {
		writer.Header().Set("Content-Type", mediaType)
	}
	h.fileServer.ServeHTTP(writer, request)
}

func mustHostedRoutes(data []byte) map[string]struct{} {
	var routes []string
	if err := json.Unmarshal(data, &routes); err != nil {
		panic(fmt.Sprintf("webui: decode hosted route catalog: %v", err))
	}
	result := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route == "/" || !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "/assets/") ||
			path.Clean(route) != route || strings.HasSuffix(route, "/") {
			panic(fmt.Sprintf("webui: invalid hosted route %q", route))
		}
		if _, exists := result[route]; exists {
			panic(fmt.Sprintf("webui: duplicate hosted route %q", route))
		}
		result[route] = struct{}{}
	}
	if len(result) == 0 {
		panic("webui: hosted route catalog is empty")
	}
	return result
}

func readBoundedFile(files fs.FS, name string, maximum int64) ([]byte, error) {
	file, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, maximum)
	}
	return data, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("contains more than one JSON value")
		}
		return err
	}
	return nil
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}
