package tracer

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
)

var _ opentracing.Span = (*openSpan)(nil)

// span implements opentracing.Span on top of ddtrace.Span.
type openSpan struct {
	ddtrace.Span
	*opentracer
}

func (s *openSpan) Context() opentracing.SpanContext                      { return s.Span.Context() }
func (s *openSpan) Finish()                                               { s.Span.Finish() }
func (s *openSpan) Tracer() opentracing.Tracer                            { return s.opentracer }
func (s *openSpan) LogEvent(event string)                                 { /* deprecated */ }
func (s *openSpan) LogEventWithPayload(event string, payload interface{}) { /* deprecated */ }
func (s *openSpan) Log(data opentracing.LogData)                          { /* deprecated */ }

func (s *openSpan) FinishWithOptions(opts opentracing.FinishOptions) {
	for _, lr := range opts.LogRecords {
		if len(lr.Fields) > 0 {
			s.LogFields(lr.Fields...)
		}
	}
	s.Span.Finish(FinishTime(opts.FinishTime))
}

func (s *openSpan) LogFields(fields ...log.Field) {
	// catch standard opentracing keys and adjust to internal ones as per spec:
	// https://github.com/opentracing/specification/blob/master/semantic_conventions.md#log-fields-table
	for _, f := range fields {
		switch f.Key() {
		case "event":
			if v, ok := f.Value().(string); ok && v == "error" {
				s.SetTag("error", true)
			}
		case "error", "error.object":
			if err, ok := f.Value().(error); ok {
				s.SetTag("error", err)
			}
		case "message":
			s.SetTag(ext.ErrorMsg, fmt.Sprint(f.Value()))
		case "stack":
			s.SetTag(ext.ErrorStack, fmt.Sprint(f.Value()))
		default:
			// not implemented
		}
	}
}

func (s *openSpan) LogKV(keyVals ...interface{}) {
	fields, err := log.InterleavedKVToFields(keyVals...)
	if err != nil {
		// TODO(gbbr): create a log package
		return
	}
	s.LogFields(fields...)
}

func (s *openSpan) SetBaggageItem(key, val string) opentracing.Span {
	s.Span.SetBaggageItem(key, val)
	return s
}

func (s *openSpan) SetOperationName(operationName string) opentracing.Span {
	s.Span.SetOperationName(operationName)
	return s
}

func (s *openSpan) SetTag(key string, value interface{}) opentracing.Span {
	s.Span.SetTag(key, value)
	return s
}
