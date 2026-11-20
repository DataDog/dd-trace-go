// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lateCloseTripper stands in for the net/http transport that
// (*httpTransport).send relies on. The net/http contract permits a RoundTripper
// to close the request body in a separate goroutine, after RoundTrip returns
// (see the RoundTripper documentation in GOROOT src/net/http/client.go). A
// trace send that failNext announces fails and keeps its body open. The test
// closes that body later, at the moment that produces the worst case order.
type lateCloseTripper struct {
	// failNext makes the next trace send fail and keep its body open.
	failNext bool
	// closeNext makes the next trace send close its body before it returns.
	// The retry of a failed attempt uses this behavior, so the retry completes
	// the payload handoff of that lifecycle.
	closeNext bool
	// calls counts the trace sends this tripper served.
	calls int
	// bodies holds the request body of every trace send, in send order.
	bodies []io.ReadCloser
}

func (rt *lateCloseTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != tracesAPIPathV1 {
		// Not a trace send. Only the startup /info probe shares the HTTP client
		// that WithHTTPClient installs. Answer it like withNoopInfoHTTPClient
		// does: drain and close any body, then report 404 with an empty body.
		if req.Body != nil {
			_, _ = io.Copy(io.Discard, req.Body)
			_ = req.Body.Close()
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	rt.calls++
	rt.bodies = append(rt.bodies, req.Body)
	if rt.failNext {
		rt.failNext = false
		rt.closeNext = true
		// A real transport closes this body from its write goroutine, which
		// can run long after RoundTrip returns. This tripper leaves the body
		// open so that the test controls the close.
		return nil, errors.New("simulated round trip failure")
	}
	_, _ = io.Copy(io.Discard, req.Body)
	if rt.closeNext {
		rt.closeNext = false
		_ = req.Body.Close()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"rate_by_service":{}}`)),
	}, nil
}

// epipeTripper stands in for a net/http transport whose connection was torn
// down by the peer. The first trace send fails with EPIPE and keeps its body
// open. The replay that doWithStaleConnRetry issues through req.GetBody
// succeeds and closes its body.
type epipeTripper struct {
	// calls counts the trace sends this tripper served.
	calls int
	// bodies holds the request body of every trace send, in send order.
	bodies []io.ReadCloser
}

func (rt *epipeTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != tracesAPIPathV1 {
		// Not a trace send. Only the startup /info probe shares the HTTP client
		// that WithHTTPClient installs. Answer it like withNoopInfoHTTPClient
		// does: drain and close any body, then report 404 with an empty body.
		if req.Body != nil {
			_, _ = io.Copy(io.Discard, req.Body)
			_ = req.Body.Close()
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}
	rt.calls++
	rt.bodies = append(rt.bodies, req.Body)
	if rt.calls == 1 {
		// A real transport closes this body from its write goroutine, which
		// can run long after RoundTrip returns. This tripper leaves the body
		// open so that the test controls the close.
		return nil, fmt.Errorf("write: %w", syscall.EPIPE)
	}
	_, _ = io.Copy(io.Discard, req.Body)
	_ = req.Body.Close()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"rate_by_service":{}}`)),
	}, nil
}

// v1PayloadOf unwraps the per-request body and the safePayload wrapper, and
// returns the *payloadV1 inside. It accepts a request body that the transport
// saw, or the writer's current payload.
func v1PayloadOf(t *testing.T, x any) *payloadV1 {
	t.Helper()
	if b, ok := x.(*pv1Body); ok {
		x = b.p
	}
	sp, ok := x.(*safePayload)
	require.True(t, ok, "expected *safePayload, got %T", x)
	p, ok := sp.p.(*payloadV1)
	require.True(t, ok, "expected *payloadV1, got %T", sp.p)
	return p
}

