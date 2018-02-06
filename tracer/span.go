package tracer

import (
	"fmt"
	stdlog "log"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/opentracing/opentracing-go/log"
)

var _ opentracing.Span = (*span)(nil)

// Span represents a computation. Callers must call Finish when a span is
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
	Sampled  bool               `json:"-"`                 // if this span is sampled (and should be kept/recorded) or not

	sync.RWMutex
	tracer   *Tracer // the tracer that generated this span
	finished bool    // true if the span has been submitted to a tracer.

	// parent contains a link to the parent. In most cases, ParentID can be inferred from this.
	// However, ParentID can technically be overridden (typical usage: distributed tracing)
	// and also, parent == nil is used to identify root and top-level ("local root") spans.
	parent  *span
	buffer  *spanBuffer
	context *spanContext
}

// Tracer provides access to the `Tracer`` that created this Span.
func (s *span) Tracer() opentracing.Tracer { return s.tracer }

// Context yields the SpanContext for this Span. Note that the return
// value of Context() is still valid after a call to Span.Finish(), as is
// a call to Span.Context() after a call to Span.Finish().
func (s *span) Context() opentracing.SpanContext {
	s.RLock()
	defer s.RUnlock()

	return s.context
}

// SetBaggageItem sets a key:value pair on this Span and its SpanContext
// that also propagates to descendants of this Span.
func (s *span) SetBaggageItem(key, val string) opentracing.Span {
	s.Lock()
	defer s.Unlock()

	s.context.setBaggageItem(key, val)
	return s
}

// BaggageItem gets the value for a baggage item given its key. Returns the empty string
// if the value isn't found in this Span.
func (s *span) BaggageItem(key string) string {
	s.Lock()
	defer s.Unlock()

	return s.context.baggage[key]
}

const (
	// SpanType defines the Span type (web, db, cache)
	SpanType = "span.type"
	// ServiceName defines the Service name for this Span
	ServiceName = "service.name"
	// ResourceName defines the Resource name for the Span
	ResourceName = "resource.name"
)

// SetTag adds a tag to the span, overwriting pre-existing values for
// the given `key`.
func (s *span) SetTag(key string, value interface{}) opentracing.Span {
	s.Lock()
	defer s.Unlock()
	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return s
	}
	if key == string(ext.SamplingPriority) {
		// setting sampling.priority per opentracing spec.
		if v, ok := value.(int); ok {
			s.setSamplingPriority(v)
		}
		return s
	}
	switch key {
	case ServiceName:
		s.Service = fmt.Sprint(value)
	case ResourceName:
		s.Resource = fmt.Sprint(value)
	case SpanType:
		s.Type = fmt.Sprint(value)
	case string(ext.Error):
		switch v := value.(type) {
		case bool:
			// bool value as per Opentracing spec.
			if !v {
				atomic.CompareAndSwapInt32(&s.Error, 1, 0)
			} else {
				atomic.CompareAndSwapInt32(&s.Error, 0, 1)
			}
		case error:
			// if anyone sets an error value as the tag, be nice here
			// and provide all the benefits.
			s.setError(v)
		case nil:
			// no error
			atomic.CompareAndSwapInt32(&s.Error, 1, 0)
		default:
			// in all other cases, let's assume that setting this tag
			// is the result of an error.
			atomic.CompareAndSwapInt32(&s.Error, 0, 1)
		}
	default:
		// regular string tag
		s.setMeta(key, fmt.Sprint(value))
	}
	return s
}

// Finish closes this Span (but not its children) providing the duration
// of this part of the tracing session. This method is idempotent so
// calling this method multiple times is safe and doesn't update the
// current Span. Once a Span has been finished, methods that modify the Span
// will become no-ops.
func (s *span) Finish() {
	s.finish(now())
}

// FinishWithOptions is like Finish() but with explicit control over
// timestamps and log data.
func (s *span) FinishWithOptions(options opentracing.FinishOptions) {
	if options.FinishTime.IsZero() {
		options.FinishTime = time.Now().UTC()
	}

	s.finish(options.FinishTime.UnixNano())
}

// SetOperationName sets or changes the operation name.
func (s *span) SetOperationName(operationName string) opentracing.Span {
	s.Lock()
	defer s.Unlock()

	s.Name = operationName
	return s
}

const (
	errorMsgKey   = "error.msg"
	errorTypeKey  = "error.type"
	errorStackKey = "error.stack"
)

