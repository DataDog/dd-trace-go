// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package datastreams

import (
	"hash/fnv"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	schemaSampleIntervalNs = int64(time.Second * 30)
)

var (
	// globalSchemaSampler stores the current schema sampler
	globalSchemaSampler *schemaSampler
)

func init() {
	globalSchemaSampler = &schemaSampler{}
}

type schemaSampler struct {
	weight           atomic.Int64
	lastSampleMillis atomic.Int64
}

func SampleSchema() (weight int64) {
	return globalSchemaSampler.sampleSchema(time.Now().UnixNano())
}

func ShouldSampleSchema() (ok bool) {
	return globalSchemaSampler.shouldSampleSchema(time.Now().UnixNano())
}

func (s *schemaSampler) sampleSchema(currentTimeNs int64) (weight int64) {
	lastSample := s.lastSampleMillis.Load()
	if currentTimeNs >= lastSample+schemaSampleIntervalNs {
		if s.lastSampleMillis.CompareAndSwap(lastSample, currentTimeNs) {
			return s.weight.Swap(0)
		}
	}
	return 0
}

func (s *schemaSampler) shouldSampleSchema(currentTimeNs int64) bool {
	s.weight.Add(1)
	lastSample := s.lastSampleMillis.Load()
	if currentTimeNs >= lastSample+schemaSampleIntervalNs {
		return true
	}
	return false
}

func GetSchemaID(schema string) string {
	h := fnv.New64()
	h.Write([]byte(schema))
	return strconv.FormatUint(h.Sum64(), 10)
}
