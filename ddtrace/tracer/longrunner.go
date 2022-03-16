// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

// Any span living longer than heartbeatInterval will have heartbeats sent every interval
var heartbeatInterval time.Duration

//TODO: is there a better performing design than this?
type longrunner struct {
	statsd statsdClient
	// stopFunc is a fire exactly once method to ensure we don't try to stop more than once
	stopFunc sync.Once
	// done chan stops the long-running "work" goroutine
	done chan struct{}
	// mu protects the lower fields
	mu sync.Mutex
	// spans is a map of tracked spans to their "partial_version"
	spans map[*span]int
}

func longrunningSpansEnabled(c *config) bool {
	if c.longRunningEnabled && !c.agent.Info {
		log.Warn("Long running span tracking requires a newer agent version than is connected")
		return false
	}
	return c.longRunningEnabled
}

// startLongrunner creates a long-running span tracker
func startLongrunner(hbInterval int64, sd statsdClient) *longrunner {
	heartbeatInterval = time.Duration(hbInterval)
	lr := longrunner{
		statsd:   sd,
		stopFunc: sync.Once{},
		done:     make(chan struct{}),
		mu:       sync.Mutex{},
		spans:    map[*span]int{},
	}

	ticker := time.NewTicker(heartbeatInterval)
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
	return &lr
}

func (lr *longrunner) stop() {
	lr.stopFunc.Do(func() {
		lr.done <- struct{}{}
	})
}

func (lr *longrunner) trackSpan(s *span) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if _, found := lr.spans[s]; !found {
		lr.spans[s] = 1
	}
}

func (lr *longrunner) stopTracking(s *span) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	delete(lr.spans, s)
}

func (lr *longrunner) work(now int64) {
	//todo: don't hold the lock this long
	lr.mu.Lock()
	defer lr.mu.Unlock()

	for s, partialVersion := range lr.spans {
		s.RLock()
		if s.finished { //todo: Do we also remove spans that are too old(perhaps 24 hours)?
			delete(lr.spans, s)
		}

		if now > s.Start+heartbeatInterval.Nanoseconds() {
			meta := make(map[string]string, len(s.Meta))
			for k, v := range s.Meta {
				meta[k] = v
			}
			metrics := make(map[string]float64, len(s.Metrics))
			for k, v := range s.Metrics {
				metrics[k] = v
			}
			//Unmark span snapshots as "top_level" to avoid stats computation in the agent
			delete(metrics, keyTopLevel)

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

			//need to pull and send finished spans from within this trace (removing the ones we send)
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
			lr.spans[s]++

			lr.statsd.Incr("datadog.tracer.longrunning.flushed", nil, 1)

			//TODO: find a good way to test this
			if t, ok := internal.GetGlobalTracer().(*tracer); ok {
				t.pushTrace(append(childrenOfHeartbeat, &heartBeatSpan))
			}
		}
		s.RUnlock()
	}
}
