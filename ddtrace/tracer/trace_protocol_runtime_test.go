// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isV04WireByte reports whether b is the first byte of a msgpack array — the
// shape of a v0.4 trace payload.
func isV04WireByte(b byte) bool {
	return b == msgpackArray16 || b == msgpackArray32 || b&0xf0 == msgpackArrayFix
}

// TestEmptyPayloadRotatesOnProtocolDowngrade pins that an idle writer does not
// hold a stale v1 payload after the agent withdraws /v1.0/traces. Neither
// add() nor flush() otherwise re-reads the effective protocol once a payload
// exists — so without rotateStalePayload, a writer that saw no traffic across
// the downgrade keeps its v1 payload indefinitely, and the first trace to
// arrive afterwards is pushed into it and POSTed to /v1.0/traces, where it is
// rejected.
func TestEmptyPayloadRotatesOnProtocolDowngrade(t *testing.T) {
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSendRetries(0))
	defer stopTracerTest(tr)

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: agent must resolve to v1")

	w := newAgentTraceWriter(tr.config, newPrioritySampler(), tr.statsd)
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "payload created while v1 was in effect must be v1")

	// Drive the downgrade through the real /info path, not a raw agent.store,
	// so the streak/hysteresis logic in refreshAgentFeatures is exercised too.
	agent.SetInfo(`{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true}`)
	tr.refreshAgentFeatures()
	require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "sanity check: the poll must downgrade")

	// The writer was idle across the downgrade and a trace arrives before any
	// scheduled flush: add() itself, not just flush(), must rotate the stale
	// v1 payload so this trace is not pushed into it.
	agent.Reset()
	w.add([]*Span{makeSpan(1)})
	require.Equal(t, traceProtocolV04, w.payload.protocol(), "a trace accepted after a protocol downgrade must not land in the stale payload")

	w.flush()
	w.wg.Wait()
	assert.Equal(t, []string{tracesAPIPath}, agent.Requests(), "the post-downgrade trace must land on /v0.4/traces")
}

// TestConcurrentProtocolChangeDuringFlush runs a flush loop and an
// agent-info-refresh loop concurrently and asserts that every observed
// request's wire format matches its intake path. Run with -race: making the
// protocol dynamic is only safe because the payload carries its own protocol,
// so there is no mutable shared "current protocol" for the two loops to race
// on.
func TestConcurrentProtocolChangeDuringFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent protocol-switch test in short mode")
	}
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent)
	defer stopTracerTest(tr)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			s := tr.StartSpan("op")
			s.Finish()
			tr.Flush()
		}
	})

	wg.Go(func() {
		v1 := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			if v1 {
				agent.SetInfo(`{"endpoints":["/v0.4/traces","/v1.0/traces","/v0.6/stats"],"client_drop_p0s":true}`)
			} else {
				agent.SetInfo(`{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true}`)
			}
			v1 = !v1
			tr.refreshAgentFeatures()
		}
	})

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	tr.Flush()

	requests := agent.Requests()
	firstBytes := agent.RequestFirstBytes()
	require.Equal(t, len(requests), len(firstBytes))
	for i, path := range requests {
		switch path {
		case tracesAPIPathV1:
			assert.True(t, isV1WireByte(firstBytes[i]), "request %d: %s must carry a msgpack map body, got 0x%02x", i, path, firstBytes[i])
		case tracesAPIPath:
			assert.True(t, isV04WireByte(firstBytes[i]), "request %d: %s must carry a msgpack array body, got 0x%02x", i, path, firstBytes[i])
		default:
			t.Fatalf("unexpected request path %q", path)
		}
	}
}
