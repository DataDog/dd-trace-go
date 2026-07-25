// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"fmt"
	"io"
	"maps"
	"testing"

	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	globalinternal "github.com/DataDog/dd-trace-go/v2/internal"
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
		Meta:      maps.Clone(span.meta.Map(true)),
		Metrics:   make(map[string]float64, len(span.metrics)),
		Tags:      make(map[string]any, span.meta.Count()+len(span.metrics)+4),
	}
	metaMap := as.Meta
	for key, val := range metaMap {
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
	tracer, err := newUnpublishedInspectableTracer(tb, agent, opts...)
	if err != nil {
		return nil, nil, err
	}

	startStopMu.Lock()
	defer startStopMu.Unlock()
	success := false
	defer func() {
		if !success {
			resetInspectableGlobalTracerLocked(tracer)
		}
	}()
	if err := activateInspectableTracerGenerationLocked(tb, tracer, true); err != nil {
		return nil, nil, err
	}
	globalinternal.SetTracerInitialized(true)
	tb.Cleanup(func() {
		startStopMu.Lock()
		defer startStopMu.Unlock()
		resetInspectableGlobalTracerLocked(tracer)
	})
	success = true
	return tracer, agent, nil
}

func startInspectableTracer(tb testing.TB, agent agenttest.Agent, opts ...StartOption) (Tracer, error) {
	tb.Helper()
	tracer, err := newUnpublishedInspectableTracer(tb, agent, opts...)
	if err != nil {
		return nil, err
	}
	if err := activateInspectableTracerWithoutReplacingGlobal(tb, tracer); err != nil {
		return nil, err
	}
	return tracer, nil
}

func newUnpublishedInspectableTracer(tb testing.TB, agent agenttest.Agent, opts ...StartOption) (*tracer, error) {
	tb.Helper()
	// withAgentTransport injects the in-process round-tripper before
	// newUnpublishedTracer runs so that bootstrap (e.g. /info discovery) never
	// touches the real network. withNoopStats prevents a real DogStatsD dial
	// during startup.
	// Both options survive the orchestrion httpClient override because they are
	// applied after it in finishConfig.
	o := append([]StartOption{
		WithAgentAddr(agent.Addr()),
		withAgentTransport(agent.Transport()),
		withForceAgentWriter(),
		withNoopStats(),
	}, opts...)
	tracer, err := newUnpublishedTracer(o...)
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
		if w, ok := tracer.traceWriter.(flushWaiter); ok {
			w.wait()
		}
		// Synchronously flush LLMObs so spans are guaranteed to arrive at the
		// collector before this function returns. This eliminates the need for
		// timeout-based WaitForSpans polling in tests.
		llmobs.FlushSync()
		done <- struct{}{}
	}
	tb.Cleanup(tracer.Stop)
	return tracer, nil
}

func activateInspectableTracer(tb testing.TB, tracer *tracer) error {
	tb.Helper()
	startStopMu.Lock()
	defer startStopMu.Unlock()
	if err := activateInspectableTracerGenerationLocked(tb, tracer, true); err != nil {
		return err
	}
	tb.Cleanup(func() {
		startStopMu.Lock()
		defer startStopMu.Unlock()
		resetInspectableGlobalTracerLocked(tracer)
	})
	return nil
}

func activateInspectableTracerWithoutReplacingGlobal(tb testing.TB, tracer *tracer) error {
	tb.Helper()
	startStopMu.Lock()
	defer startStopMu.Unlock()
	if err := activateInspectableTracerGenerationLocked(tb, tracer, false); err != nil {
		return err
	}
	registerInspectableLLMObsCleanup(tb, tracer)
	return nil
}

// activateInspectableTracerGenerationLocked publishes and starts an inspectable
// tracer while its caller holds startStopMu.
func activateInspectableTracerGenerationLocked(tb testing.TB, tracer *tracer, replaceGlobalTracer bool) error {
	tb.Helper()
	var err error
	if replaceGlobalTracer {
		err = tracer.publishAndActivate(tracer, false)
	} else {
		err = tracer.publishAndActivateWithoutReplacingGlobal()
	}
	if err != nil {
		return err
	}
	// AppSec and LLMObs use the same helpers as production Start. Instrumentation
	// telemetry and storeConfig remain omitted for this in-process test harness.
	tracer.startAppSec()
	if tracer.config.llmobs.Enabled {
		if err := llmobs.Start(tracer.config.llmobs, &llmobsTracerAdapter{}); err != nil {
			return fmt.Errorf("failed to start llmobs: %w", err)
		}
	}
	return nil
}

func registerInspectableLLMObsCleanup(tb testing.TB, tracer *tracer) {
	if !tracer.config.llmobs.Enabled {
		return
	}
	tb.Cleanup(func() {
		startStopMu.Lock()
		defer startStopMu.Unlock()
		llmobs.Stop()
	})
}

// resetInspectableGlobalTracerLocked removes tracer only while it still owns
// the global slot. Its caller must hold startStopMu.
func resetInspectableGlobalTracerLocked(tracer *tracer) {
	if getGlobalTracer() != tracer {
		return
	}
	if tracer.config.llmobs.Enabled {
		llmobs.Stop()
	}
	old := swapGlobalTracer(&NoopTracer{})
	globalinternal.SetTracerInitialized(false)
	old.Stop()
}
