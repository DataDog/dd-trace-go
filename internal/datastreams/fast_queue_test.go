// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFastQueue(t *testing.T) {
	q := newFastQueue()
	q.push(&processorInput{point: statsPoint{hash: 1}})
	q.push(&processorInput{point: statsPoint{hash: 2}})
	q.push(&processorInput{point: statsPoint{hash: 3}})
	assert.Equal(t, uint64(1), q.pop().point.hash)
	assert.Equal(t, uint64(2), q.pop().point.hash)
	q.push(&processorInput{point: statsPoint{hash: 4}})
	assert.Equal(t, uint64(3), q.pop().point.hash)
	assert.Equal(t, uint64(4), q.pop().point.hash)
	for i := 0; i < queueSize; i++ {
		q.push(&processorInput{point: statsPoint{hash: uint64(i)}})
		assert.Equal(t, uint64(i), q.pop().point.hash)
	}
}
