// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/version"

	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"google.golang.org/protobuf/proto"
)

const (
	bucketDuration     = time.Second * 10
	defaultServiceName = "unnamed-go-service"

	// maxTransactionBucketSize is the soft size limit for transaction bytes in a single bucket.
	// When exceeded, an early flush is triggered to keep individual payloads small.
	// Record size varies by transaction ID length (e.g. 46 bytes for UUID-length IDs).
	maxTransactionBucketSize = 1024 * 512

	// maxTransactionBytesPerPeriod is the maximum total transaction bytes the processor
	// will accept per bucket period (10s). Once exceeded, new transactions are dropped
	// and a warning is logged. This prevents overwhelming the downstream pipeline
	// (agent → data-pipeline-edge → Kafka) which cannot sustain arbitrarily high
	// throughput. At 46 bytes per UUID-length transaction ID, this allows ~5,000
	// transactions/sec. Shorter IDs allow proportionally higher throughput.
	maxTransactionBytesPerPeriod = 2_300_000
)

// use the same gamma and index offset as the Datadog backend, to avoid doing any conversions in
// the backend that would lead to a loss of precision
var sketchMapping, _ = mapping.NewLogarithmicMappingWithGamma(1.015625, 1.8761281912861705)

type statsPoint struct {
	edgeTags       []string
	hash           uint64
	parentHash     uint64
	timestamp      int64
	pathwayLatency int64
	edgeLatency    int64
	payloadSize    int64
	serviceName    string
	processTags    []string
}

type statsGroup struct {
	service        string
	edgeTags       []string
	processTags    []string
	hash           uint64
	parentHash     uint64
	pathwayLatency *ddsketch.DDSketch
	edgeLatency    *ddsketch.DDSketch
	payloadSize    *ddsketch.DDSketch
}

type bucket struct {
	points                     map[uint64]statsGroup
	latestCommitOffsets        map[partitionConsumerKey]int64
	latestProduceOffsets       map[partitionKey]int64
	latestHighWatermarkOffsets map[partitionKey]int64
	transactions               []byte // packed transaction records; see transactionBytes
	start                      uint64
	duration                   uint64
}

func newBucket(start, duration uint64) bucket {
	return bucket{
		points:                     make(map[uint64]statsGroup),
		latestCommitOffsets:        make(map[partitionConsumerKey]int64),
		latestProduceOffsets:       make(map[partitionKey]int64),
		latestHighWatermarkOffsets: make(map[partitionKey]int64),
		start:                      start,
		duration:                   duration,
	}
}

func (b bucket) export(timestampType TimestampType, checkpointNameMapping []byte) StatsBucket {
	stats := make([]StatsPoint, 0, len(b.points))
	for _, s := range b.points {
		pathwayLatency, err := proto.Marshal(s.pathwayLatency.ToProto())
		if err != nil {
			log.Error("can't serialize pathway latency. Ignoring: %s", err.Error())
			continue
		}
		edgeLatency, err := proto.Marshal(s.edgeLatency.ToProto())
		if err != nil {
			log.Error("can't serialize edge latency. Ignoring: %s", err.Error())
			continue
		}
		payloadSize, err := proto.Marshal(s.payloadSize.ToProto())
		if err != nil {
			log.Error("can't serialize payload size. Ignoring: %s", err.Error())
			continue
		}
		stats = append(stats, StatsPoint{
			PathwayLatency: pathwayLatency,
			EdgeLatency:    edgeLatency,
			EdgeTags:       s.edgeTags,
			Hash:           s.hash,
			ParentHash:     s.parentHash,
			TimestampType:  timestampType,
			PayloadSize:    payloadSize,
		})
	}
	exported := StatsBucket{
		Start:                    b.start,
		Duration:                 b.duration,
		Stats:                    stats,
		Backlogs:                 make([]Backlog, 0, len(b.latestCommitOffsets)+len(b.latestProduceOffsets)+len(b.latestHighWatermarkOffsets)),
		Transactions:             b.transactions,
		TransactionCheckpointIds: checkpointNameMapping,
	}
	for key, offset := range b.latestProduceOffsets {
		tags := []string{fmt.Sprintf("partition:%d", key.partition), fmt.Sprintf("topic:%s", key.topic), "type:kafka_produce"}
		if key.cluster != "" {
			tags = append(tags, fmt.Sprintf("kafka_cluster_id:%s", key.cluster))
		}
		exported.Backlogs = append(exported.Backlogs, Backlog{Tags: tags, Value: offset})
	}
	for key, offset := range b.latestCommitOffsets {
		tags := []string{fmt.Sprintf("consumer_group:%s", key.group), fmt.Sprintf("partition:%d", key.partition), fmt.Sprintf("topic:%s", key.topic), "type:kafka_commit"}
		if key.cluster != "" {
			tags = append(tags, fmt.Sprintf("kafka_cluster_id:%s", key.cluster))
		}
		exported.Backlogs = append(exported.Backlogs, Backlog{Tags: tags, Value: offset})
	}
	for key, offset := range b.latestHighWatermarkOffsets {
		tags := []string{fmt.Sprintf("partition:%d", key.partition), fmt.Sprintf("topic:%s", key.topic), "type:kafka_high_watermark"}
		if key.cluster != "" {
			tags = append(tags, fmt.Sprintf("kafka_cluster_id:%s", key.cluster))
		}
		exported.Backlogs = append(exported.Backlogs, Backlog{Tags: tags, Value: offset})
	}
	return exported
}

