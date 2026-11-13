// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import "github.com/open-feature/go-sdk/openfeature"

var _ openfeature.EventHandler = (*DatadogProvider)(nil)

// eventChannelBufferSize is the buffer size of the provider's event channel.
const eventChannelBufferSize = 8

// EventChannel implements openfeature.EventHandler. The SDK's listener calls it
// every loop iteration, so a fresh channel would drop the subscription.
func (p *DatadogProvider) EventChannel() <-chan openfeature.Event {
	return p.eventCh
}

// readinessChange is a readiness transition that updateConfiguration logs
// once p.mu is released.
type readinessChange int

const (
	readinessUnchanged readinessChange = iota
	readinessStale
	readinessRecovered
)

func (c readinessChange) logDebug() {
	switch c {
	case readinessStale:
		logOnboarding("provider is stale: configuration is unavailable; evaluations return their default values until configuration arrives")
	case readinessRecovered:
		logOnboarding("provider recovered: configuration received, provider is ready")
	}
}

// emitFirstOrChangeEvent maps a configuration update to a lifecycle event and
// returns the readiness change to log. Called under p.mu from setConfiguration.
func (p *DatadogProvider) emitFirstOrChangeEvent(config *universalFlagsConfiguration) readinessChange {
	switch {
	case config == nil:
		change := readinessUnchanged
		if p.ready {
			change = readinessStale
		}
		p.ready = false
		p.emitEvent(openfeature.Event{EventType: openfeature.ProviderStale, ProviderEventDetails: openfeature.ProviderEventDetails{
			Message: "configuration is unavailable",
		}})
		return change
	case !p.ready:
		p.ready = true
		if !p.initialReadyHandoffComplete {
			// The SDK emits its own ProviderReady when Init returns, so
			// emitting the first transition here fires handlers twice.
			p.initialReadyHandoffComplete = true
			return readinessUnchanged
		}
		// Init already returned (timed out or canceled), or configuration
		// had been removed: this is a recovery the customer should see.
		p.emitEvent(openfeature.Event{EventType: openfeature.ProviderReady, ProviderEventDetails: openfeature.ProviderEventDetails{
			Message: "configuration received",
		}})
		return readinessRecovered
	default:
		p.emitEvent(openfeature.Event{EventType: openfeature.ProviderConfigChange, ProviderEventDetails: openfeature.ProviderEventDetails{
			Message: "configuration updated",
		}})
		return readinessUnchanged
	}
}

// emitEvent sends event without blocking the caller. ProviderConfigChange is
// dropped on a full buffer; the newest ready/stale transition always survives.
func (p *DatadogProvider) emitEvent(event openfeature.Event) {
	select {
	case p.eventCh <- event:
		return
	default:
	}

	if event.EventType != openfeature.ProviderReady && event.EventType != openfeature.ProviderStale {
		return
	}

	// Losing a readiness change would leave the SDK reporting the wrong status
	// indefinitely, so evict the oldest queued event to make room for this one.
	select {
	case <-p.eventCh:
	default:
	}
	select {
	case p.eventCh <- event:
	default:
	}
}
