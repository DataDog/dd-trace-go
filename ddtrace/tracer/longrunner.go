package tracer

import (
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

// Any span living longer than heartbeatInterval will have heartbeats sent
var heartbeatInterval = 5 * time.Minute //todo: should this time be configurable?

//TODO: is there a better performing design than this?
type longrunner struct {
	mu    sync.Mutex
	spans map[*span]int
}

// startLongrunner creates a long-running span tracker
func startLongrunner() *longrunner {
	lr := longrunner{
		mu:    sync.Mutex{},
		spans: map[*span]int{},
	}

	ticker := time.NewTicker(heartbeatInterval)
	go func() {
		for {
			//todo: gracefully stop when the tracer is stopped
			<-ticker.C
			lr.work()
		}
	}()
	return &lr
}

func (lr *longrunner) trackSpan(s *span) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	lr.spans[s] = 1
}

func (lr *longrunner) stopTracking(s *span) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	delete(lr.spans, s)
}

func (lr *longrunner) work() {
	//todo: don't hold the lock this long
	lr.mu.Lock()
	defer lr.mu.Unlock()

	for s, partialVersion := range lr.spans {
		s.RLock()
		if s.finished { //todo: Do we also remove spans that are too old(perhaps 24 hours)?
			delete(lr.spans, s)
		}

		if now() > s.Start+heartbeatInterval.Nanoseconds() {
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
				Duration:        now() - s.Start,
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

			if t, ok := internal.GetGlobalTracer().(*tracer); ok {
				t.pushTrace(append(childrenOfHeartbeat, &heartBeatSpan))
			}
		}
		s.RUnlock()
	}
}
