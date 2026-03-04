// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"sync"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

// rcState bridges the tracer's early RC subscription (during tracer.Start)
// with the late-created DatadogProvider (during NewDatadogProvider).
//
// The tracer calls SubscribeRC() at startup to include FFE_FLAGS in the very
// first RC poll. The forwarding callback buffers the latest config snapshot
// until a provider is attached via attachProvider().
var rcState struct {
	sync.Mutex
	subscribed bool
	provider   *DatadogProvider
	buffered   *remoteconfig.ProductUpdate // latest snapshot; RC sends full state each time
}

// SubscribeRC subscribes to the FFE_FLAGS RC product using a forwarding
// callback. It is called by the tracer during startRemoteConfig() so that
// FFE_FLAGS is included in the first RC poll. This is safe to call even if
// no DatadogProvider will ever be created — configs are simply buffered.
func SubscribeRC() error {
	rcState.Lock()
	defer rcState.Unlock()

	if rcState.subscribed {
		return nil
	}

	if _, err := remoteconfig.Subscribe(ffeProductName, forwardingCallback, remoteconfig.FFEFlagEvaluation); err != nil {
		return err
	}

	rcState.subscribed = true
	log.Debug("openfeature: subscribed to RC product %s via tracer", ffeProductName)
	return nil
}

// forwardingCallback is the RC callback registered by SubscribeRC. If a
// provider is attached, it forwards directly. Otherwise, it buffers the
// latest update for replay when the provider attaches.
func forwardingCallback(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
	rcState.Lock()
	defer rcState.Unlock()

	if rcState.provider != nil {
		return rcState.provider.rcCallback(update)
	}

	// Buffer the latest update for replay.
	cpy := make(remoteconfig.ProductUpdate, len(update))
	for k, v := range update {
		cpy[k] = v
	}
	rcState.buffered = &cpy

	// Acknowledge all paths so RC doesn't consider them errored.
	statuses := make(map[string]rc.ApplyStatus, len(update))
	for path := range update {
		statuses[path] = rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
	}
	return statuses
}

// attachProvider wires the given provider to the global RC subscription.
// If SubscribeRC() was called (i.e. the tracer subscribed), it replays any
// buffered config and returns true. Otherwise returns false, meaning the
// caller should fall back to its own RC subscription.
func attachProvider(p *DatadogProvider) bool {
	rcState.Lock()
	defer rcState.Unlock()

	if !rcState.subscribed {
		return false
	}

	rcState.provider = p

	// Replay the buffered update if there is one.
	if rcState.buffered != nil {
		log.Debug("openfeature: replaying buffered RC config to provider")
		p.rcCallback(*rcState.buffered)
		rcState.buffered = nil
	}

	return true
}
