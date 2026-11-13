// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// clusterIDTimeout bounds the cluster ID lookup.
const clusterIDTimeout = 2 * time.Second

// ClusterIDClient fetches the Kafka cluster ID. It is implemented by the
// confluent-kafka-go AdminClient.
type ClusterIDClient interface {
	ClusterID(ctx context.Context) (string, error)
	Close()
}

// ClusterIDFetch looks up the cluster ID of a client in the background and
// sets it on the Tracer. The lookup starts at most once, on the first call to
// Start, and is cancelled and waited for on Close.
//
// The client returned by newClient must reuse the connections and the
// configuration of the traced client, for example an AdminClient created with
// NewAdminClientFromConsumer or NewAdminClientFromProducer, so the lookup does
// not open a connection of its own.
type ClusterIDFetch struct {
	tr        *Tracer
	newClient func() (ClusterIDClient, error)

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
	c, err := f.newClient()
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
// is running.
func (f *ClusterIDFetch) Close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.closed = true
	stop := f.stop
	f.stop = nil
	f.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// run fetches the cluster ID with c in a goroutine. It returns a function that
// cancels the fetch and waits for the goroutine to exit.
func (f *ClusterIDFetch) run(c ClusterIDClient) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer c.Close()
		ctx, cancel := context.WithTimeout(ctx, clusterIDTimeout)
		defer cancel()
		clusterID, err := c.ClusterID(ctx)
		if err != nil {
			if ctx.Err() == context.Canceled {
				return
			}
			f.logFailure("failed to fetch Kafka cluster ID: %s", err)
			return
		}
		f.tr.SetClusterID(clusterID)
	}()
	return func() {
		cancel()
		<-done
	}
}

// logFailure logs a failed lookup. Data Streams Monitoring needs the cluster
// ID, so the failure is a warning with it, and a debug message otherwise,
// where only the span tag is missing.
func (f *ClusterIDFetch) logFailure(msg string, err error) {
	if f.tr.DSMEnabled() {
		f.tr.instr.Logger().Warn(msg, err)
	} else {
		f.tr.instr.Logger().Debug(msg, err)
	}
}
