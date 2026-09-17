// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/version"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func buildSketch(values ...float64) []byte {
	sketch := ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor())
	for _, v := range values {
		sketch.Add(v)
	}
	bytes, _ := proto.Marshal(sketch.ToProto())
	return bytes
}

func sortedPayloads(payloads map[string]StatsPayload) map[string]StatsPayload {
	for _, payload := range payloads {
		sort.Slice(payload.Stats, func(i, j int) bool {
			return payload.Stats[i].Start < payload.Stats[j].Start
		})
		for _, bucket := range payload.Stats {
			sort.Slice(bucket.Stats, func(i, j int) bool {
				return bucket.Stats[i].Hash < bucket.Stats[j].Hash
			})
			sort.Slice(bucket.Backlogs, func(i, j int) bool {
				return strings.Join(bucket.Backlogs[i].Tags, "") < strings.Join(bucket.Backlogs[j].Tags, "")
			})
		}
	}
	return payloads
}

func TestProcessor(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp1 := time.Now().Truncate(bucketDuration)
	tp2 := tp1.Add(time.Minute)

	p.add(statsPoint{
		serviceName:    "service1",
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: time.Second.Nanoseconds(),
		edgeLatency:    time.Second.Nanoseconds(),
		payloadSize:    1,
	})
	p.add(statsPoint{
		serviceName:    "service1",
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
		payloadSize:    2,
	})
	p.add(statsPoint{
		serviceName:    "service1",
		edgeTags:       []string{"type:edge-1"},
		hash:           3,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
		payloadSize:    2,
	})
	p.add(statsPoint{
		serviceName:    "service1",
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp1.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
		payloadSize:    2,
	})
	got := sortedPayloads(p.flush(tp1.Add(bucketDuration)))
	assert.Len(t, got["service1"].Stats, 2)
	assert.Equal(t, map[string]StatsPayload{
		"service1": {
			Env:         "env",
			Service:     "service1",
			Version:     "v1",
			ProcessTags: processtags.GlobalTags().Slice(),
			Stats: []StatsBucket{
				{
					Start:    uint64(tp1.Add(-10 * time.Second).UnixNano()),
					Duration: uint64(bucketDuration.Nanoseconds()),
					Stats: []StatsPoint{{
						EdgeTags:       []string{"type:edge-1"},
						Hash:           2,
						ParentHash:     1,
						PathwayLatency: buildSketch(5),
						EdgeLatency:    buildSketch(2),
						PayloadSize:    buildSketch(2),
						TimestampType:  "origin",
					}},
					Backlogs: []Backlog{},
				},
				{
					Start:    uint64(tp1.UnixNano()),
					Duration: uint64(bucketDuration.Nanoseconds()),
					Stats: []StatsPoint{{
						EdgeTags:       []string{"type:edge-1"},
						Hash:           2,
						ParentHash:     1,
						PathwayLatency: buildSketch(5),
						EdgeLatency:    buildSketch(2),
						PayloadSize:    buildSketch(2),
						TimestampType:  "current",
					}},
					Backlogs: []Backlog{},
				},
			},
			TracerVersion: version.Tag,
			Lang:          "go",
			ProductMask:   productAPM | productDSM,
		}}, got)

	got = sortedPayloads(p.flush(tp2.Add(bucketDuration)))
	assert.Equal(t, map[string]StatsPayload{
		"service1": {
			Env:         "env",
			Service:     "service1",
			Version:     "v1",
			ProcessTags: processtags.GlobalTags().Slice(),
			Stats: []StatsBucket{
				{
					Start:    uint64(tp2.Add(-time.Second * 10).UnixNano()),
					Duration: uint64(bucketDuration.Nanoseconds()),
					Stats: []StatsPoint{
						{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           2,
							ParentHash:     1,
							PathwayLatency: buildSketch(1, 5),
							EdgeLatency:    buildSketch(1, 2),
							PayloadSize:    buildSketch(1, 2),
							TimestampType:  "origin",
						},
						{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           3,
							ParentHash:     1,
							PathwayLatency: buildSketch(5),
							EdgeLatency:    buildSketch(2),
							PayloadSize:    buildSketch(2),
							TimestampType:  "origin",
						},
					},
					Backlogs: []Backlog{},
				},
				{
					Start:    uint64(tp2.UnixNano()),
					Duration: uint64(bucketDuration.Nanoseconds()),
					Stats: []StatsPoint{
						{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           2,
							ParentHash:     1,
							PathwayLatency: buildSketch(1, 5),
							EdgeLatency:    buildSketch(1, 2),
							PayloadSize:    buildSketch(1, 2),
							TimestampType:  "current",
						},
						{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           3,
							ParentHash:     1,
							PathwayLatency: buildSketch(5),
							EdgeLatency:    buildSketch(2),
							PayloadSize:    buildSketch(2),
							TimestampType:  "current",
						},
					},
					Backlogs: []Backlog{},
				},
			},
			TracerVersion: version.Tag,
			Lang:          "go",
			ProductMask:   productAPM | productDSM,
		}}, got)

	t.Run("test_service_name_override", func(t *testing.T) {
		p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
		// Use a fixed time so the test is deterministic regardless of wall-clock speed.
		tp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Truncate(bucketDuration)
		p.timeSource = func() time.Time { return tp }
		p.add(statsPoint{
			serviceName:    "service1",
			edgeTags:       []string{"type:edge-1"},
			hash:           2,
			parentHash:     1,
			timestamp:      tp.UnixNano(),
			pathwayLatency: time.Second.Nanoseconds(),
			edgeLatency:    time.Second.Nanoseconds(),
			payloadSize:    1,
		})
		p.add(statsPoint{
			serviceName:    "service2",
			edgeTags:       []string{"type:edge-1"},
			hash:           2,
			parentHash:     1,
			timestamp:      tp.UnixNano(),
			pathwayLatency: (5 * time.Second).Nanoseconds(),
			edgeLatency:    (2 * time.Second).Nanoseconds(),
			payloadSize:    2,
		})
		got := sortedPayloads(p.flush(tp.Add(bucketDuration)))
		assert.Equal(t, map[string]StatsPayload{
			"service1": {
				Env:         "env",
				Service:     "service1",
				Version:     "v1",
				ProcessTags: processtags.GlobalTags().Slice(),
				Stats: []StatsBucket{
					{
						Start:    uint64(tp.Add(-10 * time.Second).UnixNano()),
						Duration: uint64(bucketDuration.Nanoseconds()),
						Stats: []StatsPoint{{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           2,
							ParentHash:     1,
							PathwayLatency: buildSketch(1),
							EdgeLatency:    buildSketch(1),
							PayloadSize:    buildSketch(1),
							TimestampType:  "origin",
						}},
						Backlogs: []Backlog{},
					},
					{
						Start:    uint64(tp.UnixNano()),
						Duration: uint64(bucketDuration.Nanoseconds()),
						Stats: []StatsPoint{{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           2,
							ParentHash:     1,
							PathwayLatency: buildSketch(1),
							EdgeLatency:    buildSketch(1),
							PayloadSize:    buildSketch(1),
							TimestampType:  "current",
						}},
						Backlogs: []Backlog{},
					},
				},
				TracerVersion: version.Tag,
				Lang:          "go",
				ProductMask:   productAPM | productDSM,
			},
			"service2": {
				Env:         "env",
				Service:     "service2",
				Version:     "v1",
				ProcessTags: processtags.GlobalTags().Slice(),
				Stats: []StatsBucket{
					{
						Start:    uint64(tp.Add(-10 * time.Second).UnixNano()),
						Duration: uint64(bucketDuration.Nanoseconds()),
						Stats: []StatsPoint{{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           2,
							ParentHash:     1,
							PathwayLatency: buildSketch(5),
							EdgeLatency:    buildSketch(2),
							PayloadSize:    buildSketch(2),
							TimestampType:  "origin",
						}},
						Backlogs: []Backlog{},
					},
					{
						Start:    uint64(tp.UnixNano()),
						Duration: uint64(bucketDuration.Nanoseconds()),
						Stats: []StatsPoint{{
							EdgeTags:       []string{"type:edge-1"},
							Hash:           2,
							ParentHash:     1,
							PathwayLatency: buildSketch(5),
							EdgeLatency:    buildSketch(2),
							PayloadSize:    buildSketch(2),
							TimestampType:  "current",
						}},
						Backlogs: []Backlog{},
					},
				},
				TracerVersion: version.Tag,
				Lang:          "go",
				ProductMask:   productAPM | productDSM,
			},
		}, got)
	})

}

