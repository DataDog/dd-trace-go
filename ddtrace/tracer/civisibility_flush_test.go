// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestCIVisibilityFlushDrainsAcceptedChunks(t *testing.T) {
	for _, mode := range []string{"ci", "ci-noop", "ci-mock", "application"} {
		t.Run(mode, func(t *testing.T) {
			enabled := mode != "application"
			tr, err := newUnstartedTracer(withNoopInfoHTTPClient(), func(c *config) {
				c.internalConfig.SetCIVisibilityEnabled(enabled, internalconfig.OriginCode)
				c.internalConfig.SetSpanPoolEnabled(false, internalconfig.OriginCode)
				c.tracingAsTransport = true
			})
			require.NoError(t, err)
			writer := &ciFlushRecordingWriter{testTraceWriter: newTestTraceWriter(), out: tr.out}
			tr.traceWriter = writer
			t.Cleanup(func() { setGlobalTracer(&NoopTracer{}); tr.Stop() })
			var global Tracer = tr
			if mode == "ci-noop" {
				global = wrapWithCiVisibilityNoopTracer(tr)
			}
			if mode == "ci-mock" {
				setGlobalTracer(&ciFlushInstallationMock{Tracer: &NoopTracer{}})
			}
			setGlobalTracerWithCIVisibility(global, enabled)
			if mode == "ci-mock" {
				require.Same(t, tr, getGlobalTracer().(*ciFlushInstallationMock).Tracer)
			}

			span := newBasicSpan("accepted")
			tr.out <- &chunk{spans: []*Span{span}, willSend: true}
			tr.out <- &chunk{spans: []*Span{newBasicSpan("filtered")}, filterRejected: true}
			// No worker consumes the queue in this reproduction. Invoke the
			// installed handler exactly as the real worker does, without sleeps.
			done := make(chan struct{}, 1)
			tr.flushHandler(done)
			<-done
			if enabled {
				require.Len(t, tr.out, 1, "new producer activity cannot extend this flush indefinitely")
				require.Equal(t, []*Span{span}, writer.Flushed(), "retain filtering while draining accepted chunks")
			} else {
				require.Len(t, tr.out, 2, "ordinary application Flush keeps its existing behavior")
				require.Empty(t, writer.Flushed())
			}
		})
	}
}

type ciFlushRecordingWriter struct {
	*testTraceWriter
	out chan *chunk
}

func (w *ciFlushRecordingWriter) add(spans []*Span) {
	w.testTraceWriter.add(spans)
	// Simulate a producer accepting a new chunk while the worker is draining.
	w.out <- &chunk{spans: []*Span{newBasicSpan("next flush")}, willSend: true}
}

type ciFlushInstallationMock struct{ Tracer }

func (m *ciFlushInstallationMock) SetCIVisibilityTracer(tr Tracer) bool {
	m.Tracer = tr
	return true
}
