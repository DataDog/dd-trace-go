package tracer

import (
	"fmt"
	"github.com/DataDog/datadog-go/statsd"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/pb/sketchpb"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/golang/protobuf/proto"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

var sketchMapping, _ = mapping.NewLogarithmicMapping(0.01)

type pipelineStatsPoint struct {
	service string
	receivingPipelineName string
	pipelineHash uint64
	parentHash uint64
	timestamp int64
	latency int64
}

type pipelineStatsGroup struct {
	service string
	receivingPipelineName string
	pipelineHash uint64
	parentHash uint64
	sketch *ddsketch.DDSketch
}

type bucket map[uint64]pipelineStatsGroup

func (b bucket) Export() []groupedPipelineStats {
	stats := make([]groupedPipelineStats, 0, len(b))
	for _, s := range b {
		// todo[piochelepiotr] Used optimized ddsketch.
		summary, err := proto.Marshal(s.sketch.ToProto())
		if err != nil {
			log.Error("Failed to serialize sketch with err:%v", err)
			continue
		}
		stats = append(stats, groupedPipelineStats{
			Summary: summary,
			Service: s.service,
			ReceivingPipelineName: s.receivingPipelineName,
			PipelineHash: s.pipelineHash,
			ParentHash: s.parentHash,
		})
	}
	return stats
}

type pipelineConcentrator struct {
	In chan pipelineStatsPoint

	mu sync.Mutex
	buckets map[int64]bucket
	wg         sync.WaitGroup // waits for any active goroutines
	negativeDurations int64
	bucketDuration time.Duration
	stopped uint64
	stop       chan struct{}  // closing this channel triggers shutdown
	cfg        *config        // tracer startup configuration
}

func newPipelineConcentrator(c *config, bucketDuration time.Duration) *pipelineConcentrator {
	return &pipelineConcentrator{
		buckets: make(map[int64]bucket),
		In: make(chan pipelineStatsPoint, 10000),
		stopped: 1,
		bucketDuration: bucketDuration,
		cfg: c,
	}
}

func (c *pipelineConcentrator) add(p pipelineStatsPoint) {
	btime := alignTs(p.timestamp, c.bucketDuration.Nanoseconds())
	latency := math.Max(float64(p.latency) / float64(time.Second), 0)
	b, ok := c.buckets[btime]
	if !ok {
		b = make(bucket)
		c.buckets[btime] = b
	}
	// aggregate
	group, ok := b[p.pipelineHash]
	if !ok {
		group = pipelineStatsGroup{
			service: p.service,
			receivingPipelineName: p.receivingPipelineName,
			parentHash: p.parentHash,
			pipelineHash: p.pipelineHash,
			sketch: ddsketch.NewDDSketch(sketchMapping, store.BufferedPaginatedStoreConstructor(), store.BufferedPaginatedStoreConstructor()),
		}
		b[p.pipelineHash] = group
	}
	if err := group.sketch.Add(latency); err != nil {
		log.Error("failed to merge sketches. Ignoring %v.", err)
	}
}

func (c *pipelineConcentrator) runIngester() {
	for {
		select {
		case s := <-c.In:
			c.statsd().Incr("datadog.tracer.stats.spans_in", nil, 1)
			c.add(s)
		case <-c.stop:
			return
		}
	}
}

// statsd returns any tracer configured statsd client, or a no-op.
func (c *pipelineConcentrator) statsd() statsdClient {
	if c.cfg.statsd == nil {
		return &statsd.NoOpClient{}
	}
	return c.cfg.statsd
}

func (c *pipelineConcentrator) Start() {
	if atomic.SwapUint64(&c.stopped, 0) == 0 {
		// already running
		log.Warn("(*concentrator).Start called more than once. This is likely a programming error.")
		return
	}
	c.stop = make(chan struct{})
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		tick := time.NewTicker(c.bucketDuration)
		defer tick.Stop()
		c.runFlusher(tick.C)
	}()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runIngester()
	}()
}

func (c *pipelineConcentrator) Stop() {
	if atomic.SwapUint64(&c.stopped, 1) > 0 {
		return
	}
	close(c.stop)
	c.wg.Wait()
}

func (c *pipelineConcentrator) runFlusher(tick <-chan time.Time) {
	for {
		select {
		case now := <-tick:
			p := c.flush(now)
			c.send(p)
			// if err := c.cfg.transport.sendPipelineStats(&p); err != nil {
		// 		c.statsd().Incr("datadog.tracer.pipeline_stats.flush_errors", nil, 1)
		// 		log.Error("Error sending pipeline stats payload: %v", err)
		// 	}
		case <-c.stop:
			c.send(c.flushAll())
			return
		}
	}
}

func (c *pipelineConcentrator) send(p pipelineStatsPayload) {
	if len(p.Stats) == 0 {
		// nothing to flush
		return
	}
	c.statsd().Incr("datadog.tracer.pipeline_stats.flush_payloads", nil, 1)
	c.statsd().Incr("datadog.tracer.pipeline_stats.flush_buckets", nil, float64(len(p.Stats)))


	for _, bucket := range p.Stats {
		for _, s := range bucket.Stats {
			var pb sketchpb.DDSketch
			err := proto.Unmarshal(s.Summary, &pb)
			if err != nil {
				log.Error("failed to unmarshal sketch")
				continue
			}
			sketch, err := ddsketch.FromProto(&pb)
			if err != nil {
				log.Error("failed to de-serialize sketch")
				continue
			}
			// todo[piochelepiotr] Flush all the sketch at once.
			sketch.ForEach(func(value, count float64) bool {
				tags := []string{
					"pipeline_name:"+s.ReceivingPipelineName,
					"service:"+s.Service,
					fmt.Sprintf("pipeline_hash:%d", s.PipelineHash),
					fmt.Sprintf("parent_hash:%d", s.ParentHash),
				}
				for i := 0; i < int(count); i++ {
					c.statsd().Distribution("dd.pipeline", value, tags, 1)
				}
				return false
			})
		}
	}
}

func (c *pipelineConcentrator) flushBucket(bucketStart int64) pipelineStatsBucket {
	bucket := c.buckets[bucketStart]
	// todo[piochelepiotr] Re-use sketches.
	delete(c.buckets, bucketStart)
	return pipelineStatsBucket{
		Start: uint64(bucketStart),
		Duration: uint64(c.bucketDuration.Nanoseconds()),
		Stats: bucket.Export(),
	}
}

func (c *pipelineConcentrator) flush(timenow time.Time) pipelineStatsPayload {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := timenow.UnixNano()
	sp := pipelineStatsPayload{
		Hostname: c.cfg.hostname,
		Env:      c.cfg.env,
		Version:  c.cfg.version,
		Stats:    make([]pipelineStatsBucket, 0, len(c.buckets)),
	}
	for ts := range c.buckets {
		if ts > now-c.bucketDuration.Nanoseconds() {
			// do not flush the current bucket
			continue
		}
		sp.Stats = append(sp.Stats, c.flushBucket(ts))
	}
	return sp
}

func (c *pipelineConcentrator) flushAll() pipelineStatsPayload {
	sp := pipelineStatsPayload{
		Hostname: c.cfg.hostname,
		Env:      c.cfg.env,
		Version:  c.cfg.version,
		Stats:    make([]pipelineStatsBucket, 0, len(c.buckets)),
	}
	for ts := range c.buckets {
		sp.Stats = append(sp.Stats, c.flushBucket(ts))
	}
	return sp
}
