// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"hash/fnv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPathway(t *testing.T) {
	t.Run("test SetCheckpoint", func(t *testing.T) {
		start := time.Now()
		processor := Processor{
			hashCache:  newHashCache(),
			stopped:    1,
			in:         newFastQueue(),
			service:    "service-1",
			env:        "env",
			timeSource: func() time.Time { return start },
		}
		ctx := processor.SetCheckpoint(context.Background())
		middle := start.Add(time.Hour)
		processor.timeSource = func() time.Time { return middle }
		ctx = processor.SetCheckpoint(ctx, "topic:topic1")
		end := middle.Add(time.Hour)
		processor.timeSource = func() time.Time { return end }
		ctx = processor.SetCheckpoint(ctx, "topic:topic2")
		hash1 := pathwayHash(nodeHash("service-1", "env", nil), 0)
		hash2 := pathwayHash(nodeHash("service-1", "env", []string{"topic:topic1"}), hash1)
		hash3 := pathwayHash(nodeHash("service-1", "env", []string{"topic:topic2"}), hash2)
		p, _ := PathwayFromContext(ctx)
		assert.Equal(t, hash3, p.GetHash())
		assert.Equal(t, start, p.PathwayStart())
		assert.Equal(t, end, p.EdgeStart())
		assert.Equal(t, statsPoint{
			edgeTags:       nil,
			hash:           hash1,
			parentHash:     0,
			timestamp:      start.UnixNano(),
			pathwayLatency: 0,
			edgeLatency:    0,
		}, processor.in.poll(time.Second).point)
		assert.Equal(t, statsPoint{
			edgeTags:       []string{"topic:topic1"},
			hash:           hash2,
			parentHash:     hash1,
			timestamp:      middle.UnixNano(),
			pathwayLatency: middle.Sub(start).Nanoseconds(),
			edgeLatency:    middle.Sub(start).Nanoseconds(),
		}, processor.in.poll(time.Second).point)
		assert.Equal(t, statsPoint{
			edgeTags:       []string{"topic:topic2"},
			hash:           hash3,
			parentHash:     hash2,
			timestamp:      end.UnixNano(),
			pathwayLatency: end.Sub(start).Nanoseconds(),
			edgeLatency:    end.Sub(middle).Nanoseconds(),
		}, processor.in.poll(time.Second).point)
	})

	t.Run("test new pathway creation", func(t *testing.T) {
		processor := Processor{
			hashCache:  newHashCache(),
			stopped:    1,
			in:         newFastQueue(),
			service:    "service-1",
			env:        "env",
			timeSource: time.Now,
		}

		pathwayWithNoEdgeTags, _ := PathwayFromContext(processor.SetCheckpoint(context.Background()))
		pathwayWith1EdgeTag, _ := PathwayFromContext(processor.SetCheckpoint(context.Background(), "type:internal"))
		pathwayWith2EdgeTags, _ := PathwayFromContext(processor.SetCheckpoint(context.Background(), "type:internal", "some_other_key:some_other_val"))

		hash1 := pathwayHash(nodeHash("service-1", "env", nil), 0)
		hash2 := pathwayHash(nodeHash("service-1", "env", []string{"type:internal"}), 0)
		hash3 := pathwayHash(nodeHash("service-1", "env", []string{"type:internal", "some_other_key:some_other_val"}), 0)
		assert.Equal(t, hash1, pathwayWithNoEdgeTags.GetHash())
		assert.Equal(t, hash2, pathwayWith1EdgeTag.GetHash())
		assert.Equal(t, hash3, pathwayWith2EdgeTags.GetHash())

		var statsPointWithNoEdgeTags = processor.in.poll(time.Second).point
		var statsPointWith1EdgeTag = processor.in.poll(time.Second).point
		var statsPointWith2EdgeTags = processor.in.poll(time.Second).point
		assert.Equal(t, hash1, statsPointWithNoEdgeTags.hash)
		assert.Equal(t, []string(nil), statsPointWithNoEdgeTags.edgeTags)
		assert.Equal(t, hash2, statsPointWith1EdgeTag.hash)
		assert.Equal(t, []string{"type:internal"}, statsPointWith1EdgeTag.edgeTags)
		assert.Equal(t, hash3, statsPointWith2EdgeTags.hash)
		assert.Equal(t, []string{"some_other_key:some_other_val", "type:internal"}, statsPointWith2EdgeTags.edgeTags)
	})

	t.Run("test nodeHash", func(t *testing.T) {
		assert.NotEqual(t,
			nodeHash("service-1", "env", []string{"type:internal"}),
			nodeHash("service-1", "env", []string{"type:kafka"}),
		)
		assert.NotEqual(t,
			nodeHash("service-1", "env", []string{"exchange:1"}),
			nodeHash("service-1", "env", []string{"exchange:2"}),
		)
		assert.NotEqual(t,
			nodeHash("service-1", "env", []string{"topic:1"}),
			nodeHash("service-1", "env", []string{"topic:2"}),
		)
		assert.NotEqual(t,
			nodeHash("service-1", "env", []string{"group:1"}),
			nodeHash("service-1", "env", []string{"group:2"}),
		)
		assert.NotEqual(t,
			nodeHash("service-1", "env", []string{"event_type:1"}),
			nodeHash("service-1", "env", []string{"event_type:2"}),
		)
		assert.Equal(t,
			nodeHash("service-1", "env", []string{"partition:0"}),
			nodeHash("service-1", "env", []string{"partition:1"}),
		)
	})

	t.Run("test isWellFormedEdgeTag", func(t *testing.T) {
		for _, tc := range []struct {
			s string
			b bool
		}{
			{"", false},
			{"dog", false},
			{"dog:", false},
			{"dog:bark", false},
			{"type:", true},
			{"type:dog", true},
			{"type::dog", true},
			{"type:d:o:g", true},
			{"type::", true},
			{":", false},
			{"topic:arn:aws:sns:us-east-1:727006795293:dsm-dev-sns-topic", true},
		} {
			assert.Equal(t, isWellFormedEdgeTag(tc.s), tc.b)
		}
	})

	// nodeHash assumes that the go Hash interface produces the same result
	// for a given series of Write calls as for a single Write of the same
	// byte sequence. This unit test asserts that assumption.
	t.Run("test hashWriterIsomorphism", func(t *testing.T) {
		h := fnv.New64()
		var b []byte
		b = append(b, "dog"...)
		b = append(b, "cat"...)
		b = append(b, "pig"...)
		h.Write(b)
		s1 := h.Sum64()
		h.Reset()
		h.Write([]byte("dog"))
		h.Write([]byte("cat"))
		h.Write([]byte("pig"))
		assert.Equal(t, s1, h.Sum64())
	})

	t.Run("test GetHash", func(t *testing.T) {
		pathway := Pathway{hash: nodeHash("service", "env", []string{"direction:in"})}
		assert.Equal(t, pathway.hash, pathway.GetHash())
	})
}

// Sample results at time of writing this benchmark:
// goos: darwin
// goarch: amd64
// pkg: github.com/DataDog/data-streams-go/datastreams
// cpu: Intel(R) Core(TM) i7-1068NG7 CPU @ 2.30GHz
// BenchmarkNodeHash-8   	 5167707	       232.5 ns/op	      24 B/op	       1 allocs/op
func BenchmarkNodeHash(b *testing.B) {
	service := "benchmark-runner"
	env := "test"
	edgeTags := []string{"event_type:dog", "exchange:local", "group:all", "topic:off", "type:writer"}
	for i := 0; i < b.N; i++ {
		nodeHash(service, env, edgeTags)
	}
}