type pointType int

const (
	pointTypeStats pointType = iota
	pointTypeKafkaOffset
	pointTypeTransaction
)

// transactionEntry records a single transaction checkpoint observation.
type transactionEntry struct {
	transactionID  string
	checkpointName string
	timestamp      int64 // unix nanoseconds
}

type processorInput struct {
	point            statsPoint
	kafkaOffset      kafkaOffset
	transactionEntry transactionEntry
	typ              pointType
	queuePos         int64
}

type processorStats struct {
	payloadsIn          atomic.Int64
	flushedPayloads     atomic.Int64
	flushedBuckets      atomic.Int64
	flushErrors         atomic.Int64
	dropped             atomic.Int64
	droppedTransactions atomic.Int64
}

type partitionKey struct {
	partition int32
	topic     string
	cluster   string
}

type partitionConsumerKey struct {
	partition int32
	topic     string
	group     string
	cluster   string
}

type offsetType int

const (
	produceOffset offsetType = iota
	commitOffset
	highWatermarkOffset
)

type kafkaOffset struct {
	offset     int64
	topic      string
	group      string
	partition  int32
	offsetType offsetType
	timestamp  int64
	cluster    string
}

type bucketKey struct {
	serviceName string
	btime       int64
}

// checkpointRegistry maps checkpoint names to compact integer IDs for wire encoding.
// All access is confined to the processor's run goroutine; no locking needed.
type checkpointRegistry struct {
	nameToID    map[string]byte
	nextID      byte
	encodedKeys []byte // packed: [id uint8][nameLen uint8][name bytes]...
}

func newCheckpointRegistry() checkpointRegistry {
	return checkpointRegistry{
		nameToID: make(map[string]byte),
		nextID:   1, // 0 is reserved as the zero-value sentinel; valid IDs start at 1
	}
}

// getOrAssign returns the compact ID for the given checkpoint name, registering
// it if it has not been seen before. Returns (0, false) if the registry is full.
// Not concurrency-safe; must only be called from the processor's run goroutine.
func (r *checkpointRegistry) getOrAssign(name string) (byte, bool) {
	if id, ok := r.nameToID[name]; ok {
		return id, true
	}
	if r.nextID == math.MaxUint8 {
		log.Warn("datastreams: checkpoint registry full, cannot register new checkpoint name")
		return 0, false
	}
	id := r.nextID
	r.nextID++
	r.nameToID[name] = id

	// Append [id][nameLen][name bytes] to the mapping blob.
	nameBytes := []byte(name)
	nameLen := len(nameBytes)
	if nameLen > 255 {
		nameLen = 255
		nameBytes = nameBytes[:nameLen]
	}
	r.encodedKeys = append(r.encodedKeys, id, byte(nameLen))
	r.encodedKeys = append(r.encodedKeys, nameBytes...)
	return id, true
}

type Processor struct {
	in                   *fastQueue
	hashCache            *hashCache
	inKafka              chan kafkaOffset
	tsTypeCurrentBuckets map[bucketKey]bucket
	tsTypeOriginBuckets  map[bucketKey]bucket
	checkpoints          checkpointRegistry
	wg                   sync.WaitGroup
	stopped              uint64
	stop                 chan struct{} // closing this channel triggers shutdown
	flushRequest         chan chan<- struct{}
	stats                processorStats
	transport            *httpTransport
	statsd               internal.StatsdClient
	env                  string
	primaryTag           string
	service              string
	version              string
	// earlyFlush is set by addTransaction when the transaction buffer for a bucket exceeds
	// maxTransactionBucketSize. Only accessed from the run goroutine; no locking needed.
	earlyFlush bool
	// txnBytesThisPeriod tracks the total transaction bytes accepted in the current
	// bucket period (across early flushes). Reset when the period rolls over.
	// Only accessed from the run goroutine; no locking needed.
	txnBytesThisPeriod int64
	txnPeriodStart     int64 // btime of the current period
	// used for tests
	timeSource func() time.Time
}

