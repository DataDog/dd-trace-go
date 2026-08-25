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

// emitFirstOrChangeEvent emits ProviderReady on every transition from
// not-ready to ready — the first configuration this provider ever applies,
// and every later recovery after a ProviderStale period — and
// ProviderConfigChange for a later non-nil configuration while already
// ready. A nil config (RC delivering an empty configuration set, or a poll
// that never received one) emits ProviderStale instead: evaluations return
// PROVIDER_NOT_READY, but without this the OpenFeature status would remain
// ReadyState. Called under p.mu from updateConfiguration.
func (p *DatadogProvider) emitFirstOrChangeEvent(config *universalFlagsConfiguration) {
	switch {
	case config == nil:
		p.ready = false
		p.emitEvent(openfeature.Event{EventType: openfeature.ProviderStale, ProviderEventDetails: openfeature.ProviderEventDetails{
			Message: "configuration is unavailable",
		}})
	case !p.ready:
		p.ready = true
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
// goroutine). ProviderReady and ProviderStale must not be silently dropped:
// unlike ProviderConfigChange, they change the SDK's tracked readiness
// status, so losing one on a full buffer would leave listeners on the wrong
// status indefinitely (e.g. a dropped ProviderStale leaves the SDK reporting
// ready while evaluations return PROVIDER_NOT_READY). For either, the oldest
// queued event is drained to make room before retrying once.
// ProviderConfigChange carries no flag-specific payload and doesn't affect
// status, so dropping it on a full buffer is correct coalescing, not a lost
// update — and it must never evict a still-unread ProviderReady/ProviderStale
// to make room for itself.
func (p *DatadogProvider) emitEvent(event openfeature.Event) {
	select {
	case p.eventCh <- event:
		return
	default:
	}

	if event.EventType != openfeature.ProviderReady && event.EventType != openfeature.ProviderStale {
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
