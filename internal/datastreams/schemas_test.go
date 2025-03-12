// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package datastreams

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestSchemaSampler(t *testing.T) {
	s := &schemaSampler{}
	now := time.Now().UnixNano()
	assert.True(t, s.shouldSampleSchema(now))
	assert.Equal(t, int64(1), s.sampleSchema(now))
	assert.False(t, s.shouldSampleSchema(now+10))
	assert.False(t, s.shouldSampleSchema(now+10))
	assert.True(t, s.shouldSampleSchema(now+schemaSampleIntervalNs))
	assert.Equal(t, int64(3), s.sampleSchema(now+schemaSampleIntervalNs))
}
