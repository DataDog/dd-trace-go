package tracer

import (
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"sync"
	"testing"
	"time"
)

func TestWorkRemovesFinishedSpans(t *testing.T) {
	lr := longrunner{
		mu: sync.Mutex{},
		spans: map[*span]int{
			&span{
				RWMutex:  sync.RWMutex{},
				Start:    1,
				finished: true,
			}: 1,
		},
	}

	lr.work(1)

	assert.Empty(t, lr.spans)
}

func TestWorkAlreadyFinishedSpansInTraceAreRemoved(t *testing.T) {
	heartbeatInterval = 1
	finishedS := &span{
		RWMutex:  sync.RWMutex{},
		Start:    1,
		finished: true,
	}
	s := &span{
		RWMutex: sync.RWMutex{},
		Start:   1,
	}
	s.context = newSpanContext(s, nil)
	s.context.trace.push(finishedS)
	s.context.trace.finishedOne(finishedS)
	lr := longrunner{
		mu: sync.Mutex{},
		spans: map[*span]int{
			s: 1,
		},
	}

	lr.work(3)

	assert.NotEmpty(t, lr.spans)
	assert.Equal(t, lr.spans[s], 2)
	assert.Equal(t, s.context.trace.finished, 0)
	assert.Len(t, s.context.trace.spans, 1)
}

func BenchmarkLR(b *testing.B) {
	internal.SetGlobalTracer(&internal.NoopTracer{})
	heartbeatInterval = 10 * time.Millisecond
	lr := startLongrunner(int64(heartbeatInterval))
	wg := sync.WaitGroup{}
	for i := 0; i < b.N; i++ {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			//waitTime := i
			go func() {
				s := &span{
					Name:     "testspan",
					Service:  "bench",
					Resource: "",
					SpanID:   random.Uint64(),
					TraceID:  random.Uint64(),
					Start:    now(),
				}
				s.context = newSpanContext(s, nil)
				lr.trackSpan(s)
				//every 10th is "long running"
				//if waitTime%10 == 0 {
				//	time.Sleep(heartbeatInterval * 2)
				//} else {
				//	time.Sleep(time.Duration(waitTime))
				//}
				lr.stopTracking(s)
				wg.Done()
			}()
		}
		wg.Wait()
	}
}

func BenchmarkLRWork(b *testing.B) {
	internal.SetGlobalTracer(&internal.NoopTracer{})
	heartbeatInterval = 1 * time.Hour //Large time so we can call work manually
	lr := startLongrunner(int64(heartbeatInterval))
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		lr.spans = map[*span]int{}
		var spans []*span
		for i := 0; i < 100; i++ {
			s := &span{
				Name:     "testspan",
				Service:  "bench",
				Resource: "",
				SpanID:   random.Uint64(),
				TraceID:  random.Uint64(),
				Start:    now() - (heartbeatInterval.Nanoseconds() * 2),
			}
			s.context = newSpanContext(s, nil)
			lr.trackSpan(s)
			spans = append(spans, s)
		}
		b.StartTimer()
		lr.work(now())
	}
}
