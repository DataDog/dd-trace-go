// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
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

type blockingTransport struct {
	release chan struct{}
}

func (t *blockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-t.release
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

func TestRecordDropAttribution(t *testing.T) {
	p := NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)

	p.readerState.Store(int32(readerStalledOnAgent))
	p.recordDrop()
	assert.Equal(t, int64(1), p.stats.dropped.Load())
	assert.Equal(t, int64(1), p.stats.droppedAgentStall.Load())
	assert.Equal(t, int64(0), p.stats.droppedPollStall.Load())

	p.readerState.Store(int32(readerStalledOnEmptyQueue))
	p.recordDrop()
	assert.Equal(t, int64(2), p.stats.dropped.Load())
	assert.Equal(t, int64(1), p.stats.droppedAgentStall.Load())
	assert.Equal(t, int64(1), p.stats.droppedPollStall.Load())

	p.readerState.Store(int32(readerProcessing))
	p.recordDrop()
	assert.Equal(t, int64(3), p.stats.dropped.Load())
	assert.Equal(t, int64(1), p.stats.droppedAgentStall.Load())
	assert.Equal(t, int64(1), p.stats.droppedPollStall.Load())
}

func TestRunMarksReaderStalledOnEmptyQueue(t *testing.T) {
	p := NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	p.stop = make(chan struct{})
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		p.run(tick)
		close(done)
	}()

	assert.Eventually(t, func() bool {
		return readerState(p.readerState.Load()) == readerStalledOnEmptyQueue
	}, time.Second, time.Millisecond)

	close(p.stop)
	<-done
}

func TestRunMarksReaderStalledOnAgentCall(t *testing.T) {
	release := make(chan struct{})
	client := &http.Client{Transport: &blockingTransport{release: release}}
	p := NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
	tp := time.Now().Truncate(bucketDuration).Add(-2 * bucketDuration)
	p.add(statsPoint{serviceName: "service1", hash: 1, timestamp: tp.UnixNano()})

	p.stop = make(chan struct{})
	tick := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		p.run(tick)
		close(done)
	}()
	tick <- time.Now()

	assert.Eventually(t, func() bool {
		return readerState(p.readerState.Load()) == readerStalledOnAgent
	}, time.Second, time.Millisecond)

	close(release)
	close(p.stop)
	<-done
}

func TestReportStatsEmitsDropStallMetrics(t *testing.T) {
	statsdClient := &statsdtest.TestStatsdClient{}
	p := NewProcessor(statsdClient, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	p.stop = make(chan struct{})
	p.stats.dropped.Store(3)
	p.stats.droppedAgentStall.Store(2)
	p.stats.droppedPollStall.Store(1)

	tick := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		p.reportStats(tick)
		close(done)
	}()
	tick <- time.Now()

	require.NoError(t, statsdClient.Wait(assert.New(t), 7, time.Second))
	close(p.stop)
	<-done

	counts := statsdClient.Counts()
	assert.Equal(t, int64(3), counts["datadog.datastreams.processor.dropped_payloads"])
	assert.Equal(t, int64(2), counts["datadog.datastreams.processor.dropped_payloads_agent_stall"])
	assert.Equal(t, int64(1), counts["datadog.datastreams.processor.dropped_payloads_poll_stall"])
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

func BenchmarkSetCheckpointParallel(b *testing.B) {
	client := &http.Client{
		Transport: &noOpTransport{},
	}
	p := NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
	p.Start()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
		}
	})
	p.Stop()
}

// startBenchReader runs only the reader loop, without the stats reporter, so
// that p.stats counters are not reset mid-benchmark.
func startBenchReader(b *testing.B) *Processor {
	client := &http.Client{
		Transport: &noOpTransport{},
	}
	p := NewProcessor(&statsd.NoOpClientDirect{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
	p.stop = make(chan struct{})
	done := make(chan struct{})
	go func() {
		p.run(make(chan time.Time))
		close(done)
	}()
	b.Cleanup(func() {
		close(p.stop)
		<-done
	})
	return p
}

// BenchmarkReaderWakeLatency measures how long an idle reader takes to pick
// up a single payload.
func BenchmarkReaderWakeLatency(b *testing.B) {
	p := startBenchReader(b)
	for b.Loop() {
		want := p.stats.payloadsIn.Load() + 1
		p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
		for p.stats.payloadsIn.Load() < want {
		}
	}
}

// BenchmarkReaderSustainedRate has writers push at a fixed aggregate rate and
// reports the percentage of payloads dropped. Each op is a 20ms window.
func BenchmarkReaderSustainedRate(b *testing.B) {
	const (
		writers = 8
		window  = 20 * time.Millisecond
		batch   = 64
	)
	for _, rate := range []int{500_000, 1_000_000, 2_000_000, 4_000_000} {
		b.Run(fmt.Sprintf("rate=%dk/s", rate/1000), func(b *testing.B) {
			p := startBenchReader(b)
			perWriter := float64(rate) / writers
			var pushed int64
			for b.Loop() {
				var wg sync.WaitGroup
				start := time.Now()
				for range writers {
					wg.Go(func() {
						n := 0
						for {
							elapsed := time.Since(start)
							if elapsed >= window {
								break
							}
							// Busy-wait rather than sleep so the writer keeps to its
							// schedule at sub-millisecond granularity.
							if float64(n) >= perWriter*elapsed.Seconds() {
								continue
							}
							for range batch {
								p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
							}
							n += batch
						}
						atomic.AddInt64(&pushed, int64(n))
					})
				}
				wg.Wait()
			}
			b.ReportMetric(100*float64(p.stats.dropped.Load())/float64(pushed), "drop-%")
			b.ReportMetric(float64(pushed)/float64(b.N), "pushed/op")
		})
	}
}
