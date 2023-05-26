// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/golang/protobuf/proto"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

const (
	bucketDuration     = time.Second * 10
	defaultServiceName = "unnamed-go-service"
)

var sketchMapping, _ = mapping.NewLogarithmicMapping(0.01)

type statsPoint struct {
	edgeTags       []string
	hash           uint64
	parentHash     uint64
	timestamp      int64
	pathwayLatency int64
	edgeLatency    int64
}

type statsGroup struct {
	service        string
	edgeTags       []string
	hash           uint64
	parentHash     uint64
	pathwayLatency *ddsketch.DDSketch
	edgeLatency    *ddsketch.DDSketch
}

type bucket struct {
	points               map[uint64]statsGroup
	latestCommitOffsets  map[partitionConsumerKey]int64
	latestProduceOffsets map[partitionKey]int64
	start                uint64
	duration             uint64
}

func newBucket(start, duration uint64) bucket {
	return bucket{
		points:               make(map[uint64]statsGroup),
		latestCommitOffsets:  make(map[partitionConsumerKey]int64),
		latestProduceOffsets: make(map[partitionKey]int64),
		start:                start,
		duration:             duration,
	}
}

func (b bucket) export(timestampType TimestampType) StatsBucket {
	stats := make([]StatsPoint, 0, len(b.points))
	for _, s := range b.points {
		pathwayLatency, err := proto.Marshal(s.pathwayLatency.ToProto())
		if err != nil {
			log.Printf("ERROR: can't serialize pathway latency. Ignoring: %v", err)
			continue
		}
		edgeLatency, err := proto.Marshal(s.edgeLatency.ToProto())
		if err != nil {
			log.Printf("ERROR: can't serialize edge latency. Ignoring: %v", err)
			continue
		}
		stats = append(stats, StatsPoint{
			PathwayLatency: pathwayLatency,
			EdgeLatency:    edgeLatency,
			Service:        s.service,
			EdgeTags:       s.edgeTags,
			Hash:           s.hash,
			ParentHash:     s.parentHash,
			TimestampType:  timestampType,
		})
	}
	exported := StatsBucket{
		Start:    b.start,
		Duration: b.duration,
		Stats:    stats,
		Backlogs: make([]Backlog, 0, len(b.latestCommitOffsets)+len(b.latestProduceOffsets)),
	}
	for key, offset := range b.latestProduceOffsets {
		exported.Backlogs = append(exported.Backlogs, Backlog{Tags: []string{fmt.Sprintf("partition:%d", key.partition), fmt.Sprintf("topic:%s", key.topic), "type:kafka_produce"}, Value: offset})
	}
	for key, offset := range b.latestCommitOffsets {
		exported.Backlogs = append(exported.Backlogs, Backlog{Tags: []string{fmt.Sprintf("consumer_group:%s", key.group), fmt.Sprintf("partition:%d", key.partition), fmt.Sprintf("topic:%s", key.topic), "type:kafka_commit"}, Value: offset})
	}
	return exported
}

type aggregatorStats struct {
	payloadsIn      int64
	flushedPayloads int64
	flushedBuckets  int64
	flushErrors     int64
	dropped         int64
}

type partitionKey struct {
	partition int32
	topic     string
}

type partitionConsumerKey struct {
	partition int32
	topic     string
	group     string
}

type offsetType int

const (
	produceOffset offsetType = iota
	commitOffset
)

type kafkaOffset struct {
	offset     int64
	topic      string
	group      string
	partition  int32
	offsetType offsetType
	timestamp  int64
}

type aggregator struct {
	in                   chan statsPoint
	inKafka              chan kafkaOffset
	tsTypeCurrentBuckets map[int64]bucket
	tsTypeOriginBuckets  map[int64]bucket
	wg                   sync.WaitGroup
	stopped              uint64
	stop                 chan struct{} // closing this channel triggers shutdown
	stats                aggregatorStats
	transport            *httpTransport
	statsd               statsd.ClientInterface
	env                  string
	primaryTag           string
	service              string
}

func newAggregator(statsd statsd.ClientInterface, env, primaryTag, service, agentAddr string, httpClient *http.Client, site, apiKey string, agentLess bool) *aggregator {
	return &aggregator{
		tsTypeCurrentBuckets: make(map[int64]bucket),
		tsTypeOriginBuckets:  make(map[int64]bucket),
		in:                   make(chan statsPoint, 10000),
		inKafka:              make(chan kafkaOffset, 10000),
		stopped:              1,
		statsd:               statsd,
		env:                  env,
		primaryTag:           primaryTag,
		service:              service,
		transport:            newHTTPTransport(agentAddr, site, apiKey, httpClient, agentLess),
	}
}

// alignTs returns the provided timestamp truncated to the bucket size.
// It gives us the start time of the time bucket in which such timestamp falls.
func alignTs(ts, bucketSize int64) int64 { return ts - ts%bucketSize }

func (a *aggregator) getBucket(btime int64, buckets map[int64]bucket) bucket {
	b, ok := buckets[btime]
	if !ok {
		b = newBucket(uint64(btime), uint64(bucketDuration.Nanoseconds()))
		buckets[btime] = b
	}
	return b
}
func (a *aggregator) addToBuckets(point statsPoint, btime int64, buckets map[int64]bucket) {
	b := a.getBucket(btime, buckets)
	group, ok := b.points[point.hash]
	if !ok {
		group = statsGroup{
			edgeTags:       point.edgeTags,
			parentHash:     point.parentHash,
			hash:           point.hash,
			pathwayLatency: ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
			edgeLatency:    ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
		}
		b.points[point.hash] = group
	}
	if err := group.pathwayLatency.Add(math.Max(float64(point.pathwayLatency)/float64(time.Second), 0)); err != nil {
		log.Printf("ERROR: failed to add pathway latency. Ignoring %v.", err)
	}
	if err := group.edgeLatency.Add(math.Max(float64(point.edgeLatency)/float64(time.Second), 0)); err != nil {
		log.Printf("ERROR: failed to add edge latency. Ignoring %v.", err)
	}
}

