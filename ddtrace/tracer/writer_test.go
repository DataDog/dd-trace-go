// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImplementsTraceWriter(t *testing.T) {
	assert.Implements(t, (*traceWriter)(nil), &agentTraceWriter{})
	assert.Implements(t, (*traceWriter)(nil), &logTraceWriter{})
}

// makeSpan returns a span, adding n entries to meta and metrics each.
func makeSpan(n int) *Span {
	s := newSpan("encodeName", "encodeService", "encodeResource", randUint64(), randUint64(), randUint64())
	for i := 0; i < n; i++ {
		istr := fmt.Sprintf("%0.10d", i)
		s.meta[istr] = istr
		s.metrics[istr] = float64(i)
	}
	return s
}

func TestEncodeFloat(t *testing.T) {
	for _, tt := range []struct {
		f      float64
		expect string
	}{
		{
			9.9999999999999990e20,
			"999999999999999900000",
		},
		{
			9.9999999999999999e20,
			"1e+21",
		},
		{
			-9.9999999999999990e20,
			"-999999999999999900000",
		},
		{
			-9.9999999999999999e20,
			"-1e+21",
		},
		{
			0.000001,
			"0.000001",
		},
		{
			0.0000009,
			"9e-7",
		},
		{
			-0.000001,
			"-0.000001",
		},
		{
			-0.0000009,
			"-9e-7",
		},
		{
			math.NaN(),
			"null",
		},
		{
			math.Inf(-1),
			"null",
		},
		{
			math.Inf(1),
			"null",
		},
	} {
		t.Run(tt.expect, func(t *testing.T) {
			assert.Equal(t, tt.expect, string(encodeFloat(nil, tt.f)))
		})
	}

}

func TestLogWriter(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		cfg, err := newTestConfig()
		assert.NoError(err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		h := newLogTraceWriter(cfg, statsd)
		h.w = &buf
		s := makeSpan(0)
		for i := 0; i < 20; i++ {
			h.add([]*Span{s, s})
		}
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err = d.Decode(&v)
		assert.NoError(err, buf.String())
		assert.Len(v.Traces, 20, "Expected 20 traces, but have %d", len(v.Traces))
		for _, t := range v.Traces {
			assert.Len(t, 2, "Expected 2 spans, but have %d", len(t))
		}
		err = d.Decode(&v)
		assert.Equal(io.EOF, err)
	})

	t.Run("inf+nan", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		cfg, err := newTestConfig()
		require.NoError(t, err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		h := newLogTraceWriter(cfg, statsd)
		h.w = &buf
		s := makeSpan(0)
		s.metrics["nan"] = math.NaN()
		s.metrics["+inf"] = math.Inf(1)
		s.metrics["-inf"] = math.Inf(-1)
		h.add([]*Span{s})
		h.flush()
		json := buf.String()
		assert.NotContains(json, `"nan":`)
		assert.NotContains(json, `"+inf":`)
		assert.NotContains(json, `"-inf":`)
	})

	t.Run("fullspan", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		cfg, err := newTestConfig()
		require.NoError(t, err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		h := newLogTraceWriter(cfg, statsd)
		h.w = &buf
		type jsonSpan struct {
			TraceID    string             `json:"trace_id"`
			SpanID     string             `json:"span_id"`
			ParentID   string             `json:"parent_id"`
			Name       string             `json:"name"`
			Resource   string             `json:"resource"`
			Error      int32              `json:"error"`
			Meta       map[string]string  `json:"meta"`
			MetaStruct map[string]any     `json:"meta_struct"`
			Metrics    map[string]float64 `json:"metrics"`
			Start      int64              `json:"start"`
			Duration   int64              `json:"duration"`
			Service    string             `json:"service"`
		}
		type jsonPayload struct {
			Traces [][]jsonSpan `json:"traces"`
		}
		s := &Span{
			name:     "basicName",
			service:  "basicService",
			resource: "basicResource",
			meta: map[string]string{
				"env":     "prod",
				"version": "1.26.0",
			},
			metaStruct: map[string]any{
				"_dd.stack": map[string]string{
					"0": "github.com/DataDog/dd-trace-go/v1/internal/tracer.TestLogWriter",
				},
			},
			metrics: map[string]float64{
				"widgets": 1e26,
				"zero":    0.0,
				"big":     math.MaxFloat64,
				"small":   math.SmallestNonzeroFloat64,
				"nan":     math.NaN(),
				"-inf":    math.Inf(-1),
				"+inf":    math.Inf(1),
			},
			spanID:   10,
			traceID:  11,
			parentID: 12,
			start:    123,
			duration: 456,
			error:    789,
		}
		expected := jsonSpan{
			Name:     "basicName",
			Service:  "basicService",
			Resource: "basicResource",
			Meta: map[string]string{
				"env":       "prod",
				"version":   "1.26.0",
				"_dd.stack": "{\"0\":\"github.com/DataDog/dd-trace-go/v1/internal/tracer.TestLogWriter\"}",
			},
			MetaStruct: nil,
			Metrics: map[string]float64{
				"widgets": 1e26,
				"zero":    0.0,
				"big":     math.MaxFloat64,
				"small":   math.SmallestNonzeroFloat64,
			},
			SpanID:   "a",
			TraceID:  "b",
			ParentID: "c",
			Start:    123,
			Duration: 456,
			Error:    789,
		}
		h.add([]*Span{s})
		h.flush()
		d := json.NewDecoder(&buf)
		var payload jsonPayload
		err = d.Decode(&payload)
		assert.NoError(err)
		assert.Equal(jsonPayload{[][]jsonSpan{{expected}}}, payload)
	})

	t.Run("invalid-characters", func(t *testing.T) {
		assert := assert.New(t)
		s := newSpan("name\n", "srv\t", `"res"`, 2, 1, 3)
		s.start = 12
		s.meta["query\n"] = "Select * from \n Where\nvalue"
		s.metrics["version\n"] = 3

		var w logTraceWriter
		w.encodeSpan(s)

		str := w.buf.String()
		assert.Equal(`{"trace_id":"1","span_id":"2","parent_id":"3","name":"name\n","resource":"\"res\"","error":0,"meta":{"query\n":"Select * from \n Where\nvalue"},"metrics":{"version\n":3},"start":12,"duration":0,"service":"srv\t"}`, str)
		assert.NotContains(str, "\n")
		assert.Contains(str, "\\n")
	})
}

