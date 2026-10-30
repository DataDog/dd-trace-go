// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"net"
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

// gRPC status tags set by the gRPC and Connect client integrations.
const (
	grpcCodeTag       = "grpc.code"
	grpcStatusCodeTag = "rpc.grpc.status_code"
)

// recordLLMObsDownstreamCallLocked reports s to its owning tool span if s is an outbound client
// call. It runs from finish after the one-time finished check, so a span is reported once even
// if Finish is called concurrently. Query strings, credentials, resource names and SQL are never
// read.
// +checklocks:s.mu
func (s *Span) recordLLMObsDownstreamCallLocked() {
	if kind, _ := s.meta.Get(ext.SpanKind); kind != ext.SpanKindClient {
		return
	}
	s.llmobsDownstreamOwner.RecordDownstreamCall(s.llmobsDownstreamCallLocked())
}

// +checklocksread:s.mu
func (s *Span) llmobsDownstreamCallLocked() illmobs.DownstreamCall {
	call := illmobs.DownstreamCall{Duration: time.Duration(s.duration)}
	host := s.firstMetaLocked(ext.NetworkDestinationName, ext.TargetHost, ext.ServerAddress)
	if port := s.firstMetaLocked(ext.NetworkDestinationPort, ext.TargetPort, ext.ServerPort); host != "" && port != "" {
		host = net.JoinHostPort(host, port)
	}
	call.Host = host
	switch rawURL := s.firstMetaLocked(ext.HTTPURL, ext.URLFull); {
	case rawURL != "":
		call.Kind = "http"
		call.Method = s.firstMetaLocked(ext.HTTPMethod, ext.HTTPRequestMethod)
		call.Status = s.firstMetaLocked(ext.HTTPCode, ext.HTTPResponseStatusCode)
		if u, err := url.Parse(rawURL); err == nil {
			// Some integrations record only the path and set the host separately.
			if u.Host != "" {
				call.Host = u.Host
			}
			call.Target = illmobs.DownstreamPathTemplate(u.Path)
		}
	case s.firstMetaLocked(ext.GRPCFullMethod) != "":
		call.Kind, call.Target = "grpc", s.firstMetaLocked(ext.GRPCFullMethod)
		call.Status = s.firstMetaLocked(grpcCodeTag, grpcStatusCodeTag)
	case s.firstMetaLocked(ext.DBSystem) != "":
		call.Kind, call.Method, call.Target = "db", s.firstMetaLocked(ext.DBSystem), s.name
	default:
		call.Kind, call.Method, call.Target = "other", s.firstMetaLocked(ext.Component), s.name
	}
	call.Failed = s.error != 0 || downstreamStatusFailed(call.Kind, call.Status)
	return call
}

func downstreamStatusFailed(kind, status string) bool {
	if status == "" {
		return false
	}
	if kind == "grpc" {
		return status != "OK" && status != "0"
	}
	code, _ := strconv.Atoi(status)
	return code >= 400
}

// +checklocksread:s.mu
func (s *Span) firstMetaLocked(keys ...string) string {
	for _, key := range keys {
		if v, ok := s.meta.Get(key); ok && v != "" {
			return v
		}
	}
	return ""
}
