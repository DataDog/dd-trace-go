// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
)

// TestSetProviderAndWait_EmitsProviderReadyExactlyOnce drives the real SDK,
// which is the only place the duplicate can appear: the SDK emits its own
// ProviderReady for Init's nil return and only then starts reading
// EventChannel, so a self-queued ProviderReady would fire handlers twice.
func TestSetProviderAndWait_EmitsProviderReadyExactlyOnce(t *testing.T) {
	openfeature.Shutdown() // drop providers and handlers left by other tests
	t.Cleanup(openfeature.Shutdown)

	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig())

	var ready atomic.Int32
	callback := func(openfeature.EventDetails) { ready.Add(1) }
	openfeature.AddHandler(openfeature.ProviderReady, &callback)

	if err := openfeature.SetProviderAndWait(p); err != nil {
		t.Fatalf("failed to set provider: %v", err)
	}

	// Handlers run on the SDK's listener goroutine, so wait long enough for a
	// second call to show up rather than sampling immediately.
	time.Sleep(200 * time.Millisecond)
	if got := ready.Load(); got != 1 {
		t.Errorf("expected exactly one ProviderReady, got %d", got)
	}
}

func drainEvent(t *testing.T, ch <-chan openfeature.Event) openfeature.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return openfeature.Event{}
	}
}

func TestEventChannel_ReturnsSameChannelEveryCall(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	first := p.EventChannel()
	second := p.EventChannel()
	if first != second {
		t.Fatal("EventChannel must return the same channel on every call")
	}
}

// TestUpdateConfiguration_FirstReadyIsLeftToInit pins that the first ready
// transition emits nothing on the channel. The SDK synthesises its own
// ProviderReady when InitWithContext returns, so emitting here too would fire
// every registered handler twice.
func TestUpdateConfiguration_FirstReadyIsLeftToInit(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()

	p.updateConfiguration(config)
	select {
	case event := <-p.EventChannel():
		t.Errorf("the first ready transition must emit nothing, got %v", event.EventType)
	default:
	}

	p.updateConfiguration(config)
	event := drainEvent(t, p.EventChannel())
	if event.EventType != openfeature.ProviderConfigChange {
		t.Errorf("expected ProviderConfigChange on the second update, got %v", event.EventType)
	}
}

func TestUpdateConfiguration_NilEmitsProviderStale(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig()) // first ready: left to Init, emits nothing

	p.updateConfiguration(nil)
	event := drainEvent(t, p.EventChannel())
	if event.EventType != openfeature.ProviderStale {
		t.Errorf("expected ProviderStale on a nil configuration, got %v", event.EventType)
	}

	// Recovering from stale must re-fire ProviderReady, not ProviderConfigChange:
	// the SDK's status only transitions back to ready on ProviderReady.
	p.updateConfiguration(createTestConfig())
	event = drainEvent(t, p.EventChannel())
	if event.EventType != openfeature.ProviderReady {
		t.Errorf("expected ProviderReady after recovering from a nil configuration, got %v", event.EventType)
	}

	// A further, later configuration while already ready is a plain change.
	p.updateConfiguration(createTestConfig())
	event = drainEvent(t, p.EventChannel())
	if event.EventType != openfeature.ProviderConfigChange {
		t.Errorf("expected ProviderConfigChange on a later update while ready, got %v", event.EventType)
	}
}

func TestUpdateConfiguration_ProviderReadySurvivesFullBuffer(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()

	// Reach ready, drop to stale, then recover: that recovery is the first
	// ProviderReady on the channel, since the first transition is Init's.
	p.updateConfiguration(config)
	p.updateConfiguration(nil)
	drainEvent(t, p.EventChannel()) // ProviderStale
	p.updateConfiguration(config)   // ProviderReady, left unread

	// Fill the rest of the buffer with coalescing ProviderConfigChange events,
	// then overflow it by one so a slot must be drained.
	for range eventChannelBufferSize {
		p.updateConfiguration(config)
	}

	// ProviderReady must still be found somewhere in the channel, even though
	// far more events were emitted than the buffer can hold.
	foundReady := false
	drained := 0
	for {
		select {
		case e := <-p.EventChannel():
			drained++
			if e.EventType == openfeature.ProviderReady {
				foundReady = true
			}
		default:
			goto done
		}
	}
done:
	if !foundReady {
		t.Errorf("ProviderReady must not be dropped even under a full buffer (drained %d events)", drained)
	}
}

func TestUpdateConfiguration_ProviderStaleSurvivesFullBuffer(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()

	p.updateConfiguration(config) // first ready: left to Init, emits nothing

	// Fire ProviderStale, filling one slot; leave it unread.
	p.updateConfiguration(nil)

	// Fill the rest of the buffer with coalescing ProviderConfigChange events,
	// then overflow it by one so a slot must be drained. ProviderConfigChange
	// must never evict the still-unread ProviderStale to make room for itself.
	for range eventChannelBufferSize {
		p.updateConfiguration(config)
	}

	foundStale := false
	drained := 0
	for {
		select {
		case e := <-p.EventChannel():
			drained++
			if e.EventType == openfeature.ProviderStale {
				foundStale = true
			}
		default:
			goto done
		}
	}
done:
	if !foundStale {
		t.Errorf("ProviderStale must not be dropped even under a full buffer (drained %d events)", drained)
	}
}

func TestUpdateConfiguration_NeverEmitsUntypedEvent(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig())
	p.updateConfiguration(createTestConfig())
	event := drainEvent(t, p.EventChannel())
	if event.EventType == "" {
		t.Error("must never emit an event with an untyped (empty) EventType")
	}
}
