// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.
// Copyright 2021 Spacelift, Inc.
package tracer

import (
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type customTraceWriter struct {
	spans []*span

	// climit limits the number of concurrent outgoing connections
	climit chan struct{}

	sendSpans func(spans []*CustomSpan) error

	// wg waits for all uploads to finish
	wg sync.WaitGroup
}

func NewCustomTraceWriter(sendSpans func(spans []*CustomSpan) error) *customTraceWriter {
	return &customTraceWriter{
		climit:    make(chan struct{}, concurrentConnectionLimit),
		sendSpans: sendSpans,
	}
}

func (h *customTraceWriter) add(trace []*span) {
	h.spans = append(h.spans, trace...)
	if len(h.spans) > payloadSizeLimit {
		h.flush()
	}
}

func (h *customTraceWriter) stop() {
	h.flush()
	h.wg.Wait()
}

// flush will push any currently buffered traces to the server.
func (h *customTraceWriter) flush() {
	if len(h.spans) == 0 {
		return
	}
	h.wg.Add(1)
	h.climit <- struct{}{}
	go func(spans []*span) {
		defer func(start time.Time) {
			<-h.climit
			h.wg.Done()
		}(time.Now())

		customSpans := make([]*CustomSpan, len(spans))
		for i := range spans {
			customSpans[i] = &CustomSpan{
				Name:     spans[i].Name,
				Service:  spans[i].Service,
				Resource: spans[i].Resource,
				Type:     spans[i].Type,
				Start:    spans[i].Start,
				Duration: spans[i].Duration,
				Meta:     spans[i].Meta,
				Metrics:  spans[i].Metrics,
				SpanID:   spans[i].SpanID,
				TraceID:  spans[i].TraceID,
				ParentID: spans[i].ParentID,
				Error:    spans[i].Error,
			}
		}

		if err := h.sendSpans(customSpans); err != nil {
			log.Error("lost %d spans: %v", len(spans), err)
		}
	}(h.spans)
	h.spans = nil
}

type CustomSpan struct {
	Name     string             `json:"name"`      // operation name
	Service  string             `json:"service"`   // service name (i.e. "grpc.server", "http.request")
	Resource string             `json:"resource"`  // resource name (i.e. "/user?id=123", "SELECT * FROM users")
	Type     string             `json:"type"`      // protocol associated with the span (i.e. "web", "db", "cache")
	Start    int64              `json:"start"`     // span start time expressed in nanoseconds since epoch
	Duration int64              `json:"duration"`  // duration of the span expressed in nanoseconds
	Meta     map[string]string  `json:"meta"`      // arbitrary map of metadata
	Metrics  map[string]float64 `json:"metrics"`   // arbitrary map of numeric metrics
	SpanID   uint64             `json:"span_id"`   // identifier of this span
	TraceID  uint64             `json:"trace_id"`  // identifier of the root span
	ParentID uint64             `json:"parent_id"` // identifier of the span's direct parent
	Error    int32              `json:"error"`     // error status of the span; 0 means no errors
}

type multiTraceWriter struct {
	ws []traceWriter
}

func (m *multiTraceWriter) add(spans []*span) {
	for i := range m.ws {
		m.ws[i].add(spans)
	}
}

func (m *multiTraceWriter) flush() {
	for i := range m.ws {
		m.ws[i].flush()
	}
}

func (m *multiTraceWriter) stop() {
	for i := range m.ws {
		m.ws[i].stop()
	}
}
