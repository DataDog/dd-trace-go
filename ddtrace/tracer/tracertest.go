// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"fmt"
	"io"
	"testing"

	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	globalinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

// +checklocksignore
func toAgentSpan(span *Span) *agenttest.Span {
	as := &agenttest.Span{
		SpanID:    span.spanID,
		TraceID:   span.traceID,
		ParentID:  span.parentID,
		Service:   span.service,
		Operation: span.name,
		Resource:  span.resource,
		Type:      span.spanType,
		Start:     span.start,
		Duration:  span.duration,
		Error:     span.error,
		Meta:      make(map[string]string, len(span.meta)),
		Metrics:   make(map[string]float64, len(span.metrics)),
		Tags:      make(map[string]any, len(span.meta)+len(span.metrics)+4),
	}
	for key, val := range span.meta {
		as.Meta[key] = val
		as.Tags[key] = val
	}
	for key, val := range span.metrics {
		as.Metrics[key] = val
		as.Tags[key] = val
	}
	// Populate top-level span attributes into the merged Tags view.
	as.Tags["name"] = span.name
	as.Tags["service"] = span.service
	as.Tags["resource"] = span.resource
	as.Tags["type"] = span.spanType
	return as
}

func handleV04Traces(r io.Reader) []*agenttest.Span {
	var spans []*agenttest.Span
	reader := msgp.NewReader(r)
	numTraces, err := reader.ReadArrayHeader()
	if err != nil {
		return spans
	}
	for range numTraces {
		numSpans, err := reader.ReadArrayHeader()
		if err != nil {
			return spans
		}
		for range numSpans {
			s := &Span{}
			if err := s.DecodeMsg(reader); err != nil {
				return spans
			}
			spans = append(spans, toAgentSpan(s))
		}
	}
	return spans
}

// +checklocksignore
func handleV1Traces(r io.Reader) []*agenttest.Span {
	var spans []*agenttest.Span
	body, err := io.ReadAll(r)
	if err != nil {
		return spans
	}
	p := &payloadV1{buf: body}
	if _, err := p.decodeBuffer(); err != nil {
		return spans
	}
	for _, chunk := range p.chunks {
		var tid uint64
		if len(chunk.traceID) >= 16 {
			tid = binary.BigEndian.Uint64(chunk.traceID[8:])
		} else if len(chunk.traceID) >= 8 {
			tid = binary.BigEndian.Uint64(chunk.traceID)
		}
		for _, s := range chunk.spans {
			s.traceID = tid
			spans = append(spans, toAgentSpan(s))
		}
	}
	return spans
}

func startAgentTest(tb testing.TB) (agenttest.Agent, error) {
	tb.Helper()
	agent := agenttest.New()
	agent.HandleTraces("/v0.4/traces", handleV04Traces)
	agent.HandleTraces("/v1.0/traces", handleV1Traces)
	if err := agent.Start(tb); err != nil {
		return nil, err
	}
	return agent, nil
}

func bootstrapInspectableTracer(tb testing.TB, opts ...StartOption) (Tracer, agenttest.Agent, error) {
	tb.Helper()
	agent, err := startAgentTest(tb)
	if err != nil {
		return nil, nil, err
	}
	tracer, err := startInspectableTracer(tb, agent, opts...)
	if err != nil {
		return nil, nil, err
	}
	setGlobalTracer(tracer)
	globalinternal.SetTracerInitialized(true)
	tb.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
		globalinternal.SetTracerInitialized(false)
	})
	return tracer, agent, nil
}

func startInspectableTracer(tb testing.TB, agent agenttest.Agent, opts ...StartOption) (Tracer, error) {
	tb.Helper()
	// withAgentTransport injects the in-process round-tripper before newTracer
	// runs so that bootstrap (e.g. /info discovery) never touches the real
	// network. withNoopStats prevents a real DogStatsD dial during startup.
	// Both options survive the orchestrion httpClient override because they are
	// applied after it in finishConfig.
	o := append([]StartOption{
		WithAgentAddr(agent.Addr()),
		withAgentTransport(agent.Transport()),
		withNoopStats(),
	}, opts...)
	tracer, err := newTracer(o...)
	if err != nil {
		return nil, err
	}
	tracer.flushHandler = func(done chan<- struct{}) {
		// This is a stronger flush logic, as it drains `tracer.out` before flushing.
		// The default weaker flush doesn't allow to be used in tests without
		// introducing some timeout semantics.
		// Flushing is ensured to be tested through other E2E tests like system-tests.
		for {
			select {
			case trace := <-tracer.out:
				tracer.sampleChunk(trace)
				if len(trace.spans) > 0 {
					tracer.traceWriter.add(trace.spans)
				}
			default:
				goto drained
			}
		}
	drained:
		tracer.traceWriter.flush()
		tracer.traceWriter.(*agentTraceWriter).wg.Wait()
		// Synchronously flush LLMObs so spans are guaranteed to arrive at the
		// collector before this function returns. This eliminates the need for
		// timeout-based WaitForSpans polling in tests.
		llmobs.FlushSync()
		done <- struct{}{}
	}
	// The following lines are related to services that need to be started for
	// the tests to work. This is kind of hack because we can't call `tracer.Start`.
	// We should refactor the initialization of these services as `startServices` at
	// some point.
	appsec.Start(tracer.config.appsecStartOptions...)
	if tracer.config.llmobs.Enabled {
		if err := llmobs.Start(tracer.config.llmobs, &llmobsTracerAdapter{}); err != nil {
			return nil, fmt.Errorf("failed to start llmobs: %w", err)
		}
		tb.Cleanup(llmobs.Stop)
	}
	tb.Cleanup(tracer.Stop)
	return tracer, nil
}
