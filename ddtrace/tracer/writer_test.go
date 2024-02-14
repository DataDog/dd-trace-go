// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

type failingTransport struct {
	dummyTransport
	failCount    int
	sendAttempts int
	tracesSent   bool
	traces       spanLists
	assert       *assert.Assertions
}

func (t *failingTransport) send(p *payload) (io.ReadCloser, error) {
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
