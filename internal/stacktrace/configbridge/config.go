// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package configbridge coordinates stack-trace package initialization without
// introducing a dependency cycle between internal/config and internal/telemetry.
package configbridge

import (
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

// Config contains AppSec stack-trace package settings.
type Config struct {
	Enabled       bool
	MaxDepth      int
	TopFrameDepth int
}

type providerRegistration struct {
	id       uint64
	active   bool
	provider func() Config
}

type consumerRegistration struct {
	id       uint64
	active   bool
	consumer func(Config)
}

type bridgeState struct {
	mu        locking.Mutex
	nextID    uint64
	version   uint64
	providers []providerRegistration
	consumers []consumerRegistration
}

var bridge = bridgeState{
	providers: []providerRegistration{{
		active:   true,
		provider: defaultProvider,
	}},
}

func defaultProvider() Config {
	snapshot := bootstrap.AppSecStackTrace()
	return Config{
		Enabled:       snapshot.Enabled,
		MaxDepth:      snapshot.MaxDepth,
		TopFrameDepth: snapshot.TopFrameDepth,
	}
}

// SetProvider installs the resolver for stack-trace package settings. The
// returned function removes this registration. Restores may run out of order.
func SetProvider(provider func() Config) func() {
	if provider == nil {
		provider = defaultProvider
	}

	bridge.mu.Lock()
	id := bridge.nextRegistrationIDLocked()
	bridge.providers = append(bridge.providers, providerRegistration{
		id:       id,
		active:   true,
		provider: provider,
	})
	bridge.version++
	bridge.mu.Unlock()
	reconcile()

	var restored atomic.Bool
	return func() {
		if !restored.CompareAndSwap(false, true) {
			return
		}

		bridge.mu.Lock()
		previousID, _ := bridge.currentProviderLocked()
		for i := range bridge.providers {
			if bridge.providers[i].id == id {
				bridge.providers[i].active = false
				break
			}
		}
		bridge.compactProvidersLocked()
		currentID, _ := bridge.currentProviderLocked()
		changed := previousID != currentID
		if changed {
			bridge.version++
		}
		bridge.mu.Unlock()
		if changed {
			reconcile()
		}
	}
}

// SetConsumer installs the stack-trace package callback. The returned function
// removes this registration. Restores may run out of order.
func SetConsumer(consumer func(Config)) func() {
	bridge.mu.Lock()
	id := bridge.nextRegistrationIDLocked()
	bridge.consumers = append(bridge.consumers, consumerRegistration{
		id:       id,
		active:   true,
		consumer: consumer,
	})
	bridge.version++
	bridge.mu.Unlock()
	reconcile()

	var restored atomic.Bool
	return func() {
		if !restored.CompareAndSwap(false, true) {
			return
		}

		bridge.mu.Lock()
		previousID, _ := bridge.currentConsumerLocked()
		for i := range bridge.consumers {
			if bridge.consumers[i].id == id {
				bridge.consumers[i].active = false
				break
			}
		}
		bridge.compactConsumersLocked()
		currentID, _ := bridge.currentConsumerLocked()
		changed := previousID != currentID
		if changed {
			bridge.version++
		}
		bridge.mu.Unlock()
		if changed {
			reconcile()
		}
	}
}

// reconcile applies a snapshot only while its provider and consumer remain
// current. Provider results are discarded after a concurrent replacement, and
// a consumer that overlaps a replacement is followed by another current apply.
func reconcile() {
	for {
		bridge.mu.Lock()
		version := bridge.version
		_, provider := bridge.currentProviderLocked()
		_, consumer := bridge.currentConsumerLocked()
		bridge.mu.Unlock()

		if consumer == nil {
			return
		}
		settings := provider()

		bridge.mu.Lock()
		current := version == bridge.version
		bridge.mu.Unlock()
		if !current {
			continue
		}

		consumer(settings)

		bridge.mu.Lock()
		current = version == bridge.version
		bridge.mu.Unlock()
		if current {
			return
		}
	}
}

// +checklocks:s.mu
func (s *bridgeState) nextRegistrationIDLocked() uint64 {
	s.nextID++
	return s.nextID
}

// +checklocks:s.mu
func (s *bridgeState) currentProviderLocked() (uint64, func() Config) {
	for i := len(s.providers) - 1; i >= 0; i-- {
		if s.providers[i].active {
			return s.providers[i].id, s.providers[i].provider
		}
	}
	panic("configbridge: default provider removed")
}

// +checklocks:s.mu
func (s *bridgeState) currentConsumerLocked() (uint64, func(Config)) {
	for i := len(s.consumers) - 1; i >= 0; i-- {
		if s.consumers[i].active {
			return s.consumers[i].id, s.consumers[i].consumer
		}
	}
	return 0, nil
}

// +checklocks:s.mu
func (s *bridgeState) compactProvidersLocked() {
	for len(s.providers) > 1 && !s.providers[len(s.providers)-1].active {
		s.providers = s.providers[:len(s.providers)-1]
	}
}

// +checklocks:s.mu
func (s *bridgeState) compactConsumersLocked() {
	for len(s.consumers) > 0 && !s.consumers[len(s.consumers)-1].active {
		s.consumers = s.consumers[:len(s.consumers)-1]
	}
}
