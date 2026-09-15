// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package rueidis

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// BenchmarkStartSpan measures client.startSpan, which merges static,
// build-once tags via WithStartSpanConfig and per-call tags via a single
// WithTags map instead of one tracer.Tag closure per tag. It goes through a
// real tracer.StartSpanFromContext call, like production code, so option
// values escape to the heap the same way they do in production instead of
// being optimized away by inlining (caller and tracer.Tag/WithStartSpanConfig
// live in different packages here, same as valkey's BenchmarkStartSpan).
func BenchmarkStartSpan(b *testing.B) {
	err := tracer.Start(tracer.WithTestDefaults(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	cfg := defaultConfig()
	cfg.rawCommand = true
	c := &client{
		cfg:     cfg,
		host:    "127.0.0.1",
		port:    "6379",
		dbIndex: "0",
		user:    "default",
	}
	c.spanCfg = newSpanConfig(cfg, c.host, c.port, c.dbIndex, c.user)
	cmd := command{statement: "SET", raw: "SET test_key test_value"}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		span, _ := c.startSpan(context.Background(), cmd)
		span.Finish()
	}
}
