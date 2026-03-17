// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
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
		tp := time.Now().Truncate(bucketDuration)
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
						Start:    uint64(tp1.Add(-10 * time.Second).UnixNano()),
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
						Start:    uint64(tp1.UnixNano()),
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
	hash1 := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}, processTags), 0)
	hash2 := pathwayHash(nodeHash("service-1", "env", []string{"direction:out", "type:kafka"}, processTags), hash1)

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
	hash1 := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}, pTags), 0)
	hash2 := pathwayHash(nodeHash("service-1", "env", []string{"direction:out", "type:kafka"}, pTags), hash1)

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
