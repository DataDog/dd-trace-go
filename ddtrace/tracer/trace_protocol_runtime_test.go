// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
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

	// Drive the downgrade through the real /info path, not a raw config
	// mutation, so refreshAgentFeatures's real state transition is exercised too.
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

// TestNonEmptyPayloadSealsOnProtocolDowngrade pins that a writer holding at
// least one already-buffered trace does not keep absorbing new traces into
// that stale payload after the agent withdraws /v1.0/traces.
// rotateStalePayload only rotates a payload for free when it is empty, so a
// non-empty stale payload needs add() to seal it with a real flush() instead
// — otherwise every trace accepted between the downgrade and the next
// scheduled flush would also be encoded as v1 and rejected by the agent
// alongside the trace that was already buffered before the downgrade.
func TestNonEmptyPayloadSealsOnProtocolDowngrade(t *testing.T) {
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
	// The writer already holds a buffered v1 trace across the downgrade: add()
	// must seal it via flush() before pushing this trace, rather than mixing
	// it into the stale payload.
	w.add([]*Span{makeSpan(2)})
	w.wg.Wait() // wait for the seal flush spawned inside add() before ordering-sensitive assertions below
	require.Equal(t, traceProtocolV04, w.payload.protocol(), "a trace accepted after a protocol downgrade must not land in the sealed payload")
	require.Equal(t, 1, w.payload.itemCount(), "the sealed payload's trace must not carry over into the new one")

	w.flush()
	w.wg.Wait()

	assert.Equal(t, []string{tracesAPIPathV1, tracesAPIPath}, agent.Requests(),
		"the pre-downgrade trace must go out sealed on v1.0 and the post-downgrade trace on v0.4")
}

// failingMarshaler implements msgp.Marshaler and always fails to encode, so
// a span carrying it as a meta_struct value makes payloadV04.push return an
// error deterministically -- v1's chunk encoding only warns and skips a
// failing meta_struct value (see payload_v1.go), so this only reaches push's
// error return under v0.4.
type failingMarshaler struct{}

func (failingMarshaler) MarshalMsg([]byte) ([]byte, error) {
	return nil, errors.New("injected marshal failure for TestAddSendsSealedPayloadWhenPushFails")
}

// TestAddSendsSealedPayloadWhenPushFails pins a bug where add() sealed a
// stale, non-empty payload for async send on a protocol transition, but
// returned early -- without sending it -- if the incoming trace then failed
// to encode into the replacement payload. The sealed payload held valid,
// already-buffered traces; an unrelated encoding error on the new trace must
// not orphan them.
func TestAddSendsSealedPayloadWhenPushFails(t *testing.T) {
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSendRetries(0))
	defer stopTracerTest(tr)

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: agent must resolve to v1")

	w := newAgentTraceWriter(tr.config, newPrioritySampler(), tr.statsd)
	w.add([]*Span{makeSpan(1)})
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "payload created while v1 was in effect must be v1")
	require.Equal(t, 1, w.payload.itemCount(), "sanity check: the pre-transition trace is buffered")

	// Force a protocol transition so add()'s seal path activates.
	require.True(t, tr.config.advanceTraceProtocolState(protoV04))
	require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol())

	// The incoming trace fails to encode into the new (v0.4) payload.
	bad := makeSpan(2)
	bad.SetMetaStruct("boom", failingMarshaler{})
	w.add([]*Span{bad})

	w.wg.Wait() // wait for the seal's async send spawned inside add()
	assert.Equal(t, []string{tracesAPIPathV1}, agent.Requests(),
		"the sealed pre-transition trace must still be sent, not orphaned by the unrelated push error")
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

// TestConcurrentAddNeverResurrectsDowngradedPayload stresses the interleaving
// behind a real regression: add() used to read the effective protocol before
// acquiring h.mu, so a call could read v1, get descheduled, let a concurrent
// add() race ahead -- observe the downgrade, correctly install and populate a
// v0.4 payload -- and then, on resuming, treat that newer, correct payload as
// "stale" purely because of its own outdated reading, sealing it away and
// replacing it with a freshly built v1 payload instead. That resurrected v1
// after the protocol state had already conclusively (and permanently, see
// trace_protocol_state.go) moved to v0.4.
//
// The fix moved the protocol read inside h.mu, so this exact interleaving is
// no longer reachable: whichever add() call is holding the lock always reads
// the protocol atomically with respect to h.payload, and no goroutine can
// observe a "stale" mismatch caused by its own outdated snapshot. That makes
// the old bug impossible to reproduce deterministically against the fixed
// code -- which is the point of the fix -- so this stresses it heavily
// instead: many goroutines hammering add() while the protocol downgrades
// mid-flight, then asserting the writer settles into agreement with the
// (monotone, now-terminal) effective protocol and that nothing ever reaches
// /v1.0/traces after the downgrade has taken hold. Run with -race.
func TestConcurrentAddNeverResurrectsDowngradedPayload(t *testing.T) {
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

	// Nothing re-checks h.payload's protocol except add()/flush() themselves,
	// so if every add() above happened to finish before refreshAgentFeatures's
	// HTTP round-trip landed, h.payload would still legitimately hold
	// whatever protocol was last observed -- not a bug, just nothing having
	// asked it to settle yet. One more add() forces that check now that the
	// downgrade is conclusively applied, so the assertion below observes the
	// writer's real settling behavior instead of racing the poll's network I/O.
	w.add([]*Span{makeSpan(1)})

	// The writer must have settled into agreement with the now-terminal
	// protocol state -- a reintroduced stale-read race would leave it pinned
	// to a resurrected v1 payload here.
	w.mu.Lock()
	settledProtocol := w.payload.protocol()
	w.mu.Unlock()
	assert.Equal(t, traceProtocolV04, settledProtocol, "the writer's current payload must not have been resurrected to v1 after the downgrade settled")

	// Flush and drain whatever the race above produced -- a mix of v1 and
	// v0.4 requests is expected and fine, since the downgrade landed partway
	// through the storm. What matters is that the state stays put afterward:
	// reset the agent's recording and confirm a few more adds, now that the
	// protocol has fully settled, all land on v0.4.
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