// heldPayloadPasses runs one flush per pass and checks that the pool never
// hands payloadA to a new lifecycle. Each pass sends one payload and leaves
// its request body open, so the pool can only hand payloadA back through a
// defect: a correct handoff holds the payload while any body of its lifecycle
// is open. sync.Pool documents that Get may ignore Put, and the race detector
// makes Put drop items at random. The bound of 100 passes therefore gives a
// defect a visible window in every run, and a correct implementation passes
// every pass deterministically.
func heldPayloadPasses(t *testing.T, writer *agentTraceWriter, payloadA *payloadV1, what string) {
	t.Helper()
	for pass := range 100 {
		writer.add([]*Span{makeSpan(1)})
		writer.flush()
		writer.wg.Wait()
		assert.NotSame(t, payloadA, v1PayloadOf(t, writer.payload),
			"pass %d: the pool handed the payload to a new lifecycle while %s was still open", pass, what)
		if t.Failed() {
			return
		}
	}
}

// TestPayloadV1PoolLateCloseAcrossSendRetries reproduces a payload pool handoff
// defect. The order it forced was a hypothesis until the first version of this
// test ran.
//
// sendAsync reuses one payloadV1 for every send attempt of a flush. The first
// attempt fails and keeps its body open. The retry succeeds and closes its
// body. The two-bit handoff could not tell which request a body close belonged
// to, so the deferred retirePayload returned the payload to the pool although
// the body of the failed attempt was still open. A new lifecycle then read the
// same payload.
//
// The test drives the order through the writer and a RoundTripper that
// follows the net/http contract, so the reproduction needs no sleeps. The
// payload pointer is the oracle. The test repeats flushes to give the pool a
// chance to hand the payload back, and asserts on the payload identity in
// every pass. The test fails while the defect is present.
func TestPayloadV1PoolLateCloseAcrossSendRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		trip := &lateCloseTripper{}
		cfg, err := newTestConfig(
			// The public option path: a user supplied HTTP client whose
			// RoundTripper follows the net/http contract.
			WithHTTPClient(&http.Client{Transport: trip}),
			WithSendRetries(1),
			WithRetryInterval(1),
			func(c *config) {
				c.internalConfig.SetTraceProtocol(traceProtocolV1, internalconfig.OriginCode)
			},
		)
		require.NoError(t, err)

		setTraceProtocolStateForTest(cfg, protoV1)
		require.Equal(t, traceProtocolV1, cfg.effectiveTraceProtocol())
		require.IsType(t, &httpTransport{}, cfg.ddTransport)

		writer := newAgentTraceWriter(cfg, newPrioritySampler(), &tg)
		require.Equal(t, traceProtocolV1, writer.payload.protocol(),
			"the writer must encode with protocol v1")

		// One flush with a failed first attempt. The failed attempt keeps its
		// body open. The retry drains and closes its body, so only the body of
		// the failed attempt keeps the payload out of the pool.
		trip.failNext = true
		writer.add([]*Span{makeSpan(1)})
		writer.flush()
		writer.wg.Wait()
		attempt1Body := trip.bodies[len(trip.bodies)-2]
		payloadA := v1PayloadOf(t, attempt1Body)
		require.Same(t, payloadA, v1PayloadOf(t, trip.bodies[len(trip.bodies)-1]),
			"the retry must reuse the payload of the failed attempt")

		// Invariant 1: the pool must not hand a payload to a new lifecycle
		// while the request body of a failed attempt still references it. A
		// transport may read or close that body at any time.
		heldPayloadPasses(t, writer, payloadA, "the request body of the failed attempt")

		// The late close of the failed attempt lands now. A real transport may
		// close this body at any time after the failed round trip, from a
		// separate goroutine. Payload A sits in the pool handoff. No send of a
		// new lifecycle has started.
		fireLateClose := make(chan struct{})
		lateCloseDone := make(chan struct{})
		go func() {
			<-fireLateClose
			_ = attempt1Body.Close()
			close(lateCloseDone)
		}()
		close(fireLateClose)
		<-lateCloseDone

		// Invariant 2: after every request body of the lifecycle closed, the
		// handoff must return the payload to the pool. The decision of the
		// handoff is the observable: sync.Pool may drop a Put under the race
		// detector, so a retrieval through the pool is not reliable.
		require.True(t, payloadA.handoff.put.Load(),
			"the handoff did not return the payload after every request body closed")

		// The pool may ignore the Put, so the reuse below is best effort.
		// When the pool hands payloadA back to the writer, the test drives the
		// new lifecycle and checks its isolation from the old one.
		var reused bool
		for range 100 {
			writer.add([]*Span{makeSpan(1)})
			writer.flush()
			writer.wg.Wait()
			if v1PayloadOf(t, writer.payload) == payloadA {
				reused = true
				break
			}
		}
		if !reused {
			t.Log("the pool ignored the Put of the payload; skipping the new lifecycle checks")
		} else {
			// Invariant 3: the closes of the finished lifecycle must not leak
			// into the new one. Payload A now holds the writer's buffer. A
			// well behaved transport closes each body once. These repeated
			// closes prove that the handoff of the old lifecycle no longer
			// counts.
			for _, b := range trip.bodies {
				_ = b.Close()
			}

			// The next flush sends A in its new lifecycle. The send succeeds
			// and keeps its body open. The deferred retirePayload marks the
			// flush side done while that body stays open.
			writer.add([]*Span{makeSpan(1)})
			writer.flush()
			writer.wg.Wait()
			nextFlushBody := trip.bodies[len(trip.bodies)-1]
			require.Same(t, payloadA, v1PayloadOf(t, nextFlushBody),
				"the next flush must send the recycled payload")

			// Invariant 4: the pool must not hand payloadA to a new lifecycle
			// while the request body of its own send is open.
			heldPayloadPasses(t, writer, payloadA, "the request body of the current send")
		}

		// Close every body this tripper saw, as a well behaved transport would.
		// The handoff of a body counts once, so bodies closed earlier stay
		// closed.
		for _, b := range trip.bodies {
			_ = b.Close()
		}
	})
}

