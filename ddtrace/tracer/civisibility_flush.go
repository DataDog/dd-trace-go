// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

// installCIVisibilityFlushHandler runs before the new tracer is published, so
// no caller can send a flush request while its handler is being installed. The
// worker's scheduled flush does not access this handler. Application tracers
// retain their default flush semantics.
func installCIVisibilityFlushHandler(globalTracer Tracer) {
	switch t := globalTracer.(type) {
	case *tracer:
		t.flushHandler = t.ciVisibilityFlushHandler
	case *ciVisibilityTracerRouter:
		installCIVisibilityFlushHandler(t.ciVisibilityTracer())
	}
}

// ciVisibilityFlushHandler is called only by the tracer worker. Draining its
// input before acknowledging Flush lets deferred CI events apply backpressure
// between batches, instead of overflowing the nonblocking shared span queue.
func (t *tracer) ciVisibilityFlushHandler(done chan<- struct{}) {
	// This worker is the sole consumer. A snapshot covers everything accepted
	// before this request without letting continuous producers starve Flush.
	for range len(t.out) {
		t.processOutChunk(<-t.out)
	}
	t.defaultFlushHandler(done)
}
