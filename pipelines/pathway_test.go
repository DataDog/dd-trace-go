// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package pipelines

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPathway(t *testing.T) {
	processor := processor{
		in:      make(chan statsPoint, 10),
		service: "service-1",
	}
	setGlobalProcessor(&processor)
	defer setGlobalProcessor(nil)
	start := time.Now()
	middle := start.Add(time.Hour)
	end := middle.Add(time.Hour)
	p := newPathway(start)
	p = p.setCheckpoint("edge-1", middle)
	p = p.setCheckpoint("edge-2", end)
	hash1 := pathwayHash(nodeHash("service-1", ""), 0)
	hash2 := pathwayHash(nodeHash("service-1", "edge-1"), hash1)
	hash3 := pathwayHash(nodeHash("service-1", "edge-2"), hash2)
	assert.Equal(t, Pathway{
		hash:         hash3,
		pathwayStart: start,
		edgeStart:    end,
		service:      "service-1",
		edge:         "edge-2",
	}, p)
	assert.Equal(t, statsPoint{
		service:        "service-1",
		edge:           "",
		hash:           hash1,
		parentHash:     0,
		timestamp:      start.UnixNano(),
		pathwayLatency: 0,
		edgeLatency:    0,
	}, <-processor.in)
	assert.Equal(t, statsPoint{
		service:        "service-1",
		edge:           "edge-1",
		hash:           hash2,
		parentHash:     hash1,
		timestamp:      middle.UnixNano(),
		pathwayLatency: middle.Sub(start).Nanoseconds(),
		edgeLatency:    middle.Sub(start).Nanoseconds(),
	}, <-processor.in)
	assert.Equal(t, statsPoint{
		service:        "service-1",
		edge:           "edge-2",
		hash:           hash3,
		parentHash:     hash2,
		timestamp:      end.UnixNano(),
		pathwayLatency: end.Sub(start).Nanoseconds(),
		edgeLatency:    end.Sub(middle).Nanoseconds(),
	}, <-processor.in)
}
