// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

// llmobsDownstreamOwner returns the LLMObs tool span that a span started from ctx reports its
// outbound call to, or nil. A span started inside another client span (for example a transport
// wrapped by a retrying client) is part of that call and gets no owner.
func llmobsDownstreamOwner(ctx context.Context) *illmobs.Span {
	llmSpan, ok := illmobs.ActiveLLMSpanFromContext(ctx)
	if !ok || llmSpan.Kind() != string(illmobs.SpanKindTool) {
		return nil
	}
	if parent, ok := SpanFromContext(ctx); ok && parent.isLLMObsDownstreamClient() {
		return nil
	}
	return llmSpan
}

func (s *Span) isLLMObsDownstreamClient() bool {
	if s.llmobsDownstreamOwner == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	kind, _ := s.meta.Get(ext.SpanKind)
	return kind == ext.SpanKindClient
}

// recordLLMObsDownstreamCall reports s to its owning tool span if s is an outbound client call.
// Query strings, credentials, resource names and SQL are never read.
func (s *Span) recordLLMObsDownstreamCall(finishTime int64) {
	owner := s.llmobsDownstreamOwner
	if owner == nil {
		return
	}
	s.mu.RLock()
	if s.finished {
		s.mu.RUnlock()
		return
	}
	if kind, _ := s.meta.Get(ext.SpanKind); kind != ext.SpanKindClient {
		s.mu.RUnlock()
		return
	}
	call := s.llmobsDownstreamCallLocked(finishTime)
	s.mu.RUnlock()

	owner.RecordDownstreamCall(call)
}

// +checklocksread:s.mu
func (s *Span) llmobsDownstreamCallLocked(finishTime int64) illmobs.DownstreamCall {
	call := illmobs.DownstreamCall{Duration: time.Duration(max(finishTime-s.start, 0))}
	host := s.metaStringLocked(ext.NetworkDestinationName)
	if host == "" {
		host = s.metaStringLocked(ext.TargetHost)
	}
	switch rawURL := s.metaStringLocked(ext.HTTPURL); {
	case rawURL != "":
		call.Kind, call.Method, call.Status = "http", s.metaStringLocked(ext.HTTPMethod), s.metaStringLocked(ext.HTTPCode)
		if u, err := url.Parse(rawURL); err == nil {
			call.Host, call.Target = u.Host, illmobs.DownstreamPathTemplate(u.Path)
		}
	case s.metaStringLocked(ext.GRPCFullMethod) != "":
		call.Kind, call.Host, call.Target = "grpc", host, s.metaStringLocked(ext.GRPCFullMethod)
	case s.metaStringLocked(ext.DBSystem) != "":
		call.Kind, call.Method, call.Host, call.Target = "db", s.metaStringLocked(ext.DBSystem), host, s.name
	default:
		call.Kind, call.Method, call.Host, call.Target = "other", s.metaStringLocked(ext.Component), host, s.name
	}
	status, _ := strconv.Atoi(call.Status)
	call.Failed = s.error != 0 || status >= 400
	return call
}

// +checklocksread:s.mu
func (s *Span) metaStringLocked(key string) string {
	v, _ := s.meta.Get(key)
	return v
}
