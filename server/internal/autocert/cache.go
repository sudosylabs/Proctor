// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package autocert

import (
	"context"
	"os"
	"path/filepath"
)

// ErrCacheMiss is returned when a certificate is not found in cache.
const ErrCacheMiss cacheMissError = "acme/autocert: certificate cache miss"

type cacheMissError string

func (e cacheMissError) Error() string { return string(e) }

// Cache is used by Manager to store and retrieve previously obtained certificates
// and other account data as opaque blobs.
//
// Cache implementations should not rely on the key naming pattern. Keys can
// include any printable ASCII characters, except the following: \/:*?"<>|
type Cache interface {
	// Get returns a certificate data for the specified key.
	// If there's no such key, Get returns ErrCacheMiss.
	Get(ctx context.Context, key string) ([]byte, error)

	// Put stores the data in the cache under the specified key.
	// Underlying implementations may use any data storage format,
	// as long as the reverse operation, Get, results in the original data.
	Put(ctx context.Context, key string, data []byte) error

	// Delete removes a certificate data from the cache under the specified key.
	// If there's no such key in the cache, Delete returns nil.
	Delete(ctx context.Context, key string) error
}

// DirCache implements Cache using a directory on the local filesystem.
// If the directory does not exist, it will be created with 0700 permissions.
type DirCache string

// Get reads a certificate data from the specified file name.
func (d DirCache) Get(ctx context.Context, name string) ([]byte, error) {
	name = filepath.Join(string(d), filepath.Clean("/"+name))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return nil, ErrCacheMiss
	}
	return data, err
}

// Put writes the certificate data to the specified file name.
// The file will be created with 0600 permissions.
func (d DirCache) Put(ctx context.Context, name string, data []byte) error {
	if err := os.MkdirAll(string(d), 0700); err != nil {
		return err
	}

	tmp, err := d.writeTempFile(name, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := ctx.Err(); err != nil {
		return err
	}
	newName := filepath.Join(string(d), filepath.Clean("/"+name))
	return os.Rename(tmp, newName)
}

// Delete removes the specified file name.
func (d DirCache) Delete(ctx context.Context, name string) error {
	name = filepath.Join(string(d), filepath.Clean("/"+name))
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(name)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeTempFile writes b to a temporary file, closes the file and returns its path.
func (d DirCache) writeTempFile(prefix string, b []byte) (name string, reterr error) {
	// TempFile uses 0600 permissions
	f, err := os.CreateTemp(string(d), prefix)
	if err != nil {
		return "", err
	}
	defer func() {
		if reterr != nil {
			os.Remove(f.Name())
		}
	}()
	if _, err := f.Write(b); err != nil {
		f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}
