// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafkaclusterid provides a shared async cluster ID fetcher and cache
// for Kafka integrations.
package kafkaclusterid

import (
	"context"
	"sync"
)

// Fetcher asynchronously fetches a Kafka cluster ID in a background goroutine.
// It is safe for concurrent use. On Close, any in-flight fetch is cancelled and
// the goroutine is allowed to clean up before returning.
type Fetcher struct {
	id     string
	mu     sync.RWMutex
	ready  chan struct{}
	cancel context.CancelFunc
}

// ID returns the current cluster ID, or "" if not yet available.
func (f *Fetcher) ID() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.id
}

// SetID sets the cluster ID. This is safe for concurrent use.
func (f *Fetcher) SetID(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.id = id
}

// FetchAsync launches a background goroutine that calls fetchFn with a
// cancellable context. If fetchFn returns a non-empty string, it is stored
// as the cluster ID. Use Stop to cancel any in-flight fetch.
func (f *Fetcher) FetchAsync(fetchFn func(ctx context.Context) string) {
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.ready = make(chan struct{})
	go func() {
		defer close(f.ready)
		if id := fetchFn(ctx); id != "" {
			f.SetID(id)
		}
	}()
}

// Stop cancels any in-flight fetch and waits for the goroutine to finish
// cleanup. This is non-blocking in the common case where the fetch has already
// completed, and near-instant when cancelling an in-flight network call.
func (f *Fetcher) Stop() {
	if f.cancel != nil {
		f.cancel()
	}
	if f.ready != nil {
		<-f.ready
	}
}

// Wait blocks until any in-flight fetch completes. Use this in tests to ensure
// the cluster ID is available before asserting.
func (f *Fetcher) Wait() {
	if f.ready != nil {
		<-f.ready
	}
}
