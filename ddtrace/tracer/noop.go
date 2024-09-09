// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

var _ Tracer = (*NoopTracer)(nil)

// NoopTracer is an implementation of Tracer that is a no-op.
type NoopTracer struct{}

// StartSpan implements Tracer.
func (NoopTracer) StartSpan(_ string, _ ...StartSpanOption) *Span {
	return nil
}

// SetServiceInfo implements Tracer.
func (NoopTracer) SetServiceInfo(_, _, _ string) {}

// Extract implements Tracer.
func (NoopTracer) Extract(_ interface{}) (*SpanContext, error) {
	return nil, nil
}

// Inject implements Tracer.
func (NoopTracer) Inject(_ *SpanContext, _ interface{}) error { return nil }

// Stop implements Tracer.
func (NoopTracer) Stop() {}

func (NoopTracer) TracerConf() TracerConf {
	return TracerConf{}
}

func (NoopTracer) Submit(*Span)       {}
func (NoopTracer) SubmitChunk(*Chunk) {}
func (NoopTracer) Flush()             {}
