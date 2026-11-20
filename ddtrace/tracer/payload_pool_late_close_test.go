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

type lateCloseTripper struct {
	failNext  bool
	closeNext bool
	calls     int
	bodies    []io.ReadCloser
}

func (rt *lateCloseTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != tracesAPIPathV1 {
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

type epipeTripper struct {
	calls  int
	bodies []io.ReadCloser
}

func (rt *epipeTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != tracesAPIPathV1 {
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
		return nil, fmt.Errorf("write: %w", syscall.EPIPE)
	}
	_, _ = io.Copy(io.Discard, req.Body)
	_ = req.Body.Close()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"rate_by_service":{}}`)),
	}, nil
}

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

func TestPayloadV1PoolLateCloseAcrossSendRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		trip := &lateCloseTripper{}
		cfg, err := newTestConfig(
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

		trip.failNext = true
		writer.add([]*Span{makeSpan(1)})
		writer.flush()
		writer.wg.Wait()
		attempt1Body := trip.bodies[len(trip.bodies)-2]
		payloadA := v1PayloadOf(t, attempt1Body)
		require.Same(t, payloadA, v1PayloadOf(t, trip.bodies[len(trip.bodies)-1]),
			"the retry must reuse the payload of the failed attempt")

		heldPayloadPasses(t, writer, payloadA, "the request body of the failed attempt")

		fireLateClose := make(chan struct{})
		lateCloseDone := make(chan struct{})
		go func() {
			<-fireLateClose
			_ = attempt1Body.Close()
			close(lateCloseDone)
		}()
		close(fireLateClose)
		<-lateCloseDone

		require.True(t, payloadA.handoff.put.Load(),
			"the handoff did not return the payload after every request body closed")

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
			for _, b := range trip.bodies {
				_ = b.Close()
			}

			writer.add([]*Span{makeSpan(1)})
			writer.flush()
			writer.wg.Wait()
			nextFlushBody := trip.bodies[len(trip.bodies)-1]
			require.Same(t, payloadA, v1PayloadOf(t, nextFlushBody),
				"the next flush must send the recycled payload")

			heldPayloadPasses(t, writer, payloadA, "the request body of the current send")
		}

		for _, b := range trip.bodies {
			_ = b.Close()
		}
	})
}

func TestPayloadV1PoolLateCloseAcrossStaleConnReplays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var tg statsdtest.TestStatsdClient
		trip := &epipeTripper{}
		cfg, err := newTestConfig(
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

		heldPayloadPasses(t, writer, payloadA, "the request body of the failed request")

		_ = firstBody.Close()

		require.True(t, payloadA.handoff.put.Load(),
			"the handoff did not return the payload after every request body closed")

		for _, b := range trip.bodies {
			_ = b.Close()
		}
	})
}
