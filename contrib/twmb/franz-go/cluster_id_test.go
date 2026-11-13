// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package kgo

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// fakeRequestor answers metadata requests with clusterID, or blocks until the
// context is done when block is set.
type fakeRequestor struct {
	clusterID *string
	err       error
	block     bool
	requests  atomic.Int32
	lastReq   atomic.Pointer[kmsg.MetadataRequest]
}

func (r *fakeRequestor) Request(ctx context.Context, req kmsg.Request) (kmsg.Response, error) {
	r.requests.Add(1)
	r.lastReq.Store(req.(*kmsg.MetadataRequest))
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	resp := kmsg.NewPtrMetadataResponse()
	resp.ClusterID = r.clusterID
	return resp, nil
}

func TestFetchClusterID(t *testing.T) {
	id := "test-cluster-id"
	for _, tc := range []struct {
		name    string
		r       *fakeRequestor
		want    string
		wantErr bool
	}{
		{name: "cluster ID", r: &fakeRequestor{clusterID: &id}, want: id},
		{name: "no cluster ID", r: &fakeRequestor{}},
		{name: "error", r: &fakeRequestor{err: errors.New("broker down")}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fetchClusterID(context.Background(), tc.r)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, got)
			req := tc.r.lastReq.Load()
			require.NotNil(t, req)
			assert.NotNil(t, req.Topics, "a nil topic list requests all topics")
			assert.Empty(t, req.Topics)
		})
	}
}

func TestClusterIDLookup(t *testing.T) {
	id := "test-cluster-id"
	t.Run("starts once", func(t *testing.T) {
		r := &fakeRequestor{clusterID: &id}
		var l clusterIDLookup
		l.start(r)
		l.start(r)
		require.Eventually(t, func() bool { return l.get() == id }, time.Second, time.Millisecond)
		l.close()
		assert.Equal(t, int32(1), r.requests.Load())
	})
	t.Run("close before start", func(t *testing.T) {
		r := &fakeRequestor{clusterID: &id}
		var l clusterIDLookup
		l.close()
		l.start(r)
		assert.Equal(t, int32(0), r.requests.Load())
		assert.Empty(t, l.get())
	})
	t.Run("close cancels the lookup", func(t *testing.T) {
		r := &fakeRequestor{block: true}
		var l clusterIDLookup
		l.start(r)
		require.Eventually(t, func() bool { return r.requests.Load() == 1 }, time.Second, time.Millisecond)
		start := time.Now()
		l.close()
		assert.Less(t, time.Since(start), clusterIDTimeout)
		assert.Empty(t, l.get())
	})
}

func TestClusterIDNotStartedWithoutRecords(t *testing.T) {
	h := newTracingHook()
	cl := newOfflineClient(t, h)
	cl.Close()
	assert.False(t, h.clusterID.started.Load())
}

func TestClusterIDTag(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	ph := newTracingHook()
	producerCl, err := kgo.NewClient(kgo.SeedBrokers(kafkaBrokers...), kgo.WithHooks(ph))
	require.NoError(t, err)
	defer producerCl.Close()
	assert.False(t, ph.clusterID.started.Load(), "the lookup must not start before the first record")

	ctx := context.Background()
	for i, key := range []string{"key1", "key2"} {
		err = producerCl.ProduceSync(ctx, &kgo.Record{Topic: topic, Key: []byte(key)}).FirstErr()
		require.NoError(t, err)
		if i == 0 {
			// The first record starts the lookup.
			require.Eventually(t, func() bool { return ph.clusterID.get() != "" }, 5*time.Second, 10*time.Millisecond)
		}
	}
	clusterID := ph.clusterID.get()

	ch := newTracingHook()
	consumerCl, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaBrokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.WithHooks(ch),
	)
	require.NoError(t, err)
	var consumed int
	for consumed < 2 {
		fetches := consumerCl.PollRecords(ctx, 1)
		require.NoError(t, fetches.Err())
		consumed += len(fetches.Records())
		if consumed == 1 {
			require.Eventually(t, func() bool { return ch.clusterID.get() != "" }, 5*time.Second, 10*time.Millisecond)
		}
	}
	consumerCl.Close()
	assert.Equal(t, clusterID, ch.clusterID.get())

	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)
	tagged := map[string]int{}
	for _, s := range spans {
		if s.Tag(ext.MessagingKafkaClusterID) == clusterID {
			tagged[s.OperationName()]++
		}
	}
	// The records after the first one are produced and consumed once the
	// cluster ID is known.
	assert.GreaterOrEqual(t, tagged["kafka.produce"], 1)
	assert.GreaterOrEqual(t, tagged["kafka.consume"], 1)
}
