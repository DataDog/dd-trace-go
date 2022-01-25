// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package pipelines

import (
	"log"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/golang/protobuf/proto"
)

const (
	bucketDuration     = time.Second * 10
	defaultServiceName = "unnamed-go-service"
)

var sketchMapping, _ = mapping.NewLogarithmicMapping(0.01)

type statsPoint struct {
	service        string
	edge           string
	hash           uint64
	parentHash     uint64
	timestamp      int64
	pathwayLatency int64
	edgeLatency    int64
}

type statsGroup struct {
	service        string
	edge           string
	hash           uint64
	parentHash     uint64
	pathwayLatency *ddsketch.DDSketch
	edgeLatency    *ddsketch.DDSketch
}

type bucket map[uint64]statsGroup

func (b bucket) export() []groupedStats {
	stats := make([]groupedStats, 0, len(b))
	for _, s := range b {
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
		stats = append(stats, groupedStats{
			PathwayLatency: pathwayLatency,
			EdgeLatency:    edgeLatency,
			Service:        s.service,
			Edge:           s.edge,
			Hash:           s.hash,
			ParentHash:     s.parentHash,
		})
	}
	return stats
}

type processorStats struct {
	payloadsIn      int64
	flushedPayloads int64
	flushedBuckets  int64
	flushErrors     int64
}

type processor struct {
	in        chan statsPoint
	buckets   map[int64]bucket
	wg        sync.WaitGroup
	stopped   uint64
	stop      chan struct{} // closing this channel triggers shutdown
	stats     processorStats
	transport *httpTransport
	statsd    statsd.ClientInterface
	env       string
	service   string
}

func newProcessor(statsd statsd.ClientInterface, env, service, agentAddr string, httpClient *http.Client, site, apiKey string, agentLess bool) *processor {
	return &processor{
		buckets:   make(map[int64]bucket),
		in:        make(chan statsPoint, 10000),
		stopped:   1,
		statsd:    statsd,
		env:       env,
		service:   service,
		transport: newHTTPTransport(agentAddr, site, apiKey, httpClient, agentLess),
	}
}

// alignTs returns the provided timestamp truncated to the bucket size.
// It gives us the start time of the time bucket in which such timestamp falls.
func alignTs(ts, bucketSize int64) int64 { return ts - ts%bucketSize }

func (p *processor) add(point statsPoint) {
	btime := alignTs(point.timestamp, bucketDuration.Nanoseconds())
	b, ok := p.buckets[btime]
	if !ok {
		b = make(bucket)
		p.buckets[btime] = b
	}
	group, ok := b[point.hash]
	if !ok {
		group = statsGroup{
			service:        point.service,
			edge:           point.edge,
			parentHash:     point.parentHash,
			hash:           point.hash,
			pathwayLatency: ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
			edgeLatency:    ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
		}
		b[point.hash] = group
	}
	if err := group.pathwayLatency.Add(math.Max(float64(point.pathwayLatency)/float64(time.Second), 0)); err != nil {
		log.Printf("ERROR: failed to add pathway latency. Ignoring %v.", err)
	}
	if err := group.edgeLatency.Add(math.Max(float64(point.edgeLatency)/float64(time.Second), 0)); err != nil {
		log.Printf("ERROR: failed to add edge latency. Ignoring %v.", err)
	}
}

func (p *processor) run(tick <-chan time.Time) {
	for {
		select {
		case s := <-p.in:
			atomic.AddInt64(&p.stats.payloadsIn, 1)
			p.add(s)
		case now := <-tick:
			p.sendToAgent(p.flush(now))
		case <-p.stop:
			// drop in flight payloads on the input channel
			p.sendToAgent(p.flush(time.Now().Add(bucketDuration * 10)))
			return
		}
	}
}

func (p *processor) Start() {
	if atomic.SwapUint64(&p.stopped, 0) == 0 {
		// already running
		log.Print("WARN: (*processor).Start called more than once. This is likely a programming error.")
		return
	}
	p.stop = make(chan struct{})
	p.wg.Add(1)
	go p.reportStats()
	go func() {
		defer p.wg.Done()
		tick := time.NewTicker(bucketDuration)
		defer tick.Stop()
		p.run(tick.C)
	}()
}

func (p *processor) Stop() {
	if atomic.SwapUint64(&p.stopped, 1) > 0 {
		return
	}
	close(p.stop)
	p.wg.Wait()
}

func (p *processor) reportStats() {
	for range time.NewTicker(time.Second * 10).C {
		p.statsd.Count("datadog.pipelines.processor.payloads_in", atomic.SwapInt64(&p.stats.payloadsIn, 0), nil, 1)
		p.statsd.Count("datadog.pipelines.processor.flushed_payloads", atomic.SwapInt64(&p.stats.flushedPayloads, 0), nil, 1)
		p.statsd.Count("datadog.pipelines.processor.flushed_buckets", atomic.SwapInt64(&p.stats.flushedBuckets, 0), nil, 1)
		p.statsd.Count("datadog.pipelines.processor.flush_errors", atomic.SwapInt64(&p.stats.flushErrors, 0), nil, 1)
	}
}

func (p *processor) runFlusher() {
	for {
		select {
		case <-p.stop:
			// flush everything, so add a few bucketDurations to the current time in order to get a good margin.
			return
		}
	}
}

func (p *processor) flushBucket(bucketStart int64) statsBucket {
	bucket := p.buckets[bucketStart]
	delete(p.buckets, bucketStart)
	return statsBucket{
		Start:    uint64(bucketStart),
		Duration: uint64(bucketDuration.Nanoseconds()),
		Stats:    bucket.export(),
	}
}

func (p *processor) flush(now time.Time) statsPayload {
	nowNano := now.UnixNano()
	sp := statsPayload{
		Env:   p.env,
		Stats: make([]statsBucket, 0, len(p.buckets)),
	}
	for ts := range p.buckets {
		if ts > nowNano-bucketDuration.Nanoseconds() {
			// do not flush the current bucket
			continue
		}
		sp.Stats = append(sp.Stats, p.flushBucket(ts))
	}
	return sp
}

func (p *processor) sendToAgent(payload statsPayload) {
	atomic.AddInt64(&p.stats.flushedPayloads, 1)
	atomic.AddInt64(&p.stats.flushedBuckets, int64(len(payload.Stats)))
	if err := p.transport.sendPipelineStats(&payload); err != nil {
		atomic.AddInt64(&p.stats.flushErrors, 1)
		log.Printf("ERROR: Error sending stats payload: %v", err)
	}
}

func getService() string {
	if processor := getGlobalProcessor(); processor != nil && processor.service != "" {
		return processor.service
	}
	return defaultServiceName
}
