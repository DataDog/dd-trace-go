// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer // import "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

import (
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func newSpan(operationName string, cfg *ddtrace.StartSpanConfig) *tracer.Span {
	return tracer.SpanStart(operationName, func(c *ddtrace.StartSpanConfig) {
		*c = *cfg
	})
}

type Span struct {
	*tracer.Span
}

func MockSpan(s *tracer.Span) *Span {
	if s == nil {
		return nil
	}
	return &Span{Span: s}
}

func (s *Span) OperationName() string {
	if s == nil {
		return ""
	}
	s.RLock()
	defer s.RUnlock()
	return s.Name
}

func (s *Span) Tag(k string) interface{} {
	if s == nil {
		return nil
	}
	s.RLock()
	defer s.RUnlock()
	switch k {
	case ext.SpanName:
		return s.Name
	case ext.ServiceName:
		return s.Service
	case ext.ResourceName:
		return s.Resource
	case ext.SpanType:
		return s.Type
	}
	if s.Meta != nil {
		if r, ok := s.Meta[k]; ok {
			return r
		}
	}
	if s.Metrics != nil {
		if r, ok := s.Metrics[k]; ok {
			return r
		}
	}
	return nil
}

func (s *Span) Tags() map[string]interface{} {
	if s == nil {
		return make(map[string]interface{})
	}
	s.RLock()
	defer s.RUnlock()
	r := make(map[string]interface{}, len(s.Meta)+len(s.Metrics))
	for k, v := range s.Meta {
		r[k] = v
	}
	for k, v := range s.Metrics {
		r[k] = v
	}
	r[ext.SpanName] = s.Name
	r[ext.ServiceName] = s.Service
	r[ext.ResourceName] = s.Resource
	r[ext.SpanType] = s.Type
	return r
}

func (s *Span) String() string {
	if s == nil {
		return ""
	}
	sc := s.Context()
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
trace: %d
baggage: %#v
`, s.Name, s.Tags(), s.StartTime(), s.Duration(), sc.SpanID(), s.ParentID(), sc.TraceID(), baggage)
}

func (s *Span) ParentID() uint64 {
	if s == nil {
		return 0
	}
	s.RLock()
	defer s.RUnlock()
	return s.Span.ParentID
}

func (s *Span) SpanID() uint64 {
	if s == nil {
		return 0
	}
	s.RLock()
	defer s.RUnlock()
	return s.Span.SpanID
}

func (s *Span) TraceID() uint64 {
	if s == nil {
		return 0
	}
	s.RLock()
	defer s.RUnlock()
	return s.Span.TraceID
}

func (s *Span) StartTime() time.Time {
	if s == nil {
		return time.Unix(0, 0)
	}
	s.RLock()
	defer s.RUnlock()
	return time.Unix(0, s.Span.Start)
}

func (s *Span) Duration() time.Duration {
	if s == nil {
		return time.Duration(0)
	}
	s.RLock()
	defer s.RUnlock()
	return time.Duration(s.Span.Duration)
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
	return s.Span
}