func (p *Processor) time() time.Time {
	if p.timeSource != nil {
		return p.timeSource()
	}
	return time.Now()
}

func NewProcessor(statsd internal.StatsdClient, env, service, version string, agentURL *url.URL, httpClient *http.Client) *Processor {
	if service == "" {
		service = defaultServiceName
	}
	p := &Processor{
		tsTypeCurrentBuckets: make(map[bucketKey]bucket),
		tsTypeOriginBuckets:  make(map[bucketKey]bucket),
		hashCache:            newHashCache(),
		in:                   newFastQueue(),
		stopped:              1,
		statsd:               statsd,
		env:                  env,
		service:              service,
		version:              version,
		transport:            newHTTPTransport(agentURL, httpClient),
		timeSource:           time.Now,
		checkpoints:          newCheckpointRegistry(),
	}
	return p
}

// alignTs returns the provided timestamp truncated to the bucket size.
// It gives us the start time of the time bucket in which such timestamp falls.
func alignTs(ts, bucketSize int64) int64 { return ts - ts%bucketSize }

func (p *Processor) getBucket(btime int64, service string, buckets map[bucketKey]bucket) bucket {
	k := bucketKey{serviceName: service, btime: btime}
	b, ok := buckets[k]
	if !ok {
		b = newBucket(uint64(btime), uint64(bucketDuration.Nanoseconds()))
		buckets[k] = b
	}
	return b
}
func (p *Processor) addToBuckets(point statsPoint, btime int64, buckets map[bucketKey]bucket) {
	b := p.getBucket(btime, point.serviceName, buckets)
	group, ok := b.points[point.hash]
	if !ok {
		group = statsGroup{
			edgeTags:       point.edgeTags,
			parentHash:     point.parentHash,
			hash:           point.hash,
			pathwayLatency: ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
			edgeLatency:    ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
			payloadSize:    ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
		}
		b.points[point.hash] = group
	}
	if err := group.pathwayLatency.Add(math.Max(float64(point.pathwayLatency)/float64(time.Second), 0)); err != nil {
		log.Error("failed to add pathway latency. Ignoring %v.", err.Error())
	}
	if err := group.edgeLatency.Add(math.Max(float64(point.edgeLatency)/float64(time.Second), 0)); err != nil {
		log.Error("failed to add edge latency. Ignoring %v.", err.Error())
	}
	if err := group.payloadSize.Add(float64(point.payloadSize)); err != nil {
		log.Error("failed to add payload size. Ignoring %v.", err.Error())
	}
}

func (p *Processor) add(point statsPoint) {
	currentBucketTime := alignTs(point.timestamp, bucketDuration.Nanoseconds())
	p.addToBuckets(point, currentBucketTime, p.tsTypeCurrentBuckets)
	originTimestamp := point.timestamp - point.pathwayLatency
	originBucketTime := alignTs(originTimestamp, bucketDuration.Nanoseconds())
	p.addToBuckets(point, originBucketTime, p.tsTypeOriginBuckets)
}

func (p *Processor) addKafkaOffset(o kafkaOffset) {
	btime := alignTs(o.timestamp, bucketDuration.Nanoseconds())
	b := p.getBucket(btime, p.service, p.tsTypeCurrentBuckets)
	if o.offsetType == produceOffset {
		b.latestProduceOffsets[partitionKey{
			partition: o.partition,
			topic:     o.topic,
			cluster:   o.cluster,
		}] = o.offset
		return
	}
	if o.offsetType == highWatermarkOffset {
		b.latestHighWatermarkOffsets[partitionKey{
			partition: o.partition,
			topic:     o.topic,
			cluster:   o.cluster,
		}] = o.offset
		return
	}
	b.latestCommitOffsets[partitionConsumerKey{
		partition: o.partition,
		group:     o.group,
		topic:     o.topic,
		cluster:   o.cluster,
	}] = o.offset
}

