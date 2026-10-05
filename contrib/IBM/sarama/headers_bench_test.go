// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/IBM/sarama"
)

// foreachOnlyReader hides the carrier's Get method so ExtractFromBase64Carrier
// falls back to ForeachKey. This lets each benchmark measure the exact same
// message both ways (slow ForeachKey vs the fast single-key path).
type foreachOnlyReader struct {
	r datastreams.TextMapReader
}

func (f foreachOnlyReader) ForeachKey(handler func(key, val string) error) error {
	return f.r.ForeachKey(handler)
}

func pathwayHeaderValue(topic string) string {
	ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:kafka", "topic:"+topic)
	pm := &sarama.ProducerMessage{}
	datastreams.InjectToBase64Carrier(ctx, NewProducerMessageCarrier(pm))
	for _, h := range pm.Headers {
		if string(h.Key) == "dd-pathway-ctx-base64" {
			return string(h.Value)
		}
	}
	return ""
}

func noiseHeaders(pathway string, extra int) []*sarama.RecordHeader {
	hs := []*sarama.RecordHeader{{Key: []byte("dd-pathway-ctx-base64"), Value: []byte(pathway)}}
	for i := range extra {
		hs = append(hs, &sarama.RecordHeader{
			Key:   fmt.Appendf(nil, "x-header-%d", i),
			Value: fmt.Appendf(nil, "some-moderately-long-header-value-%d", i),
		})
	}
	return hs
}

func benchExtract(b *testing.B, r datastreams.TextMapReader) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		datastreams.ExtractFromBase64Carrier(context.Background(), r)
	}
}

// BenchmarkExtractConsumer measures ExtractFromBase64Carrier on a consumer message
// carrying the pathway header plus 10 other headers (typical of a real message).
func BenchmarkExtractConsumer(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()
	msg := &sarama.ConsumerMessage{Headers: noiseHeaders(pathwayHeaderValue("orders"), 10)}
	carrier := NewConsumerMessageCarrier(msg)
	b.Run("ForeachKey", func(b *testing.B) { benchExtract(b, foreachOnlyReader{carrier}) })
	b.Run("FastPath", func(b *testing.B) { benchExtract(b, carrier) })
}
