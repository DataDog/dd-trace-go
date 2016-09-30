package tracer

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

const (
	defaultErrorMeta = "error.msg"
)

// Span is the common struct we use to represent a dapper-like span.
// More information about the structure of the Span can be found
// here: http://research.google.com/pubs/pub36356.html
type Span struct {
	Name     string             `json:"name"`              // the name of what we're monitoring (e.g. redis.command)
	Service  string             `json:"service"`           // the service related to this trace (e.g. redis)
	Resource string             `json:"resource"`          // the natural key of what we measure (e.g. GET)
	Type     string             `json:"type"`              // protocol associated with the span
	Start    int64              `json:"start"`             // span start time expressed in nanoseconds since epoch
	Duration int64              `json:"duration"`          // duration of the span expressed in nanoseconds
	Error    int32              `json:"error"`             // error status of the span; 0 means no errors
	Meta     map[string]string  `json:"meta,omitempty"`    // arbitrary map of metadata
	Metrics  map[string]float64 `json:"metrics,omitempty"` // arbitrary map of numeric metrics
	SpanID   uint64             `json:"span_id"`           // identifier of this span
	TraceID  uint64             `json:"trace_id"`          // identifier of the root span
	ParentID uint64             `json:"parent_id"`         // identifier of the span's direct parent

	Sampled bool `json:"-"` // if this span is sampled (and should be kept/recorded) or not

	tracer *Tracer // the tracer that generated this span

	mu sync.Mutex // lock the Span to make it thread-safe
}

// NewSpan creates a new Span with the given arguments, and sets
// the internal Start field.
func newSpan(name, service, resource string, spanID, traceID, parentID uint64, tracer *Tracer) *Span {
	return &Span{
		Name:     name,
		Service:  service,
		Resource: resource,
		SpanID:   spanID,
		TraceID:  traceID,
		ParentID: parentID,
		Start:    Now(),
		Sampled:  true,
		tracer:   tracer,
	}
}

// SetMeta adds an arbitrary meta field to the current Span.
func (s *Span) SetMeta(key, value string) {
	if s == nil {
		return
	}

	s.mu.Lock()

	if s.Meta == nil {
		s.Meta = make(map[string]string)
	}
	s.Meta[key] = value

	s.mu.Unlock()
}

// SetMetrics adds a metric field to the current Span.
func (s *Span) SetMetrics(key string, value float64) {
	if s == nil {
		return
	}

	s.mu.Lock()
	if s.Metrics == nil {
		s.Metrics = make(map[string]float64)
	}
	s.Metrics[key] = value
	s.mu.Unlock()
}

// SetError stores an error object within the span meta. The Error status is
// updated and the error.Error() string is included with a default meta key.
func (s *Span) SetError(err error) {
	if s == nil {
		return
	}

	if err != nil {
		s.Error = 1
		s.SetMeta(defaultErrorMeta, err.Error())
	}
}

// SetErrorMeta stores an error object within the span meta. The error.Error()
// string is included in the user defined meta key.
func (s *Span) SetErrorMeta(meta string, err error) {
	if s == nil {
		return
	}

	if err != nil {
		s.SetMeta(meta, err.Error())
	}
}

// IsFinished returns true if the span.Finish() method has been called.
// Under the hood, any Span with a Duration has to be considered closed.
func (s *Span) IsFinished() bool {
	if s == nil {
		return false
	}

	return s.Duration > 0
}

// Finish closes this Span (but not its children) providing the duration
// of this part of the tracing session. This method is idempotent so
// calling this method multiple times is safe and doesn't update the
// current Span.
func (s *Span) Finish() {
	if s == nil {
		return
	}

	s.mu.Lock()
	finished := s.Duration > 0
	if !finished {
		s.Duration = Now() - s.Start
	}
	s.mu.Unlock()

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
	}

	return strings.Join(lines, "\n")
}

// nextSpanID returns a new random identifier. It is meant to be used as a
// SpanID for the Span struct. Changing this function impacts the whole
// package.
func nextSpanID() uint64 {
	return uint64(rand.Int63())
}
