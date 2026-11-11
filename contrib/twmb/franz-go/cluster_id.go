// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package kgo

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kmsg"
)

// clusterIDTimeout bounds the cluster ID lookup.
const clusterIDTimeout = 2 * time.Second

// clusterIDLookup looks up the Kafka cluster ID in the background. The lookup
// starts at most once, and is cancelled and waited for on close.
type clusterIDLookup struct {
	id      atomic.Pointer[string]
	started atomic.Bool // fast path for start, set under mu
	mu      sync.Mutex
	closed  bool   // +checklocks:mu
	stop    func() // +checklocks:mu
}

// get returns the cluster ID, or "" if it is not known yet.
func (l *clusterIDLookup) get() string {
	if id := l.id.Load(); id != nil {
		return *id
	}
	return ""
}

// start starts the lookup with r, unless it was already started or close was
// called. It is cheap to call on every record.
func (l *clusterIDLookup) start(r kmsg.Requestor) {
	if l.started.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.started.Load() {
		return
	}
	l.started.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), clusterIDTimeout)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		id, err := fetchClusterID(ctx, r)
		if err != nil {
			if ctx.Err() != context.Canceled {
				instr.Logger().Debug("contrib/twmb/franz-go: failed to fetch Kafka cluster ID: %s", err.Error())
			}
			return
		}
		if id != "" {
			l.id.Store(&id)
		}
	}()
	l.stop = func() {
		cancel()
		<-done
	}
}

// close prevents the lookup from starting, and cancels and waits for it if it
// is running.
func (l *clusterIDLookup) close() {
	l.mu.Lock()
	l.closed = true
	stop := l.stop
	l.stop = nil
	l.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// fetchClusterID sends a metadata request for no topics with r and returns
// the cluster ID of the response. Sent with a *kgo.Client, the request goes
// through the client's own broker connections and configuration, including
// TLS and SASL.
func fetchClusterID(ctx context.Context, r kmsg.Requestor) (string, error) {
	req := kmsg.NewPtrMetadataRequest()
	// An empty, non-nil topic list requests no topic metadata: only the
	// brokers, the controller and the cluster ID.
	req.Topics = []kmsg.MetadataRequestTopic{}
	resp, err := req.RequestWith(ctx, r)
	if err != nil {
		return "", err
	}
	if resp.ClusterID == nil {
		return "", nil
	}
	return *resp.ClusterID, nil
}
