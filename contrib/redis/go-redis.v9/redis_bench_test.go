// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/redis/go-redis/v9"
)

// BenchmarkProcessHook measures the span-starting work inside the
// ProcessHook closure returned by datadogHook.ProcessHook, which runs once
// per Redis command issued through a traced client. It merges static,
// build-once tags via WithStartSpanConfig and per-call tags via a single
// WithTags map instead of rebuilding ServiceNameWithSource and
// re-appending the additionalTags slice on every call. It goes through a
// real tracer.StartSpanFromContext call, like production code, so option
// values escape to the heap the same way they do in production instead of
// being optimized away by inlining (caller and tracer.Tag/
// WithStartSpanConfig live in different packages here, same as valkey's
// BenchmarkStartSpan).
func BenchmarkProcessHook(b *testing.B) {
	err := tracer.Start(tracer.WithTestDefaults(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	cfg := new(clientConfig)
	defaults(cfg)
	additionalTags := []tracer.StartSpanOption{
		tracer.Tag("target.host", "127.0.0.1"),
		tracer.Tag("target.port", "6379"),
		tracer.Tag("db.redis.database_index", "0"),
	}
	hookParams := &params{config: cfg}
	hookParams.spanCfg = newSpanConfig(cfg, additionalTags)
	ddh := &datadogHook{params: hookParams}
	hook := ddh.ProcessHook(func(ctx context.Context, cmd redis.Cmder) error { return nil })
	ctx := context.Background()
	cmd := redis.NewCmd(ctx, "SET", "test_key", "test_value")

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = hook(ctx, cmd)
	}
}
