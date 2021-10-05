package tracer

import (
	"github.com/DataDog/datadog-go/statsd"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/golang/protobuf/proto"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"sync"
	"sync/atomic"
	"time"
)

type pipelineStatsPoint struct {
	service string
	receivingPipelineName string
	summary *ddsketch.DDSketch
}

type bucket map[uint64]pipelineStatsPoint

func (b bucket) Export() []groupedPipelineStats {
	stats := make([]groupedPipelineStats, 0, len(b))
	for h, s := range b {
		// todo[piochelepiotr] Used optimized ddsketch.
		summary, err := proto.Marshal(s.summary.ToProto())
		if err != nil {
			log.Error("Failed to serialize sketch with err:%v", err)
			continue
		}
		stats = append(stats, groupedPipelineStats{
			Summary: summary,
			Service: s.service,
			ReceivingPipelineName: s.receivingPipelineName,
			PipelineHash: h,
		})
	}
	return stats
}

type pipelineConcentrator struct {
	In chan dataPipeline

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
		In: make(chan dataPipeline, 10000),
		stopped: 1,
		bucketDuration: bucketDuration,
		cfg: c,
	}
}

func (c *pipelineConcentrator) add(p dataPipeline) {
	btime := p.callTime.Truncate(c.bucketDuration).UnixNano()
	b, ok := c.buckets[btime]
	if !ok {
		b = make(bucket)
		c.buckets[btime] = b
	}
	// aggregate
	for _, l := range p.latencies {
		currentPoint, ok := b[l.Hash]
		if ok {
			if err := currentPoint.summary.MergeWith(l.Summary); err != nil {
				log.Error("failed to merge sketches. Ignoring %v.", err)
			}
		} else {
			b[l.Hash] = pipelineStatsPoint{
				service: p.service,
				receivingPipelineName: p.pipelineName,
				summary: l.Summary.Copy(),
			}
		}
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
			if len(p.Stats) == 0 {
				// nothing to flush
				continue
			}
			c.statsd().Incr("datadog.tracer.pipeline_stats.flush_payloads", nil, 1)
			c.statsd().Incr("datadog.tracer.pipeline_stats.flush_buckets", nil, float64(len(p.Stats)))
			if err := c.cfg.transport.sendPipelineStats(&p); err != nil {
				c.statsd().Incr("datadog.tracer.pipeline_stats.flush_errors", nil, 1)
				log.Error("Error sending pipeline stats payload: %v", err)
			}
		case <-c.stop:
			c.flushAll()
			return
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
		log.Info("flushing bucket %d", ts)
		log.Debug("Flushing bucket %d", ts)
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
