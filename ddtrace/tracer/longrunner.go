// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"sync"
	"time"
)

const NumShards = 32

// TrackingExpirationLimit is the limit for how long to track a long-running span before no longer sending snapshots
const TrackingExpirationLimit = 12 * time.Hour

type longrunner struct {
	// Any span living longer than heartbeatInterval will have heartbeats sent every interval
	heartbeatInterval time.Duration
	statsd            statsdClient
	// stopFunc is a fire exactly once method to ensure we don't try to stop more than once
	stopFunc sync.Once
	// done chan stops the long-running "work" goroutine
	done chan struct{}
	// spans is a map of tracked spans to their "partial_version"
	spanShards []shard
}

type shard struct {
	lock  *sync.Mutex
	spans map[*span]int
}

func longrunningSpansEnabled(c *config) bool {
	if c.longRunningEnabled && !c.agent.Info {
		log.Warn("Long running span tracking requires a newer agent version than is connected")
		return false
	}
	return c.longRunningEnabled
}

func limitHeartbeat(hb int64) time.Duration {
	hbDur := time.Duration(hb)
	const minInterval = 20 * time.Second
	// This maxKeepAliveInterval is to avoid a limitation where after 15 minutes this trace chunk
	// will be dropped in the backend. We use half the 15 minutes to avoid the worst case where a span is
	// opened right after a flush and is forced to wait for double the interval.
	const maxKeepAliveInterval = (7 * time.Minute) + (30 * time.Second)
	switch {
	case hbDur < minInterval:
		log.Warn("Long Running Span Configured interval too short, defaulting to minimum 20 seconds")
		return minInterval
	case hbDur > maxKeepAliveInterval:
		log.Warn("Long Running Span Configured interval too long, defaulting to maximum 7.5 minutes")
		return maxKeepAliveInterval
	default:
		return hbDur
	}
}

func getShardNum(s *span) int {
	s.RLock()
	defer s.RUnlock()
	// splitmix64 used to get an even distribution across spanShards faster than SHA
	// ~10x improvement when benchmarked on an i7-1068NG7
	return int(splitMix64(s.SpanID))
}

func splitMix64(n uint64) uint64 {
	n = n + 0x9e3779b97f4a7c15
	n = (n ^ (n >> 30)) * 0xbf58476d1ce4e5b9
	n = (n ^ (n >> 27)) * 0x94d049bb133111eb
	return (n ^ (n >> 31)) % NumShards
}

// newLongrunner creates the default longrunner struct
func newLongrunner(hbInterval int64, sd statsdClient) *longrunner {
	s := make([]shard, 32)
	for i := 0; i < NumShards; i++ {
		s[i] = shard{
			lock:  &sync.Mutex{},
			spans: map[*span]int{},
		}
	}
	hb := limitHeartbeat(hbInterval)
	lr := longrunner{
		heartbeatInterval: hb,
		statsd:            sd,
		stopFunc:          sync.Once{},
		done:              make(chan struct{}),
		spanShards:        s,
	}
	return &lr
}

// startLongrunner creates a long-running span tracker
func startLongrunner(hbInterval int64, sd statsdClient) *longrunner {
	lr := newLongrunner(hbInterval, sd)
	ticker := time.NewTicker(lr.heartbeatInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				lr.work(now())
			case <-lr.done:
				return
			}
		}
	}()
	return lr
}

func (lr *longrunner) stop() {
	lr.stopFunc.Do(func() {
		lr.done <- struct{}{}
	})
}

func (lr *longrunner) trackSpan(s *span) {
	shard := lr.spanShards[getShardNum(s)]
	shard.lock.Lock()
	defer shard.lock.Unlock()
	if _, found := shard.spans[s]; !found {
		shard.spans[s] = 1
	}
}

func (lr *longrunner) stopTracking(s *span) {
	shard := lr.spanShards[getShardNum(s)]
	shard.lock.Lock()
	defer shard.lock.Unlock()

	delete(shard.spans, s)
}

func (lr *longrunner) work(now int64) {
	for _, shard := range lr.spanShards {
		func() {
			shard.lock.Lock()
			defer shard.lock.Unlock()
			for s, partialVersion := range shard.spans {
				func() {
					s.RLock()
					defer s.RUnlock()

					if s.finished {
						delete(shard.spans, s)
						return
					}
					if (s.Start + TrackingExpirationLimit.Nanoseconds()) < now {
						lr.statsd.Incr("datadog.tracer.longrunning.expired", nil, 1)
						delete(shard.spans, s)
						return
					}

					if now > s.Start+lr.heartbeatInterval.Nanoseconds() {
						meta := make(map[string]string, len(s.Meta))
						for k, v := range s.Meta {
							meta[k] = v
						}
						metrics := make(map[string]float64, len(s.Metrics))
						for k, v := range s.Metrics {
							metrics[k] = v
						}

						heartBeatSpan := span{
							Name:            s.Name,
							Service:         s.Service,
							Resource:        s.Resource,
							Type:            s.Type,
							Start:           s.Start,
							Duration:        now - s.Start,
							Meta:            meta,
							Metrics:         metrics,
							SpanID:          s.SpanID,
							TraceID:         s.TraceID,
							ParentID:        s.ParentID,
							Error:           s.Error,
							noDebugStack:    s.noDebugStack,
							finished:        s.finished,
							context:         s.context,
							pprofCtxActive:  s.pprofCtxActive,
							pprofCtxRestore: s.pprofCtxRestore,
						}

						// Need to pull and send finished spans from within this trace (removing the ones we send)
						var childrenOfHeartbeat []*span
						func() {
							t := s.context.trace
							t.mu.Lock()
							defer t.mu.Unlock()

							var unfinishedSpans []*span
							for i, childSpan := range t.spans {
								if childSpan.finished {
									childrenOfHeartbeat = append(childrenOfHeartbeat, childSpan)
									t.spans[i] = nil
									t.finished--
								} else {
									unfinishedSpans = append(unfinishedSpans, childSpan)
								}
							}
							t.spans = unfinishedSpans
						}()

						heartBeatSpan.setMetric("_dd.partial_version", float64(partialVersion))
						shard.spans[s]++

						lr.statsd.Incr("datadog.tracer.longrunning.flushed", nil, 1)

						// TODO: find a good way to test this
						if t, ok := internal.GetGlobalTracer().(*tracer); ok {
							t.pushTrace(append(childrenOfHeartbeat, &heartBeatSpan))
						}
					}
				}()
			}
		}()
	}
}