// LogFields is an efficient and type-checked way to record key:value
// logging data about a Span, though the programming interface is a little
// more verbose than LogKV().
func (s *span) LogFields(fields ...log.Field) {
	var invalidFields bool
	s.Lock()
	defer s.Unlock()
	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return
	}
	// catch standard opentracing keys and adjust to internal ones as per spec:
	// https://github.com/opentracing/specification/blob/master/semantic_conventions.md#log-fields-table
	for _, f := range fields {
		switch f.Key() {
		case "event":
			if v, ok := f.Value().(string); ok && v == "error" {
				atomic.CompareAndSwapInt32(&s.Error, 0, 1)
			}
		case "error", "error.object":
			if err, ok := f.Value().(error); ok {
				s.setError(err)
			}
		case "message":
			s.setMeta(errorMsgKey, fmt.Sprint(f.Value()))
		case "stack":
			s.setMeta(errorStackKey, fmt.Sprint(f.Value()))
		default:
			invalidFields = true
		}
	}
	if s.tracer.config.debug && invalidFields {
		stdlog.Println(`LOGFIELDS: Valid log keys are: "error", "error.object", "message" and "stack".`)
	}
}

// LogKV is a concise, readable way to record key:value logging data about
// a span, though unfortunately this also makes it less efficient and less
// type-safe than LogFields().
func (s *span) LogKV(keyVals ...interface{}) {
	fields, err := log.InterleavedKVToFields(keyVals...)
	if err != nil {
		s.RLock()
		debug := s.tracer.config.debug
		s.RUnlock()
		if debug {
			stdlog.Printf("LogKV: %v\n", err)
			return
		}
	}
	s.LogFields(fields...)
}

// LogEvent is deprecated: use LogFields or LogKV
func (s *span) LogEvent(event string) {
	s.RLock()
	defer s.RUnlock()
	if s.tracer.config.debug {
		stdlog.Println("span.LogEvent is deprecated, use LogFields or LogKV.\n")
	}
}

// LogEventWithPayload deprecated: use LogFields or LogKV
func (s *span) LogEventWithPayload(event string, payload interface{}) {
	s.RLock()
	defer s.RUnlock()
	if s.tracer.config.debug {
		stdlog.Println("span.LogEventWithPayload is deprecated, use LogFields or LogKV.\n")
	}
}

// Log is deprecated: use LogFields or LogKV
func (s *span) Log(data opentracing.LogData) {
	s.RLock()
	defer s.RUnlock()
	if s.tracer.config.debug {
		stdlog.Println("span.Log is deprecated, use LogFields or LogKV.\n")
	}
}

// newSpan creates a new span. This is a low-level function, required for testing and advanced usage.
// Most of the time one should prefer the Tracer NewRootSpan or NewChildSpan methods.
func newSpan(name, service, resource string, spanID, traceID, parentID uint64, tracer *Tracer) *span {
	return &span{
		Name:     name,
		Service:  service,
		Resource: resource,
		Meta:     map[string]string{},
		Metrics:  map[string]float64{},
		SpanID:   spanID,
		TraceID:  traceID,
		ParentID: parentID,
		Start:    now(),
		Sampled:  true,
		tracer:   tracer,
	}
}

// setMeta adds an arbitrary meta field to the current Span. The span
// must be locked outside of this function
func (s *span) setMeta(key, value string) {
	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return
	}
	s.Meta[key] = value
}

// getMeta will return the value for the given tag or the empty string if it
// doesn't exist.
func (s *span) getMeta(key string) string {
	s.RLock()
	defer s.RUnlock()
	return s.Meta[key]
}

// setMetric sets a float64 value for the given key. It acts
// like `set_meta()` and it simply add a tag without further processing.
// This method doesn't create a Datadog metric.
func (s *span) setMetric(key string, val float64) {
	s.Lock()
	defer s.Unlock()
	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return
	}
	s.Metrics[key] = val
}

// setError sets the appropriate span properties and tags based on the passed error.
// Callers must guard.
func (s *span) setError(err error) {
	atomic.CompareAndSwapInt32(&s.Error, 0, 1)
	s.setMeta(errorMsgKey, err.Error())
	s.setMeta(errorTypeKey, reflect.TypeOf(err).String())
	s.setMeta(errorStackKey, string(debug.Stack()))
}

func (s *span) finish(finishTime int64) {
	s.Lock()
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
	s.Unlock()

	if s.buffer == nil {
		if s.tracer != nil {
			s.tracer.pushErr(&errorNoSpanBuf{SpanName: s.Name})
		}
		return
	}

	// If not sampled, drop it
	if !s.Sampled {
		return
	}

	s.buffer.AckFinish() // put data in channel only if trace is completely finished

	// It's important that when Finish() exits, the data is put in
	// the channel for real, when the trace is finished.
	// Otherwise, tests could become flaky (because you never know in what state
	// the channel is).
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

// setSamplingPriority sets the sampling priority.
func (s *span) setSamplingPriority(priority int) {
	s.setMetric(samplingPriorityKey, float64(priority))
}

// hasSamplingPriority returns true if sampling priority is set.
// It can be defined to either zero or non-zero.
// Not safe for concurrent use.
func (s *span) hasSamplingPriority() bool {
	_, ok := s.Metrics[samplingPriorityKey]
	return ok
}

// getSamplingPriority gets the sampling priority.
// Not safe for concurrent use.
func (s *span) getSamplingPriority() int {
	return int(s.Metrics[samplingPriorityKey])
}
