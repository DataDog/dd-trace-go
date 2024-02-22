// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer // import "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

import (
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func newSpan(operationName string, cfg *tracer.StartSpanConfig) *tracer.Span {
	return tracer.SpanStart(operationName, func(c *tracer.StartSpanConfig) {
		*c = *cfg
	})
}

type Span struct {
	sp *tracer.Span
	m  map[string]interface{}
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
	return s.m[k]
}

func (s *Span) Tags() map[string]interface{} {
	if s == nil {
		return make(map[string]interface{})
	}
	m := make(map[string]interface{}, len(s.m))
	for k, v := range s.m {
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
		}
		m[k] = v
	}
	return m
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

func (s *Span) Context() *tracer.SpanContext {
	return s.sp.Context()
}
