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
	"slices"
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
// with the late-created DatadogProviders (during NewDatadogProvider). One
// forwarding subscription serves every provider in the process, so an
// application can register several providers, for example under different
// OpenFeature domains.
var rcState struct {
	sync.Mutex
	subscribed bool
	callbacks  map[uint64]Callback
	nextID     uint64
	// buffered holds, per path, the last configuration the attached providers
	// accepted. RC sends only changed files and nil for removed ones, so each
	// update is merged in. It is replayed to each provider that attaches later.
	buffered remoteconfig.ProductUpdate
	// capabilityUnregistered records that the last provider detached and
	// unregistered FFE_FLAGS evaluation, so the next one registers it again.
	capabilityUnregistered bool
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
		// Attached providers stay attached and receive the new client's updates.
		log.Debug("openfeature: RC subscription for %s was lost (tracer restart?), re-subscribing", FFEProductName)
		rcState.subscribed = false
	}

	if has, _ := remoteconfig.HasProduct(FFEProductName); has {
		log.Debug("openfeature: RC product %s already subscribed via provider, skipping tracer subscription", FFEProductName)
		return nil
	}

	if _, err := remoteconfig.Subscribe(FFEProductName, forwardingCallback, remoteconfig.FFEFlagEvaluation); err != nil {
		return err
	}

	rcState.subscribed = true
	rcState.capabilityUnregistered = false
	log.Debug("openfeature: subscribed to RC product %s via tracer", FFEProductName)
	return nil
}

// forwardingCallback is the RC callback registered by SubscribeRC or
// SubscribeProvider. It forwards the update to every attached provider and
// keeps what they accepted for providers that attach later. A path is
// acknowledged unless a provider reports another state for it; a path a
// provider rejected keeps its previous replay value, as the providers keep
// their last known configuration.
func forwardingCallback(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
	rcState.Lock()
	defer rcState.Unlock()

	statuses := make(map[string]rc.ApplyStatus, len(update))
	for path := range update {
		statuses[path] = rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
	}
	for _, id := range attachedIDs() {
		for path, status := range rcState.callbacks[id](update) {
			if status.State != rc.ApplyStateAcknowledged {
				statuses[path] = status
			}
		}
	}

	if rcState.buffered == nil {
		rcState.buffered = make(remoteconfig.ProductUpdate, len(update))
	}
	for path, data := range update {
		switch {
		case statuses[path].State == rc.ApplyStateError:
		case data == nil:
			delete(rcState.buffered, path)
		default:
			rcState.buffered[path] = bytes.Clone(data)
		}
	}
	return statuses
}

// attachedIDs returns the attached callback IDs in attachment order.
// The caller holds rcState.
func attachedIDs() []uint64 {
	ids := make([]uint64, 0, len(rcState.callbacks))
	for id := range rcState.callbacks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// AttachCallback wires the given callback to the global RC subscription.
// If SubscribeRC() or SubscribeProvider() subscribed, it replays the latest
// update and returns a function that detaches the callback, and true.
// Otherwise it returns false. Detaching the last callback unregisters the
// FFE_FLAGS evaluation capability.
func AttachCallback(cb Callback) (detach func(), ok bool) {
	rcState.Lock()
	defer rcState.Unlock()

	if !rcState.subscribed {
		return nil, false
	}

	if rcState.callbacks == nil {
		rcState.callbacks = make(map[uint64]Callback)
	}
	if len(rcState.callbacks) == 0 && rcState.capabilityUnregistered {
		if err := remoteconfig.RegisterCapability(remoteconfig.FFEFlagEvaluation); err == nil {
			rcState.capabilityUnregistered = false
		}
	}
	id := rcState.nextID
	rcState.nextID++
	rcState.callbacks[id] = cb

	if len(rcState.buffered) > 0 {
		log.Debug("openfeature: replaying buffered RC config to provider")
		// Apply statuses are intentionally discarded: the RC client already received
		// statuses for this update from forwardingCallback.
		// This call only initializes the provider's in-memory state.
		cb(rcState.buffered)
	}

	return sync.OnceFunc(func() { detachCallback(id) }), true
}

func detachCallback(id uint64) {
	rcState.Lock()
	defer rcState.Unlock()

	delete(rcState.callbacks, id)
	if len(rcState.callbacks) == 0 {
		_ = remoteconfig.UnregisterCapability(remoteconfig.FFEFlagEvaluation)
		rcState.capabilityUnregistered = true
		log.Debug("openfeature: unregistered from Remote Config")
	}
}

// SubscribeProvider ensures the FFE_FLAGS subscription exists for a provider
// that will attach with AttachCallback. When the tracer has not subscribed, it
// starts RC and subscribes the forwarding callback itself (slow path).
// It holds the subscription mutex to prevent races with SubscribeRC.
func SubscribeProvider() error {
	rcState.Lock()
	defer rcState.Unlock()

	if rcState.subscribed {
		// The subscription is gone once the RC client stops, for example on
		// tracer.Stop; subscribe again on a new client.
		if has, _ := remoteconfig.HasProduct(FFEProductName); has {
			return nil
		}
		rcState.subscribed = false
	}

	// Slow path: tracer didn't subscribe, so we need to start RC and subscribe ourselves.
	if err := remoteconfig.Start(remoteconfig.DefaultClientConfig()); err != nil {
		return fmt.Errorf("failed to start Remote Config: %w", err)
	}

	if has, _ := remoteconfig.HasProduct(FFEProductName); has {
		return fmt.Errorf("RC product %s already subscribed", FFEProductName)
	}

	if _, err := remoteconfig.Subscribe(FFEProductName, forwardingCallback, remoteconfig.FFEFlagEvaluation); err != nil {
		return err
	}

	rcState.subscribed = true
	rcState.capabilityUnregistered = false
	log.Debug("openfeature: provider subscribed to RC product %s", FFEProductName)
	return nil
}
