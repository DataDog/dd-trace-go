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

// TestEmptyPayloadStaysStaleUntilNextFlushAfterProtocolDowngrade pins the
// deliberate trade-off from reverting #5167's add()-side protocol
// re-evaluation (issue #5258 diagnostic): add() no longer calls
// rotateStalePayload, so an idle writer's stale v1 payload is not corrected
// the moment a trace arrives after the agent withdraws /v1.0/traces — it
// keeps absorbing traces under its original protocol until the next flush()
// call, which still unconditionally rebuilds h.payload against the current
// effective protocol regardless of what the old payload's protocol was.
// Before this revert, add() itself rotated a stale *empty* payload for free;
// see TestNonEmptyPayloadKeepsAbsorbingTracesUntilNextFlushAfterProtocolDowngrade
// for the non-empty case, which used to be sealed and sent immediately
// instead.
func TestEmptyPayloadStaysStaleUntilNextFlushAfterProtocolDowngrade(t *testing.T) {
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSendRetries(0))
	defer stopTracerTest(tr)

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: agent must resolve to v1")

	w := newAgentTraceWriter(tr.config, newPrioritySampler(), tr.statsd)
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "payload created while v1 was in effect must be v1")

	// Drive the downgrade through the real /info path, not a raw config
	// mutation, so refreshAgentFeatures's real state transition is exercised too.
	agent.SetInfo(`{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true}`)
	tr.refreshAgentFeatures()
	require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "sanity check: the poll must downgrade")

	// The writer was idle across the downgrade and a trace arrives before any
	// scheduled flush: add() no longer rotates the stale v1 payload, so the
	// trace lands in it anyway.
	agent.Reset()
	w.add([]*Span{makeSpan(1)})
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "add() no longer re-checks the effective protocol; the stale payload is untouched until the next flush")

	w.flush()
	w.wg.Wait()
	assert.Equal(t, []string{tracesAPIPathV1}, agent.Requests(),
		"the trace added before any post-downgrade flush still goes out on v1.0 -- the accepted trade-off of this revert")
}

// TestNonEmptyPayloadKeepsAbsorbingTracesUntilNextFlushAfterProtocolDowngrade
// pins the non-empty counterpart of the trade-off above: a writer already
// holding a buffered trace across a protocol downgrade keeps absorbing new
// traces into that same stale payload -- no seal, no off-cadence flush --
// until the next real flush() sends everything buffered so far under
// whatever protocol the payload was built with.
func TestNonEmptyPayloadKeepsAbsorbingTracesUntilNextFlushAfterProtocolDowngrade(t *testing.T) {
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSendRetries(0))
	defer stopTracerTest(tr)

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: agent must resolve to v1")

	w := newAgentTraceWriter(tr.config, newPrioritySampler(), tr.statsd)
	w.add([]*Span{makeSpan(1)})
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "payload created while v1 was in effect must be v1")
	require.Equal(t, 1, w.payload.itemCount(), "sanity check: the pre-downgrade trace must be buffered")

	// Drive the downgrade through the real /info path, not a raw config
	// mutation, so refreshAgentFeatures's real state transition is exercised too.
	agent.SetInfo(`{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true}`)
	tr.refreshAgentFeatures()
	require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "sanity check: the poll must downgrade")

	agent.Reset()
	// The writer already holds a buffered v1 trace across the downgrade:
	// add() no longer seals it, so this trace joins it in the same payload.
	w.add([]*Span{makeSpan(2)})
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "add() no longer seals a non-empty stale payload")
	require.Equal(t, 2, w.payload.itemCount(), "the pre- and post-downgrade traces are now buffered together")

	w.flush()
	w.wg.Wait()

	assert.Equal(t, []string{tracesAPIPathV1}, agent.Requests(),
		"both traces go out together on v1.0 at the next flush -- the accepted trade-off of this revert")
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

// TestConcurrentAddSettlesOnNextFlushAfterProtocolDowngrade exercises many
// goroutines hammering add() concurrently while the protocol downgrades
// mid-flight. This test used to pin a specific race (add() reading the
// effective protocol before acquiring h.mu, letting a descheduled goroutine
// resurrect a stale v1 payload after the downgrade had settled) — with
// #5167's add()-side re-evaluation reverted here (issue #5258 diagnostic),
// add() no longer reads protocol state at all, so that exact race is
// structurally gone, but so is add()'s own correction: concurrent add()s
// during the storm may all land in whatever payload happens to exist,
// v1 or v0.4, and that is an accepted consequence now (see the two tests
// above), not a bug. What must still hold: a single flush() after the storm
// always settles the writer onto the current effective protocol — flush()
// unconditionally rebuilds h.payload for it regardless of what the old
// payload's protocol was — and every request after that settles stays on the
// now-terminal v0.4 protocol, since nothing in add() can resurrect v1
// afterward. Run with -race: this still exercises the same concurrent h.mu
// access pattern the original #5167 fix was written against.
func TestConcurrentAddSettlesOnNextFlushAfterProtocolDowngrade(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent protocol-downgrade stress test in short mode")
	}
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSendRetries(0))
	defer stopTracerTest(tr)

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: agent must resolve to v1")

	w := newAgentTraceWriter(tr.config, newPrioritySampler(), tr.statsd)

	const goroutines = 32
	const itersPerGoroutine = 200

	var addWG sync.WaitGroup
	start := make(chan struct{})
	for range goroutines {
		addWG.Go(func() {
			<-start
			for range itersPerGoroutine {
				// makeSpan(1) -- its argument is the number of meta/metric
				// tags to attach, not an identifier, so keep it minimal here:
				// this test only needs cheap traces to stress the lock, not
				// large ones.
				w.add([]*Span{makeSpan(1)})
			}
		})
	}

	var downgradeWG sync.WaitGroup
	downgradeWG.Go(func() {
		<-start
		agent.SetInfo(`{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true}`)
		tr.refreshAgentFeatures()
	})

	close(start)
	addWG.Wait()
	downgradeWG.Wait()

	require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "sanity check: the downgrade must have applied")

	// Flush and drain whatever the storm produced -- a mix of v1 and v0.4
	// requests is expected and fine now, since add() no longer tracks
	// protocol itself and the downgrade landed partway through. What matters
	// is that flush() settles the writer regardless, and it stays settled
	// afterward: reset the agent's recording and confirm a few more adds,
	// now that the protocol has fully settled, all land on v0.4.
	w.flush()
	w.wg.Wait()
	agent.Reset()

	for range 10 {
		w.add([]*Span{makeSpan(1)})
	}
	w.flush()
	w.wg.Wait()

	for _, path := range agent.Requests() {
		assert.Equal(t, tracesAPIPath, path, "once the protocol has settled at v0.4, every request must land there")
	}
}
