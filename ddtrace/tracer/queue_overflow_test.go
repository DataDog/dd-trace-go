// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product contains software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"

	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
)

// blockingTransport is a ddTransport whose send blocks until release is
// closed. It stands in for a trace-agent that accepts the connection but
// never responds — the condition that saturates the writer's
// concurrentConnectionLimit outgoing connections in production (APMS-20060).
type blockingTransport struct {
	release chan struct{}
}

func (b *blockingTransport) send(p payload) (io.ReadCloser, error) {
	defer p.Close()
	if _, err := io.Copy(io.Discard, p); err != nil {
		return nil, err
	}
	<-b.release
	return io.NopCloser(strings.NewReader("OK")), nil
}

func (b *blockingTransport) sendStats(*pb.ClientStatsPayload, int) error { return nil }

func (b *blockingTransport) endpoint(float64) string { return "http://localhost:9/v1.0/traces" }

var _ ddTransport = (*blockingTransport)(nil)

// TestQueueOverflowOnStalledAgent is the fixed behavior for the APMS-20060
// root cause: a slow or unresponsive agent saturates every one of the
// writer's concurrentConnectionLimit outgoing connections. traceWriter.flush()
// tries its connection slot non-blockingly and returns without swapping the
// payload when none is free, so the tracer's single worker goroutine — the
// only flush() caller on the scheduled path — stays free to keep draining
// t.out. The traces instead accumulate in the payload buffer, nothing is
// dropped with reason:queue_full, and once the stall clears the next tick
// sends everything. (Before the fix, flush() blocked the worker on the
// connection slot, t.out filled past its capacity, and pushChunk dropped
// chunks with reason:queue_full.)
func TestQueueOverflowOnStalledAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		release := make(chan struct{})
		bt := &blockingTransport{release: release}

		trc, _, flush, stop, err := startTestTracer(t,
			withTransport(bt),
			withNoopInfoHTTPClient(),
			withStatsdClient(&tg),
		)
		require.NoError(t, err)
		defer stop()
		// Unblock every stalled send before stop() waits on the worker/writer,
		// or the cleanup itself deadlocks. Registered after defer stop() so it
		// runs first (LIFO).
		var releaseOnce sync.Once
		releaseStall := func() { releaseOnce.Do(func() { close(release) }) }
		defer releaseStall()

		// Saturate every outgoing connection slot: one chunk plus one flush
		// per slot. Waiting for the worker to drain the chunk (first Wait)
		// before ticking, and for the flush to fully land — spawn its send
		// goroutine and block inside it — before moving to the next (second
		// Wait), keeps slot accounting deterministic. Without the first Wait,
		// select in the worker's loop could pick the tick case while the
		// chunk is still sitting undrained in t.out, flushing an empty
		// payload and skipping a connection slot.
		for range concurrentConnectionLimit {
			trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("queue-overflow")}, willSend: true})
			synctest.Wait()
			flush(-1)
			synctest.Wait()
		}

		// Push more chunks than t.out can hold while every connection is
		// stalled. The worker must keep draining chunks, so no chunk may drop
		// with reason:queue_full.
		//
		// Batch the pushes, and drain t.out between batches. synctest
		// virtualizes time, not CPU: a tight push loop races the worker's
		// add(), which msgpack-encodes each chunk. When the loop outruns the
		// worker, t.out fills, and pushChunk drops chunks with
		// reason:queue_full. A batch never exceeds the queue capacity, so a
		// drained queue accepts each batch in full, and the choreography
		// stays deterministic.
		const overCapacity = 25
		queueSize := cap(trc.out)
		for remaining := queueSize + overCapacity; remaining > 0; {
			batch := min(queueSize, remaining)
			for range batch {
				trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("queue-overflow")}, willSend: true})
			}
			synctest.Wait()
			remaining -= batch
		}
		require.Zero(t, len(trc.out), "the worker must keep draining t.out while every connection is in flight")

		// A tick with all connections saturated: flush() defers — it must not
		// swap the payload, block the worker, or lose anything.
		flush(-1)
		synctest.Wait()

		var queueFullDrops, encodingDrops int64
		for _, c := range tg.GetCallsByName("datadog.tracer.traces_dropped") {
			if slices.Contains(c.Tags(), "reason:queue_full") {
				queueFullDrops += c.IntVal()
			}
			if slices.Contains(c.Tags(), "reason:encoding_error") {
				encodingDrops += c.IntVal()
			}
		}
		assert.Zero(t, queueFullDrops, "no trace should be dropped for reason:queue_full while the agent is stalled")
		assert.Zero(t, encodingDrops, "the payload buffer should absorb every drained trace")

		// The deferred traces are still sitting in the payload buffer, whole.
		aw := trc.traceWriter.(*agentTraceWriter)
		aw.mu.Lock()
		buffered := aw.payload.itemCount()
		aw.mu.Unlock()
		assert.Equal(t, queueSize+overCapacity, buffered,
			"the deferred flush must have left the traces in the payload buffer")

		// The stall clears: every send finishes and frees its slot...
		releaseStall()
		synctest.Wait()

		// ...and the next tick flushes everything that was absorbed.
		flush(-1)
		synctest.Wait()

		var flushTraces int64
		for _, c := range tg.GetCallsByName("datadog.tracer.flush_traces") {
			flushTraces += c.IntVal()
		}
		assert.Equal(t, int64(concurrentConnectionLimit+queueSize+overCapacity), flushTraces,
			"every trace pushed during and after the stall must eventually be sent")
	})
}

