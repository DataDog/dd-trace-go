// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for github.com/segmentio/kafka-go.
// See otelc.yaml. It is a separate module so otelc never reaches contrib users
// who do not use it.
package otelc

import (
	"context"
	"runtime"
	"sync"
	"weak"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otelc/pkg/hook"

	kafkatrace "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"
)

// readers and writers hold the tracer of each kafka.Reader and kafka.Writer,
// built the first time it is used. The values hold no reference to their
// reader or writer, and an entry is deleted once its key is freed.
var (
	readers sync.Map // weak.Pointer[kafka.Reader] -> *kafkatrace.ReaderTracer
	writers sync.Map // weak.Pointer[kafka.Writer] -> *kafkatrace.WriterTracer
)

// readerTracer returns the tracer of r.
//
// A call before the contrib's init panics in NewReaderTracer. The hook
// trampoline recovers it and the message is read untraced. Nothing is stored,
// so the next call builds the tracer again.
func readerTracer(r *kafka.Reader) *kafkatrace.ReaderTracer {
	key := weak.Make(r)
	if rt, ok := readers.Load(key); ok {
		return rt.(*kafkatrace.ReaderTracer)
	}
	rt, loaded := readers.LoadOrStore(key, kafkatrace.NewReaderTracer(r))
	if !loaded {
		runtime.AddCleanup(r, func(key weak.Pointer[kafka.Reader]) { readers.Delete(key) }, key)
	}
	return rt.(*kafkatrace.ReaderTracer)
}

// writerTracer returns the tracer of w. See readerTracer.
func writerTracer(w *kafka.Writer) *kafkatrace.WriterTracer {
	key := weak.Make(w)
	if wt, ok := writers.Load(key); ok {
		return wt.(*kafkatrace.WriterTracer)
	}
	wt, loaded := writers.LoadOrStore(key, kafkatrace.NewWriterTracer(w))
	if !loaded {
		runtime.AddCleanup(w, func(key weak.Pointer[kafka.Writer]) { writers.Delete(key) }, key)
	}
	return wt.(*kafkatrace.WriterTracer)
}

type fetchCall struct {
	ctx context.Context
	rt  *kafkatrace.ReaderTracer
}

// BeforeReaderFetchMessage finishes the span of the previous message r
// fetched. ReadMessage calls FetchMessage, so this covers both. Calls the
// contrib's Reader makes are marked and left alone.
func BeforeReaderFetchMessage(ictx hook.HookContext, r *kafka.Reader, ctx context.Context) {
	if r == nil || kafkatrace.TraceMarked(ctx, r) {
		return
	}
	rt := readerTracer(r)
	rt.FinishPrevious()
	ictx.SetData(fetchCall{ctx: ctx, rt: rt})
}

// AfterReaderFetchMessage starts the span of the fetched message. The span
// context is injected into the message headers, so the message is written
// back.
func AfterReaderFetchMessage(ictx hook.HookContext, msg kafka.Message, err error) {
	call, ok := ictx.GetData().(fetchCall)
	if !ok || err != nil {
		return
	}
	call.rt.StartConsumeSpan(call.ctx, &msg)
	ictx.SetReturnVal(0, msg)
}

// BeforeReaderClose finishes the span of the last message r fetched.
func BeforeReaderClose(_ hook.HookContext, r *kafka.Reader) {
	if r == nil {
		return
	}
	if rt, ok := readers.LoadAndDelete(weak.Make(r)); ok {
		rt.(*kafkatrace.ReaderTracer).FinishPrevious()
	}
}

// BeforeWriterWriteMessages starts one span for each message w writes. Calls
// the contrib's KafkaWriter makes are marked and left alone.
func BeforeWriterWriteMessages(ictx hook.HookContext, w *kafka.Writer, ctx context.Context, msgs ...kafka.Message) {
	if w == nil || len(msgs) == 0 || kafkatrace.TraceMarked(ctx, w) {
		return
	}
	ictx.SetData(writerTracer(w).StartProduceSpans(ctx, w, msgs))
}

// AfterWriterWriteMessages finishes the spans BeforeWriterWriteMessages
// started.
func AfterWriterWriteMessages(ictx hook.HookContext, err error) {
	if finish, ok := ictx.GetData().(func(error)); ok {
		finish(err)
	}
}
