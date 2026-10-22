// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package internal

import "testing"

type snapshotTestTracer struct{ stops int }

func (*snapshotTestTracer) Flush()  {}
func (t *snapshotTestTracer) Stop() { t.stops++ }

func TestGlobalTracerSnapshotDoesNotReplaceLaterPublication(t *testing.T) {
	first, second := &snapshotTestTracer{}, &snapshotTestTracer{}
	SetGlobalTracer[tracerLike](first)
	snapshot := SnapshotGlobalTracer[tracerLike]()
	SetGlobalTracer[tracerLike](second)
	// Re-publishing the same instance must not revive the older snapshot.
	SetGlobalTracer[tracerLike](first)
	if snapshot.Replace(second) {
		t.Fatal("stale snapshot replaced a later publication")
	}
	fresh := SnapshotGlobalTracer[tracerLike]()
	stops := first.stops
	if !fresh.Replace(second) || GetGlobalTracer[tracerLike]() != second {
		t.Fatal("current snapshot failed to publish its replacement")
	}
	if first.stops != stops {
		t.Fatal("replacement closed a delegate before the caller could adopt it")
	}
}
