// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

type simpleEvent struct{ msg *simpleMessage }

func (e simpleEvent) KafkaMessage() (Message, bool)                   { return e.msg, true }
func (e simpleEvent) KafkaOffsetsCommitted() (OffsetsCommitted, bool) { return nil, false }
func translateSimpleEvent(e simpleEvent) simpleEvent                  { return e }
func newSimpleEvent() simpleEvent                                     { return simpleEvent{msg: &simpleMessage{}} }

// closeWithin fails the test if g.Close does not return within d.
func closeWithin(t *testing.T, g *DeliveryGuard, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		g.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("DeliveryGuard.Close did not return")
	}
}

// TestDeliveryGuardBlockedForward checks that Close unblocks a forwarder
// stuck sending to a channel nobody reads, so the channel can then be
// closed without a "send on closed channel" panic (#5806).
func TestDeliveryGuardBlockedForward(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	tr := NewKafkaTracer(testInstr, CKGoVersion2, 0)
	g := NewDeliveryGuard()
	out := make(chan simpleEvent) // never read

	span := tr.StartProduceSpan(&simpleMessage{})
	wrapped, _ := WrapDeliveryChannelGuarded(tr, out, g, span, translateSimpleEvent)
	wrapped <- newSimpleEvent() // the forwarder now tries to send to out

	closeWithin(t, g, 5*time.Second)
	close(out)
	assert.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
}

// TestDeliveryGuardAfterClose checks that an event delivered after Close is
// dropped instead of sent, and that its span still finishes.
func TestDeliveryGuardAfterClose(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	tr := NewKafkaTracer(testInstr, CKGoVersion2, 0)
	g := NewDeliveryGuard()
	out := make(chan simpleEvent, 1)

	span := tr.StartProduceSpan(&simpleMessage{})
	wrapped, _ := WrapDeliveryChannelGuarded(tr, out, g, span, translateSimpleEvent)
	g.Close()
	g.Close() // Close is idempotent
	close(out)
	wrapped <- newSimpleEvent()

	assert.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
	_, ok := <-out
	assert.False(t, ok, "no event must be sent after Close")
}

// TestDeliveryGuardForwards checks that, before Close, events still reach the
// delivery channel, with and without a guard.
func TestDeliveryGuardForwards(t *testing.T) {
	for name, g := range map[string]*DeliveryGuard{"guarded": NewDeliveryGuard(), "unguarded": nil} {
		t.Run(name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			tr := NewKafkaTracer(testInstr, CKGoVersion2, 0)
			out := make(chan simpleEvent)

			span := tr.StartProduceSpan(&simpleMessage{})
			wrapped, _ := WrapDeliveryChannelGuarded(tr, out, g, span, translateSimpleEvent)
			evt := newSimpleEvent()
			wrapped <- evt
			select {
			case got := <-out:
				assert.Same(t, evt.msg, got.msg)
			case <-time.After(5 * time.Second):
				t.Fatal("event not forwarded")
			}
			assert.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, 5*time.Second, time.Millisecond)
		})
	}
}

// TestDeliveryGuardConcurrent races many forwarders with Close and the close
// of their shared channel. Run it with -race.
func TestDeliveryGuardConcurrent(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	tr := NewKafkaTracer(testInstr, CKGoVersion2, 0)
	g := NewDeliveryGuard()
	out := make(chan simpleEvent, 1)

	const n = 50
	var wg sync.WaitGroup
	for range n {
		span := tr.StartProduceSpan(&simpleMessage{})
		wrapped, _ := WrapDeliveryChannelGuarded(tr, out, g, span, translateSimpleEvent)
		wg.Go(func() { wrapped <- newSimpleEvent() })
	}
	closeWithin(t, g, 5*time.Second)
	close(out)
	wg.Wait()
	require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == n }, 5*time.Second, time.Millisecond)
}
