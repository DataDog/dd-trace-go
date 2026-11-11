// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClusterIDClient returns id, or blocks until the context is done when
// block is set.
type fakeClusterIDClient struct {
	id     string
	block  bool
	closed atomic.Bool
}

func (c *fakeClusterIDClient) ClusterID(ctx context.Context) (string, error) {
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