// TestFlushSendsOnSaturatedAgent pins the explicit Flush contract: Flush()
// must hand the buffered traces to a send even when every
// concurrentConnectionLimit outgoing connection is in flight. The scheduled
// flush defers under those conditions (see TestQueueOverflowOnStalledAgent),
// but an explicit flush has no next tick to defer to — its caller (a Lambda
// handler between invocations, an OTel ForceFlush at shutdown) may never run
// again — so it waits for a connection slot instead.
func TestFlushSendsOnSaturatedAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		release := make(chan struct{})
		bt := &blockingTransport{release: release}

		trc, _, flush, stop, err := startTestTracer(t,
			withTransport(bt),
			withNoopInfoHTTPClient(),
			withStatsdClient(&tg),
		)
		require.NoError(t, err)
		defer stop()
		var releaseOnce sync.Once
		releaseStall := func() { releaseOnce.Do(func() { close(release) }) }
		defer releaseStall()

		// Saturate every outgoing connection slot, same choreography as
		// TestQueueOverflowOnStalledAgent.
		for range concurrentConnectionLimit {
			trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("flush-saturated")}, willSend: true})
			synctest.Wait()
			flush(-1)
			synctest.Wait()
		}

		// Queue more traces while every connection is in flight: the worker
		// drains them into the payload buffer, which the stalled sends can't
		// pick up yet.
		const buffered = 3
		for range buffered {
			trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("flush-saturated")}, willSend: true})
		}
		synctest.Wait()

		// Flush() runs on its own goroutine: it parks the worker inside the
		// blocking flush variant, waiting for a connection slot, so the stall
		// can be cleared from here.
		flushed := make(chan struct{})
		go func() {
			defer close(flushed)
			trc.Flush()
		}()
		synctest.Wait()

		// The stall clears, and the parked flush proceeds through a freed slot.
		releaseStall()
		synctest.Wait()
		<-flushed

		var flushTraces, dropped int64
		for _, c := range tg.GetCallsByName("datadog.tracer.flush_traces") {
			flushTraces += c.IntVal()
		}
		for _, c := range tg.GetCallsByName("datadog.tracer.traces_dropped") {
			dropped += c.IntVal()
		}
		assert.Equal(t, int64(concurrentConnectionLimit+buffered), flushTraces,
			"Flush() must send every queued trace, including those buffered while all connections were in flight")
		assert.Zero(t, dropped, "nothing should be dropped by an explicit flush")
	})
}

