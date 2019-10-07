package tracer

import (
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

// droppedSpan represents a span which was dropped by the local sampler.
type droppedSpan struct {
	traceID uint64
	once    sync.Once
	sctx    *droppedSpanContext
}

func (*droppedSpan) SetTag(_ string, _ interface{})      {}
func (*droppedSpan) SetOperationName(_ string)           {}
func (*droppedSpan) BaggageItem(_ string) string         { return "" }
func (*droppedSpan) SetBaggageItem(_ string, val string) {}
func (*droppedSpan) Finish(_ ...ddtrace.FinishOption)    {}

// Context returns the span context of this dropped span. It ensures that distributed
// parts of a trace are also dropped, in order to avoid broken traces, but that they
// do reach the agent for stats computations.
func (d *droppedSpan) Context() ddtrace.SpanContext {
	d.once.Do(func() {
		sctx := &spanContext{
			spanID:  d.traceID,
			traceID: d.traceID,
			trace: &trace{
				priority: func(i float64) *float64 {
					return &i
				}(ext.PriorityUserReject),
			},
		}
		d.sctx = &droppedSpanContext{
			droppedSpan: d,
			spanContext: sctx,
		}
	})
	return d.sctx
}

type droppedSpanContext struct {
	*spanContext
	droppedSpan *droppedSpan
}
