// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"fmt"
)

type CacheRedis struct {
	Addresses      []string `json:"Addresses"`
	Username       string   `json:"Username"`
	Password       string   `json:"Password"`
	Database       int      `json:"Database"`
	TLS            bool     `json:"TLS"`
	ConnectTimeout Duration `json:"ConnectTimeout"`
}

// CacheMemory bounds the process-local disposable cache. MaxBytes accounts for
// retained key and encoded-value bytes; implementation overhead is additional.
// MaxCounters separately bounds the atomic counters that hold authentication
// rate-limit windows: they are never evicted, so when every slot is live a new
// counter is refused and the guarded action fails closed.
type CacheMemory struct {
	MaxEntries  int   `json:"MaxEntries"`
	MaxBytes    int64 `json:"MaxBytes"`
	MaxCounters int   `json:"MaxCounters"`
}

type Cache struct {
	Backend   string      `json:"Backend"`
	Namespace string      `json:"Namespace"`
	Memory    CacheMemory `json:"Memory"`
	Redis     CacheRedis  `json:"Redis"`
}

func validateNamespace(field, namespace string, add func(string, string)) {
	if len(namespace) == 0 || len(namespace) > 128 {
		add(field, "must contain between 1 and 128 characters")
		return
	}
	for _, character := range namespace {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			add(field, "contains an invalid character")
			return
		}
	}
}

func validateCache(cache Cache, add func(string, string)) {
	switch cache.Backend {
	case "memory":
		if cache.Memory.MaxEntries < 1 || cache.Memory.MaxEntries > 1_000_000 {
			add("cache.memory.max_entries", "must be between 1 and 1000000")
		}
		if cache.Memory.MaxBytes < 1<<20 || cache.Memory.MaxBytes > 1<<30 {
			add("cache.memory.max_bytes", "must be between 1048576 and 1073741824")
		}
		if cache.Memory.MaxCounters < 1 || cache.Memory.MaxCounters > 1_000_000 {
			add("cache.memory.max_counters", "must be between 1 and 1000000")
		}
	case "redis":
		if len(cache.Redis.Addresses) == 0 {
			add("cache.redis.addresses", "must contain at least one address")
		}
		for index, address := range cache.Redis.Addresses {
			if !validHostPort(address) {
				add(fmt.Sprintf("cache.redis.addresses[%d]", index), "must be a host:port TCP address")
			}
		}
		if cache.Redis.Database < 0 {
			add("cache.redis.database", "must not be negative")
		}
		if cache.Redis.ConnectTimeout.Duration <= 0 {
			add("cache.redis.connect_timeout", "must be greater than zero")
		}
	default:
		add("cache.backend", "must be memory or redis")
	}
	validateNamespace("cache.namespace", cache.Namespace, add)
}
