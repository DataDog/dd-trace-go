// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redigo

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// BenchmarkNewChildSpan measures newChildSpan, which is called once per
// Redis command issued through a traced connection. Every tag it sets is
// constant for the connection's lifetime, so it starts the span directly
// from a StartSpanConfig built once per connection instead of rebuilding a
// tracer.Tag closure per tag and re-setting three tags via SetTag on every
// call. It goes through a real tracer.StartSpanFromContext call, like
// production code, so option values escape to the heap the same way they do
// in production instead of being optimized away by inlining (caller and
// tracer.Tag/WithStartSpanConfig live in different packages here, same as
// valkey's BenchmarkStartSpan).
func BenchmarkNewChildSpan(b *testing.B) {
	err := tracer.Start(tracer.WithTestDefaults(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	cfg := new(dialConfig)
	defaults(cfg)
	p := &params{
		config:  cfg,
		network: "tcp",
		host:    "127.0.0.1",
		port:    "6379",
	}
	p.spanCfg = newSpanConfig(cfg, p.network, p.host, p.port)
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		span := newChildSpan(ctx, p)
		span.Finish()
	}
}
