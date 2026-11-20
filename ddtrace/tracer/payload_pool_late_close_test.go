// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
	"io"
	"net/http"
	"strings"
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

// v1PayloadOf unwraps a *safePayload and returns the *payloadV1 inside it. It
// accepts the request body that the transport saw, or the writer's current
// payload.
func v1PayloadOf(t *testing.T, x any) *payloadV1 {
	t.Helper()
	sp, ok := x.(*safePayload)
	require.True(t, ok, "expected *safePayload, got %T", x)
	p, ok := sp.p.(*payloadV1)
	require.True(t, ok, "expected *payloadV1, got %T", sp.p)
	return p
}

// TestPayloadV1PoolLateCloseAcrossSendRetries reproduces a payload pool handoff
// defect. The order it forces was a hypothesis until this test ran.
//
// sendAsync reuses one payloadV1 for every send attempt of a flush. The first
// attempt fails. Its body close is still pending when the retry succeeds. The
// close of the retry plus the deferred retirePayload complete the two-bit
// handoff, and the payload returns to the pool. A later flush takes the payload
// from the pool and clears its pool state. The late close of the failed attempt
// then lands on the new lifecycle and corrupts it.
//
// The test drives the order through channels, so the reproduction itself is
// deterministic. sync.Pool documents that Get may ignore Put, and the race
// detector makes Put drop items at random. The test therefore repeats the
// flush pair until the pool hands the payload of a failed attempt back to the
// writer, and only then forces the late close. Each assertion states an
// invariant that a correct implementation must keep. The test fails while the
// defect is present.
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

		// Each pass of this loop produces one flush pair. The first flush of
		// the pair sends a payload that fails once and then succeeds. The
		// failed attempt keeps its body open. The retry drains and closes its
		// body, so the deferred retirePayload returns the payload to the pool.
		// The second flush sends the writer's buffer with an open body, and
		// the writer takes its next payload from the pool. The loop repeats
		// the pair until that payload is the one the failed attempt used.
		var attempt1Body io.ReadCloser
		var payloadA *payloadV1
		for pass := 0; ; pass++ {
			require.LessOrEqual(t, pass, 100,
				"the pool never handed the payload of a failed attempt back to the writer")

			trip.failNext = true
			writer.add([]*Span{makeSpan(1)})
			writer.flush()
			writer.wg.Wait()
			last := len(trip.bodies)
			attempt1Body = trip.bodies[last-2]
			payloadA = v1PayloadOf(t, attempt1Body)
			require.Same(t, payloadA, v1PayloadOf(t, trip.bodies[last-1]),
				"the retry must reuse the payload of the failed attempt")

			writer.add([]*Span{makeSpan(1)})
			writer.flush()
			writer.wg.Wait()
			if v1PayloadOf(t, writer.payload) == payloadA {
				break
			}
			// The pool did not hand the payload back. sync.Pool may ignore a
			// Put, so the test repeats the flush pair.
		}

		// The late close of the failed attempt lands now. A real transport may
		// close this body at any time after the failed round trip, from a
		// separate goroutine. Payload A sits in the writer's buffer. No send
		// of this lifecycle has started.
		fireLateClose := make(chan struct{})
		lateCloseDone := make(chan struct{})
		go func() {
			<-fireLateClose
			_ = attempt1Body.Close()
			close(lateCloseDone)
		}()
		close(fireLateClose)
		<-lateCloseDone

		// Invariant 1: the delayed close of a failed attempt must not touch
		// the payload's new lifecycle. A fresh lifecycle must have a clean
		// pool state, because no HTTP activity of its own has happened yet.
		assert.Zero(t, payloadA.poolState.Load(),
			"the late Close of the failed attempt set BodyClosed on the recycled payload's new lifecycle (poolState=%03b)",
			payloadA.poolState.Load())

		// The next flush sends A in its new lifecycle. The send succeeds and
		// keeps its body open. The deferred retirePayload hands off FlushDone.
		// The stale BodyClosed from the failed attempt satisfies the
		// counterpart bit, so A returns to the pool although the body of this
		// lifecycle is open.
		writer.add([]*Span{makeSpan(1)})
		writer.flush()
		writer.wg.Wait()
		nextFlushBody := trip.bodies[len(trip.bodies)-1]
		require.Same(t, payloadA, v1PayloadOf(t, nextFlushBody),
			"the next flush must send the recycled payload")

		// One more flush takes A back from the pool and makes it the writer's
		// buffer again.
		writer.add([]*Span{makeSpan(1)})
		writer.flush()
		writer.wg.Wait()

		// Invariant 2: the pool must not hand a payload to a new lifecycle
		// while the request body of an earlier lifecycle still references it.
		// A transport may read or close that body at any time.
		assert.NotSame(t, payloadA, v1PayloadOf(t, writer.payload),
			"the pool handed the payload to a new lifecycle while the request body of the previous lifecycle was still open")

		// Invariant 3: the close of the previous lifecycle's own body must not
		// corrupt the lifecycle that the writer buffers now. The defect
		// repeats on every later cycle when it is present.
		bufferedPayload := v1PayloadOf(t, writer.payload)
		_ = nextFlushBody.Close()
		assert.Zero(t, bufferedPayload.poolState.Load()&pv1StateBodyClosed,
			"the Close of the previous lifecycle's body set BodyClosed on the next lifecycle (poolState=%03b)",
			bufferedPayload.poolState.Load())

		// Close every body this tripper saw, as a well behaved transport would.
		// handoff is idempotent, so bodies closed earlier stay closed.
		for _, b := range trip.bodies {
			_ = b.Close()
		}
	})
}
