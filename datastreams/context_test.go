// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContext(t *testing.T) {
	t.Run("SetCheckpoint", func(t *testing.T) {
		aggregator := aggregator{
			stopped:    1,
			in:         make(chan statsPoint, 10),
			service:    "service-1",
			env:        "env",
			primaryTag: "d:1",
		}
		setGlobalAggregator(&aggregator)
		defer setGlobalAggregator(nil)
		hash1 := pathwayHash(nodeHash("service-1", "env", "d:1", []string{"type:internal"}), 0)
		hash2 := pathwayHash(nodeHash("service-1", "env", "d:1", []string{"type:kafka"}), hash1)

		ctx := context.Background()
		pathway, ctx := SetCheckpoint(ctx, "type:internal")
		pathway, _ = SetCheckpoint(ctx, "type:kafka")

		statsPt1 := <-aggregator.in
		statsPt2 := <-aggregator.in

		assert.Equal(t, []string{"type:internal"}, statsPt1.edgeTags)
		assert.Equal(t, hash1, statsPt1.hash)
		assert.Equal(t, uint64(0), statsPt1.parentHash)

		assert.Equal(t, []string{"type:kafka"}, statsPt2.edgeTags)
		assert.Equal(t, hash2, statsPt2.hash)
		assert.Equal(t, hash1, statsPt2.parentHash)

		assert.Equal(t, statsPt2.hash, pathway.hash)
	})
}
