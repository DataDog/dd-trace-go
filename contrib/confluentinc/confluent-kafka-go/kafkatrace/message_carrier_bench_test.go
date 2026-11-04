// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// benchHeader/benchMsg mirror the real confluent wHeader/wMessage: GetHeaders
// allocates a fresh []Header and boxes each header on every call, so the
// benchmark reflects production per-message allocation behavior.
type benchHeader struct {
	key string
	val []byte
}

func (h *benchHeader) GetKey() string   { return h.key }
func (h *benchHeader) GetValue() []byte { return h.val }

type benchMsg struct {
	raw []benchHeader
}

func (m *benchMsg) GetValue() []byte { return nil }
func (m *benchMsg) GetKey() []byte   { return nil }
func (m *benchMsg) GetHeaders() []Header {
	hs := make([]Header, 0, len(m.raw))
	for i := range m.raw {
		hs = append(hs, &m.raw[i])
	}
	return hs
}
func (m *benchMsg) SetHeaders([]Header)               {}
func (m *benchMsg) GetTopicPartition() TopicPartition { return nil }
func (m *benchMsg) Unwrap() any                       { return nil }

// foreachOnlyReader hides Get so ExtractFromBase64Carrier uses the ForeachKey
// fallback, letting us benchmark the same message both ways.
type foreachOnlyReader struct {
	r datastreams.TextMapReader
}

func (f foreachOnlyReader) ForeachKey(handler func(key, val string) error) error {
	return f.r.ForeachKey(handler)
}

func pathwayHeaderValue() string {
	ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:kafka", "topic:orders")
	w := &writeMsg{}
	datastreams.InjectToBase64Carrier(ctx, NewMessageCarrier(w))
	for _, h := range w.headers {
		if h.GetKey() == "dd-pathway-ctx-base64" {
			return string(h.GetValue())
		}
	}
	return ""
}

// writeMsg captures headers written by InjectToBase64Carrier.
type writeMsg struct {
	headers []Header
}

func (m *writeMsg) GetValue() []byte                  { return nil }
func (m *writeMsg) GetKey() []byte                    { return nil }
func (m *writeMsg) GetHeaders() []Header              { return m.headers }
func (m *writeMsg) SetHeaders(h []Header)             { m.headers = h }
func (m *writeMsg) GetTopicPartition() TopicPartition { return nil }
func (m *writeMsg) Unwrap() any                       { return nil }

func benchExtract(b *testing.B, r datastreams.TextMapReader) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		datastreams.ExtractFromBase64Carrier(context.Background(), r)
	}
}

// BenchmarkExtract measures ExtractFromBase64Carrier on a message carrying the
// pathway header plus 10 other headers (typical of a real message).
func BenchmarkExtract(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()
	raw := make([]benchHeader, 0, 11)
	raw = append(raw, benchHeader{key: "dd-pathway-ctx-base64", val: []byte(pathwayHeaderValue())})
	for i := range 10 {
		raw = append(raw, benchHeader{
			key: fmt.Sprintf("x-header-%d", i),
			val: fmt.Appendf(nil, "some-moderately-long-header-value-%d", i),
		})
	}
	carrier := NewMessageCarrier(&benchMsg{raw: raw})
	b.Run("ForeachKey", func(b *testing.B) { benchExtract(b, foreachOnlyReader{carrier}) })
	b.Run("FastPath", func(b *testing.B) { benchExtract(b, carrier) })
}