func (p *Processor) processInput(in *processorInput) {
	p.stats.payloadsIn.Add(1)
	if in.typ == pointTypeStats {
		p.add(in.point)
	} else if in.typ == pointTypeKafkaOffset {
		p.addKafkaOffset(in.kafkaOffset)
	} else if in.typ == pointTypeTransaction {
		p.addTransaction(in.transactionEntry)
	}
}

// appendTransactionBytes appends a single transaction record to dst and returns
// the extended slice. The wire format is shared with the Java tracer:
//
//	[checkpointId uint8][timestamp int64 big-endian][idLen uint8][id bytes]
//
// IDs longer than 255 bytes are truncated.
func appendTransactionBytes(dst []byte, checkpointID byte, timestamp int64, transactionID string) []byte {
	idLen := min(len(transactionID), 255)
	dst = append(dst, checkpointID)
	dst = binary.BigEndian.AppendUint64(dst, uint64(timestamp))
	dst = append(dst, byte(idLen))
	dst = append(dst, transactionID[:idLen]...)
	return dst
}

func (p *Processor) addTransaction(e transactionEntry) {
	log.Debug("datastreams: addTransaction checkpoint=%q txnID=%q ts=%d", e.checkpointName, e.transactionID, e.timestamp)
	btime := alignTs(e.timestamp, bucketDuration.Nanoseconds())

	// Reset the per-period budget when we roll into a new bucket period.
	if btime != p.txnPeriodStart {
		p.txnBytesThisPeriod = 0
		p.txnPeriodStart = btime
	}

	// Estimate the record size before appending: 1 (checkpointID) + 8 (timestamp) + 1 (idLen) + len(id).
	recordSize := int64(10 + min(len(e.transactionID), 255))
	if p.txnBytesThisPeriod+recordSize > maxTransactionBytesPerPeriod {
		p.stats.droppedTransactions.Add(1)
		return
	}

	k := bucketKey{serviceName: p.service, btime: btime}
	b, ok := p.tsTypeCurrentBuckets[k]
	if !ok {
		b = newBucket(uint64(btime), uint64(bucketDuration.Nanoseconds()))
	}
	checkpointID, ok := p.checkpoints.getOrAssign(e.checkpointName)
	if !ok {
		p.tsTypeCurrentBuckets[k] = b
		return
	}
	b.transactions = appendTransactionBytes(b.transactions, checkpointID, e.timestamp, e.transactionID)
	p.txnBytesThisPeriod += recordSize
	p.tsTypeCurrentBuckets[k] = b
	log.Debug("datastreams: bucket now has %d transaction bytes (period total: %d)", len(b.transactions), p.txnBytesThisPeriod)
	if len(b.transactions) >= maxTransactionBucketSize {
		// Transaction buffer has grown large; trigger an early flush of older buckets,
		// matching the Java tracer's behavior (DefaultDataStreamsMonitoring.java:444).
		p.earlyFlush = true
	}
}

func (p *Processor) flushInput() {
	for {
		in := p.in.pop()
		if in == nil {
			return
		}
		p.processInput(in)
	}
}

func (p *Processor) run(tick <-chan time.Time) {
	for {
		select {
		case <-p.stop:
			// drop in flight payloads on the input channel
			p.sendToAgent(p.flush(time.Now().Add(bucketDuration * 10)))
			return
		case now := <-tick:
			p.sendToAgent(p.flush(now))
		case done := <-p.flushRequest:
			p.flushInput()
			p.sendToAgent(p.flush(time.Now().Add(bucketDuration * 10)))
			close(done)
		default:
			s := p.in.pop()
			if s == nil {
				time.Sleep(time.Millisecond * 10)
				continue
			}
			p.processInput(s)
			if !p.earlyFlush {
				continue
			}
			p.earlyFlush = false
			// The current bucket has exceeded maxTransactionBucketSize. Force-flush it
			// immediately by advancing the cutoff time by one full bucket duration, which
			// makes the current bucket appear "old enough" to flush. This prevents the
			// payload from growing beyond the agent's ~10MB request body limit.
			p.sendToAgent(p.flush(p.time().Add(bucketDuration)))
		}
	}
}

func (p *Processor) Start() {
	if atomic.SwapUint64(&p.stopped, 0) == 0 {
		// already running
		log.Warn("(*Processor).Start called more than once. This is likely a programming error.")
		return
	}
	p.stop = make(chan struct{})
	p.flushRequest = make(chan chan<- struct{})
	p.wg.Go(func() {
		p.reportStats()
	})
	p.wg.Go(func() {
		tick := time.NewTicker(bucketDuration)
		defer tick.Stop()
		p.run(tick.C)
	})
}