// TestPayloadBoundWhileAgentStalled pins the payload bound: the deferred
// flush can hold the payload for as long as every connection is in flight,
// so add() must stop accepting traces once the payload holds
// payloadSizeLimit bytes. Without the bound, a sustained stall grows the
// payload without limit and later sends it as one request that exceeds the
// agent's payloadMaxLimit, which drops every buffered trace at once.
func TestPayloadBoundWhileAgentStalled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		release := make(chan struct{})
		bt := &blockingTransport{release: release}

		trc, _, flush, stop, err := startTestTracer(t,
			withTransport(bt),
			withNoopInfoHTTPClient(),
			withStatsdClient(&tg),
		)
		require.NoError(t, err)
		defer stop()
		var releaseOnce sync.Once
		releaseStall := func() { releaseOnce.Do(func() { close(release) }) }
		defer releaseStall()

		// Saturate every outgoing connection slot, same choreography as
		// TestQueueOverflowOnStalledAgent.
		for range concurrentConnectionLimit {
			trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("payload-bound")}, willSend: true})
			synctest.Wait()
			flush(-1)
			synctest.Wait()
		}

		// Push traces of roughly 1 MiB each until the payload crosses
		// payloadSizeLimit (4.75 MiB). The trace that crosses it stays: the
		// flush it triggers defers, and the bound only drops the traces that
		// arrive after it.
		big := strings.Repeat("a", 1<<20)
		bigTrace := func() *chunk {
			s := newBasicSpan("payload-bound")
			s.SetTag("payload", big)
			return &chunk{spans: []*Span{s}, willSend: true}
		}
		aw := trc.traceWriter.(*agentTraceWriter)
		accepted := 0
		for {
			aw.mu.Lock()
			size := aw.payload.size()
			aw.mu.Unlock()
			if size >= int(payloadSizeLimit) {
				break
			}
			trc.pushChunk(bigTrace())
			accepted++
			synctest.Wait()
		}

		// One more trace past the bound: dropped, and the payload keeps its
		// accepted traces.
		trc.pushChunk(bigTrace())
		synctest.Wait()

		var payloadFullDrops int64
		for _, c := range tg.GetCallsByName("datadog.tracer.traces_dropped") {
			if slices.Contains(c.Tags(), "reason:payload_full") {
				payloadFullDrops += c.IntVal()
			}
		}
		assert.Equal(t, int64(1), payloadFullDrops,
			"exactly one trace should be dropped for reason:payload_full once the payload holds payloadSizeLimit bytes")
		aw.mu.Lock()
		buffered := aw.payload.itemCount()
		aw.mu.Unlock()
		assert.Equal(t, accepted, buffered,
			"the dropped trace must not enlarge the buffered payload")

		// The stall clears without any tick in between: every send finished,
		// so a connection slot is free, but the payload still holds
		// payloadSizeLimit bytes. The next add must retry the deferred flush
		// instead of dropping: the retry acquires the freed slot, swaps the
		// full payload into a send, and accepts the trace.
		releaseStall()
		synctest.Wait()
		trc.pushChunk(bigTrace())
		synctest.Wait()

		payloadFullDrops = 0
		for _, c := range tg.GetCallsByName("datadog.tracer.traces_dropped") {
			if slices.Contains(c.Tags(), "reason:payload_full") {
				payloadFullDrops += c.IntVal()
			}
		}
		assert.Equal(t, int64(1), payloadFullDrops,
			"a freed connection must accept the trace instead of dropping it for reason:payload_full")
		aw.mu.Lock()
		buffered = aw.payload.itemCount()
		aw.mu.Unlock()
		assert.Equal(t, 1, buffered,
			"the retried flush must swap the full payload for a fresh one holding the new trace")
	})
}

// TestStopFlushesOnSaturatedAgent pins the shutdown half of the fix: Stop()
// must still send every queued trace when all concurrentConnectionLimit
// outgoing connections are saturated. The scheduled flush() defers under
// those conditions (see TestQueueOverflowOnStalledAgent), but shutdown has
// no next tick to retry on, so it flushes through the blocking variant and
// waits for a connection slot instead.
func TestStopFlushesOnSaturatedAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		release := make(chan struct{})
		bt := &blockingTransport{release: release}

		trc, _, flush, stop, err := startTestTracer(t,
			withTransport(bt),
			withNoopInfoHTTPClient(),
			withStatsdClient(&tg),
		)
		require.NoError(t, err)

		var releaseOnce sync.Once
		releaseStall := func() { releaseOnce.Do(func() { close(release) }) }
		// If an assertion fails before the stall clears, stop() blocks
		// forever inside flushBlocking, waiting for a connection slot.
		// Registered after defer stop() so the LIFO order releases the
		// stall before stop() runs.
		defer stop()
		defer releaseStall()

		// Saturate every outgoing connection slot, same choreography as
		// TestQueueOverflowOnStalledAgent.
		for range concurrentConnectionLimit {
			trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("stop-saturated")}, willSend: true})
			synctest.Wait()
			flush(-1)
			synctest.Wait()
		}

		// Queue more traces while every connection is in flight: the worker
		// drains them into the payload buffer, which the stalled sends can't
		// pick up yet.
		const buffered = 3
		for range buffered {
			trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("stop-saturated")}, willSend: true})
		}
		synctest.Wait()

		// Stop() runs on its own goroutine so the stall can be cleared from
		// here once its flush is parked waiting for a connection slot.
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			stop()
		}()
		releaseStall()
		synctest.Wait()
		<-stopped

		var flushTraces, dropped int64
		for _, c := range tg.GetCallsByName("datadog.tracer.flush_traces") {
			flushTraces += c.IntVal()
		}
		for _, c := range tg.GetCallsByName("datadog.tracer.traces_dropped") {
			dropped += c.IntVal()
		}
		assert.Equal(t, int64(concurrentConnectionLimit+buffered), flushTraces,
			"Stop() must send every queued trace, including those buffered while all connections were in flight")
		assert.Zero(t, dropped, "nothing should be dropped on shutdown")
	})
}
