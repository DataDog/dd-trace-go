// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func TestImplementsTraceWriter(t *testing.T) {
	assert.Implements(t, (*traceWriter)(nil), &agentTraceWriter{})
	assert.Implements(t, (*traceWriter)(nil), &logTraceWriter{})
}

// makeSpan returns a span, adding n entries to meta and metrics each.
func makeSpan(n int) *span {
	s := newSpan("encodeName", "encodeService", "encodeResource", random.Uint64(), random.Uint64(), random.Uint64())
	for i := 0; i < n; i++ {
		istr := fmt.Sprintf("%0.10d", i)
		s.Meta[istr] = istr
		s.Metrics[istr] = float64(i)
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
		h := newLogTraceWriter(newConfig())
		h.w = &buf
		s := makeSpan(0)
		for i := 0; i < 20; i++ {
			h.add([]*span{s, s})
		}
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err := d.Decode(&v)
		assert.NoError(err)
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
		h := newLogTraceWriter(newConfig())
		h.w = &buf
		s := makeSpan(0)
		s.Metrics["nan"] = math.NaN()
		s.Metrics["+inf"] = math.Inf(1)
		s.Metrics["-inf"] = math.Inf(-1)
		h.add([]*span{s})
		h.flush()
		json := string(buf.Bytes())
		assert.NotContains(json, `"nan":`)
		assert.NotContains(json, `"+inf":`)
		assert.NotContains(json, `"-inf":`)
	})

	t.Run("fullspan", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		h := newLogTraceWriter(newConfig())
		h.w = &buf
		type jsonSpan struct {
			TraceID  string             `json:"trace_id"`
			SpanID   string             `json:"span_id"`
			ParentID string             `json:"parent_id"`
			Name     string             `json:"name"`
			Resource string             `json:"resource"`
			Error    int32              `json:"error"`
			Meta     map[string]string  `json:"meta"`
			Metrics  map[string]float64 `json:"metrics"`
			Start    int64              `json:"start"`
			Duration int64              `json:"duration"`
			Service  string             `json:"service"`
		}
		type jsonPayload struct {
			Traces [][]jsonSpan `json:"traces"`
		}
		s := &span{
			Name:     "basicName",
			Service:  "basicService",
			Resource: "basicResource",
			Meta: map[string]string{
				"env":     "prod",
				"version": "1.26.0",
			},
			Metrics: map[string]float64{
				"widgets": 1e26,
				"zero":    0.0,
				"big":     math.MaxFloat64,
				"small":   math.SmallestNonzeroFloat64,
				"nan":     math.NaN(),
				"-inf":    math.Inf(-1),
				"+inf":    math.Inf(1),
			},
			SpanID:   10,
			TraceID:  11,
			ParentID: 12,
			Start:    123,
			Duration: 456,
			Error:    789,
		}
		expected := jsonSpan{
			Name:     "basicName",
			Service:  "basicService",
			Resource: "basicResource",
			Meta: map[string]string{
				"env":     "prod",
				"version": "1.26.0",
			},
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
		h.add([]*span{s})
		h.flush()
		d := json.NewDecoder(&buf)
		var payload jsonPayload
		err := d.Decode(&payload)
		assert.NoError(err)
		assert.Equal(jsonPayload{[][]jsonSpan{{expected}}}, payload)
	})
}

func TestLogWriterOverflow(t *testing.T) {
	log.UseLogger(new(testLogger))
	t.Run("single-too-big", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		var tg testStatsdClient
		h := newLogTraceWriter(newConfig(withStatsdClient(&tg)))
		h.w = &buf
		s := makeSpan(10000)
		h.add([]*span{s})
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err := d.Decode(&v)
		assert.Equal(io.EOF, err)
		assert.Contains(tg.CallNames(), "datadog.tracer.traces_dropped")
	})

	t.Run("split", func(t *testing.T) {
		assert := assert.New(t)
		var buf bytes.Buffer
		var tg testStatsdClient
		h := newLogTraceWriter(newConfig(withStatsdClient(&tg)))
		h.w = &buf
		s := makeSpan(10)
		var trace []*span
		for i := 0; i < 500; i++ {
			trace = append(trace, s)
		}
		h.add(trace)
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err := d.Decode(&v)
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
		h := newLogTraceWriter(newConfig())
		h.w = &buf
		s := makeSpan(4000)
		h.add([]*span{s})
		h.add([]*span{s})
		h.flush()
		v := struct{ Traces [][]map[string]interface{} }{}
		d := json.NewDecoder(&buf)
		err := d.Decode(&v)
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
func TestJsonEncodeSpanNewLines(t *testing.T) {
	assert := assert.New(t)
	s := newSpan("name", "srv", "res", 2, 1, 3)
	s.Start = 12
	s.Meta["query"] = "Select * from \n Where value"
	s.Meta["query\n"] = "Select * from \n Where\nvalue"

	h := &logTraceWriter{}
	h.resetBuffer()
	h.encodeSpan(s)

	str := h.buf.String()
	assert.Equal(`{"traces": [{"trace_id":"1","span_id":"2","parent_id":"3","name":"name","resource":"res","error":0,"meta":{"query":"Select * from \n Where value","query\n":"Select * from \n Where\nvalue"},"metrics":{},"start":12,"duration":0,"service":"srv"}`, str)
	assert.NotContains(h.buf.String(), "\n")
	assert.Contains(str, "\\n")
}

func BenchmarkJsonEncodeSpan(b *testing.B) {
	s := makeSpan(10)
	s.Metrics["nan"] = math.NaN()
	s.Metrics["+inf"] = math.Inf(1)
	s.Metrics["-inf"] = math.Inf(-1)
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
