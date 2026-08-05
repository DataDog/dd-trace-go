// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io"
	"slices"
	"strings"
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

func (b *blockingTransport) endpoint() string { return "http://localhost:9/v1.0/traces" }

var _ ddTransport = (*blockingTransport)(nil)

// TestQueueOverflowOnStalledAgent reproduces the APMS-20060 root cause: a slow
// or unresponsive agent saturates every one of the writer's
// concurrentConnectionLimit outgoing connections. traceWriter.flush()
// acquires that connection slot on the caller's goroutine (see writer.go),
// and the caller is always the tracer's single worker goroutine. Once every
// slot is taken, the next scheduled flush blocks the worker inside flush()
// itself, so it can no longer drain t.out — and once t.out fills past
// payloadQueueSize, pushChunk starts dropping chunks.
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
		defer close(release)

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

		// One more tick: the writer now tries to acquire a 101st connection
		// slot and blocks on it — the worker goroutine can no longer reach
		// its select loop, so it stops draining t.out entirely.
		trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("queue-overflow")}, willSend: true})
		synctest.Wait()
		flush(-1)
		synctest.Wait()

		// t.out is now undrained. Fill it to capacity...
		for range payloadQueueSize {
			trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("queue-overflow")}})
		}
		require.Len(t, trc.out, payloadQueueSize)

		// ...and this one is the drop this test reproduces.
		trc.pushChunk(&chunk{spans: []*Span{newBasicSpan("queue-overflow")}})

		var queueFullDrops int64
		for _, c := range tg.GetCallsByName("datadog.tracer.traces_dropped") {
			if slices.Contains(c.Tags(), "reason:queue_full") {
				queueFullDrops += c.IntVal()
			}
		}
		assert.Equal(t, int64(1), queueFullDrops, "exactly one trace should have been dropped for reason:queue_full")
	})
}
