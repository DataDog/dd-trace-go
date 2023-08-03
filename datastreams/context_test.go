// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestContext(t *testing.T) {
	t.Run("SetCheckpoint", func(t *testing.T) {
		processor := Processor{
			stopped:    1,
			in:         make(chan statsPoint, 10),
			service:    "service-1",
			env:        "env",
			timeSource: time.Now,
		}
		hash1 := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}), 0)
		hash2 := pathwayHash(nodeHash("service-1", "env", []string{"direction:out", "type:kafka"}), hash1)

		ctx := context.Background()
		pathway, ctx := processor.SetCheckpoint(ctx, "direction:in", "type:kafka")
		pathway, _ = processor.SetCheckpoint(ctx, "direction:out", "type:kafka")

		statsPt1 := <-processor.in
		statsPt2 := <-processor.in

		assert.Equal(t, []string{"direction:in", "type:kafka"}, statsPt1.edgeTags)
		assert.Equal(t, hash1, statsPt1.hash)
		assert.Equal(t, uint64(0), statsPt1.parentHash)

		assert.Equal(t, []string{"direction:out", "type:kafka"}, statsPt2.edgeTags)
		assert.Equal(t, hash2, statsPt2.hash)
		assert.Equal(t, hash1, statsPt2.parentHash)

		assert.Equal(t, statsPt2.hash, pathway.GetHash())
	})
}
