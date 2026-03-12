// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"testing"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

func TestForwardingCallbackBuffersWhenNoCallback(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	update := remoteconfig.ProductUpdate{"path/1": []byte(`{"format":"SERVER"}`)}
	statuses := forwardingCallback(update)

	assert.Equal(t, rc.ApplyStateAcknowledged, statuses["path/1"].State)

	rcState.Lock()
	require.NotNil(t, rcState.buffered)
	assert.Contains(t, rcState.buffered, "path/1")
	rcState.Unlock()
}

func TestForwardingCallbackBuffersOnlyLatest(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	forwardingCallback(remoteconfig.ProductUpdate{"path/old": []byte(`old`)})
	forwardingCallback(remoteconfig.ProductUpdate{"path/new": []byte(`new`)})

	rcState.Lock()
	require.NotNil(t, rcState.buffered)
	assert.NotContains(t, rcState.buffered, "path/old")
	assert.Contains(t, rcState.buffered, "path/new")
	rcState.Unlock()
}

func TestForwardingCallbackForwardsWhenCallbackAttached(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	var received remoteconfig.ProductUpdate
	cb := func(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
		received = update
		statuses := make(map[string]rc.ApplyStatus, len(update))
		for path := range update {
			statuses[path] = rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
		}
		return statuses
	}

	rcState.Lock()
	rcState.subscribed = true
	rcState.callback = cb
	rcState.Unlock()

	update := remoteconfig.ProductUpdate{"path/live": []byte(`live`)}
	statuses := forwardingCallback(update)

	assert.Equal(t, rc.ApplyStateAcknowledged, statuses["path/live"].State)
	assert.Contains(t, received, "path/live")

	rcState.Lock()
	assert.Nil(t, rcState.buffered)
	rcState.Unlock()
}

func TestAttachCallbackNotSubscribed(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	got := AttachCallback(func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus { return nil })
	assert.False(t, got)
}

func TestAttachCallbackReplaysBuffer(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	buffered := remoteconfig.ProductUpdate{"path/buf": []byte(`buffered`)}
	rcState.Lock()
	rcState.subscribed = true
	rcState.buffered = buffered
	rcState.Unlock()

	var received remoteconfig.ProductUpdate
	cb := func(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
		received = update
		return nil
	}

	got := AttachCallback(cb)
	assert.True(t, got)
	assert.Contains(t, received, "path/buf")

	rcState.Lock()
	assert.Nil(t, rcState.buffered)
	rcState.Unlock()
}

func TestAttachCallbackNoBuffer(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	rcState.Lock()
	rcState.subscribed = true
	rcState.Unlock()

	called := false
	got := AttachCallback(func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
		called = true
		return nil
	})
	assert.True(t, got)
	assert.False(t, called, "callback should not be called when no buffer")
}

func TestForwardingCallbackDeepCopiesBuffer(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	// Create original data that we'll modify after buffering.
	original := []byte(`{"format":"SERVER"}`)
	update := remoteconfig.ProductUpdate{"path/1": original}

	forwardingCallback(update)

	// Modify the original byte slice after buffering.
	original[0] = 'X'

	// Verify buffered data is independent of the original.
	rcState.Lock()
	buffered := rcState.buffered["path/1"]
	rcState.Unlock()

	assert.Equal(t, byte('{'), buffered[0], "buffered data should be isolated from original")
	assert.Equal(t, byte('X'), original[0], "original should be modified")
}

func TestSubscribeRCAfterTracerRestart(t *testing.T) {
	ResetForTest()
	defer ResetForTest()
	defer remoteconfig.Reset()

	// Simulate first tracer start + subscription
	require.NoError(t, remoteconfig.Start(remoteconfig.DefaultClientConfig()))
	require.NoError(t, SubscribeRC())

	// First update arrives, gets buffered
	forwardingCallback(remoteconfig.ProductUpdate{"path/1": []byte(`v1`)})
	require.NotNil(t, GetBufferedForTest())

	// Simulate tracer stop + restart (RC client destroyed and recreated)
	remoteconfig.Stop()
	require.NoError(t, remoteconfig.Start(remoteconfig.DefaultClientConfig()))

	// Second SubscribeRC — does it actually re-subscribe on the new client?
	require.NoError(t, SubscribeRC())

	// Verify: FFE_FLAGS should be registered on the new RC client
	has, err := remoteconfig.HasProduct(FFEProductName)
	require.NoError(t, err)
	assert.True(t, has, "FFE_FLAGS should be subscribed on the new RC client after restart")
}
