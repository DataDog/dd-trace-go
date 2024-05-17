// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Tests in this file rely on parsing execution tracer data, which can change
// formats across Go releases. This guard should be updated as the Go trace
// parser library is upgraded to support new versions.
//go:build !go1.21

package tracer

import (
	"bytes"
	"context"
	"encoding/binary"
	rt "runtime/trace"
	"testing"

	"github.com/stretchr/testify/assert"
	gotraceui "honnef.co/go/gotraceui/trace"
)

func TestExecutionTraceSpans(t *testing.T) {
	if rt.IsEnabled() {
		t.Skip("runtime execution tracing is already enabled")
	}

	buf := new(bytes.Buffer)
	if err := rt.Start(buf); err != nil {
		t.Fatal(err)
	}
	// Ensure we unconditionally stop tracing. It's safe to call this
	// multiple times.
	defer rt.Stop()

	_, stop := startTestTracer(t)
	defer stop()

	root, ctx := StartSpanFromContext(context.Background(), "root")
	child, _ := StartSpanFromContext(ctx, "child")
	root.Finish()
	child.Finish()

	rt.Stop()

	execTrace, err := gotraceui.Parse(buf, nil)
	if err != nil {
		t.Fatalf("parsing trace: %s", err)
	}

	type traceSpan struct {
		name   string
		parent string
		spanID uint64
	}

	spans := make(map[int]*traceSpan)
	for _, ev := range execTrace.Events {
		switch ev.Type {
		case gotraceui.EvUserTaskCreate:
			id := int(ev.Args[0])
			name := execTrace.Strings[ev.Args[2]]
			var parent string
			if p, ok := spans[int(ev.Args[1])]; ok {
				parent = p.name
			}
			spans[id] = &traceSpan{
				name:   name,
				parent: parent,
			}
		case gotraceui.EvUserLog:
			id := int(ev.Args[0])
			span, ok := spans[id]
			if !ok {
				continue
			}
			key := execTrace.Strings[ev.Args[1]]
			if key == "datadog.uint64_span_id" {
				span.spanID = binary.LittleEndian.Uint64([]byte(execTrace.Strings[ev.Args[3]]))
			}
		}
	}

	want := []traceSpan{
		{name: "root", spanID: root.Context().SpanID()},
		{name: "child", parent: "root", spanID: child.Context().SpanID()},
	}
	var got []traceSpan
	for _, v := range spans {
		got = append(got, *v)
	}

	assert.ElementsMatch(t, want, got)
}
