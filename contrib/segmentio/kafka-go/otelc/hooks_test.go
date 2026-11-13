// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"testing"
	"time"
	"weak"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// The helpers below run the hooks the way the trampolines do, with result
// standing in for the original call. Nothing reaches a broker.

func writeMessages(ctx context.Context, w *kafka.Writer, err error, msgs ...kafka.Message) {
	ictx := hooktest.NewMockHookContext(w, ctx, msgs)
	BeforeWriterWriteMessages(ictx, w, ctx, msgs...)
	AfterWriterWriteMessages(ictx, err)
}

func fetchMessage(ctx context.Context, r *kafka.Reader, msg kafka.Message, err error) kafka.Message {
	ictx := hooktest.NewMockHookContext(r, ctx)
	BeforeReaderFetchMessage(ictx, r, ctx)
	ictx.ReturnVals = []any{msg, err}
	AfterReaderFetchMessage(ictx, msg, err)
	return ictx.GetReturnVal(0).(kafka.Message)
}

func closeReader(r *kafka.Reader) {
	BeforeReaderClose(hooktest.NewMockHookContext(r), r)
}

func newReader(t *testing.T) *kafka.Reader {
	t.Helper()
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{"broker:9092"}, Topic: "topic", GroupID: "group"})
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func header(msg kafka.Message, key string) string {
	for _, h := range msg.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestWriteMessagesHooks(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
	w := &kafka.Writer{Addr: kafka.TCP("broker:9092"), Topic: "topic"}
	msgs := []kafka.Message{{Value: []byte("a")}, {Value: []byte("b")}}
	writeMessages(ctx, w, nil, msgs...)
	writeMessages(ctx, w, errors.New("boom"), kafka.Message{Value: []byte("c")})
	writeMessages(ctx, w, nil)
	root.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)
	var produced []*mocktracer.Span
	for _, s := range spans {
		if s.OperationName() == "kafka.produce" {
			produced = append(produced, s)
		}
	}
	require.Len(t, produced, 3, "one span per message")
	for i, s := range produced {
		assert.Equal(t, root.Context().SpanID(), s.ParentID(), "span %d is a child of the root", i)
		assert.Equal(t, "Produce Topic topic", s.Tag(ext.ResourceName))
		assert.Equal(t, "broker:9092", s.Tag(ext.KafkaBootstrapServers))
		assert.Equal(t, "segmentio/kafka.go.v0", s.Tag(ext.Component))
	}
	assert.Nil(t, produced[0].Tag(ext.ErrorMsg))
	assert.Equal(t, "boom", produced[2].Tag(ext.ErrorMsg))
	// The span context is injected into the messages the original call writes.
	for i, msg := range msgs {
		assert.Equal(t, strconv.FormatUint(produced[i].SpanID(), 10), header(msg, "x-datadog-parent-id"))
	}
}

func TestWriteMessagesHooksNilAddr(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	writeMessages(context.Background(), &kafka.Writer{}, errors.New("nil address"), kafka.Message{Topic: "topic"})
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Nil(t, spans[0].Tag(ext.KafkaBootstrapServers))
}

func TestFetchMessageHooks(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	r := newReader(t)
	msg := fetchMessage(context.Background(), r, kafka.Message{Topic: "topic", Partition: 1, Offset: 7}, nil)
	require.Empty(t, mt.FinishedSpans(), "the span stays open until the next fetch")

	fetchMessage(context.Background(), r, kafka.Message{}, errors.New("boom"))
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1, "the next fetch finishes the span, and a failed fetch has no span")
	consumed := spans[0]
	assert.Equal(t, "kafka.consume", consumed.OperationName())
	assert.Equal(t, "Consume Topic topic", consumed.Tag(ext.ResourceName))
	assert.Equal(t, strconv.FormatUint(consumed.SpanID(), 10), header(msg, "x-datadog-parent-id"),
		"the returned message carries the span context")

	fetchMessage(context.Background(), r, kafka.Message{Topic: "topic"}, nil)
	closeReader(r)
	assert.Len(t, mt.FinishedSpans(), 2, "Close finishes the last span")
	_, ok := readers.Load(weak.Make(r))
	assert.False(t, ok, "Close drops the reader's tracer")
}

func TestFetchMessageHooksContinueTrace(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	w := &kafka.Writer{Addr: kafka.TCP("broker:9092"), Topic: "topic"}
	msgs := []kafka.Message{{Topic: "topic"}}
	writeMessages(context.Background(), w, nil, msgs...)
	r := newReader(t)
	fetchMessage(context.Background(), r, msgs[0], nil)
	closeReader(r)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	produced, consumed := spans[0], spans[1]
	assert.Equal(t, produced.SpanID(), consumed.ParentID())
	assert.Equal(t, produced.TraceID(), consumed.TraceID())
}

func TestTracersFreeReaderAndWriter(t *testing.T) {
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: []string{"broker:9092"}, Topic: "topic"})
	w := &kafka.Writer{Addr: kafka.TCP("broker:9092")}
	rkey, wkey := weak.Make(r), weak.Make(w)
	fetchMessage(context.Background(), r, kafka.Message{}, errors.New("boom"))
	writeMessages(context.Background(), w, nil, kafka.Message{Topic: "topic"})
	_, ok := readers.Load(rkey)
	require.True(t, ok)
	_, ok = writers.Load(wkey)
	require.True(t, ok)
	require.NoError(t, r.Close())

	r, w = nil, nil
	assert.Eventually(t, func() bool {
		runtime.GC()
		_, rok := readers.Load(rkey)
		_, wok := writers.Load(wkey)
		return rkey.Value() == nil && wkey.Value() == nil && !rok && !wok
	}, 5*time.Second, 10*time.Millisecond)
}
