// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashCache(t *testing.T) {
	cache := newHashCache()
	assert.Equal(t, pathwayHash(nodeHash("service", "env", []string{"type:kafka"}, nil), 1234), cache.get("service", "env", []string{"type:kafka"}, nil, 1234))
	assert.Len(t, cache.m, 1)
	assert.Equal(t, pathwayHash(nodeHash("service", "env", []string{"type:kafka"}, nil), 1234), cache.get("service", "env", []string{"type:kafka"}, nil, 1234))
	assert.Len(t, cache.m, 1)
	assert.Equal(t, pathwayHash(nodeHash("service", "env", []string{"type:kafka2"}, nil), 1234), cache.get("service", "env", []string{"type:kafka2"}, nil, 1234))
	assert.Len(t, cache.m, 2)

	pTags := []string{"entrypoint.name:something", "entrypoint.type:executable"}
	h1 := pathwayHash(nodeHash("service", "env", []string{"type:kafka"}, pTags), 1234)
	h2 := cache.get("service", "env", []string{"type:kafka"}, pTags, 1234)

	assert.Equal(t, h1, h2)
	assert.Len(t, cache.m, 3)
}

func TestGetHashKey(t *testing.T) {
	parentHash := uint64(87234)
	key := getHashKey([]string{"type:kafka", "topic:topic1", "group:group1"}, []string{"entrypoint.name:something", "entrypoint.type:executable"}, parentHash)
	hash := make([]byte, 8)
	binary.LittleEndian.PutUint64(hash, parentHash)
	assert.Equal(t, "type:kafkatopic:topic1group:group1entrypoint.name:somethingentrypoint.type:executable"+string(hash), key)
}
