// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"encoding/binary"
	"hash/maphash"
	"sync"
	"sync/atomic"
)

const (
	maxHashCacheSize = 1000
)

// hashCache maps a fingerprint to a pathway hash. sync.Map gives lock-free reads on the per-message hot path; size bounds growth.
type hashCache struct {
	m    sync.Map // map[uint64]uint64
	size atomic.Int32
}

var maphashSeed = maphash.MakeSeed()

// computeFingerprint returns a fast, allocation-free fingerprint for a cache lookup key.
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

func (c *hashCache) get(service, env string, edgeTags, processTags []string, containerTagsHash string, parentHash uint64) uint64 {
	fp := computeFingerprint(edgeTags, processTags, containerTagsHash, parentHash)
	if v, ok := c.m.Load(fp); ok {
		return v.(uint64)
	}
	hash := pathwayHash(nodeHash(service, env, edgeTags, processTags, containerTagsHash), parentHash)
	// Reserve a slot atomically; give it back if we'd exceed the bound or the key is already present.
	if c.size.Add(1) > maxHashCacheSize {
		c.size.Add(-1)
		return hash
	}
	if _, loaded := c.m.LoadOrStore(fp, hash); loaded {
		c.size.Add(-1)
	}
	return hash
}

func newHashCache() *hashCache {
	return &hashCache{}
}
