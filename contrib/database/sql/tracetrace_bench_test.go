// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// BenchmarkTryTrace measures traceParams.tryTrace, which is called once per
// query/exec/prepare/begin/ping on a traced connection. It goes through a
// real tracer.StartSpanFromContext call, like production code, so option
// values escape to the heap the same way they do in production instead of
// being optimized away by inlining (caller and tracer.Tag/WithStartSpanConfig
// live in different packages here, same as valkey's BenchmarkStartSpan).
func BenchmarkTryTrace(b *testing.B) {
	err := tracer.Start(tracer.WithTestDefaults(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	cfg := &config{
		serviceName:   "test-db",
		spanName:      "mysql.query",
		analyticsRate: math.NaN(),
		tags: map[string]interface{}{
			"custom.tag1": "value1",
			"custom.tag2": "value2",
		},
	}
	tp := &traceParams{
		driverName: "mysql",
		cfg:        cfg,
		spanCfg:    newSpanConfig(cfg, "mysql"),
	}
	ctx := context.Background()
	start := time.Now()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tp.tryTrace(ctx, QueryTypeQuery, "SELECT * FROM users WHERE id = ?", start, nil)
	}
}