func TestSetCheckpoint(t *testing.T) {
	processor := Processor{
		hashCache:  newHashCache(),
		stopped:    1,
		in:         newFastQueue(),
		service:    "service-1",
		env:        "env",
		timeSource: time.Now,
	}
	processTags := processtags.GlobalTags().Slice()
	hash1 := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}, processTags, ""), 0)
	hash2 := pathwayHash(nodeHash("service-1", "env", []string{"direction:out", "type:kafka"}, processTags, ""), hash1)

	ctx := processor.SetCheckpoint(context.Background(), "direction:in", "type:kafka")
	pathway, _ := PathwayFromContext(processor.SetCheckpoint(ctx, "direction:out", "type:kafka"))

	statsPt1 := processor.in.pop().point
	statsPt2 := processor.in.pop().point

	assert.Equal(t, []string{"direction:in", "type:kafka"}, statsPt1.edgeTags)
	assert.Equal(t, hash1, statsPt1.hash)
	assert.Equal(t, uint64(0), statsPt1.parentHash)

	assert.Equal(t, []string{"direction:out", "type:kafka"}, statsPt2.edgeTags)
	assert.Equal(t, hash2, statsPt2.hash)
	assert.Equal(t, hash1, statsPt2.parentHash)

	assert.Equal(t, statsPt2.hash, pathway.GetHash())
}

