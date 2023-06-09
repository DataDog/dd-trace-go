// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package mocktracer provides a mock implementation of the tracer used in testing. It
// allows querying spans generated at runtime, without having them actually be sent to
// an agent. It provides a simple way to test that instrumentation is running correctly
// in your application.
//
// Simply call "Start" at the beginning of your tests to start and obtain an instance
// of the mock tracer.
package mocktracer

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tinylib/msgp/msgp"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

var _ ddtrace.Tracer = (*mocktracer)(nil)
var _ Tracer = (*mocktracer)(nil)

const (
	msgpackArrayFix byte = 144  // up to 15 items
	msgpackArray16       = 0xdc // up to 2^16-1 items, followed by size in 2 bytes
	msgpackArray32       = 0xdd // up to 2^32-1 items, followed by size in 4 bytes
)

// Tracer exposes an interface for querying the currently running mock tracer.
type Tracer interface {
	// OpenSpans returns the set of started spans that have not been finished yet.
	OpenSpans() []Span

	// FinishedSpans returns the set of finished spans.
	FinishedSpans() []Span

	// Reset resets the spans and services recorded in the tracer. This is
	// especially useful when running tests in a loop, where a clean start
	// is desired for FinishedSpans calls.
	Reset()

	// Stop deactivates the mock tracer and allows a normal tracer to take over.
	// It should always be called when testing has finished.
	Stop()
}

// Start sets the internal tracer to a mock and returns an interface
// which allows querying it. Call Start at the beginning of your tests
// to activate the mock tracer. When your test runs, use the returned
// interface to query the tracer's state.
func Start() Tracer {
	t := newMockTracer()
	internal.SetGlobalTracer(t)
	internal.Testing = true
	return t
}

type mocktracer struct {
	sync.RWMutex  // guards below spans
	finishedSpans []Span
	openSpans     map[uint64]Span
}

func newMockTracer() *mocktracer {
	var t mocktracer
	t.openSpans = make(map[uint64]Span)
	return &t
}

// Stop deactivates the mock tracer and sets the active tracer to a no-op.
func (*mocktracer) Stop() {
	internal.SetGlobalTracer(&internal.NoopTracer{})
	internal.Testing = false
}

func (t *mocktracer) StartSpan(operationName string, opts ...ddtrace.StartSpanOption) ddtrace.Span {
	var cfg ddtrace.StartSpanConfig
	for _, fn := range opts {
		fn(&cfg)
	}
	span := newSpan(t, operationName, &cfg)

	t.Lock()
	t.openSpans[span.SpanID()] = span
	t.Unlock()

	return span
}

func (t *mocktracer) OpenSpans() []Span {
	t.RLock()
	defer t.RUnlock()
	spans := make([]Span, 0, len(t.openSpans))
	for _, s := range t.openSpans {
		spans = append(spans, s)
	}
	return spans
}

func ConvertToEncodableMockSpans(spans []Span) []*encodablemockspan {
	result := make([]*encodablemockspan, len(spans))
	for i, span := range spans {
		result[i] = &encodablemockspan{
			name:      span.OperationName(),
			tags:      span.Tags(),
			startTime: span.StartTime(),
			parentID:  span.ParentID(),
		}
	}
	return result
}

func (t *mocktracer) FinishedSpans() []Span {
	t.RLock()
	defer t.RUnlock()

	// send finished spans to test agent, along with trace headers and new header with all Datadog env variables at time of trace, ex: X-Datadog-Trace-Env-Variables => "DD_SERVICE=my-service;DD_KEY=VAL, ...."
	// sendTracesViaPost(ConvertToEncodableMockSpans(t.finishedSpans), "127.0.0.1", 9126)
	sendSpansJson(ConvertToEncodableMockSpans(t.finishedSpans), "127.0.0.1", 9126)
	return t.finishedSpans
}

func (t *mocktracer) Reset() {
	t.Lock()
	defer t.Unlock()
	for k := range t.openSpans {
		delete(t.openSpans, k)
	}
	t.finishedSpans = nil
}

func (t *mocktracer) addFinishedSpan(s Span) {
	t.Lock()
	defer t.Unlock()
	delete(t.openSpans, s.SpanID())
	if t.finishedSpans == nil {
		t.finishedSpans = make([]Span, 0, 1)
	}
	t.finishedSpans = append(t.finishedSpans, s)
}

const (
	traceHeader    = tracer.DefaultTraceIDHeader
	spanHeader     = tracer.DefaultParentIDHeader
	priorityHeader = tracer.DefaultPriorityHeader
	baggagePrefix  = tracer.DefaultBaggageHeaderPrefix
)

