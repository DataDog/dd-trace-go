package mocktracer

import (
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/ddtrace"
)

var _ ddtrace.SpanContext = (*spanContext)(nil)

type spanContext struct {
	sync.RWMutex // guards baggage
	baggage      map[string]string

	spanID  uint64
	traceID uint64
	span    *mockspan // context owner
}

func (sc *spanContext) ForeachBaggageItem(handler func(k, v string) bool) {
	sc.RLock()
	defer sc.RUnlock()
	for k, v := range sc.baggage {
		if !handler(k, v) {
			break
		}
	}
}

func (sc *spanContext) setBaggageItem(k, v string) {
	sc.Lock()
	defer sc.Unlock()
	if sc.baggage == nil {
		sc.baggage = make(map[string]string, 1)
	}
	sc.baggage[k] = v
}

func (sc *spanContext) baggageItem(k string) string {
	sc.RLock()
	defer sc.RUnlock()
	return sc.baggage[k]
}

var mockIDSource uint64 = 123

func nextID() uint64 { return atomic.AddUint64(&mockIDSource, 1) }