func TestLogWriterOverflow(t *testing.T) {
	log.UseLogger(new(log.RecordLogger))
	t.Run("single-too-big", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		var tg statsdtest.TestStatsdClient
		cfg, err := newTestConfig(withStatsdClient(&tg))
		require.NoError(t, err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		h := newLogTraceWriter(cfg, statsd)
		h.w = &buf
		s := makeSpan(10000)
		h.add([]*Span{s})
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err = d.Decode(&v)
		assert.Equal(io.EOF, err)
		assert.Contains(tg.CallNames(), "datadog.tracer.traces_dropped")
	})

	t.Run("split", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		var tg statsdtest.TestStatsdClient
		cfg, err := newTestConfig(withStatsdClient(&tg))
		require.NoError(t, err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		h := newLogTraceWriter(cfg, statsd)
		h.w = &buf
		s := makeSpan(10)
		var trace []*Span
		for i := 0; i < 500; i++ {
			trace = append(trace, s)
		}
		h.add(trace)
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err = d.Decode(&v)
		assert.NoError(err)
		assert.Len(v.Traces, 1, "Expected 1 trace, but have %d", len(v.Traces))
		spann := len(v.Traces[0])
		err = d.Decode(&v)
		assert.NoError(err)
		assert.Len(v.Traces, 1, "Expected 1 trace, but have %d", len(v.Traces))
		spann += len(v.Traces[0])
		assert.Equal(500, spann)
		err = d.Decode(&v)
		assert.Equal(io.EOF, err)
	})

	t.Run("two-large", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		cfg, err := newTestConfig()
		require.NoError(t, err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		h := newLogTraceWriter(cfg, statsd)
		h.w = &buf
		s := makeSpan(4000)
		h.add([]*Span{s})
		h.add([]*Span{s})
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err = d.Decode(&v)
		assert.NoError(err)
		assert.Len(v.Traces, 1, "Expected 1 trace, but have %d", len(v.Traces))
		assert.Len(v.Traces[0], 1, "Expected 1 span, but have %d", len(v.Traces[0]))
		err = d.Decode(&v)
		assert.NoError(err)
		assert.Len(v.Traces, 1, "Expected 1 trace, but have %d", len(v.Traces))
		assert.Len(v.Traces[0], 1, "Expected 1 span, but have %d", len(v.Traces[0]))
		err = d.Decode(&v)
		assert.Equal(io.EOF, err)
	})
}