func (t *mocktracer) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	reader, ok := carrier.(tracer.TextMapReader)
	if !ok {
		return nil, tracer.ErrInvalidCarrier
	}
	var sc spanContext
	err := reader.ForeachKey(func(key, v string) error {
		k := strings.ToLower(key)
		if k == traceHeader {
			id, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return tracer.ErrSpanContextCorrupted
			}
			sc.traceID = id
		}
		if k == spanHeader {
			id, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return tracer.ErrSpanContextCorrupted
			}
			sc.spanID = id
		}
		if k == priorityHeader {
			p, err := strconv.Atoi(v)
			if err != nil {
				return tracer.ErrSpanContextCorrupted
			}
			sc.priority = p
			sc.hasPriority = true
		}
		if strings.HasPrefix(k, baggagePrefix) {
			sc.setBaggageItem(strings.TrimPrefix(k, baggagePrefix), v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sc.traceID == 0 || sc.spanID == 0 {
		return nil, tracer.ErrSpanContextNotFound
	}
	return &sc, err
}

func (t *mocktracer) Inject(context ddtrace.SpanContext, carrier interface{}) error {
	writer, ok := carrier.(tracer.TextMapWriter)
	if !ok {
		return tracer.ErrInvalidCarrier
	}
	ctx, ok := context.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return tracer.ErrInvalidSpanContext
	}
	writer.Set(traceHeader, strconv.FormatUint(ctx.traceID, 10))
	writer.Set(spanHeader, strconv.FormatUint(ctx.spanID, 10))
	if ctx.hasSamplingPriority() {
		writer.Set(priorityHeader, strconv.Itoa(ctx.priority))
	}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		writer.Set(baggagePrefix+k, v)
		return true
	})
	return nil
}

func getDDEnvVars() string {
	var envVars []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "DD_") {
			envVars = append(envVars, e)
		}
	}
	return strings.Join(envVars, ",")
}

const (
	// headerComputedTopLevel specifies that the client has marked top-level spans, when set.
	// Any non-empty value will mean 'yes'.
	headerComputedTopLevel = "Datadog-Client-Computed-Top-Level"
)

var defaultDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}

var defaultClient = &http.Client{
	// We copy the transport to avoid using the default one, as it might be
	// augmented with tracing and we don't want these calls to be recorded.
	// See https://golang.org/pkg/net/http/#DefaultTransport .
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           defaultDialer.DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
	Timeout: defaultHTTPTimeout,
}

const (
	defaultHostname    = "localhost"
	defaultPort        = "8126"
	defaultAddress     = defaultHostname + ":" + defaultPort
	defaultHTTPTimeout = 2 * time.Second         // defines the current timeout before giving up with the send process
	traceCountHeader   = "X-Datadog-Trace-Count" // header containing the number of traces in the payload
)

var defaultHeaders = map[string]string{
	"Datadog-Meta-Lang":             "go",
	"Datadog-Meta-Lang-Version":     strings.TrimPrefix(runtime.Version(), "go"),
	"Datadog-Meta-Lang-Interpreter": runtime.Compiler + "-" + runtime.GOARCH + "-" + runtime.GOOS,
	"Datadog-Meta-Tracer-Version":   version.Tag,
	"Content-Type":                  "application/msgpack",
}

func sendTracesViaPost(trace []*encodablemockspan, host string, port int) (body io.ReadCloser, err error) {
	var size int
	var trace_count int

	mp, err := encode_finished_spans(trace)

	size, trace_count = mp.buf.Len()+len(mp.header)-mp.off, int(mp.count)
	log.Debug("Sending payload: size: %d traces: %d\n", size, trace_count)

	// Create a new HTTP request with the POST method
	req, err := http.NewRequest("POST", "http://"+host+":"+strconv.Itoa(port)+"/v0.4/traces", mp)
	if err != nil {
		return nil, fmt.Errorf("cannot create http request: %v", err)
	}

	for header, value := range defaultHeaders {
		req.Header.Set(header, value)
	}
	req.Header.Set("X-Datadog-Trace-Env-Variables", getDDEnvVars())
	req.Header.Set(traceCountHeader, strconv.Itoa(trace_count))
	req.Header.Set("Content-Length", strconv.Itoa(size))
	req.Header.Set(headerComputedTopLevel, "yes")
	req.Header.Set("Content-Type", "application/json")

	response, err := defaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if code := response.StatusCode; code >= 400 {
		// error, check the body for context information and
		// return a nice error.
		msg := make([]byte, 1000)
		n, _ := response.Body.Read(msg)
		response.Body.Close()
		txt := http.StatusText(code)
		if n > 0 {
			return nil, fmt.Errorf("%s (Status: %s)", msg[:n], txt)
		}
		return nil, fmt.Errorf("%s", txt)
	}
	return response.Body, nil
}

func encode_finished_spans(t encodableMockSpanList) (*mockpayload, error) {
	mp := &mockpayload{
		header: make([]byte, 8),
		off:    8,
	}

	mp.buf = bytes.Buffer{}
	mp.count = 0

	if err := msgp.Encode(&mp.buf, t); err != nil {
		return mp, err
	}
	atomic.AddUint32(&mp.count, 1)

	n := uint64(atomic.LoadUint32(&mp.count))
	switch {
	case n <= 15:
		mp.header[7] = msgpackArrayFix + byte(n)
		mp.off = 7
	case n <= 1<<16-1:
		binary.BigEndian.PutUint64(mp.header, n) // writes 2 bytes
		mp.header[5] = msgpackArray16
		mp.off = 5
	default: // n <= 1<<32-1
		binary.BigEndian.PutUint64(mp.header, n) // writes 4 bytes
		mp.header[3] = msgpackArray32
		mp.off = 3
	}
	return mp, nil
}

func sendSpansJson(spans []*encodablemockspan, host string, port int) error {
	// convert spans to JSON
	jsonSpans, err := json.Marshal(spans)
	if err != nil {
		return err
	}

	// create POST request with JSON data
	req, err := http.NewRequest("POST", "http://"+host+":"+strconv.Itoa(port)+"/v0.4/traces", bytes.NewBuffer(jsonSpans))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// send request and get response
	resp, err := defaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// check for any errors in response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 response code: %d", resp.StatusCode)
	}

	return nil
}
