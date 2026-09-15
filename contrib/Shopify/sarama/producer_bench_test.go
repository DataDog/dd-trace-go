// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"math"
	"testing"

	"github.com/Shopify/sarama"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// BenchmarkStartProducerSpan measures startProducerSpan, which merges
// static, build-once tags via WithStartSpanConfig and per-message tags via
// a single WithTags map instead of one tracer.Tag closure per tag. It goes
// through a real tracer.StartSpan call, like production code, so option
// values escape to the heap the same way they do in production instead of
// being optimized away by inlining (caller and tracer.Tag/WithStartSpanConfig
// live in different packages here, same as valkey's BenchmarkStartSpan).
func BenchmarkStartProducerSpan(b *testing.B) {
	err := tracer.Start(tracer.WithTestDefaults(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	cfg := new(config)
	defaults(cfg)
	cfg.analyticsRate = math.NaN()
	spanCfg := newProducerSpanConfig(cfg)
	msg := &sarama.ProducerMessage{Topic: "test-topic"}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		span := startProducerSpan(cfg, spanCfg, sarama.V0_11_0_0, msg)
		span.Finish()
	}
}
