package tracer

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/ddtrace"
	"github.com/DataDog/dd-trace-go/ddtrace/ext"
)

var _ ddtrace.Span = (*span)(nil)

// span represents a computation. Callers must call Finish when a span is
// complete to ensure it's submitted.
//
//	span := tracer.NewRootSpan("web.request", "datadog.com", "/user/{id}")
//	defer span.Finish()  // or FinishWithErr(err)
//
// In general, spans should be created with the tracer.NewSpan* functions,
// so they will be submitted on completion.
type span struct {
	// Name is the name of the operation being measured. Some examples
	// might be "http.handler", "fileserver.upload" or "video.decompress".
	// Name should be set on every span.
	Name string `json:"name"`

	// Service is the name of the process doing a particular job. Some
	// examples might be "user-database" or "datadog-web-app". Services
	// will be inherited from parents, so only set this in your app's
	// top level span.
	Service string `json:"service"`

	// Resource is a query to a service. A web application might use
	// resources like "/user/{user_id}". A sql database might use resources
	// like "select * from user where id = ?".
	//
	// You can track thousands of resources (not millions or billions) so
	// prefer normalized resources like "/user/{id}" to "/user/123".
	//
	// Resources should only be set on an app's top level spans.
	Resource string `json:"resource"`

	Type     string             `json:"type"`              // protocol associated with the span
	Start    int64              `json:"start"`             // span start time expressed in nanoseconds since epoch
	Duration int64              `json:"duration"`          // duration of the span expressed in nanoseconds
	Meta     map[string]string  `json:"meta,omitempty"`    // arbitrary map of metadata
	Metrics  map[string]float64 `json:"metrics,omitempty"` // arbitrary map of numeric metrics
	SpanID   uint64             `json:"span_id"`           // identifier of this span
	TraceID  uint64             `json:"trace_id"`          // identifier of the root span
	ParentID uint64             `json:"parent_id"`         // identifier of the span's direct parent
	Error    int32              `json:"error"`             // error status of the span; 0 means no errors

	sync.RWMutex
	tracer   *tracer // the tracer that generated this span
	finished bool    // true if the span has been submitted to a tracer.

	// parent contains a link to the parent. In most cases, ParentID can be inferred from this.
	// However, ParentID can technically be overridden (typical usage: distributed tracing)
	// and also, parent == nil is used to identify root and top-level ("local root") spans.
	parent  *span
	context *spanContext
}

// Context yields the SpanContext for this Span. Note that the return
// value of Context() is still valid after a call to Finish().
func (s *span) Context() ddtrace.SpanContext { return s.context }

// SetBaggageItem sets a key/value pair as baggage on the span.
func (s *span) SetBaggageItem(key, val string) ddtrace.Span {
	s.context.setBaggageItem(key, val)
	return s
}

// BaggageItem gets the value for a baggage item given its key. Returns the
// empty string if the value isn't found in this Span.
func (s *span) BaggageItem(key string) string {
	return s.context.baggageItem(key)
}

// SetTag adds a tag to the span, overwriting pre-existing values for
// the given key.
func (s *span) SetTag(key string, value interface{}) ddtrace.Span {
	s.Lock()
	defer s.Unlock()
	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return s
	}
	if v, ok := toFloat64(value); ok {
		// sent as numeric value, so we can store it as a metric
		switch key {
		case ext.SamplingPriority:
			// setting sampling priority per spec
			s.Metrics[samplingPriorityKey] = v
		default:
			s.Metrics[key] = v
		}
		return s
	}
	switch key {
	case ext.ServiceName:
		s.Service = fmt.Sprint(value)
	case ext.ResourceName:
		s.Resource = fmt.Sprint(value)
	case ext.SpanType:
		s.Type = fmt.Sprint(value)
	case ext.Error:
		switch v := value.(type) {
		case bool:
			// bool value as per Opentracing spec.
			if !v {
				s.Error = 0
			} else {
				s.Error = 1
			}
		case error:
			// if anyone sets an error value as the tag, be nice here
			// and provide all the benefits.
			s.Error = 1
			s.Meta[ext.ErrorMsg] = v.Error()
			s.Meta[ext.ErrorType] = reflect.TypeOf(v).String()
			s.Meta[ext.ErrorStack] = string(debug.Stack())
		case nil:
			// no error
			s.Error = 0
		default:
			// in all other cases, let's assume that setting this tag
			// is the result of an error.
			s.Error = 1
		}
	default:
		// regular string tag
		s.Meta[key] = fmt.Sprint(value)
	}
	return s
}

// Finish closes this Span (but not its children) providing the duration
// of this part of the tracing session. This method is idempotent so
// calling this method multiple times is safe and doesn't update the
// current Span. Once a Span has been finished, methods that modify the Span
// will become no-ops.
func (s *span) Finish(opts ...ddtrace.FinishOption) {
	var cfg ddtrace.FinishConfig
	for _, fn := range opts {
		fn(&cfg)
	}
	var t int64
	if cfg.FinishTime.IsZero() {
		t = now()
	} else {
		t = cfg.FinishTime.UnixNano()
	}
	if cfg.Error != nil {
		s.SetTag(ext.Error, cfg.Error)
	}
	s.finish(t)
}

// SetOperationName sets or changes the operation name.
func (s *span) SetOperationName(operationName string) ddtrace.Span {
	s.Lock()
	defer s.Unlock()

	s.Name = operationName
	return s
}

func (s *span) finish(finishTime int64) {
	s.Lock()
	defer s.Unlock()
	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		// already finished
		return
	}
	if s.Duration == 0 {
		s.Duration = finishTime - s.Start
	}
	s.finished = true

	if !s.context.sampled {
		// not sampled
		return
	}
	s.context.finish()
}

// String returns a human readable representation of the span. Not for
// production, just debugging.
func (s *span) String() string {
	lines := []string{
		fmt.Sprintf("Name: %s", s.Name),
		fmt.Sprintf("Service: %s", s.Service),
		fmt.Sprintf("Resource: %s", s.Resource),
		fmt.Sprintf("TraceID: %d", s.TraceID),
		fmt.Sprintf("SpanID: %d", s.SpanID),
		fmt.Sprintf("ParentID: %d", s.ParentID),
		fmt.Sprintf("Start: %s", time.Unix(0, s.Start)),
		fmt.Sprintf("Duration: %s", time.Duration(s.Duration)),
		fmt.Sprintf("Error: %d", s.Error),
		fmt.Sprintf("Type: %s", s.Type),
		"Tags:",
	}
	s.RLock()
	for key, val := range s.Meta {
		lines = append(lines, fmt.Sprintf("\t%s:%s", key, val))
	}
	s.RUnlock()
	return strings.Join(lines, "\n")
}

const samplingPriorityKey = "_sampling_priority_v1"
