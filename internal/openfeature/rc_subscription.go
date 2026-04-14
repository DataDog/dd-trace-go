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

// rcState bridges the tracer's early RC subscription (during tracer.Start)
// with the late-created DatadogProvider (during NewDatadogProvider).
var rcState struct {
	sync.Mutex
	subscribed bool
	callback   Callback
	buffered   remoteconfig.ProductUpdate // latest snapshot; RC sends full state each time
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
	}

	if has, _ := remoteconfig.HasProduct(FFEProductName); has {
		log.Debug("openfeature: RC product %s already subscribed via provider, skipping tracer subscription", FFEProductName)
		return nil
	}

	if _, err := remoteconfig.Subscribe(FFEProductName, forwardingCallback, remoteconfig.FFEFlagEvaluation); err != nil {
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
// buffered config and returns true. Otherwise returns false, meaning the
// caller should fall back to its own RC subscription.
func AttachCallback(cb Callback) bool {
	rcState.Lock()
	defer rcState.Unlock()

	if !rcState.subscribed {
		return false
	}

	if rcState.callback != nil {
		log.Warn("openfeature: callback already attached, multiple providers are not supported")
		return false
	}

	rcState.callback = cb

	if rcState.buffered != nil {
		log.Debug("openfeature: replaying buffered RC config to provider")
		// Apply statuses are intentionally discarded: the RC client already received
		// ApplyStateAcknowledged from forwardingCallback when this update was buffered.
		// This call only initializes the provider's in-memory state.
		cb(rcState.buffered)
		rcState.buffered = nil
	}

	return true
}

// SubscribeProvider attempts to subscribe a provider callback to RC.
// It holds the subscription mutex to prevent races with SubscribeRC.
func SubscribeProvider(cb remoteconfig.ProductCallback) (tracerOwnsSubscription bool, err error) {
	rcState.Lock()
	defer rcState.Unlock()

	if rcState.subscribed {
		return true, nil
	}

	// Slow path: tracer didn't subscribe, so we need to start RC and subscribe ourselves.
	if err := remoteconfig.Start(remoteconfig.DefaultClientConfig()); err != nil {
		return false, fmt.Errorf("failed to start Remote Config: %w", err)
	}

	if has, _ := remoteconfig.HasProduct(FFEProductName); has {
		return false, fmt.Errorf("RC product %s already subscribed", FFEProductName)
	}

	if _, err := remoteconfig.Subscribe(FFEProductName, cb, remoteconfig.FFEFlagEvaluation); err != nil {
		return false, err
	}

	log.Debug("openfeature: provider subscribed to RC product %s", FFEProductName)
	return false, nil
}
