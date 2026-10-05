// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// hashCacheKey returns a distinct, pre-sorted edge-tag set and parent hash for index i.
func hashCacheKey(i int) (edgeTags []string, parentHash uint64) {
	return []string{"direction:in", "topic:topic" + strconv.Itoa(i), "type:kafka"}, uint64(i)
}

func hashCacheExpected(i int) uint64 {
	et, parentHash := hashCacheKey(i)
	return pathwayHash(nodeHash("svc", "env", et, nil, ""), parentHash)
}

func TestHashCache(t *testing.T) {
	cache := newHashCache()
	assert.Equal(t, pathwayHash(nodeHash("service", "env", []string{"type:kafka"}, nil, ""), 1234), cache.get("service", "env", []string{"type:kafka"}, nil, "", 1234))
	assert.Equal(t, int32(1), cache.size.Load())
	assert.Equal(t, pathwayHash(nodeHash("service", "env", []string{"type:kafka"}, nil, ""), 1234), cache.get("service", "env", []string{"type:kafka"}, nil, "", 1234))
	assert.Equal(t, int32(1), cache.size.Load())
	assert.Equal(t, pathwayHash(nodeHash("service", "env", []string{"type:kafka2"}, nil, ""), 1234), cache.get("service", "env", []string{"type:kafka2"}, nil, "", 1234))
	assert.Equal(t, int32(2), cache.size.Load())

	pTags := []string{"entrypoint.name:something", "entrypoint.type:executable"}
	h1 := pathwayHash(nodeHash("service", "env", []string{"type:kafka"}, pTags, "container-hash"), 1234)
	h2 := cache.get("service", "env", []string{"type:kafka"}, pTags, "container-hash", 1234)

	assert.Equal(t, h1, h2)
	assert.Equal(t, int32(3), cache.size.Load())

	h3 := cache.get("service", "env", []string{"type:kafka"}, pTags, "other-container-hash", 1234)
	assert.NotEqual(t, h2, h3)
	assert.Equal(t, int32(4), cache.size.Load())
}

// TestHashCacheConcurrent drives concurrent misses then hits over distinct keys, checking each against a reference (run under -race).
func TestHashCacheConcurrent(t *testing.T) {
	const (
		goroutines = 32
		keys       = 200 // < maxHashCacheSize so nothing is dropped
		iters      = 20
	)
	expected := make([]uint64, keys)
	for i := range keys {
		expected[i] = hashCacheExpected(i)
	}

	cache := newHashCache()
	var wg sync.WaitGroup
	errs := make(chan string, goroutines)
	for range goroutines {
		wg.Go(func() {
			for range iters {
				for i := range keys {
					et, parentHash := hashCacheKey(i) // fresh slice per call
					if got := cache.get("svc", "env", et, nil, "", parentHash); got != expected[i] {
						select {
						case errs <- strconv.Itoa(i) + ": got " + strconv.FormatUint(got, 10) + " want " + strconv.FormatUint(expected[i], 10):
						default:
						}
						return
					}
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	if msg, ok := <-errs; ok {
		t.Fatal(msg)
	}
	assert.Equal(t, int32(keys), cache.size.Load())
}

// TestHashCacheEviction overflows past maxHashCacheSize and verifies the cache stays bounded and still correct.
func TestHashCacheEviction(t *testing.T) {
	cache := newHashCache()
	n := maxHashCacheSize + 50
	for i := range n {
		et, parentHash := hashCacheKey(i)
		assert.Equal(t, hashCacheExpected(i), cache.get("svc", "env", et, nil, "", parentHash))
		assert.LessOrEqual(t, cache.size.Load(), int32(maxHashCacheSize))
	}
	// Uncached keys past the bound still return the correct value (recomputed).
	et, parentHash := hashCacheKey(n)
	assert.Equal(t, hashCacheExpected(n), cache.get("svc", "env", et, nil, "", parentHash))
}

func TestComputeFingerprint(t *testing.T) {
	parentHash := uint64(87234)
	edgeTags := []string{"type:kafka", "topic:topic1", "group:group1"}
	pTags := []string{"entrypoint.name:something", "entrypoint.type:executable"}

	fp := computeFingerprint(edgeTags, pTags, "container-hash", parentHash)
	// Deterministic for identical inputs within a process.
	assert.Equal(t, fp, computeFingerprint(edgeTags, pTags, "container-hash", parentHash))

	// Each component contributes to the fingerprint.
	assert.NotEqual(t, fp, computeFingerprint([]string{"type:kafka", "topic:topic2", "group:group1"}, pTags, "container-hash", parentHash))
	assert.NotEqual(t, fp, computeFingerprint(edgeTags, nil, "container-hash", parentHash))
	assert.NotEqual(t, fp, computeFingerprint(edgeTags, pTags, "other-container-hash", parentHash))
	assert.NotEqual(t, fp, computeFingerprint(edgeTags, pTags, "container-hash", parentHash+1))
}
