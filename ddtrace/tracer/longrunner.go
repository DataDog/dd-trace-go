package tracer

import (
	"strconv"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// option 1.
// Spans added to a cache
// some number of goroutines periodically check the cache for "old" spans
// old spans have a heart beat sent on their behalf
// when a span is finished or gets "too old" it is removed from the cache

// open questions:
//	if a span is marked as "not keep" do we still send heartbeats?
// 	can a span go from being "not keep"
//		yes

var initRunner sync.Once

// Any span living longer than heartbeatStart will have heartbeats sent
const heartbeatStart = 5 * time.Minute //todo: what should this value be

type longrunner struct {
	mu    sync.Mutex
	spans map[*span]struct{} //todo: sync map might be better (or worse since this is likely to be more write than read heavy)
}

func (lr *longrunner) trackSpan(s *span) {
	initRunner.Do(func() {
		lr = &longrunner{spans: make(map[*span]struct{})}
		//todo: should this time be configurable?
		// longer timer means less cpu usage but more memory
		ticker := time.NewTicker(heartbeatStart)
		go func() {
			for {
				//todo: stop
				<-ticker.C
				lr.work()
			}
		}()
	})
	lr.mu.Lock()
	defer lr.mu.Unlock()

	lr.spans[s] = struct{}{}
}

func (lr *longrunner) stopTracking(s *span) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	delete(lr.spans, s)
}

func (lr *longrunner) work() {
	//todo: don't hold the lock this long
	log.Info("starting work loop")
	lr.mu.Lock()
	defer lr.mu.Unlock()

	for s := range lr.spans {
		s.Lock()
		if s.finished { //todo OR span is too old
			delete(lr.spans, s)
		}

		duration := time.Duration(now() - s.Start)
		log.Info("checking %s with dur %s now %s and start+beatdelay %s", s.Name, duration, now(), (s.Start + heartbeatStart.Nanoseconds()))

		if now() > s.Start+heartbeatStart.Nanoseconds() {
			//TODO: if we want to disconnect the frequency of heartbeats with the frequency of cache flushes

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
					// childSpan.RLock() //CANT DO THIS since our own already locked span is in this list
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

			var correlationId string
			if cid, ok := s.Meta["correlation-id"]; ok {
				correlationId = cid
			} else {
				correlationId = strconv.FormatUint(s.SpanID, 10)
				s.setMeta("correlation-id", correlationId)
			}

			heartBeatSpan.setMeta("correlation-id", correlationId)
			s.SpanID = random.Uint64()

			log.Info("sending heartbeat for long running span %s with %d children", s.Name, len(childrenOfHeartbeat))
			if t, ok := internal.GetGlobalTracer().(*tracer); ok {
				t.pushTrace(append(childrenOfHeartbeat, &heartBeatSpan))
			}
		}
		s.Unlock()
	}
}
