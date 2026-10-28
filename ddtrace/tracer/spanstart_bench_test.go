// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	traceinternal "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer/internal"
)

type spanStartBenchKey int

type spanStartInput struct {
	name string
	opts []StartSpanOption
}

// BenchmarkBenchMCPSpanStart drives spanStart with a weighted mix modeled on the
// metrics-querier prod CPU profile (Live Debugger capture skipped; inputs are
// synthesized from source types and the profile).
//
// Input classes:
//   - 60% child spans started from a deep context chain (StartSpanFromContext
//     style: ChildOf + withContext). The chain alternates spanCtx and
//     context.WithValue nodes ~12 levels deep with a cancelCtx in the middle.
//     setLLMObsPropagatingTags walks the whole chain looking for an LLM span
//     that is never there, which is the ~20% (*spanCtx).Value/(*valueCtx).Value
//     recursion in prod. Children also hit setMetricInit (sampling priority)
//     and newSpanContext's trace.push.
//   - 25% tag-heavy child spans on the same deep chain with 10-14 string Tag
//     options. More than 8 tags pushes the span meta map past the small-map
//     size in setTags (growToTable), and the Tag closures fill cfg.Tags, which
//     matches the mapassign growth under Tag.func1/ApplyV1Options in prod. The
//     v1 shim (ApplyV1Options) is not in this repo, so its frames cannot appear.
//   - 15% root spans (no parent, background context, 3-4 tags): newTrace,
//     SetProfilerRootTags and markTopLevel.
//
// Parents are recreated on each pool regeneration so trace.spans does not grow
// without bound.
func BenchmarkBenchMCPSpanStart(b *testing.B) {
	const poolSize = 1000
	var shared traceinternal.SpanAttributes

	newParent := func() (*Span, context.Context) {
		root, _, _ := spanStart("http.request", &shared, false, ServiceName("metrics-querier"))
		root.context.setSamplingPriority(1, 0)
		ctx := context.Background()
		for d := 0; d < 24; d++ {
			if d == 12 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				b.Cleanup(cancel)
			}
			ctx = context.WithValue(ctx, spanStartBenchKey(d), d)
			ctx = &spanCtx{Context: ctx, span: root, snapshot: root.context}
		}
		return root, ctx
	}

	genPool := func() []spanStartInput {
		parent, deep := newParent()
		pool := make([]spanStartInput, poolSize)
		for i := range pool {
			switch r := i % 20; {
			case r < 12:
				pool[i] = spanStartInput{name: "query.execute", opts: []StartSpanOption{
					ChildOf(parent.context), withContext(deep),
					ResourceName(fmt.Sprintf("q%d", i%37)),
					Tag("org_id", "o"),
					Tag(ext.Component, "metrics"),
				}}
			case r < 17:
				opts := []StartSpanOption{ChildOf(parent.context), withContext(deep),
					ServiceName("metrics-querier"), SpanType("db"), ResourceName("fetch")}
				for k := 0; k < 10+i%5; k++ {
					opts = append(opts, Tag(fmt.Sprintf("tag.%d", k), "v"))
				}
				opts = append(opts, Measured())
				pool[i] = spanStartInput{name: "storage.fetch", opts: opts}
			default:
				pool[i] = spanStartInput{name: "grpc.server", opts: []StartSpanOption{
					ServiceName("metrics-querier"), ResourceName("/Query"),
					SpanType("rpc"), Tag("peer", "x"),
				}}
			}
		}
		return pool
	}

	pool := genPool()
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		if i == poolSize {
			b.StopTimer()
			pool = genPool()
			i = 0
			b.StartTimer()
		}
		spanStart(pool[i].name, &shared, false, pool[i].opts...)
		i++
	}
}
