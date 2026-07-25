// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package configbridge

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

type configRecorder struct {
	mu     locking.Mutex
	values []Config
}

func (r *configRecorder) record(config Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, config)
}

func (r *configRecorder) last(t *testing.T) Config {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.values)
	return r.values[len(r.values)-1]
}

func TestOutOfOrderProviderRestoresDoNotResurrectTemporaryProvider(t *testing.T) {
	base := Config{Enabled: true, MaxDepth: 32, TopFrameDepth: 8}
	first := Config{Enabled: true, MaxDepth: 48, TopFrameDepth: 12}
	second := Config{Enabled: false, MaxDepth: 64, TopFrameDepth: 16}
	restoreBase := SetProvider(func() Config { return base })
	t.Cleanup(restoreBase)
	recorder := new(configRecorder)
	restoreConsumer := SetConsumer(recorder.record)
	t.Cleanup(restoreConsumer)

	restoreFirst := SetProvider(func() Config { return first })
	restoreSecond := SetProvider(func() Config { return second })
	restoreFirst()
	restoreSecond()

	require.Equal(t, base, recorder.last(t))
}

func TestOutOfOrderConsumerRestoresDoNotResurrectTemporaryConsumer(t *testing.T) {
	firstConfig := Config{Enabled: true, MaxDepth: 32, TopFrameDepth: 8}
	secondConfig := Config{Enabled: false, MaxDepth: 64, TopFrameDepth: 16}
	restoreProvider := SetProvider(func() Config { return firstConfig })
	t.Cleanup(restoreProvider)

	base := new(configRecorder)
	restoreBase := SetConsumer(base.record)
	t.Cleanup(restoreBase)
	var firstCalls atomic.Int64
	restoreFirst := SetConsumer(func(Config) { firstCalls.Add(1) })
	restoreSecond := SetConsumer(func(Config) {})
	restoreFirst()
	restoreSecond()
	callsBeforeChange := firstCalls.Load()

	restoreChange := SetProvider(func() Config { return secondConfig })
	t.Cleanup(restoreChange)

	require.Equal(t, callsBeforeChange, firstCalls.Load())
	require.Equal(t, secondConfig, base.last(t))
}

func TestSlowProviderCannotApplyStaleConfigAfterReplacement(t *testing.T) {
	base := Config{Enabled: true, MaxDepth: 32, TopFrameDepth: 8}
	stale := Config{Enabled: true, MaxDepth: 48, TopFrameDepth: 12}
	current := Config{Enabled: false, MaxDepth: 64, TopFrameDepth: 16}
	restoreBase := SetProvider(func() Config { return base })
	t.Cleanup(restoreBase)
	recorder := new(configRecorder)
	restoreConsumer := SetConsumer(recorder.record)
	t.Cleanup(restoreConsumer)

	entered := make(chan struct{})
	release := make(chan struct{})
	restoreSlow := make(chan func(), 1)
	var enteredOnce sync.Once
	go func() {
		restoreSlow <- SetProvider(func() Config {
			enteredOnce.Do(func() { close(entered) })
			<-release
			return stale
		})
	}()
	awaitBridgeSignal(t, entered)

	restoreCurrent := SetProvider(func() Config { return current })
	close(release)
	slowRestore := <-restoreSlow
	t.Cleanup(func() {
		slowRestore()
		restoreCurrent()
	})

	require.Equal(t, current, recorder.last(t))
}

func TestSlowConsumerSelfHealsAfterProviderReplacement(t *testing.T) {
	stale := Config{Enabled: true, MaxDepth: 32, TopFrameDepth: 8}
	current := Config{Enabled: false, MaxDepth: 64, TopFrameDepth: 16}
	restoreBase := SetProvider(func() Config { return stale })
	t.Cleanup(restoreBase)

	recorder := new(configRecorder)
	entered := make(chan struct{})
	release := make(chan struct{})
	var first atomic.Bool
	restoreSlow := make(chan func(), 1)
	go func() {
		restoreSlow <- SetConsumer(func(config Config) {
			if first.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
			recorder.record(config)
		})
	}()
	awaitBridgeSignal(t, entered)

	restoreCurrent := SetProvider(func() Config { return current })
	close(release)
	slowRestore := <-restoreSlow
	t.Cleanup(func() {
		slowRestore()
		restoreCurrent()
	})

	require.Equal(t, current, recorder.last(t))
}

func awaitBridgeSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bridge callback")
	}
}
