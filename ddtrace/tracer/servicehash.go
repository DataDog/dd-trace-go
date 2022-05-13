// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package tracer

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

const maxCacheSize = 1000

var (
	cache = make(map[string]string)
	lock  sync.RWMutex
)

// Hash computes hash for `service`. Will use a local cache to avoid recomputation.
func Hash(service string) string {
	hash := getHashFromCache(service)
	if len(hash) > 0 {
		return hash
	}
	hashb := sha256.Sum256([]byte(service))
	// Only grab first 10 characters (2 chars per byte * 5 bytes = 10 chars)
	hash = hex.EncodeToString(hashb[:5])
	setHashInCache(service, hash)
	return hash
}

func getHashFromCache(service string) string {
	lock.RLock()
	defer lock.RUnlock()
	if hash, ok := cache[service]; ok {
		return hash
	}
	return ""
}

func setHashInCache(service, hash string) {
	lock.Lock()
	defer lock.Unlock()
	if len(cache) == maxCacheSize {
		// deletes a random key
		for k := range cache {
			delete(cache, k)
			break
		}
	}
	cache[service] = hash
}
