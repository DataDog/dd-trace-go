// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package servicehash

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

const (
	maxCachSize = 1000
)

var (
	h                       = sha256.New()
	cache map[string]string = make(map[string]string)
	lock  sync.RWMutex
)

// Hash computes hash for `service`. Will use a local cache to avoid recomputation.
func Hash(service string) string {
	hash := getHashFromCache(service)
	if len(hash) > 0 {
		return hash
	}
	hashb := sha256.Sum256([]byte(service))
	hash = hex.EncodeToString(hashb[:4])
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
	if len(cache) == maxCachSize {
		// deletes a random key
		for k := range cache {
			delete(cache, k)
			break
		}
	}
	cache[service] = hash
}
