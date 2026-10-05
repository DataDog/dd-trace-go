// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// benchMsg is a minimal tracing.Message used to exercise the real MessageCarrier.
type benchMsg struct {
	headers []Header
}

func (m *benchMsg) GetValue() []byte      { return nil }
func (m *benchMsg) GetKey() []byte        { return nil }
func (m *benchMsg) GetHeaders() []Header  { return m.headers }
func (m *benchMsg) SetHeaders(h []Header) { m.headers = h }
func (m *benchMsg) GetTopic() string      { return "" }
func (m *benchMsg) GetPartition() int     { return 0 }
func (m *benchMsg) GetOffset() int64      { return 0 }

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
	msg := &benchMsg{}
	datastreams.InjectToBase64Carrier(ctx, NewMessageCarrier(msg))
	for _, h := range msg.headers {
		if h.GetKey() == "dd-pathway-ctx-base64" {
			return string(h.GetValue())
		}
	}
	return ""
}

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
	hs := []Header{KafkaHeader{Key: "dd-pathway-ctx-base64", Value: []byte(pathwayHeaderValue())}}
	for i := range 10 {
		hs = append(hs, KafkaHeader{
			Key:   fmt.Sprintf("x-header-%d", i),
			Value: fmt.Appendf(nil, "some-moderately-long-header-value-%d", i),
		})
	}
	carrier := NewMessageCarrier(&benchMsg{headers: hs})
	b.Run("ForeachKey", func(b *testing.B) { benchExtract(b, foreachOnlyReader{carrier}) })
	b.Run("FastPath", func(b *testing.B) { benchExtract(b, carrier) })
}
