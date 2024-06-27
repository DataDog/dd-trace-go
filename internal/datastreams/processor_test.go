// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/stretchr/testify/assert"
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

func TestProcessor(t *testing.T) {
	p := NewProcessor(nil, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, nil)
	tp1 := time.Now().Truncate(bucketDuration)
	tp2 := tp1.Add(time.Minute)

	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: time.Second.Nanoseconds(),
		edgeLatency:    time.Second.Nanoseconds(),
		payloadSize:    1,
	})
	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
		payloadSize:    2,
	})
	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           3,
		parentHash:     1,
		timestamp:      tp2.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
		payloadSize:    2,
	})
	p.add(statsPoint{
		edgeTags:       []string{"type:edge-1"},
		hash:           2,
		parentHash:     1,
		timestamp:      tp1.UnixNano(),
		pathwayLatency: (5 * time.Second).Nanoseconds(),
		edgeLatency:    (2 * time.Second).Nanoseconds(),
		payloadSize:    2,
	})
	got := p.flush(tp1.Add(bucketDuration))
	sort.Slice(got.Stats, func(i, j int) bool {
		return got.Stats[i].Start < got.Stats[j].Start
	})
	assert.Len(t, got.Stats, 2)
	assert.Equal(t, StatsPayload{
		Env:     "env",
		Service: "service",
		Version: "v1",
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
	}, got)

	sp := p.flush(tp2.Add(bucketDuration))
	sort.Slice(sp.Stats, func(i, j int) bool {
		return sp.Stats[i].Start < sp.Stats[j].Start
	})
	for k := range sp.Stats {
		sort.Slice(sp.Stats[k].Stats, func(i, j int) bool {
			return sp.Stats[k].Stats[i].Hash < sp.Stats[k].Stats[j].Hash
		})
	}
	assert.Equal(t, StatsPayload{
		Env:     "env",
		Service: "service",
		Version: "v1",
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
	}, sp)
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
	hash1 := pathwayHash(nodeHash("service-1", "env", []string{"direction:in", "type:kafka"}), 0)
	hash2 := pathwayHash(nodeHash("service-1", "env", []string{"direction:out", "type:kafka"}), hash1)

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
	point := p.flush(tp1.Add(bucketDuration * 2))
	sort.Slice(point.Stats[0].Backlogs, func(i, j int) bool {
		return strings.Join(point.Stats[0].Backlogs[i].Tags, "") < strings.Join(point.Stats[0].Backlogs[j].Tags, "")
	})
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
	assert.Equal(t, expectedBacklogs, point.Stats[0].Backlogs)
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

func BenchmarkSetCheckpoint(b *testing.B) {
	client := &http.Client{
		Transport: &noOpTransport{},
	}
	p := NewProcessor(&statsd.NoOpClient{}, "env", "service", "v1", &url.URL{Scheme: "http", Host: "agent-address"}, client)
	p.Start()
	for i := 0; i < b.N; i++ {
		p.SetCheckpointWithParams(context.Background(), options.CheckpointParams{PayloadSize: 1000}, "type:edge-1", "direction:in", "type:kafka", "topic:topic1", "group:group1")
	}
	p.Stop()
}