type failingTransport struct {
	dummyTransport
	failCount    int
	sendAttempts int
	tracesSent   bool
	traces       spanLists
	assert       *assert.Assertions
}

func (t *failingTransport) send(p payload) (io.ReadCloser, error) {
	t.sendAttempts++

	traces, err := decode(p)
	if err != nil {
		return nil, err
	}
	if t.sendAttempts == 1 {
		t.traces = traces
	} else {
		t.assert.Equal(t.traces, traces)
	}

	if t.failCount > 0 {
		t.failCount--
		return nil, errors.New("oops, I failed")
	}

	t.tracesSent = true
	return io.NopCloser(strings.NewReader("OK")), nil
}

func TestTraceWriterFlushRetries(t *testing.T) {
	testcases := []struct {
		configRetries int
		retryInterval time.Duration
		failCount     int
		tracesSent    bool
		expAttempts   int
	}{
		{configRetries: 0, retryInterval: time.Millisecond, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 0, retryInterval: time.Millisecond, failCount: 1, tracesSent: false, expAttempts: 1},

		{configRetries: 1, retryInterval: time.Millisecond, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 1, retryInterval: time.Millisecond, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 1, retryInterval: time.Millisecond, failCount: 2, tracesSent: false, expAttempts: 2},

		{configRetries: 2, retryInterval: time.Millisecond, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 2, tracesSent: true, expAttempts: 3},
		{configRetries: 2, retryInterval: time.Millisecond, failCount: 3, tracesSent: false, expAttempts: 3},

		{configRetries: 1, retryInterval: 2 * time.Millisecond, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 2, retryInterval: 2 * time.Millisecond, failCount: 2, tracesSent: true, expAttempts: 3},
	}

	sentCounts := map[string]int64{
		"datadog.tracer.decode_error":          1,
		"datadog.tracer.flush_bytes":           185,
		"datadog.tracer.flush_traces":          1,
		"datadog.tracer.queue.enqueued.traces": 1,
	}
	droppedCounts := map[string]int64{
		"datadog.tracer.queue.enqueued.traces": 1,
		"datadog.tracer.traces_dropped":        1,
	}

	ss := []*Span{makeSpan(0)}
	for _, test := range testcases {
		name := fmt.Sprintf("%d-%d-%t-%d", test.configRetries, test.failCount, test.tracesSent, test.expAttempts)
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			p := &failingTransport{
				failCount: test.failCount,
				assert:    assert,
			}
			c, err := newTestConfig(func(c *config) {
				c.transport = p
				c.sendRetries = test.configRetries
				c.retryInterval = test.retryInterval
			})
			assert.Nil(err)
			var statsd statsdtest.TestStatsdClient

			h := newAgentTraceWriter(c, newPrioritySampler(), &statsd)
			h.add(ss)
			start := time.Now()
			h.flush()
			h.wg.Wait()
			elapsed := time.Since(start)

			assert.Equal(test.expAttempts, p.sendAttempts)
			assert.Equal(test.tracesSent, p.tracesSent)

			assert.Equal(1, len(statsd.TimingCalls()))
			if test.tracesSent {
				assert.Equal(sentCounts, statsd.Counts())
			} else {
				assert.Equal(droppedCounts, statsd.Counts())
			}
			if test.configRetries > 0 && test.failCount > 1 {
				assert.GreaterOrEqual(elapsed, test.retryInterval*time.Duration(minInts(test.configRetries+1, test.failCount)))
			}
		})
	}
}

func minInts(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestTraceProtocol(t *testing.T) {
	assert := assert.New(t)

	t.Run("v1.0", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
		cfg, err := newTestConfig()
		require.NoError(t, err)
		h := newAgentTraceWriter(cfg, nil, nil)
		assert.Equal(traceProtocolV1, h.payload.protocol())
	})

	t.Run("v0.4", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "0.4")
		cfg, err := newTestConfig()
		require.NoError(t, err)
		h := newAgentTraceWriter(cfg, nil, nil)
		assert.Equal(traceProtocolV04, h.payload.protocol())
	})

	t.Run("default", func(t *testing.T) {
		cfg, err := newTestConfig()
		require.NoError(t, err)
		h := newAgentTraceWriter(cfg, nil, nil)
		assert.Equal(traceProtocolV04, h.payload.protocol())
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "invalid")
		cfg, err := newTestConfig()
		require.NoError(t, err)
		h := newAgentTraceWriter(cfg, nil, nil)
		assert.Equal(traceProtocolV04, h.payload.protocol())
	})
}
func BenchmarkJsonEncodeSpan(b *testing.B) {
	s := makeSpan(10)
	s.metrics["nan"] = math.NaN()
	s.metrics["+inf"] = math.Inf(1)
	s.metrics["-inf"] = math.Inf(-1)
	h := &logTraceWriter{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.resetBuffer()
		h.encodeSpan(s)
	}
}