func TestSetCheckpointProcessTags(t *testing.T) {
	processtags.Reload()
	pTags := processtags.GlobalTags().Slice()
	require.NotEmpty(t, pTags)

	processor := Processor{
		hashCache:  newHashCache(),
		stopped:    1,
		in:         newFastQueue(),
		service:    "service-1",
		env:        "env",
		timeSource: time.Now,
	}
	hash1 := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}, pTags, ""), 0)
	hash2 := pathwayHash(nodeHash("service-1", "env", []string{"direction:out", "type:kafka"}, pTags, ""), hash1)

	ctx := processor.SetCheckpoint(context.Background(), "direction:in", "type:kafka")
	pathway, _ := PathwayFromContext(processor.SetCheckpoint(ctx, "direction:out", "type:kafka"))

	statsPt1 := processor.in.pop().point
	statsPt2 := processor.in.pop().point

	assert.Equal(t, []string{"direction:in", "type:kafka"}, statsPt1.edgeTags)
	assert.Equal(t, hash1, statsPt1.hash)
	assert.Equal(t, uint64(0), statsPt1.parentHash)

	assert.Equal(t, []string{"direction:out", "type:kafka"}, statsPt2.edgeTags)
	assert.Equal(t, hash2, statsPt2.hash)
	assert.Equal(t, hash1, statsPt2.parentHash)

	assert.Equal(t, statsPt2.hash, pathway.GetHash())
}

func TestSetCheckpointContainerTagsHash(t *testing.T) {
	t.Cleanup(func() {
		processtags.SetContainerTagsHash("")
		processtags.Reload()
	})
	processtags.Reload()
	processtags.SetContainerTagsHash("container-tags-hash")
	pTags := processtags.GlobalTags().Slice()
	require.NotEmpty(t, pTags)

	processor := Processor{
		hashCache:  newHashCache(),
		stopped:    1,
		in:         newFastQueue(),
		service:    "service-1",
		env:        "env",
		timeSource: time.Now,
	}
	hash1 := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}, pTags, "container-tags-hash"), 0)
	hash2 := pathwayHash(nodeHash("service-1", "env", []string{"direction:out", "type:kafka"}, pTags, "container-tags-hash"), hash1)

	ctx := processor.SetCheckpoint(context.Background(), "direction:in", "type:kafka")
	pathway, _ := PathwayFromContext(processor.SetCheckpoint(ctx, "direction:out", "type:kafka"))

	statsPt1 := processor.in.pop().point
	statsPt2 := processor.in.pop().point

	assert.Equal(t, hash1, statsPt1.hash)
	assert.Equal(t, uint64(0), statsPt1.parentHash)
	assert.Equal(t, hash2, statsPt2.hash)
	assert.Equal(t, hash1, statsPt2.parentHash)
	assert.Equal(t, statsPt2.hash, pathway.GetHash())
}

func TestSetCheckpointContainerTagsHashRequiresProcessTags(t *testing.T) {
	t.Cleanup(func() {
		processtags.SetContainerTagsHash("")
		processtags.Reload()
	})
	t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
	processtags.Reload()
	processtags.SetContainerTagsHash("container-tags-hash")

	processor := Processor{
		hashCache:  newHashCache(),
		stopped:    1,
		in:         newFastQueue(),
		service:    "service-1",
		env:        "env",
		timeSource: time.Now,
	}
	expectedHash := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}, nil, ""), 0)

	pathway, _ := PathwayFromContext(processor.SetCheckpoint(context.Background(), "direction:in", "type:kafka"))
	statsPt := processor.in.pop().point

	assert.Equal(t, expectedHash, statsPt.hash)
	assert.Equal(t, expectedHash, pathway.GetHash())
}

func TestKafkaLag(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp1 := time.Now()
	p.addKafkaOffset(kafkaOffset{offset: 1, topic: "topic1", partition: 1, group: "group1", offsetType: commitOffset})
	p.addKafkaOffset(kafkaOffset{offset: 10, topic: "topic2", partition: 1, group: "group1", offsetType: commitOffset})
	p.addKafkaOffset(kafkaOffset{offset: 5, topic: "topic1", partition: 1, offsetType: produceOffset})
	p.addKafkaOffset(kafkaOffset{offset: 15, topic: "topic1", partition: 1, offsetType: produceOffset})
	payloads := sortedPayloads(p.flush(tp1.Add(bucketDuration * 2)))
	expectedBacklogs := []Backlog{
		{
			Tags:  []string{"consumer_group:group1", "partition:1", "topic:topic1", "type:kafka_commit"},
			Value: 1,
		},
		{
			Tags:  []string{"consumer_group:group1", "partition:1", "topic:topic2", "type:kafka_commit"},
			Value: 10,
		},
		{
			Tags:  []string{"partition:1", "topic:topic1", "type:kafka_produce"},
			Value: 15,
		},
	}
	assert.Equal(t, expectedBacklogs, payloads["service"].Stats[0].Backlogs)
}

