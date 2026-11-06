// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package openfeature provides a lightweight bridge between the tracer's early
// Remote Config subscription and the late-created OpenFeature DatadogProvider.
//
// This package intentionally has minimal dependencies (only internal/remoteconfig)
// so the tracer can import it without pulling in the OpenFeature SDK or OTel.
package openfeature

import (
	"bytes"
	"fmt"
	"sync"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

const (
	// FFEProductName is the RC product name for feature flag evaluation.
	FFEProductName = "FFE_FLAGS"
)

// Callback is a function that processes an RC product update and returns
// apply statuses. This matches the signature of DatadogProvider.rcCallback.
type Callback func(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus

// ProviderToken identifies one provider's ownership of either a tracer-owned
// callback attachment or a provider-owned slow-path subscription.
type ProviderToken uint64

// rcState bridges the tracer's early RC subscription (during tracer.Start)
// with the late-created DatadogProvider (during NewDatadogProvider).
var rcState struct {
	sync.Mutex
	subscribed           bool
	callback             Callback
	callbackToken        ProviderToken
	buffered             remoteconfig.ProductUpdate // latest snapshot; RC sends full state each time
	providerSubscription remoteconfig.SubscriptionToken
	providerToken        ProviderToken
	providerCapabilities []remoteconfig.Capability
	nextProviderToken    ProviderToken
}

// SubscribeRC subscribes to the FFE_FLAGS RC product using a forwarding
// callback. It is called by the tracer during startRemoteConfig() so that
// FFE_FLAGS is included in the first RC poll.
func SubscribeRC() error {
	rcState.Lock()
	defer rcState.Unlock()

	if rcState.subscribed {
		// Verify the subscription is still live (it won't be after a tracer restart
		// because remoteconfig.Stop() destroys all subscriptions).
		if has, _ := remoteconfig.HasProduct(FFEProductName); has {
			return nil
		}
		log.Debug("openfeature: RC subscription for %s was lost (tracer restart?), re-subscribing", FFEProductName)
		rcState.subscribed = false
		rcState.callback = nil
		rcState.callbackToken = 0
	}

	if has, _ := remoteconfig.HasProduct(FFEProductName); has {
		log.Debug("openfeature: RC product %s already subscribed via provider, skipping tracer subscription", FFEProductName)
		return nil
	}

	if _, err := remoteconfig.Subscribe(FFEProductName, forwardingCallback, remoteConfigCapabilities(EvaluatorCapabilities())...); err != nil {
		return err
	}

	rcState.subscribed = true
	log.Debug("openfeature: subscribed to RC product %s via tracer", FFEProductName)
	return nil
}

// forwardingCallback is the RC callback registered by SubscribeRC. If a
// provider callback is attached, it forwards directly. Otherwise, it buffers
// the latest update for replay when the provider attaches.
func forwardingCallback(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
	rcState.Lock()
	defer rcState.Unlock()

	if rcState.callback != nil {
		return rcState.callback(update)
	}

	// Buffer the latest update for replay.
	cpy := make(remoteconfig.ProductUpdate, len(update))
	for k, v := range update {
		cpy[k] = bytes.Clone(v)
	}
	rcState.buffered = cpy

	// Acknowledge all paths so RC doesn't consider them errored.
	statuses := make(map[string]rc.ApplyStatus, len(update))
	for path := range update {
		statuses[path] = rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
	}
	return statuses
}

// AttachCallback wires the given callback to the global RC subscription.
// If SubscribeRC() was called (i.e. the tracer subscribed), it replays any
// buffered config and returns an ownership token with true. Otherwise it
// returns a zero token with false, meaning the caller should fall back to its
// own RC subscription.
func AttachCallback(cb Callback) (ProviderToken, bool) {
	rcState.Lock()
	defer rcState.Unlock()

	if !rcState.subscribed {
		return 0, false
	}

	if rcState.callback != nil {
		log.Warn("openfeature: callback already attached, multiple providers are not supported") //errtrack:ignore caller initialized multiple providers
		return 0, false
	}

	token := nextProviderTokenLocked()
	rcState.callback = cb
	rcState.callbackToken = token

	if rcState.buffered != nil {
		log.Debug("openfeature: replaying buffered RC config to provider")
		// Apply statuses are intentionally discarded: the RC client already received
		// ApplyStateAcknowledged from forwardingCallback when this update was buffered.
		// This call only initializes the provider's in-memory state.
		cb(rcState.buffered)
		rcState.buffered = nil
	}

	return token, true
}

// SubscribeProvider attempts to subscribe a provider callback to RC. A
// provider-owned slow-path subscription returns its ownership token; the
// tracer-owned fast path returns a zero token until AttachCallback succeeds.
// It holds the subscription mutex to prevent races with SubscribeRC.
func SubscribeProvider(cb remoteconfig.ProductCallback) (tracerOwnsSubscription bool, token ProviderToken, err error) {
	rcState.Lock()
	defer rcState.Unlock()

	if rcState.subscribed {
		return true, 0, nil
	}

	// Slow path: tracer didn't subscribe, so we need to start RC and subscribe ourselves.
	if err := remoteconfig.Start(remoteconfig.DefaultClientConfig()); err != nil {
		return false, 0, fmt.Errorf("failed to start Remote Config: %w", err)
	}

	if has, _ := remoteconfig.HasProduct(FFEProductName); has {
		return false, 0, fmt.Errorf("RC product %s already subscribed", FFEProductName)
	}

	capabilities := remoteConfigCapabilities(EvaluatorCapabilities())
	subscription, err := remoteconfig.Subscribe(FFEProductName, cb, capabilities...)
	if err != nil {
		return false, 0, err
	}
	token = nextProviderTokenLocked()
	rcState.providerSubscription = subscription
	rcState.providerToken = token
	rcState.providerCapabilities = append(rcState.providerCapabilities[:0], capabilities...)

	log.Debug("openfeature: provider subscribed to RC product %s", FFEProductName)
	return false, token, nil
}

// nextProviderTokenLocked returns a non-zero token. The caller holds rcState.
func nextProviderTokenLocked() ProviderToken {
	rcState.nextProviderToken++
	if rcState.nextProviderToken == 0 {
		rcState.nextProviderToken++
	}
	return rcState.nextProviderToken
}

// UnsubscribeProvider releases only the provider ownership represented by
// token. Fast-path ownership detaches the matching callback while retaining
// the tracer subscription; slow-path ownership removes its subscription and
// exact capability set. Stale tokens are harmless.
func UnsubscribeProvider(token ProviderToken) error {
	rcState.Lock()
	defer rcState.Unlock()

	if token == 0 {
		return nil
	}
	if rcState.callbackToken == token {
		rcState.callback = nil
		rcState.callbackToken = 0
		return nil
	}
	if rcState.providerToken != token {
		return nil
	}
	subscription := rcState.providerSubscription
	capabilities := append([]remoteconfig.Capability(nil), rcState.providerCapabilities...)
	rcState.providerSubscription = 0
	rcState.providerToken = 0
	rcState.providerCapabilities = nil

	if err := remoteconfig.Unsubscribe(subscription); err != nil {
		return err
	}
	for _, capability := range capabilities {
		if err := remoteconfig.UnregisterCapability(capability); err != nil {
			return err
		}
	}
	return nil
}
