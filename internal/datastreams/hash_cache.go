// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"encoding/binary"
	"hash/maphash"
	"sync"
)

const (
	maxHashCacheSize = 1000
)

type hashCache struct {
	mu sync.RWMutex
	m  map[uint64]uint64
}

var maphashSeed = maphash.MakeSeed()

// computeFingerprint returns a fast, allocation-free fingerprint for a cache lookup key.
// Collision probability is ~2^-64 per distinct input pair — negligible for a telemetry cache.
func computeFingerprint(edgeTags, processTags []string, containerTagsHash string, parentHash uint64) uint64 {
	var h maphash.Hash
	h.SetSeed(maphashSeed)
	for _, t := range edgeTags {
		_, _ = h.WriteString(t)
	}
	for _, t := range processTags {
		_, _ = h.WriteString(t)
	}
	_, _ = h.WriteString(containerTagsHash)
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], parentHash)
	_, _ = h.Write(b[:])
	return h.Sum64()
}

func (c *hashCache) computeAndGet(fp uint64, parentHash uint64, service, env string, edgeTags, processTags []string, containerTagsHash string) uint64 {
	hash := pathwayHash(nodeHash(service, env, edgeTags, processTags, containerTagsHash), parentHash)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= maxHashCacheSize {
		// high cardinality of hashes shouldn't happen in practice, due to a limited amount of topics consumed
		// by each service.
		c.m = make(map[uint64]uint64)
	}
	c.m[fp] = hash
	return hash
}

func (c *hashCache) get(service, env string, edgeTags, processTags []string, containerTagsHash string, parentHash uint64) uint64 {
	fp := computeFingerprint(edgeTags, processTags, containerTagsHash, parentHash)
	c.mu.RLock()
	if hash, ok := c.m[fp]; ok {
		c.mu.RUnlock()
		return hash
	}
	c.mu.RUnlock()
	return c.computeAndGet(fp, parentHash, service, env, edgeTags, processTags, containerTagsHash)
}

func newHashCache() *hashCache {
	return &hashCache{m: make(map[uint64]uint64)}
}
