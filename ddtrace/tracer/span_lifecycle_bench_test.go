// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// BenchmarkSpanLifecycle measures root span lifecycle throughput where all 6 hot-path
// optimizations converge: setTags batch (#4425), serviceEnvKey (#4423), DecisionMaker
// lookup (#4424), _dd.p.dm pre-compute (#4576), git metadata arrays (#4472), and
// traceID hex caching (#4481).
//
// Child spans skip 4 of 6 optimizations (sampling priority inherited from parent),
// so all sub-benchmarks focus on root spans to maximize signal.
func BenchmarkSpanLifecycle(b *testing.B) {
	b.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/DataDog/dd-trace-go")
	b.Setenv("DD_GIT_COMMIT_SHA", "abc123def456789abc123def456789abc123def4")

	tracer, _, _, stop, err := startTestTracer(b,
		WithLogger(log.DiscardLogger{}),
		WithService("bench-svc"),
		WithEnv("bench"),
		WithGlobalTag("team", "apm-ecosystems"),
		WithGlobalTag("version", "1.0.0"),
	)
	assert.NoError(b, err)
	defer stop()

	// Don't use b.Loop() here because it'll cause measurement artifacts
	// with the tracer's internal buffering (same as BenchmarkTracerAddSpans).

	// Minimal: root span with typical HTTP tags.
	// Exercises all 6 optimizations on the shortest path.
	b.Run("Minimal", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N { //nolint:modernize
			span := tracer.StartSpan("http.request",
				ServiceName("web-server"),
				ResourceName("GET /api/users"),
				Tag("http.method", "GET"),
				Tag("http.url", "/api/users"),
				Tag("http.status_code", "200"),
			)
			span.Finish()
		}
	})

	// TagHeavy: root span with many tags to amplify setTags/setTagLocked (#4425).
	// 10 Tag() options + 2 global tags = 12 tags through the batch path.
	b.Run("TagHeavy", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N { //nolint:modernize
			span := tracer.StartSpan("http.request",
				ServiceName("web-server"),
				ResourceName("GET /api/users"),
				Tag("http.method", "GET"),
				Tag("http.url", "/api/users"),
				Tag("http.status_code", "200"),
				Tag("http.host", "api.example.com"),
				Tag("http.useragent", "bench/1.0"),
				Tag("network.client.ip", "10.0.0.1"),
				Tag("usr.id", "user-123"),
				Tag("component", "net/http"),
				Tag("span.kind", "server"),
				Tag("peer.service", "frontend"),
			)
			span.Finish()
		}
	})

	// MultiService: root spans with varying service/env to exercise serviceEnvKey
	// map lookups (#4423) with different keys each iteration.
	b.Run("MultiService", func(b *testing.B) {
		services := [4]string{"web-server", "api-gateway", "auth-service", "user-service"}
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N { //nolint:modernize
			span := tracer.StartSpan("http.request",
				ServiceName(services[i%len(services)]),
				ResourceName("GET /"),
				Tag("http.method", "GET"),
			)
			span.Finish()
		}
	})
}
