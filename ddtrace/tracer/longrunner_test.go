package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"sync"
	"testing"
	"time"
)

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
		lr.work()
	}
}