func TestTrackTransaction(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp := time.Now().Truncate(bucketDuration)

	p.addTransaction(transactionEntry{
		transactionID:  "tx-1",
		checkpointName: "ingested",
		timestamp:      tp.UnixNano(),
	})
	p.addTransaction(transactionEntry{
		transactionID:  "tx-2",
		checkpointName: "processed",
		timestamp:      tp.UnixNano(),
	})
	p.addTransaction(transactionEntry{
		transactionID:  "tx-3",
		checkpointName: "ingested", // same checkpoint as tx-1; should reuse ID 1
		timestamp:      tp.UnixNano(),
	})

	payloads := p.flush(tp.Add(bucketDuration * 2))

	// Transactions are keyed by p.service in tsTypeCurrentBuckets; they show
	// up in the payload for the service bucket.
	var found *StatsBucket
	for _, payload := range payloads {
		for i := range payload.Stats {
			if len(payload.Stats[i].Transactions) > 0 {
				found = &payload.Stats[i]
				break
			}
		}
	}
	require.NotNil(t, found, "expected a bucket containing Transactions")

	// Verify two distinct checkpoint IDs were registered.
	assert.NotEmpty(t, found.TransactionCheckpointIds)

	// Verify transaction blob is non-empty and contains three records.
	// Each record: 1 (checkpointId) + 8 (timestamp) + 1 (idLen) + len(id)
	// tx-1: 1+8+1+4 = 14 bytes; tx-2: 1+8+1+4 = 14 bytes; tx-3: 1+8+1+4 = 14 bytes
	assert.Equal(t, 42, len(found.Transactions))

	// First record: checkpointId=1 ("ingested"), transactionID="tx-1"
	assert.Equal(t, byte(1), found.Transactions[0], "first checkpoint ID should be 1 (ingested)")
	// Skip timestamp (bytes 1-8)
	assert.Equal(t, byte(4), found.Transactions[9], "id length should be 4")
	assert.Equal(t, "tx-1", string(found.Transactions[10:14]))

	// Third record starts at offset 14+14=28: checkpointId=1 again ("ingested")
	// Layout: [checkpointId=28][timestamp=29..36][idLen=37][id=38..41]
	assert.Equal(t, byte(1), found.Transactions[28], "third record should reuse checkpoint ID 1")
	assert.Equal(t, "tx-3", string(found.Transactions[38:42]))
}

// TestTrackTransactionHighVolume ensures that transaction records are never
// dropped regardless of volume. Previously a 1 MiB cap caused the majority of
// records to be silently dropped at high throughput (e.g. 25k TPS).
func TestTrackTransactionHighVolume(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp := time.Now().Truncate(bucketDuration)

	const count = 30_000
	for i := range count {
		p.addTransaction(transactionEntry{
			transactionID:  fmt.Sprintf("tx-%d", i),
			checkpointName: "ingested",
			timestamp:      tp.UnixNano(),
		})
	}

	payloads := p.flush(tp.Add(bucketDuration * 2))

	var totalBytes int
	for _, payload := range payloads {
		for _, bucket := range payload.Stats {
			totalBytes += len(bucket.Transactions)
		}
	}

	// Each record: 1 (checkpointId) + 8 (timestamp) + 1 (idLen) + len("tx-N")
	// IDs range from "tx-0" (4 bytes) to "tx-29999" (8 bytes); minimum per record is 14 bytes.
	minExpected := count * 14 // conservative lower bound using shortest possible ID
	assert.GreaterOrEqual(t, totalBytes, minExpected,
		"all %d transaction records should be present; got %d bytes, want at least %d", count, totalBytes, minExpected)
}

func TestCheckpointRegistry(t *testing.T) {
	r := newCheckpointRegistry()

	id1, ok1 := r.getOrAssign("alpha")
	id2, ok2 := r.getOrAssign("beta")
	id3, ok3 := r.getOrAssign("alpha") // should return same id as id1

	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.True(t, ok3)
	assert.Equal(t, byte(1), id1)
	assert.Equal(t, byte(2), id2)
	assert.Equal(t, id1, id3, "same checkpoint name should return same ID")

	// encodedKeys format: [id][nameLen][name bytes] for each unique name
	// "alpha": [1][5][a,l,p,h,a], "beta": [2][4][b,e,t,a]
	expected := []byte{1, 5, 'a', 'l', 'p', 'h', 'a', 2, 4, 'b', 'e', 't', 'a'}
	assert.Equal(t, expected, r.encodedKeys)
}