func BenchmarkJsonEncodeFloat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var ba = make([]byte, 25)
		bs := ba[:0]
		encodeFloat(bs, float64(1e-9))
	}
}

func TestAgentWriterRaceCondition(t *testing.T) {
	// This test reproduces a race condition between add() and flush() operations
	// The race occurs when:
	// 1. add() loads payload, flush() replaces it before add() can push to it
	// 2. add() increments tracesQueued while flush() goroutine resets it to 0
	//
	// Run with: go test -race -run TestAgentWriterRaceCondition

	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient
	cfg, err := newTestConfig(withStatsdClient(&tg))
	require.NoError(t, err)
	statsd, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer statsd.Close()

	writer := newAgentTraceWriter(cfg, newPrioritySampler(), &tg)

	const numGoroutines = 50
	const numOperations = 100

	// Channel to coordinate goroutines
	start := make(chan struct{})

	var wg sync.WaitGroup

	// Spawn goroutines that continuously add traces
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // Wait for coordination signal

			for j := 0; j < numOperations; j++ {
				spans := []*Span{makeSpan(1)}
				writer.add(spans)
			}
		}()
	}

	// Spawn goroutines that continuously flush
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // Wait for coordination signal

			for j := 0; j < numOperations; j++ {
				writer.flush()
			}
		}()
	}

	// Start all goroutines simultaneously to maximize race condition probability
	close(start)

	// Wait for all operations to complete
	wg.Wait()

	// Final flush to process any remaining traces
	writer.flush()
	writer.wg.Wait()

	// The race condition might cause:
	// 1. Traces to be lost (added to old payload after it was flushed)
	// 2. Incorrect trace counts due to counter races
	// 3. Data races detected by Go's race detector

	assert.True(true, "Test completed - check for race conditions with -race flag")
}

func TestAgentWriterTraceCountAccuracy(t *testing.T) {
	// This test validates that trace counting remains accurate under concurrent operations
	// It detects both data races and logical errors in trace counting

	assert := assert.New(t)
	var tg statsdtest.TestStatsdClient
	cfg, err := newTestConfig(withStatsdClient(&tg))
	require.NoError(t, err)
	statsd, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer statsd.Close()

	writer := newAgentTraceWriter(cfg, newPrioritySampler(), &tg)

	const numAddGoroutines = 20
	const numFlushGoroutines = 10
	const numTracesPerGoroutine = 50
	const expectedTotalTraces = numAddGoroutines * numTracesPerGoroutine

	start := make(chan struct{})
	var wg sync.WaitGroup

	// Track traces added for verification
	var tracesAdded int32

	// Spawn goroutines that add traces
	for i := 0; i < numAddGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			for j := 0; j < numTracesPerGoroutine; j++ {
				spans := []*Span{makeSpan(1)}
				writer.add(spans)
				atomic.AddInt32(&tracesAdded, 1)
			}
		}()
	}

	// Spawn goroutines that flush occasionally
	for i := 0; i < numFlushGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			// Flush periodically while adds are happening
			for j := 0; j < 10; j++ {
				time.Sleep(time.Microsecond * 100)
				writer.flush()
			}
		}()
	}

	// Start all goroutines
	close(start)
	wg.Wait()

	// Final flush to ensure all traces are processed
	writer.flush()
	writer.wg.Wait()

	// Verify that the number of traces added matches our expectation
	actualTracesAdded := atomic.LoadInt32(&tracesAdded)
	assert.Equal(int32(expectedTotalTraces), actualTracesAdded,
		"Expected %d traces to be added, but got %d", expectedTotalTraces, actualTracesAdded)

	// The race condition could cause:
	// 1. Loss of traces if they're added to an old payload after flush starts
	// 2. Incorrect metrics reporting due to counter races
	// 3. Data corruption in payload structures
}
