// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mongo

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// BenchmarkMonitorStarted measures monitor.Started, which the mongo driver
// calls once per command via the event.CommandMonitor hook returned by
// NewMonitor. It goes through a real tracer.StartSpanFromContext call, like
// production code, so option values escape to the heap the same way they do
// in production instead of being optimized away by inlining (caller and
// tracer.Tag/WithStartSpanConfig live in different packages here, same as
// valkey's BenchmarkStartSpan). NewMonitor defaults maxQuerySize to -1 (query
// capture on, uncapped), so "default" explicitly disables it via
// WithMaxQuerySize(0) and "with_query_capture" uses NewMonitor's actual
// default to add the extra dynamic tag, matching the n=6 scenario cited in
// CONTRIBUTING.md's BenchmarkTagsVsWithTags table.
func BenchmarkMonitorStarted(b *testing.B) {
	err := tracer.Start(tracer.WithTestDefaults(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	raw, err := bson.Marshal(bson.D{{Key: "test-item", Value: "test-value"}})
	if err != nil {
		b.Fatal(err)
	}
	evt := &event.CommandStartedEvent{
		Command:      raw,
		DatabaseName: "test-database",
		CommandName:  "insert",
		RequestID:    1,
		ConnectionID: "localhost:27017[1]",
	}

	b.Run("default", func(b *testing.B) {
		cm := NewMonitor(WithMaxQuerySize(0))
		ctx := context.Background()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cm.Started(ctx, evt)
		}
	})

	b.Run("with_query_capture", func(b *testing.B) {
		cm := NewMonitor()
		ctx := context.Background()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			cm.Started(ctx, evt)
		}
	})
}
