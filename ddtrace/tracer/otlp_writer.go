// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"slices"
	"sync"
	"time"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

var _ traceWriter = (*otlpTraceWriter)(nil)

type otlpTraceWriter struct {
	config    *config
	transport *otlpTransport
	mu        locking.Mutex
	resource  *otlpresource.Resource
	scope     *otlpcommon.InstrumentationScope
	spans     []*otlptrace.Span // +checklocks:mu
	buffSize  int               // +checklocks:mu
	baseSize  int
	climit    chan struct{}
	wg        sync.WaitGroup
}

func newOTLPTraceWriter(c *config) *otlpTraceWriter {
	resource := buildResource(c.internalConfig)
	scope := &otlpcommon.InstrumentationScope{Name: "dd-trace-go", Version: version.Tag}
	baseSize := proto.Size(&otlptrace.TracesData{
		ResourceSpans: []*otlptrace.ResourceSpans{{
			Resource: resource,
			ScopeSpans: []*otlptrace.ScopeSpans{{
				Scope: scope,
			}},
		}},
	})
	return &otlpTraceWriter{
		config:    c,
		transport: newOTLPTransport(internal.DefaultHTTPClient(c.httpClientTimeout, false), c.internalConfig.OTLPTraceURL(), c.internalConfig.OTLPHeaders()),
		resource:  resource,
		scope:     scope,
		spans:     make([]*otlptrace.Span, 0),
		buffSize:  baseSize,
		baseSize:  baseSize,
		climit:    make(chan struct{}, concurrentConnectionLimit),
	}
}

// reset swaps out the current span buffer and returns it, resetting the
// writer to an empty state ready for new spans.
// +checklocks:w.mu
func (w *otlpTraceWriter) reset() []*otlptrace.Span {
	old := w.spans
	w.spans = make([]*otlptrace.Span, 0)
	w.buffSize = w.baseSize
	return old
}

func (w *otlpTraceWriter) add(spanList []*Span) {
	defaultServiceName := w.config.internalConfig.ServiceName()
	w.mu.Lock()
	w.spans = slices.Grow(w.spans, len(spanList))
	for _, span := range spanList {
		if otlpSpan := convertSpan(span, defaultServiceName); otlpSpan != nil {
			w.spans = append(w.spans, otlpSpan)
			w.buffSize += proto.Size(otlpSpan)
		}
	}
	needsFlush := w.buffSize > payloadSizeLimit
	w.mu.Unlock()
	if needsFlush {
		w.flush()
	}
}

func (w *otlpTraceWriter) flush() {
	w.mu.Lock()
	if len(w.spans) == 0 {
		w.mu.Unlock()
		return
	}
	readySpans := w.reset()
	w.mu.Unlock()

	w.climit <- struct{}{}
	w.wg.Add(1)
	go func() {
		defer func() {
			<-w.climit
			w.wg.Done()
		}()

		spanCount := len(readySpans)
		tracesData := &otlptrace.TracesData{
			ResourceSpans: []*otlptrace.ResourceSpans{
				{
					Resource: w.resource,
					ScopeSpans: []*otlptrace.ScopeSpans{
						{
							Scope: w.scope,
							Spans: readySpans,
						},
					},
				},
			},
		}
		b, err := proto.Marshal(tracesData)
		readySpans = nil
		tracesData = nil
		if err != nil {
			log.Error("Error marshalling OTLP traces data: %s", err.Error())
			return
		}

		var sendErr error
		for attempt := 0; attempt <= w.config.sendRetries; attempt++ {
			log.Debug("OTLP: attempt %d to send payload: %d bytes, %d spans", attempt+1, len(b), spanCount)
			sendErr = w.transport.send(b)
			if sendErr == nil {
				log.Debug("OTLP: sent traces after %d attempts", attempt+1)
				return
			}
			log.Error("OTLP: failure sending traces (attempt %d of %d): %v", attempt+1, w.config.sendRetries+1, sendErr.Error())
			time.Sleep(w.config.internalConfig.RetryInterval())
		}
		log.Error("OTLP: lost %d spans: %v", spanCount, sendErr.Error())
	}()
}

func (w *otlpTraceWriter) stop() {
	w.flush()
	w.wg.Wait()
}
