// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer // import "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
	_ "unsafe" // Needed for go:linkname directive.

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

//go:linkname spanStart github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.spanStart
func spanStart(operationName string, options ...tracer.StartSpanOption) *tracer.Span

func newSpan(operationName string, cfg *tracer.StartSpanConfig) *tracer.Span {
	return spanStart(operationName, func(c *tracer.StartSpanConfig) {
		*c = *cfg
	})
}

type Span struct {
	sp    *tracer.Span
	m     map[string]interface{}
	links []tracer.SpanLink
}

func MockSpan(s *tracer.Span) *Span {
	if s == nil {
		return nil
	}
	return &Span{sp: s, m: s.AsMap()}
}

func (s *Span) OperationName() string {
	if s == nil {
		return ""
	}
	return s.m[ext.SpanName].(string)
}

func (s *Span) SetTag(k string, v interface{}) {
	if s == nil {
		return
	}
	s.m[k] = v
	s.sp.SetTag(k, v)
}

func (s *Span) Tag(k string) interface{} {
	if s == nil {
		return nil
	}
	// It's possible that a tag wasn't set through mocktracer.Span.SetTag,
	// in which case we need to retrieve it from the underlying tracer.Span.
	v := s.sp.AsMap()[k]
	if v != nil {
		return v
	}
	v, ok := s.m[k]
	if ok {
		return v
	}
	return nil
}

func (s *Span) Tags() map[string]interface{} {
	if s == nil {
		return make(map[string]interface{})
	}
	tm := s.sp.AsMap()
	m := make(map[string]interface{}, len(s.m)+len(tm))
	extractTags(s.m, m)
	extractTags(tm, m)
	return m
}

func extractTags(src, m map[string]interface{}) {
	for k, v := range src {
		switch k {
		case ext.MapSpanStart:
			continue
		case ext.MapSpanDuration:
			continue
		case ext.MapSpanID:
			continue
		case ext.MapSpanTraceID:
			continue
		case ext.MapSpanParentID:
			continue
		case ext.MapSpanError:
			continue
		case ext.MapSpanEvents:
			continue
		}
		m[k] = v
	}
}

func (s *Span) String() string {
	if s == nil {
		return ""
	}
	sc := s.sp.Context()
	baggage := make(map[string]string)
	sc.ForeachBaggageItem(func(k, v string) bool {
		baggage[k] = v
		return true
	})

	return fmt.Sprintf(`
name: %s
tags: %#v
start: %s
duration: %s
id: %d
parent: %d
trace: %v
baggage: %#v
`, s.OperationName(), s.Tags(), s.StartTime(), s.Duration(), sc.SpanID(), s.ParentID(), sc.TraceID(), baggage)
}

func (s *Span) ParentID() uint64 {
	if s == nil {
		return 0
	}
	return s.m[ext.MapSpanParentID].(uint64)
}

// Context returns the SpanContext of this Span.
func (s *Span) Context() *tracer.SpanContext { return s.sp.Context() }

// SetUser associates user information to the current trace which the
// provided span belongs to. The options can be used to tune which user
// bit of information gets monitored. This mockup only sets the user
// information as span tags of the root span of the current trace.
func (s *Span) SetUser(id string, opts ...tracer.UserMonitoringOption) {
	root := s.sp.Root()
	if root == nil {
		return
	}

	cfg := tracer.UserMonitoringConfig{
		Metadata: make(map[string]string),
	}
	for _, fn := range opts {
		fn(&cfg)
	}

	root.SetTag("usr.id", id)
	root.SetTag("usr.login", cfg.Login)
	root.SetTag("usr.org", cfg.Org)
	root.SetTag("usr.email", cfg.Email)
	root.SetTag("usr.name", cfg.Name)
	root.SetTag("usr.role", cfg.Role)
	root.SetTag("usr.scope", cfg.Scope)
	root.SetTag("usr.session_id", cfg.SessionID)

	for k, v := range cfg.Metadata {
		root.SetTag(fmt.Sprintf("usr.%s", k), v)
	}
}

func (s *Span) SpanID() uint64 {
	if s == nil {
		return 0
	}
	return s.m[ext.MapSpanID].(uint64)
}

func (s *Span) TraceID() uint64 {
	if s == nil {
		return 0
	}
	return s.m[ext.MapSpanTraceID].(uint64)
}

func (s *Span) StartTime() time.Time {
	if s == nil {
		return time.Unix(0, 0)
	}
	return time.Unix(0, s.m[ext.MapSpanStart].(int64))
}

func (s *Span) Duration() time.Duration {
	if s == nil {
		return time.Duration(0)
	}
	return time.Duration(s.m[ext.MapSpanDuration].(int64))
}

func (s *Span) FinishTime() time.Time {
	if s == nil {
		return time.Unix(0, 0)
	}
	return s.StartTime().Add(s.Duration())
}

func (s *Span) Unwrap() *tracer.Span {
	if s == nil {
		return nil
	}
	return s.sp
}

// Links returns the span's span links.
func (s *Span) Links() []tracer.SpanLink {
	payload := s.Tag("_dd.span_links")
	if payload == nil {
		return nil
	}
	// Unmarshal the JSON payload into the SpanLink slice.
	var links []tracer.SpanLink
	json.Unmarshal([]byte(payload.(string)), &links)
	return links
}

// SpanEvent represents a span event from a mockspan.
type SpanEvent struct {
	Name         string         `json:"name"`
	TimeUnixNano uint64         `json:"time_unix_nano"`
	Attributes   map[string]any `json:"attributes"`
}

// AssertAttributes compares the given attributes with the current event ones.
// The comparison is made against the JSON representation of both, since the data comes from
// the span.AsMap() function which provides the JSON representation of the events, and some types
// could have changed (e.g. 1 could be transformed to 1.0 after marshal/unmarshal).
func (s SpanEvent) AssertAttributes(t *testing.T, wantAttrs map[string]any) {
	t.Helper()
	want, err := json.Marshal(wantAttrs)
	require.NoError(t, err)
	got, err := json.Marshal(s.Attributes)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

// Events returns the current span events.
func (s *Span) Events() []SpanEvent {
	if s == nil {
		return nil
	}
	eventsJSON, ok := s.m[ext.MapSpanEvents].(string)
	if !ok {
		return nil
	}

	var events []SpanEvent
	if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil {
		log.Error("mocktracer: failed to unmarshal span events: %s", err.Error())
		return nil
	}
	return events
}

// Integration returns the component from which the mockspan was created.
func (s *Span) Integration() string {
	return s.Tag(ext.Component).(string)
}
