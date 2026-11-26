// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// clusterIDTimeout bounds each attempt to look up the cluster ID.
const clusterIDTimeout = 2 * time.Second

// After a failed attempt, the lookup waits clusterIDRetryMin before it tries
// again, doubling the wait up to clusterIDRetryMax. It then keeps trying every
// clusterIDRetryMax until it succeeds or the client is closed.
const (
	clusterIDRetryMin = time.Second
	clusterIDRetryMax = time.Minute
)

// retryBounds returns minWait and maxWait, or the defaults where they are zero.
func retryBounds(minWait, maxWait time.Duration) (time.Duration, time.Duration) {
	if minWait == 0 {
		minWait = clusterIDRetryMin
	}
	if maxWait == 0 {
		maxWait = clusterIDRetryMax
	}
	return minWait, maxWait
}

// ErrClientClosed is returned by a ClusterIDClient whose traced client was
// closed without the wrapper's Close, for example by calling Close on the
// embedded confluent-kafka-go client. The lookup then stops, because the
// native handle it uses is destroyed.
var ErrClientClosed = errors.New("kafka client is closed")

// ClusterIDClient fetches the Kafka cluster ID. It is implemented by the
// confluent-kafka-go AdminClient.
type ClusterIDClient interface {
	ClusterID(ctx context.Context) (string, error)
	Close()
}

// ClusterIDFetch looks up the cluster ID of a client in the background and
// sets it on the Tracer. The lookup starts at most once, on the first call to
// Start, retries with backoff until it succeeds, and is cancelled and waited
// for on Close.
//
// The client returned by newClient must reuse the connections and the
// configuration of the traced client, for example an AdminClient created with
// NewAdminClientFromConsumer or NewAdminClientFromProducer, so the lookup does
// not open a connection of its own.
type ClusterIDFetch struct {
	tr *Tracer
	// newClient is dropped once it was called or Close was called, so the
	// fetch does not keep the traced client alive. Read and written under mu
	// after construction.
	newClient func() (ClusterIDClient, error)

	// Waits between attempts; zero means clusterIDRetryMin and clusterIDRetryMax. Set by tests.
	retryMin, retryMax time.Duration

	started atomic.Bool // fast path for Start, set under mu
	mu      sync.Mutex
	closed  bool   // +checklocks:mu
	stop    func() // +checklocks:mu
}

// NewClusterIDFetch returns a ClusterIDFetch that sets the cluster ID on tr.
func NewClusterIDFetch(tr *Tracer, newClient func() (ClusterIDClient, error)) *ClusterIDFetch {
	return &ClusterIDFetch{tr: tr, newClient: newClient}
}

// Start starts the lookup, unless it was already started or Close was called.
// It is cheap to call on every message.
func (f *ClusterIDFetch) Start() {
	if f == nil || f.started.Load() {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.started.Load() {
		return
	}
	f.started.Store(true)
	newClient := f.newClient
	f.newClient = nil
	c, err := newClient()
	if err != nil {
		f.logFailure("failed to create admin client for cluster ID, not adding cluster_id tags: %s", err)
		return
	}
	f.stop = f.run(c)
}

// Started reports whether the lookup was started.
func (f *ClusterIDFetch) Started() bool {
	return f != nil && f.started.Load()
}

// Close prevents the lookup from starting, and cancels and waits for it if it
// is running. Every caller waits, also when several close concurrently: the
// lookup uses the traced client's native handle, which the caller destroys
// after Close returns.
func (f *ClusterIDFetch) Close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.closed = true
	f.newClient = nil
	stop := f.stop // kept: stop can be called more than once
	f.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// run fetches the cluster ID with c in a goroutine, trying again with backoff
// after a failure, such as brokers that can't be reached yet. It returns a
// function that cancels the fetch and waits for the goroutine to exit.
func (f *ClusterIDFetch) run(c ClusterIDClient) func() {
	minWait, maxWait := retryBounds(f.retryMin, f.retryMax)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer c.Close()
		logged := false
		for wait := minWait; ; wait = min(2*wait, maxWait) {
			clusterID, err := fetchClusterID(ctx, c)
			if err == nil {
				f.tr.SetClusterID(clusterID)
				clusterIDsFound.Add(1)
				return
			}
			if ctx.Err() != nil || errors.Is(err, ErrClientClosed) {
				return
			}
			if !logged {
				// Only the first failure, so that brokers that stay down don't flood the log
				f.logFailure("failed to fetch Kafka cluster ID, retrying: %s", err)
				logged = true
			}
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// clusterIDsFound counts the lookups that found a cluster ID.
var clusterIDsFound atomic.Int64

// ClusterIDsFound returns how many cluster ID lookups of this process have
// found a cluster ID. It is meant for tests, which can wait for it to grow
// after the first message of a client instead of sleeping.
func ClusterIDsFound() int64 {
	return clusterIDsFound.Load()
}

// fetchClusterID makes one attempt, bounded by clusterIDTimeout.
func fetchClusterID(ctx context.Context, c ClusterIDClient) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, clusterIDTimeout)
	defer cancel()
	return c.ClusterID(ctx)
}

// logFailure logs a failed lookup as a warning, so a user can tell why the
// cluster ID tag is missing. A lookup logs at most one failure.
func (f *ClusterIDFetch) logFailure(msg string, err error) {
	f.tr.instr.Logger().Warn(msg, err)
}