func TestCheckpointRegistryOverflow(t *testing.T) {
	r := newCheckpointRegistry()
	// Simulate a full registry by setting nextID to the sentinel boundary.
	r.nextID = math.MaxUint8

	id, ok := r.getOrAssign("overflow")
	assert.Equal(t, byte(0), id)
	assert.False(t, ok, "should refuse to assign when registry is full")

	// Confirm the name was not registered.
	_, existed := r.nameToID["overflow"]
	assert.False(t, existed, "name must not be added to nameToID when overflow is detected")

	// A previously registered name should still be returned successfully.
	r.nextID = 1
	r.nameToID["existing"] = 1
	id2, ok2 := r.getOrAssign("existing")
	assert.True(t, ok2)
	assert.Equal(t, byte(1), id2)
}

func TestTransactionBytes(t *testing.T) {
	ts := int64(1700000000000000000)
	b := appendTransactionBytes(nil, 3, ts, "my-tx")
	require.Len(t, b, 1+8+1+5)

	assert.Equal(t, byte(3), b[0])

	var gotTS int64
	gotTS = int64(uint64(b[1])<<56 | uint64(b[2])<<48 | uint64(b[3])<<40 | uint64(b[4])<<32 |
		uint64(b[5])<<24 | uint64(b[6])<<16 | uint64(b[7])<<8 | uint64(b[8]))
	assert.Equal(t, ts, gotTS)

	assert.Equal(t, byte(5), b[9])
	assert.Equal(t, "my-tx", string(b[10:]))
}

func TestTrackTransactionViaMethod(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	fixedTime := time.Now().Truncate(bucketDuration)
	p.timeSource = func() time.Time { return fixedTime }

	// Push via the public method which goes through the fast queue.
	p.TrackTransaction("tx-abc", "delivered")

	// processInput processes it directly without starting the goroutine.
	in := p.in.pop()
	require.NotNil(t, in)
	assert.Equal(t, pointTypeTransaction, in.typ)
	assert.Equal(t, "tx-abc", in.transactionEntry.transactionID)
	assert.Equal(t, "delivered", in.transactionEntry.checkpointName)
	assert.Equal(t, fixedTime.UnixNano(), in.transactionEntry.timestamp)

	p.processInput(in)
	payloads := p.flush(fixedTime.Add(bucketDuration * 2))

	var found *StatsBucket
	for _, payload := range payloads {
		for i := range payload.Stats {
			if len(payload.Stats[i].Transactions) > 0 {
				found = &payload.Stats[i]
				break
			}
		}
	}
	require.NotNil(t, found)
	assert.NotEmpty(t, found.Transactions)
	assert.NotEmpty(t, found.TransactionCheckpointIds)
}

func TestKafkaLagWithCluster(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp1 := time.Now()
	p.addKafkaOffset(kafkaOffset{offset: 1, topic: "topic1", partition: 1, group: "group1", offsetType: commitOffset, cluster: "cluster-1"})
	p.addKafkaOffset(kafkaOffset{offset: 10, topic: "topic2", partition: 1, group: "group1", offsetType: commitOffset, cluster: "cluster-1"})
	p.addKafkaOffset(kafkaOffset{offset: 5, topic: "topic1", partition: 1, offsetType: produceOffset, cluster: "cluster-1"})
	p.addKafkaOffset(kafkaOffset{offset: 15, topic: "topic1", partition: 1, offsetType: produceOffset, cluster: "cluster-1"})
	p.addKafkaOffset(kafkaOffset{offset: 20, topic: "topic1", partition: 1, offsetType: highWatermarkOffset, cluster: "cluster-1"})
	payloads := sortedPayloads(p.flush(tp1.Add(bucketDuration * 2)))
	expectedBacklogs := []Backlog{
		{
			Tags:  []string{"consumer_group:group1", "partition:1", "topic:topic1", "type:kafka_commit", "kafka_cluster_id:cluster-1"},
			Value: 1,
		},
		{
			Tags:  []string{"consumer_group:group1", "partition:1", "topic:topic2", "type:kafka_commit", "kafka_cluster_id:cluster-1"},
			Value: 10,
		},
		{
			Tags:  []string{"partition:1", "topic:topic1", "type:kafka_high_watermark", "kafka_cluster_id:cluster-1"},
			Value: 20,
		},
		{
			Tags:  []string{"partition:1", "topic:topic1", "type:kafka_produce", "kafka_cluster_id:cluster-1"},
			Value: 15,
		},
	}
	assert.Equal(t, expectedBacklogs, payloads["service"].Stats[0].Backlogs)
}

