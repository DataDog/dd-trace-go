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
	rcState.Unlock()
	_, ok := AttachCallback(cb)
	require.True(t, ok)

	update := remoteconfig.ProductUpdate{"path/live": []byte(`live`)}
	statuses := forwardingCallback(update)

	assert.Equal(t, rc.ApplyStateAcknowledged, statuses["path/live"].State)
	assert.Contains(t, received, "path/live")

	// The update is kept for providers that attach later.
	rcState.Lock()
	assert.Contains(t, rcState.buffered, "path/live")
	rcState.Unlock()
}

func TestForwardingCallbackFansOutToEveryProvider(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	rcState.Lock()
	rcState.subscribed = true
	rcState.Unlock()

	var first, second []string
	record := func(into *[]string, state rc.ApplyState) Callback {
		return func(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
			statuses := make(map[string]rc.ApplyStatus, len(update))
			for path := range update {
				*into = append(*into, path)
				statuses[path] = rc.ApplyStatus{State: state}
			}
			return statuses
		}
	}
	detachFirst, ok := AttachCallback(record(&first, rc.ApplyStateAcknowledged))
	require.True(t, ok)
	_, ok = AttachCallback(record(&second, rc.ApplyStateError))
	require.True(t, ok)

	statuses := forwardingCallback(remoteconfig.ProductUpdate{"path/a": []byte(`a`)})
	assert.Equal(t, []string{"path/a"}, first)
	assert.Equal(t, []string{"path/a"}, second)
	assert.Equal(t, rc.ApplyStateError, statuses["path/a"].State, "a provider's error is reported")

	detachFirst()
	detachFirst()
	forwardingCallback(remoteconfig.ProductUpdate{"path/b": []byte(`b`)})
	assert.Equal(t, []string{"path/a"}, first, "a detached provider gets no updates")
	assert.Equal(t, []string{"path/a", "path/b"}, second)

	// A provider attaching later gets only what the attached providers accepted.
	var late []string
	_, ok = AttachCallback(record(&late, rc.ApplyStateAcknowledged))
	require.True(t, ok)
	assert.Empty(t, late, "path/b was rejected, so it is not replayed")
}

func TestDetachLastCallbackUnregistersCapability(t *testing.T) {
	ResetForTest()
	defer ResetForTest()
	defer remoteconfig.Reset()
	require.NoError(t, remoteconfig.Start(remoteconfig.DefaultClientConfig()))
	require.NoError(t, SubscribeRC())

	noop := func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus { return nil }
	detachFirst, ok := AttachCallback(noop)
	require.True(t, ok)
	detachSecond, ok := AttachCallback(noop)
	require.True(t, ok)

	detachFirst()
	has, err := remoteconfig.HasCapability(remoteconfig.FFEFlagEvaluation)
	require.NoError(t, err)
	assert.True(t, has, "another provider is still attached")

	detachSecond()
	has, err = remoteconfig.HasCapability(remoteconfig.FFEFlagEvaluation)
	require.NoError(t, err)
	assert.False(t, has)

	_, ok = AttachCallback(noop)
	require.True(t, ok)
	has, err = remoteconfig.HasCapability(remoteconfig.FFEFlagEvaluation)
	require.NoError(t, err)
	assert.True(t, has, "a new provider registers the capability again")
}

func TestAttachCallbackNotSubscribed(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	_, got := AttachCallback(func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus { return nil })
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

	_, got := AttachCallback(cb)
	assert.True(t, got)
	assert.Contains(t, received, "path/buf")

	rcState.Lock()
	assert.Contains(t, rcState.buffered, "path/buf", "the update is kept for later providers")
	rcState.Unlock()
}

func TestAttachCallbackNoBuffer(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	rcState.Lock()
	rcState.subscribed = true
	rcState.Unlock()

	called := false
	_, got := AttachCallback(func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
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

// A provider created after the RC client stopped, for example after
// tracer.Stop, must subscribe again on a new client rather than trust the
// previous subscription.
func TestSubscribeProviderAfterRCStop(t *testing.T) {
	ResetForTest()
	defer ResetForTest()
	defer remoteconfig.Reset()

	require.NoError(t, SubscribeProvider())
	remoteconfig.Stop()

	require.NoError(t, SubscribeProvider())
	has, err := remoteconfig.HasProduct(FFEProductName)
	require.NoError(t, err)
	assert.True(t, has, "FFE_FLAGS should be subscribed on the new RC client")
}

// rejectingCallback acknowledges every payload except "bad" and nil deletions
// it is told to reject, as a provider rejecting an invalid configuration does.
func rejectingCallback(received *[]remoteconfig.ProductUpdate) Callback {
	return func(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
		if received != nil {
			*received = append(*received, update)
		}
		statuses := make(map[string]rc.ApplyStatus, len(update))
		for path, data := range update {
			if string(data) == "bad" {
				statuses[path] = rc.ApplyStatus{State: rc.ApplyStateError, Error: "invalid"}
				continue
			}
			statuses[path] = rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
		}
		return statuses
	}
}

// A rejected replacement must not reach providers that attach later: they get
// the configuration the attached providers kept.
func TestAttachCallbackReplaysLastAcceptedConfig(t *testing.T) {
	ResetForTest()
	defer ResetForTest()
	SetSubscribedForTest(true)

	detach, ok := AttachCallback(rejectingCallback(nil))
	require.True(t, ok)
	defer detach()

	forwardingCallback(remoteconfig.ProductUpdate{"path/1": []byte("good")})
	statuses := forwardingCallback(remoteconfig.ProductUpdate{"path/1": []byte("bad")})
	require.Equal(t, rc.ApplyStateError, statuses["path/1"].State)

	var received []remoteconfig.ProductUpdate
	detachLate, ok := AttachCallback(rejectingCallback(&received))
	require.True(t, ok)
	defer detachLate()
	require.Len(t, received, 1)
	assert.Equal(t, remoteconfig.ProductUpdate{"path/1": []byte("good")}, received[0])
}

// RC sends only changed files and nil for removed ones, so the replay buffer
// keeps unchanged paths and drops the ones RC removed.
func TestForwardingCallbackMergesUpdatesIntoBuffer(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	forwardingCallback(remoteconfig.ProductUpdate{"path/a": []byte("a1"), "path/b": []byte("b1")})
	forwardingCallback(remoteconfig.ProductUpdate{"path/a": []byte("a2")})
	assert.Equal(t, remoteconfig.ProductUpdate{"path/a": []byte("a2"), "path/b": []byte("b1")}, GetBufferedForTest())

	forwardingCallback(remoteconfig.ProductUpdate{"path/b": nil})
	assert.Equal(t, remoteconfig.ProductUpdate{"path/a": []byte("a2")}, GetBufferedForTest())

	forwardingCallback(remoteconfig.ProductUpdate{"path/a": nil})
	assert.Empty(t, GetBufferedForTest())
}