// Flush triggers a flush and waits for it to complete.
func (p *Processor) Flush() {
	if atomic.LoadUint64(&p.stopped) > 0 {
		return
	}
	done := make(chan struct{})
	select {
	case p.flushRequest <- done:
		<-done
	case <-p.stop:
	}
}

func (p *Processor) Stop() {
	if atomic.SwapUint64(&p.stopped, 1) > 0 {
		return
	}
	close(p.stop)
	p.wg.Wait()
}

func (p *Processor) reportStats() {
	tick := time.NewTicker(time.Second * 10)
	defer tick.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-tick.C:
		}
		p.statsd.Count("datadog.datastreams.processor.payloads_in", p.stats.payloadsIn.Swap(0), nil, 1)
		p.statsd.Count("datadog.datastreams.processor.flushed_payloads", p.stats.flushedPayloads.Swap(0), nil, 1)
		p.statsd.Count("datadog.datastreams.processor.flushed_buckets", p.stats.flushedBuckets.Swap(0), nil, 1)
		p.statsd.Count("datadog.datastreams.processor.flush_errors", p.stats.flushErrors.Swap(0), nil, 1)
		p.statsd.Count("datadog.datastreams.processor.dropped_payloads", p.stats.dropped.Swap(0), nil, 1)
		if dt := p.stats.droppedTransactions.Swap(0); dt > 0 {
			p.statsd.Count("datadog.datastreams.processor.dropped_transactions", dt, nil, 1)
			log.Warn("datastreams: dropped %d transactions this period — transaction throughput exceeds ~5,000/sec capacity, consider distributing load across more service instances", dt)
		}
	}
}

func (p *Processor) flushBucket(buckets map[bucketKey]bucket, bk bucketKey, timestampType TimestampType) StatsBucket {
	b := buckets[bk]
	delete(buckets, bk)
	var mapping []byte
	if len(b.transactions) > 0 {
		log.Debug("datastreams: flushing bucket with %d transaction bytes, %d mapping bytes", len(b.transactions), len(p.checkpoints.encodedKeys))
		mapping = p.checkpoints.encodedKeys
	}
	return b.export(timestampType, mapping)
}

func (p *Processor) flush(now time.Time) map[string]StatsPayload {
	nowNano := now.UnixNano()
	payloads := make(map[string]StatsPayload)
	addBucket := func(service string, bucket StatsBucket) {
		payload, ok := payloads[service]
		if !ok {
			payload = StatsPayload{
				Service:       service,
				Version:       p.version,
				Env:           p.env,
				Lang:          "go",
				TracerVersion: version.Tag,
				Stats:         make([]StatsBucket, 0, 1),
				ProcessTags:   processtags.GlobalTags().Slice(),
				// ProductMask advertises supported products; always set to APM|DSM while
				// the DSM processor is running.
				ProductMask: productAPM | productDSM,
			}
		}
		payload.Stats = append(payload.Stats, bucket)
		payloads[service] = payload
	}
	for bucketKey := range p.tsTypeCurrentBuckets {
		if bucketKey.btime > nowNano-bucketDuration.Nanoseconds() {
			// do not flush the bucket at the current time
			continue
		}
		addBucket(bucketKey.serviceName, p.flushBucket(p.tsTypeCurrentBuckets, bucketKey, TimestampTypeCurrent))
	}
	for bucketKey := range p.tsTypeOriginBuckets {
		if bucketKey.btime > nowNano-bucketDuration.Nanoseconds() {
			// do not flush the bucket at the current time
			continue
		}
		addBucket(bucketKey.serviceName, p.flushBucket(p.tsTypeOriginBuckets, bucketKey, TimestampTypeOrigin))
	}
	return payloads
}

func (p *Processor) sendToAgent(payloads map[string]StatsPayload) {
	for _, payload := range payloads {
		p.stats.flushedPayloads.Add(1)
		p.stats.flushedBuckets.Add(int64(len(payload.Stats)))
		if err := p.transport.sendPipelineStats(&payload); err != nil {
			p.stats.flushErrors.Add(1)
		}
	}
}

func (p *Processor) SetCheckpoint(ctx context.Context, edgeTags ...string) context.Context {
	return p.SetCheckpointWithParams(ctx, options.CheckpointParams{}, edgeTags...)
}

