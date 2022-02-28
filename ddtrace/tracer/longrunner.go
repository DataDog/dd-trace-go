package tracer

import (
	"sync"
	"time"
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
var lr *longrunner

// Any span living longer than heartbeatStart will have heartbeats sent
const heartbeatStart = 15 * time.Second //todo: what should this value be

type longrunner struct {
	insmu   sync.Mutex
	inspans []*span

	smu   sync.Mutex
	spans []*span
}

func trackSpan(s *span) {
	initRunner.Do(func() {
		lr = &longrunner{spans: []*span{}}
		//todo: should this time be configurable?
		// longer timer means less cpu usage but more memory
		timer := time.NewTimer(1 * time.Second)
		go func() {
			for {
				//todo: stop
				<-timer.C
				lr.work()
			}
		}()
	})
	lr.insmu.Lock()
	defer lr.insmu.Unlock()

	lr.spans = append(lr.spans, s)
}

func (lr *longrunner) work() {
	//todo: if work is single-threaded this mutex may be unnecessary
	lr.smu.Lock()
	var newIn []*span
	func() {
		lr.insmu.Lock()
		defer lr.insmu.Unlock()
		newIn = make([]*span, len(lr.inspans))
		copy(newIn, lr.inspans)
		lr.inspans = []*span{}
	}()
	defer lr.smu.Unlock()

	//todo: is it faster to do this and walk everything?
	// Probably depends on how long the average span lives for
	// if the vast majority of spans are short lived (read: < work flush rate)
	// then we can save a lot of memory / allocations by just dropping those spans
	//lr.spans = append(lr.spans, newIn...)
	for _, s := range newIn {
		s.RLock() //todo: is it safe to just read `s.finished`
		//If a new span is already finished it's not long-running, ignore it
		if !s.finished {
			lr.spans = append(lr.spans, s)
		}
		s.RUnlock()
	}

	i := 0
	for _, s := range lr.spans {
		s.RLock()
		if !s.finished { //todo: also expire really old spans
			lr.spans[i] = s
			i++
			if s.Start+heartbeatStart.Nanoseconds() > time.Now().UnixNano() {
				//oh, just send heartbeats every time `work` runs. This gets more complicated
				// if we want to disconnect the frequency of heartbeats with the frequency of cache flushes
				// (could have a second routine that is responsible just for evicting things perhaps)

				//do we bucket these together to be sent asynchronously and as a group?
				//TODO: send/schedule the heartbeat
			}
		}

		s.RUnlock()
	}
	//truncate slice
	for j := i; j < len(lr.spans); j++ {
		lr.spans[j] = nil
	}
	lr.spans = lr.spans[:i]

}
