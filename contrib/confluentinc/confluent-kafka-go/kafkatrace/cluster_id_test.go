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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClusterIDClient returns id, or blocks until the context is done when
// block is set, or until release is closed when release is set, ignoring the
// context like a lookup that can't be cancelled. Its first fail attempts fail;
// fail < 0 fails them all.
type fakeClusterIDClient struct {
	id       string
	block    bool
	release  chan struct{}
	fail     int32
	attempts atomic.Int32
	closed   atomic.Bool
}

func (c *fakeClusterIDClient) ClusterID(ctx context.Context) (string, error) {
	if n := c.attempts.Add(1); c.fail < 0 || n <= c.fail {
		return "", errors.New("brokers not reachable")
	}
	if c.release != nil {
		<-c.release
		return "", ctx.Err()
	}
	if c.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return c.id, nil
}

func (c *fakeClusterIDClient) Close() { c.closed.Store(true) }

func TestClusterIDFetch(t *testing.T) {
	newTracer := func() *Tracer { return NewKafkaTracer(Package(CKGoVersion2), CKGoVersion2, 0) }

	t.Run("starts once", func(t *testing.T) {
		tr := newTracer()
		client := &fakeClusterIDClient{id: "test-cluster-id"}
		var created atomic.Int32
		f := NewClusterIDFetch(tr, func() (ClusterIDClient, error) {
			created.Add(1)
			return client, nil
		})
		assert.False(t, f.Started())

		f.Start()
		f.Start()
		assert.True(t, f.Started())
		require.Eventually(t, func() bool { return tr.ClusterID() == "test-cluster-id" }, time.Second, time.Millisecond)
		f.Close()
		assert.Equal(t, int32(1), created.Load())
		assert.True(t, client.closed.Load())
	})
	t.Run("close before start", func(t *testing.T) {
		tr := newTracer()
		f := NewClusterIDFetch(tr, func() (ClusterIDClient, error) {
			t.Error("the client must not be created after Close")
			return nil, errors.New("unexpected")
		})
		f.Close()
		f.Start()
		assert.False(t, f.Started())
		assert.Empty(t, tr.ClusterID())
	})
	t.Run("close cancels the lookup", func(t *testing.T) {
		tr := newTracer()
		client := &fakeClusterIDClient{block: true}
		f := NewClusterIDFetch(tr, func() (ClusterIDClient, error) { return client, nil })
		f.Start()

		start := time.Now()
		f.Close()
		assert.Less(t, time.Since(start), clusterIDTimeout)
		assert.True(t, client.closed.Load())
		assert.Empty(t, tr.ClusterID())
	})
	t.Run("concurrent closes wait for the lookup", func(t *testing.T) {
		tr := newTracer()
		client := &fakeClusterIDClient{release: make(chan struct{})}
		f := NewClusterIDFetch(tr, func() (ClusterIDClient, error) { return client, nil })
		f.Start()

		var returned atomic.Int32
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				f.Close()
				// The lookup must be over before any Close returns.
				assert.True(t, client.closed.Load())
				returned.Add(1)
			})
		}
		time.Sleep(50 * time.Millisecond)
		assert.Zero(t, returned.Load(), "Close returned while the lookup was running")
		close(client.release)
		wg.Wait()
		assert.Equal(t, int32(2), returned.Load())
	})
	t.Run("retries until the lookup succeeds", func(t *testing.T) {
		setClusterIDRetry(t, time.Millisecond, 4*time.Millisecond)
		tr := newTracer()
		client := &fakeClusterIDClient{id: "test-cluster-id", fail: 3}
		f := NewClusterIDFetch(tr, func() (ClusterIDClient, error) { return client, nil })
		f.Start()
		require.Eventually(t, func() bool { return tr.ClusterID() == "test-cluster-id" }, time.Second, time.Millisecond)
		f.Close()
		assert.Equal(t, int32(4), client.attempts.Load())
		assert.True(t, client.closed.Load())
	})
	t.Run("close stops the retries", func(t *testing.T) {
		setClusterIDRetry(t, time.Hour, time.Hour)
		tr := newTracer()
		client := &fakeClusterIDClient{fail: -1}
		f := NewClusterIDFetch(tr, func() (ClusterIDClient, error) { return client, nil })
		f.Start()
		require.Eventually(t, func() bool { return client.attempts.Load() == 1 }, time.Second, time.Millisecond)

		start := time.Now()
		f.Close() // during the wait before the next attempt
		assert.Less(t, time.Since(start), time.Second)
		assert.Equal(t, int32(1), client.attempts.Load())
		assert.True(t, client.closed.Load())
		assert.Empty(t, tr.ClusterID())
	})
	t.Run("client creation fails", func(t *testing.T) {
		tr := newTracer()
		f := NewClusterIDFetch(tr, func() (ClusterIDClient, error) { return nil, errors.New("closed client") })
		f.Start()
		assert.True(t, f.Started())
		f.Close()
		assert.Empty(t, tr.ClusterID())
	})
	t.Run("nil fetch", func(t *testing.T) {
		var f *ClusterIDFetch
		f.Start()
		f.Close()
		assert.False(t, f.Started())
	})
}

// setClusterIDRetry sets the retry backoff for the duration of the test.
func setClusterIDRetry(t *testing.T, minWait, maxWait time.Duration) {
	oldMin, oldMax := clusterIDRetryMin, clusterIDRetryMax
	clusterIDRetryMin, clusterIDRetryMax = minWait, maxWait
	t.Cleanup(func() { clusterIDRetryMin, clusterIDRetryMax = oldMin, oldMax })
}
