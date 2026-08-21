// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"github.com/open-feature/go-sdk/openfeature"
)

var _ openfeature.EventHandler = (*DatadogProvider)(nil)

// eventChannelBufferSize is the buffer size of the provider's event channel.
const eventChannelBufferSize = 8

// EventChannel implements openfeature.EventHandler. It must return the same
// stored channel on every call: the SDK's listener goroutine calls this on
// every loop iteration, so returning a fresh channel each time would
// silently drop the subscription.
func (p *DatadogProvider) EventChannel() <-chan openfeature.Event {
	return p.eventCh
}

// emitFirstOrChangeEvent emits ProviderReady exactly once, on the first
// non-nil configuration this provider ever applies, and ProviderConfigChange
// on every later one. A nil config (RC delivering an empty configuration
// set, or a poll that never received one) emits ProviderStale instead:
// evaluations return PROVIDER_NOT_READY, but without this the OpenFeature
// status would remain ReadyState. Called under p.mu from updateConfiguration.
func (p *DatadogProvider) emitFirstOrChangeEvent(config *universalFlagsConfiguration) {
	switch {
	case config == nil:
		p.emitEvent(openfeature.Event{EventType: openfeature.ProviderStale, ProviderEventDetails: openfeature.ProviderEventDetails{
			Message: "configuration is unavailable",
		}})
	case !p.firstConfigSeen:
		p.firstConfigSeen = true
		p.emitEvent(openfeature.Event{EventType: openfeature.ProviderReady, ProviderEventDetails: openfeature.ProviderEventDetails{
			Message: "configuration received",
		}})
	default:
		p.emitEvent(openfeature.Event{EventType: openfeature.ProviderConfigChange, ProviderEventDetails: openfeature.ProviderEventDetails{
			Message: "configuration updated",
		}})
	}
}

// emitEvent sends event without blocking the caller (a poll or RC callback
// goroutine). ProviderReady must not be silently dropped — a full buffer is
// almost certainly holding coalescing ProviderConfigChange/ProviderStale
// events, so one is drained to make room before retrying once. Any other
// event type is allowed to be dropped on a full buffer: that is correct
// coalescing, not a lost update, since these events carry no flag-specific
// payload.
func (p *DatadogProvider) emitEvent(event openfeature.Event) {
	select {
	case p.eventCh <- event:
		return
	default:
	}

	if event.EventType != openfeature.ProviderReady {
		return
	}

	select {
	case <-p.eventCh:
	default:
	}
	select {
	case p.eventCh <- event:
	default:
	}
}