// TestPayloadV1PoolLateCloseAcrossStaleConnReplays covers the second path that
// issues several request bodies for one payloadV1 lifecycle: doWithStaleConnRetry
// replays the request through req.GetBody after a transient connection error.
// The first request fails with EPIPE and keeps its body open. The replay
// succeeds and closes its body. The pool must hold the payload until the
// first request's body closes, and must hand it back afterwards.
func TestPayloadV1PoolLateCloseAcrossStaleConnReplays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		trip := &epipeTripper{}
		cfg, err := newTestConfig(
			// The public option path: a user supplied HTTP client whose
			// RoundTripper follows the net/http contract.
			WithHTTPClient(&http.Client{Transport: trip}),
			func(c *config) {
				c.internalConfig.SetTraceProtocol(traceProtocolV1, internalconfig.OriginCode)
			},
		)
		require.NoError(t, err)

		setTraceProtocolStateForTest(cfg, protoV1)
		require.Equal(t, traceProtocolV1, cfg.effectiveTraceProtocol())
		require.IsType(t, &httpTransport{}, cfg.ddTransport)

		writer := newAgentTraceWriter(cfg, newPrioritySampler(), &tg)
		require.Equal(t, traceProtocolV1, writer.payload.protocol(),
			"the writer must encode with protocol v1")

		// One flush. The first request fails with EPIPE, and the replay that
		// doWithStaleConnRetry issues succeeds.
		writer.add([]*Span{makeSpan(1)})
		writer.flush()
		writer.wg.Wait()
		require.Len(t, trip.bodies, 2,
			"the stale connection retry must replay the request exactly once")
		require.NotSame(t, trip.bodies[0], trip.bodies[1],
			"every request must get its own body")
		firstBody := trip.bodies[0]
		payloadA := v1PayloadOf(t, firstBody)
		require.Same(t, payloadA, v1PayloadOf(t, trip.bodies[1]),
			"the replay must read the payload of the failed request")

		// Invariant 1: the pool must not hand a payload to a new lifecycle
		// while the request body of the failed request still references it.
		heldPayloadPasses(t, writer, payloadA, "the request body of the failed request")

		// The late close of the failed request lands now. A real transport may
		// close this body at any time after the failed round trip, from a
		// separate goroutine.
		_ = firstBody.Close()

		// Invariant 2: after every request body of the lifecycle closed, the
		// handoff must return the payload to the pool. The decision of the
		// handoff is the observable: sync.Pool may drop a Put under the race
		// detector, so a retrieval through the pool is not reliable.
		require.True(t, payloadA.handoff.put.Load(),
			"the handoff did not return the payload after every request body closed")

		// Close every body this tripper saw, as a well behaved transport would.
		for _, b := range trip.bodies {
			_ = b.Close()
		}
	})
}