// TestTrackTransactionAtUsesProvidedTime verifies that TrackTransactionAt stores the
// caller-supplied timestamp rather than the processor's clock.
func TestTrackTransactionAtUsesProvidedTime(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	// Set the processor clock to a different time to confirm it is not used.
	processorTime := time.Now().Truncate(bucketDuration)
	p.timeSource = func() time.Time { return processorTime }

	customTime := processorTime.Add(-5 * time.Minute)
	p.TrackTransactionAt("tx-custom", "ingested", customTime)

	in := p.in.pop()
	require.NotNil(t, in)
	assert.Equal(t, pointTypeTransaction, in.typ)
	assert.Equal(t, "tx-custom", in.transactionEntry.transactionID)
	assert.Equal(t, "ingested", in.transactionEntry.checkpointName)
	// The stored timestamp must match the caller-supplied time, not the processor clock.
	assert.Equal(t, customTime.UnixNano(), in.transactionEntry.timestamp)

	p.processInput(in)
	payloads := p.flush(customTime.Add(bucketDuration * 2))

	var found *StatsBucket
	for _, payload := range payloads {
		for i := range payload.Stats {
			if len(payload.Stats[i].Transactions) > 0 {
				found = &payload.Stats[i]
				break
			}
		}
	}
	require.NotNil(t, found, "expected a bucket containing transactions for the custom timestamp")
	assert.NotEmpty(t, found.Transactions)
}

type noOpTransport struct{}

// RoundTrip does nothing and returns a dummy response.
func (t *noOpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// You can customize the dummy response if needed.
	return &http.Response{
		StatusCode:    200,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Request:       req,
		ContentLength: -1,
		Body:          http.NoBody,
	}, nil
}

func TestTransactionBytesLongID(t *testing.T) {
	longID := strings.Repeat("x", 300)
	b := appendTransactionBytes(nil, 1, 0, longID)
	// Record layout: [checkpointId uint8][timestamp int64][idLen uint8][id bytes]
	// ID must be capped at 255 bytes.
	require.Equal(t, byte(255), b[9], "idLen field should be capped at 255")
	require.Len(t, b, 1+8+1+255, "total record length should reflect the 255-byte cap")
}

func TestCheckpointRegistryLongName(t *testing.T) {
	r := newCheckpointRegistry()
	longName := strings.Repeat("n", 300)
	id, ok := r.getOrAssign(longName)
	assert.True(t, ok)
	assert.Equal(t, byte(1), id)
	// Encoded layout: [id uint8][nameLen uint8][name bytes].
	// Name must be truncated to 255 bytes.
	require.Len(t, r.encodedKeys, 1+1+255)
	assert.Equal(t, byte(255), r.encodedKeys[1], "nameLen field should be capped at 255")
}

func TestAddTransactionFullRegistry(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	// Fill the registry to capacity so getOrAssign returns (0, false).
	p.checkpoints.nextID = math.MaxUint8

	ts := time.Now().Truncate(bucketDuration)
	p.addTransaction(transactionEntry{
		transactionID:  "tx-1",
		checkpointName: "overflow",
		timestamp:      ts.UnixNano(),
	})

	// The bucket should exist but carry no transaction bytes.
	payloads := p.flush(ts.Add(bucketDuration * 2))
	for _, payload := range payloads {
		for _, bucket := range payload.Stats {
			assert.Empty(t, bucket.Transactions, "no transactions should be recorded when the registry is full")
		}
	}
}

func TestTransactionBytesPerPeriodLimit(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp := time.Now().Truncate(bucketDuration)

	// Use UUID-length IDs (36 bytes) so each record is exactly 46 bytes.
	// The budget is maxTransactionBytesPerPeriod = 2,300,000 bytes.
	// 2,300,000 / 46 = 50,000 records fit within budget.
	const recordSize = 46
	maxRecords := int(maxTransactionBytesPerPeriod / recordSize)
	totalAttempted := maxRecords + 5000

	for i := range totalAttempted {
		id := fmt.Sprintf("xxxxxxxx-xxxx-xxxx-xxxx-%012d", i) // 36-byte UUID-shaped ID
		p.addTransaction(transactionEntry{
			transactionID:  id,
			checkpointName: "ingested",
			timestamp:      tp.UnixNano(),
		})
	}

	// Verify the dropped count matches the overshoot.
	droppedCount := p.stats.droppedTransactions.Load()
	assert.Equal(t, int64(totalAttempted-maxRecords), droppedCount,
		"expected %d dropped transactions", totalAttempted-maxRecords)

	// Verify the period budget is at capacity.
	assert.LessOrEqual(t, p.txnBytesThisPeriod, int64(maxTransactionBytesPerPeriod),
		"period bytes should not exceed the budget")
}