func (a *aggregator) add(point statsPoint) {
	currentBucketTime := alignTs(point.timestamp, bucketDuration.Nanoseconds())
	a.addToBuckets(point, currentBucketTime, a.tsTypeCurrentBuckets)
	originTimestamp := point.timestamp - point.pathwayLatency
	originBucketTime := alignTs(originTimestamp, bucketDuration.Nanoseconds())
	a.addToBuckets(point, originBucketTime, a.tsTypeOriginBuckets)
}

func (a *aggregator) addKafkaOffset(o kafkaOffset) {
	btime := alignTs(o.timestamp, bucketDuration.Nanoseconds())
	b := a.getBucket(btime, a.tsTypeCurrentBuckets)
	if o.offsetType == produceOffset {
		b.latestProduceOffsets[partitionKey{
			partition: o.partition,
			topic:     o.topic,
		}] = o.offset
		return
	}
	b.latestCommitOffsets[partitionConsumerKey{
		partition: o.partition,
		group:     o.group,
		topic:     o.topic,
	}] = o.offset
}

func (a *aggregator) run(tick <-chan time.Time) {
	for {
		select {
		case s := <-a.in:
			atomic.AddInt64(&a.stats.payloadsIn, 1)
			a.add(s)
		case o := <-a.inKafka:
			a.addKafkaOffset(o)
		case now := <-tick:
			a.sendToAgent(a.flush(now))
		case <-a.stop:
			// drop in flight payloads on the input channel
			a.sendToAgent(a.flush(time.Now().Add(bucketDuration * 10)))
			return
		}
	}
}

func (a *aggregator) Start() {
	if atomic.SwapUint64(&a.stopped, 0) == 0 {
		// already running
		log.Print("WARN: (*aggregator).Start called more than once. This is likely a programming error.")
		return
	}
	a.stop = make(chan struct{})
	a.wg.Add(1)
	go a.reportStats()
	go func() {
		defer a.wg.Done()
		tick := time.NewTicker(bucketDuration)
		defer tick.Stop()
		a.run(tick.C)
	}()
}

func (a *aggregator) Stop() {
	if atomic.SwapUint64(&a.stopped, 1) > 0 {
		return
	}
	close(a.stop)
	a.wg.Wait()
}

func (a *aggregator) reportStats() {
	for range time.NewTicker(time.Second * 10).C {
		a.statsd.Count("datadog.datastreams.aggregator.payloads_in", atomic.SwapInt64(&a.stats.payloadsIn, 0), nil, 1)
		a.statsd.Count("datadog.datastreams.aggregator.flushed_payloads", atomic.SwapInt64(&a.stats.flushedPayloads, 0), nil, 1)
		a.statsd.Count("datadog.datastreams.aggregator.flushed_buckets", atomic.SwapInt64(&a.stats.flushedBuckets, 0), nil, 1)
		a.statsd.Count("datadog.datastreams.aggregator.flush_errors", atomic.SwapInt64(&a.stats.flushErrors, 0), nil, 1)
		a.statsd.Count("datadog.datastreams.dropped_payloads", atomic.SwapInt64(&a.stats.dropped, 0), nil, 1)
	}
}

func (a *aggregator) runFlusher() {
	for {
		select {
		case <-a.stop:
			// flush everything, so add a few bucketDurations to the current time in order to get a good margin.
			return
		}
	}
}

func (a *aggregator) flushBucket(buckets map[int64]bucket, bucketStart int64, timestampType TimestampType) StatsBucket {
	bucket := buckets[bucketStart]
	delete(buckets, bucketStart)
	return bucket.export(timestampType)
}

func (a *aggregator) flush(now time.Time) StatsPayload {
	nowNano := now.UnixNano()
	sp := StatsPayload{
		Service:       a.service,
		Env:           a.env,
		PrimaryTag:    a.primaryTag,
		Lang:          "go",
		TracerVersion: version.Tag,
		Stats:         make([]StatsBucket, 0, len(a.tsTypeCurrentBuckets)+len(a.tsTypeOriginBuckets)),
	}
	for ts := range a.tsTypeCurrentBuckets {
		if ts > nowNano-bucketDuration.Nanoseconds() {
			// do not flush the bucket at the current time
			continue
		}
		sp.Stats = append(sp.Stats, a.flushBucket(a.tsTypeCurrentBuckets, ts, TimestampTypeCurrent))
	}
	for ts := range a.tsTypeOriginBuckets {
		if ts > nowNano-bucketDuration.Nanoseconds() {
			// do not flush the bucket at the current time
			continue
		}
		sp.Stats = append(sp.Stats, a.flushBucket(a.tsTypeOriginBuckets, ts, TimestampTypeOrigin))
	}
	return sp
}

func (a *aggregator) sendToAgent(payload StatsPayload) {
	atomic.AddInt64(&a.stats.flushedPayloads, 1)
	atomic.AddInt64(&a.stats.flushedBuckets, int64(len(payload.Stats)))
	if err := a.transport.sendPipelineStats(&payload); err != nil {
		atomic.AddInt64(&a.stats.flushErrors, 1)
	}
}
