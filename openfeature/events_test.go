// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
)

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

func TestUpdateConfiguration_EmitsProviderReadyOnce(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	config := createTestConfig()

	p.updateConfiguration(config)
	event := drainEvent(t, p.EventChannel())
	if event.EventType != openfeature.ProviderReady {
		t.Errorf("expected ProviderReady, got %v", event.EventType)
	}

	p.updateConfiguration(config)
	event = drainEvent(t, p.EventChannel())
	if event.EventType != openfeature.ProviderConfigChange {
		t.Errorf("expected ProviderConfigChange on the second update, got %v", event.EventType)
	}
}

func TestUpdateConfiguration_NilEmitsProviderStale(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig())
	drainEvent(t, p.EventChannel()) // ProviderReady

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

	// First update fires ProviderReady, filling one slot; leave it unread.
	p.updateConfiguration(config)

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

	p.updateConfiguration(config)
	drainEvent(t, p.EventChannel()) // ProviderReady

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
	event := drainEvent(t, p.EventChannel())
	if event.EventType == "" {
		t.Error("must never emit an event with an untyped (empty) EventType")
	}
}