func (p *Processor) SetCheckpointWithParams(ctx context.Context, params options.CheckpointParams, edgeTags ...string) context.Context {
	parent, hasParent := PathwayFromContext(ctx)
	parentHash := uint64(0)
	now := p.time()
	pathwayStart := now
	edgeStart := now
	if hasParent {
		pathwayStart = parent.PathwayStart()
		edgeStart = parent.EdgeStart()
		parentHash = parent.GetHash()
	}
	service := p.service
	if params.ServiceOverride != "" {
		service = params.ServiceOverride
	}
	processTags := processtags.GlobalTags().Slice()
	child := Pathway{
		hash:         p.hashCache.get(service, p.env, edgeTags, processTags, parentHash),
		pathwayStart: pathwayStart,
		edgeStart:    now,
	}
	dropped := p.in.push(&processorInput{typ: pointTypeStats, point: statsPoint{
		serviceName:    service,
		edgeTags:       edgeTags,
		parentHash:     parentHash,
		hash:           child.hash,
		timestamp:      now.UnixNano(),
		pathwayLatency: now.Sub(pathwayStart).Nanoseconds(),
		edgeLatency:    now.Sub(edgeStart).Nanoseconds(),
		payloadSize:    params.PayloadSize,
	}})
	if dropped {
		p.stats.dropped.Add(1)
	}
	return ContextWithPathway(ctx, child)
}

func (p *Processor) TrackKafkaCommitOffset(group string, topic string, partition int32, offset int64) {
	p.TrackKafkaCommitOffsetWithCluster("", group, topic, partition, offset)
}

func (p *Processor) TrackKafkaCommitOffsetWithCluster(cluster string, group string, topic string, partition int32, offset int64) {
	dropped := p.in.push(&processorInput{typ: pointTypeKafkaOffset, kafkaOffset: kafkaOffset{
		offset:     offset,
		group:      group,
		topic:      topic,
		partition:  partition,
		offsetType: commitOffset,
		timestamp:  p.time().UnixNano(),
		cluster:    cluster}})
	if dropped {
		p.stats.dropped.Add(1)
	}
}

func (p *Processor) TrackKafkaProduceOffset(topic string, partition int32, offset int64) {
	p.TrackKafkaProduceOffsetWithCluster("", topic, partition, offset)
}

func (p *Processor) TrackKafkaProduceOffsetWithCluster(cluster string, topic string, partition int32, offset int64) {
	dropped := p.in.push(&processorInput{typ: pointTypeKafkaOffset, kafkaOffset: kafkaOffset{
		offset:     offset,
		topic:      topic,
		partition:  partition,
		offsetType: produceOffset,
		timestamp:  p.time().UnixNano(),
		cluster:    cluster,
	}})
	if dropped {
		p.stats.dropped.Add(1)
	}
}

// TrackKafkaHighWatermarkOffset should be used in the consumer, to track the high watermark offsets of each partition.
func (p *Processor) TrackKafkaHighWatermarkOffset(cluster string, topic string, partition int32, offset int64) {
	dropped := p.in.push(&processorInput{typ: pointTypeKafkaOffset, kafkaOffset: kafkaOffset{
		offset:     offset,
		topic:      topic,
		partition:  partition,
		offsetType: highWatermarkOffset,
		timestamp:  p.time().UnixNano(),
		cluster:    cluster,
	}})
	if dropped {
		p.stats.dropped.Add(1)
	}
}

// trackTransactionAt is the shared implementation for TrackTransaction and TrackTransactionAt.
func (p *Processor) trackTransactionAt(transactionID, checkpointName string, t time.Time) {
	dropped := p.in.push(&processorInput{
		typ: pointTypeTransaction,
		transactionEntry: transactionEntry{
			transactionID:  transactionID,
			checkpointName: checkpointName,
			timestamp:      t.UnixNano(),
		},
	})
	if dropped {
		p.stats.dropped.Add(1)
	}
}

// TrackTransaction records a manual transaction checkpoint observation. Use this to
// track when a specific transaction ID is seen at a named checkpoint in a data pipeline.
// transactionID identifies the transaction (e.g. a message ID or correlation ID).
// checkpointName is a stable label for the processing stage (e.g. "ingested", "processed").
func (p *Processor) TrackTransaction(transactionID, checkpointName string) {
	p.trackTransactionAt(transactionID, checkpointName, p.time())
}

// TrackTransactionAt records a manual transaction checkpoint observation at the
// provided time t instead of the current time. Use this when the observation time
// is already known (e.g. a timestamp embedded in a message header). t is expected
// to be in UTC.
func (p *Processor) TrackTransactionAt(transactionID, checkpointName string, t time.Time) {
	p.trackTransactionAt(transactionID, checkpointName, t)
}
