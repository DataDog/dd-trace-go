// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/riverqueue/river/rivertype"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func BenchmarkInsertMany(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()

	for _, size := range []int{1, 100} {
		for _, bc := range []struct {
			name string
			opts []Option
		}{
			{name: "propagation", opts: nil},
			{name: "no-propagation", opts: []Option{WithDistributedTracing(false)}},
		} {
			for _, md := range []struct {
				name     string
				metadata string
			}{
				{name: "empty-metadata", metadata: `{}`},
				{name: "user-metadata", metadata: `{"customer":"acme","request_id":"7c0f3a52","tags":["a","b"]}`},
			} {
				b.Run(fmt.Sprintf("jobs=%d/%s/%s", size, bc.name, md.name), func(b *testing.B) {
					m := NewMiddleware(bc.opts...)
					params := make([]*rivertype.JobInsertParams, size)
					results := make([]*rivertype.JobInsertResult, size)
					root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
					defer root.Finish()
					doInner := func(context.Context) ([]*rivertype.JobInsertResult, error) { return results, nil }
					b.ReportAllocs()
					for b.Loop() {
						for i := range params {
							params[i] = &rivertype.JobInsertParams{Kind: "sort", Queue: "default", Metadata: []byte(md.metadata)}
						}
						if _, err := m.InsertMany(ctx, params, doInner); err != nil {
							b.Fatal(err)
						}
						mt.Reset()
					}
				})
			}
		}
	}
}

func BenchmarkWork(b *testing.B) {
	mt := mocktracer.Start()
	defer mt.Stop()

	insert := tracer.StartSpan("insert")
	params := []*rivertype.JobInsertParams{{Metadata: []byte(`{}`)}}
	if err := injectIntoParams(insert, params); err != nil {
		b.Fatal(err)
	}
	insert.Finish()

	for _, bc := range []struct {
		name     string
		metadata []byte
	}{
		{name: "propagated", metadata: params[0].Metadata},
		{name: "not-propagated", metadata: []byte(`{"customer":"acme"}`)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			m := NewMiddleware()
			job := &rivertype.JobRow{ID: 1, Attempt: 1, Kind: "sort", Queue: "default", Metadata: bc.metadata}
			doInner := func(context.Context) error { return nil }
			b.ReportAllocs()
			for b.Loop() {
				if err := m.Work(context.Background(), job, doInner); err != nil {
					b.Fatal(err)
				}
				mt.Reset()
			}
		})
	}
}

func BenchmarkWithTraceContext(b *testing.B) {
	traceContext := []byte(`{"traceparent":"00-6aead61400000000a1b2c3d4e5f60718-1c175f550bf7502f-01","x-datadog-parent-id":"2022781018465963055","x-datadog-sampling-priority":"1","x-datadog-trace-id":"11651371389484959512"}`)
	for _, bc := range []struct {
		name     string
		metadata string
	}{
		{name: "empty", metadata: `{}`},
		{name: "small", metadata: `{"customer":"acme","request_id":"7c0f3a52","tags":["a","b"]}`},
		{name: "1KiB", metadata: `{"customer":"acme","payload":"` + strings.Repeat("x", 1000) + `"}`},
	} {
		b.Run(bc.name, func(b *testing.B) {
			metadata := []byte(bc.metadata)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := withTraceContext(metadata, traceContext); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