func TestTransactionBytesPerPeriodResetsOnNewPeriod(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp := time.Now().Truncate(bucketDuration)

	// Fill most of the budget in period 1.
	for i := range 40_000 {
		id := fmt.Sprintf("xxxxxxxx-xxxx-xxxx-xxxx-%012d", i)
		p.addTransaction(transactionEntry{
			transactionID:  id,
			checkpointName: "ingested",
			timestamp:      tp.UnixNano(),
		})
	}
	assert.Greater(t, p.txnBytesThisPeriod, int64(0))

	// Move to the next bucket period — budget should reset.
	tp2 := tp.Add(bucketDuration)
	p.addTransaction(transactionEntry{
		transactionID:  "first-in-new-period",
		checkpointName: "ingested",
		timestamp:      tp2.UnixNano(),
	})

	expectedSize := int64(10 + len("first-in-new-period"))
	assert.Equal(t, expectedSize, p.txnBytesThisPeriod,
		"budget should reset when the bucket period rolls over")
}

func BenchmarkSetCheckpoint(b *testing.B) {
	client := &http.Client{
		Transport: &noOpTransport{},
	}
	p := NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
	p.Start()
	for b.Loop() {
		p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
	}
	p.Stop()
}

func BenchmarkSetCheckpointProcessTags(b *testing.B) {
	processtags.Reload()

	client := &http.Client{
		Transport: &noOpTransport{},
	}
	p := NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
	p.Start()
	for b.Loop() {
		p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
	}
	p.Stop()
}

// latencyTransport answers after a fixed delay, standing in for an agent that
// is slow to accept pipeline stats.
type latencyTransport struct {
	noOpTransport
	latency time.Duration
}

func (t *latencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	time.Sleep(t.latency)
	return t.noOpTransport.RoundTrip(req)
}

// droppedCounter accumulates the dropped_payloads counts that reportStats
// swaps out of the processor every 10s, so a benchmark that runs longer than
// one reporting interval doesn't lose them.
type droppedCounter struct {
	statsd.NoOpClientDirect
	dropped atomic.Int64
}

func (c *droppedCounter) Count(name string, value int64, _ []string, _ float64) error {
	if name == "datadog.datastreams.processor.dropped_payloads" {
		c.dropped.Add(value)
	}
	return nil
}

const (
	// benchFlushInterval compresses the production flush cadence
	// (bucketDuration, 10s) so a sub-second benchmark still observes several
	// agent calls. It is held constant across sub-benchmarks so that agent
	// latency is the only variable.
	benchFlushInterval = 20 * time.Millisecond

	// benchPacedRate is the checkpoint rate, in calls per second, of the
	// "paced" sub-benchmarks. It has to sit comfortably below what the single
	// run goroutine can consume, so that drops are attributable to the agent
	// stall rather than to a permanently overflowing queue. At this rate the
	// queue (defaultQueueSize, 10000) holds roughly 50ms worth of checkpoints,
	// so loss is expected to begin somewhere between the 10ms and 100ms cases.
	benchPacedRate = 200_000

	// benchPacingBatch is how many checkpoints are pushed between pacing
	// sleeps. At benchPacedRate it corresponds to ~1.3ms, which is around the
	// floor of what time.Sleep can resolve.
	benchPacingBatch = 256
)

