package tracer

import (
	"context"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const (
	errorMsgKey   = "error.msg"
	errorTypeKey  = "error.type"
	errorStackKey = "error.stack"
)

// Span represents a computation. Callers must call Finish when a span is
// complete to ensure it's submitted.
//
//	span := tracer.NewRootSpan("web.request", "datadog.com", "/user/{id}")
//	defer span.Finish()  // or FinishWithErr(err)
//
// In general, spans should be created with the tracer.NewSpan* functions,
// so they will be submitted on completion.
type Span struct {
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
}

// NewSpan creates a new span.
func NewSpan(name, service, resource string, spanID, traceID, parentID uint64, tracer *Tracer) *Span {
	return &Span{
		Name:     name,
		Service:  service,
		Resource: resource,
		Meta:     tracer.GetAllMeta(),
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
func (s *Span) setMeta(key, value string) {
	if s == nil {
		return
	}

	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return
	}

	if s.Meta == nil {
		s.Meta = make(map[string]string)
	}
	s.Meta[key] = value

}

// SetMeta adds an arbitrary meta field to the current Span.
// If the Span has been finished, it will not be modified by the method.
func (s *Span) SetMeta(key, value string) {
	if s == nil {
		return
	}

	s.Lock()
	defer s.Unlock()

	s.setMeta(key, value)

}

// GetMeta will return the value for the given tag or the empty string if it
// doesn't exist.
func (s *Span) GetMeta(key string) string {
	if s == nil {
		return ""
	}
	s.RLock()
	defer s.RUnlock()
	if s.Meta == nil {
		return ""
	}
	return s.Meta[key]
}

// SetMetrics adds a metric field to the current Span.
// DEPRECATED: Use SetMetric
func (s *Span) SetMetrics(key string, value float64) {
	if s == nil {
		return
	}
	s.SetMetric(key, value)
}

// SetMetric adds a metric field to the current Span. If the Span has been
// finished, it will not be modified by this method.
func (s *Span) SetMetric(key string, val float64) {
	if s == nil {
		return
	}

	s.Lock()
	defer s.Unlock()

	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return
	}

	if s.Metrics == nil {
		s.Metrics = make(map[string]float64)
	}
	s.Metrics[key] = val
}

// SetError stores an error object within the span meta. The Error status is
// updated and the error.Error() string is included with a default meta key.
// If the Span has been finished, it will not be modified by this method.
func (s *Span) SetError(err error) {
	if err == nil || s == nil {
		return
	}

	s.Lock()
	defer s.Unlock()
	// We don't lock spans when flushing, so we could have a data race when
	// modifying a span as it's being flushed. This protects us against that
	// race, since spans are marked `finished` before we flush them.
	if s.finished {
		return
	}
	s.Error = 1

	s.setMeta(errorMsgKey, err.Error())
	s.setMeta(errorTypeKey, reflect.TypeOf(err).String())
	stack := debug.Stack()
	s.setMeta(errorStackKey, string(stack))
}

// Finish closes this Span (but not its children) providing the duration
// of this part of the tracing session. This method is idempotent so
// calling this method multiple times is safe and doesn't update the
// current Span. Once a Span has been finished, methods that modify the Span
// will become no-ops.
func (s *Span) Finish() {
	if s == nil {
		return
	}

	s.Lock()
	finished := s.finished
	if !finished {
		if s.Duration == 0 {
			s.Duration = now() - s.Start
		}
		s.finished = true
	}
	s.Unlock()

	if s.tracer != nil && !finished {
		s.tracer.record(s)
	}
}

// FinishWithErr marks a span finished and sets the given error if it's
// non-nil.
func (s *Span) FinishWithErr(err error) {
	if s == nil {
		return
	}
	s.SetError(err)
	s.Finish()
}

// String returns a human readable representation of the span. Not for
// production, just debugging.
func (s *Span) String() string {
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

// Context returns a copy of the given context that includes this span.
// This span can be accessed downstream with SpanFromContext and friends.
func (s *Span) Context(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, spanKey, s)
}

// Tracer returns the tracer that created this span.
func (s *Span) Tracer() *Tracer {
	if s == nil {
		return nil
	}
	return s.tracer
}

// NextSpanID returns a new random span id.
func NextSpanID() uint64 {
	return uint64(randGen.Int63())
}

// now returns current UTC time in nanos.
func now() int64 {
	return time.Now().UTC().UnixNano()
}