// BenchmarkSetCheckpointSlowAgent measures checkpoint loss when the agent is
// slow to respond. (*Processor).run pops the input queue and calls sendToAgent
// from the same goroutine, so an in-flight agent request stops queue
// consumption entirely, while fastQueue.push never blocks its caller and
// silently overwrites unread slots once it wraps. The cost therefore shows up
// as dropped checkpoints, not as a slower SetCheckpointWithParams: drops/op is
// the metric of interest here and ns/op is not meaningful (in the paced cases
// it mostly measures the pacing sleep).
//
// The "paced" cases push below the consumer's capacity, isolating the loss
// caused by the agent stall. The "saturated" cases push as fast as the
// caller can, which overruns the queue at every latency including zero, and
// so measures the consumer's own throughput ceiling rather than the stall.
func BenchmarkSetCheckpointSlowAgent(b *testing.B) {
	latencies := []time.Duration{0, time.Millisecond, 10 * time.Millisecond, 100 * time.Millisecond}
	for _, mode := range []struct {
		name string
		rate float64 // pushes per second; 0 means push as fast as possible
	}{
		{"paced", benchPacedRate},
		{"saturated", 0},
	} {
		for _, latency := range latencies {
			b.Run(mode.name+"/"+latency.String(), func(b *testing.B) {
				stats := &droppedCounter{}
				client := &http.Client{Transport: &latencyTransport{latency: latency}}
				p := NewProcessor(stats, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
				p.Start()

				// Flush from a dedicated goroutine: in production nothing
				// blocks the caller of SetCheckpoint, the run goroutine merely
				// stops popping while the agent call is in flight. Calling
				// p.Flush from the measured loop would serialize the two and
				// model the wrong thing.
				stopFlushing := make(chan struct{})
				flusherDone := make(chan struct{})
				var flushes atomic.Int64
				go func() {
					defer close(flusherDone)
					tick := time.NewTicker(benchFlushInterval)
					defer tick.Stop()
					for {
						select {
						case <-stopFlushing:
							return
						case <-tick.C:
							p.Flush()
							flushes.Add(1)
						}
					}
				}()

				var pushed int64
				start := time.Now()
				for b.Loop() {
					p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
					pushed++
					if mode.rate > 0 && pushed%benchPacingBatch == 0 {
						due := time.Duration(float64(pushed) / mode.rate * float64(time.Second))
						if ahead := due - time.Since(start); ahead > 0 {
							time.Sleep(ahead)
						}
					}
				}

				elapsed := time.Since(start)

				close(stopFlushing)
				<-flusherDone
				p.Stop()

				dropped := stats.dropped.Load() + p.stats.dropped.Load()
				b.ReportMetric(float64(dropped)/float64(pushed), "drops/op")
				b.ReportMetric(100*float64(dropped)/float64(pushed), "%drops")
				b.ReportMetric(float64(pushed)/elapsed.Seconds(), "pushes/s")
				b.ReportMetric(float64(flushes.Load()), "flushes")
			})
		}
	}
}

// benchSustainedRates are offered rates the processor's single consumer can
// keep up with. That is the regime BenchmarkSetCheckpointSlowAgent cannot
// show: its saturated cases pin the queue full, which leaves the consumer
// reading the slot the producer is overwriting, and cost there is dominated
// by the cache line those two fight over rather than by the work a checkpoint
// actually does.
var benchSustainedRates = []float64{50_000, 200_000, 1_000_000}

// BenchmarkSetCheckpointSustained reports what one checkpoint costs at a rate
// the processor keeps up with.
//
// Read ns/push, not ns/op: the benchmark timer spans the pacing sleeps too,
// while ns/push is accumulated only across batches of calls. drops/op is the
// control -- it has to stay at zero, otherwise the offered rate outran the
// consumer and the case is measuring saturation after all.
func BenchmarkSetCheckpointSustained(b *testing.B) {
	for _, rate := range benchSustainedRates {
		for _, producers := range []int{1, 8, 64} {
			name := strconv.Itoa(int(rate)/1000) + "k/s/" + strconv.Itoa(producers) + "-producers"
			b.Run(name, func(b *testing.B) {
				stats := &droppedCounter{}
				client := &http.Client{Transport: &noOpTransport{}}
				p := NewProcessor(stats, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
				p.Start()

				var pushNanos, pushed atomic.Int64
				perProducer := rate / float64(producers)
				// Pace in roughly 1ms units whatever the producer count: with
				// a fixed batch, a producer's share of a low rate turns into a
				// sleep of hundreds of milliseconds, and what the timer then
				// samples is mostly cold caches rather than the call.
				batchSize := int64(perProducer / 1000)
				batchSize = max(1, min(int64(benchPacingBatch), batchSize))
				var wg sync.WaitGroup
				start := time.Now()
				for w := range producers {
					// b.N is split across producers rather than using
					// b.Loop, which a single goroutine has to own.
					iters := int64(b.N / producers)
					if w < b.N%producers {
						iters++
					}
					wg.Go(func() {
						start := time.Now()
						var done int64
						var timed time.Duration
						for done < iters {
							batch := min(batchSize, iters-done)
							t0 := time.Now()
							for range batch {
								p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
							}
							timed += time.Since(t0)
							done += batch
							due := time.Duration(float64(done) / perProducer * float64(time.Second))
							if ahead := due - time.Since(start); ahead > 0 {
								time.Sleep(ahead)
							}
						}
						pushNanos.Add(int64(timed))
						pushed.Add(done)
					})
				}
				wg.Wait()
				elapsed := time.Since(start)
				// Flush before Stop so any checkpoints still sitting in the
				// queue when producers finished get processed rather than
				// silently discarded: Stop's stop-case flushes only buckets
				// already built from processed input, not the raw queue, so
				// without this, leftover backlog would vanish without
				// incrementing dropped and drops/op would understate loss.
				p.Flush()
				p.Stop()

				n := pushed.Load()
				dropped := stats.dropped.Load() + p.stats.dropped.Load()
				b.ReportMetric(float64(pushNanos.Load())/float64(n), "ns/push")
				b.ReportMetric(float64(dropped)/float64(n), "drops/op")
				b.ReportMetric(float64(n)/elapsed.Seconds(), "pushes/s")
			})
		}
	}
}
