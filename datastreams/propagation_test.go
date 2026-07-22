// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/datastreams"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type carrier map[string]string

func (c carrier) Set(key, val string) {
	c[key] = val
}

func (c carrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func TestBase64Propagation(t *testing.T) {
	c := make(carrier)
	mt := mocktracer.Start()
	defer mt.Stop()
	ctx := context.Background()
	ctx, _ = tracer.SetDataStreamsCheckpoint(ctx, "direction:out", "type:kafka", "topic:topic1")
	InjectToBase64Carrier(ctx, c)
	got, _ := datastreams.PathwayFromContext(ExtractFromBase64Carrier(context.Background(), c))
	expected, _ := datastreams.PathwayFromContext(ctx)
	assert.Equal(t, expected.GetHash(), got.GetHash())
	assert.NotEqual(t, 0, expected.GetHash())
}

type kv struct{ k, v string }

// slowCarrier implements only ForeachKey, so ExtractFromBase64Carrier uses the
// fallback path. It preserves order and duplicate keys (unlike a map carrier).
type slowCarrier struct{ hs []kv }

func (c *slowCarrier) Set(key, val string) { c.hs = append(c.hs, kv{key, val}) }
func (c *slowCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, e := range c.hs {
		if err := handler(e.k, e.v); err != nil {
			return err
		}
	}
	return nil
}

// fastCarrier also implements TextMapReaderByKey, so extraction uses the fast path.
type fastCarrier struct{ slowCarrier }

func (c *fastCarrier) Get(key string) (val string, ok bool) {
	for _, e := range c.hs {
		if e.k == key {
			val, ok = e.v, true // last occurrence wins, matching ForeachKey
		}
	}
	return val, ok
}

func inject(t *testing.T, ctx context.Context) string {
	t.Helper()
	w := &slowCarrier{}
	InjectToBase64Carrier(ctx, w)
	require.Len(t, w.hs, 1)
	return w.hs[0].v
}

// The fast path (Get) and the fallback path (ForeachKey) must extract the same
// pathway from the same headers.
func TestExtractFastPathMatchesFallback(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:kafka", "topic:topic1")
	pathwayVal := inject(t, ctx)
	hs := []kv{{"other-header", "x"}, {datastreams.PropagationKeyBase64, pathwayVal}, {"another", "y"}}

	slow := &slowCarrier{hs: hs}
	fast := &fastCarrier{slowCarrier{hs: hs}}
	_, slowIsByKey := any(slow).(TextMapReaderByKey)
	_, fastIsByKey := any(fast).(TextMapReaderByKey)
	require.False(t, slowIsByKey, "slowCarrier must exercise the fallback path")
	require.True(t, fastIsByKey, "fastCarrier must exercise the fast path")

	expected, _ := datastreams.PathwayFromContext(ctx)
	gotSlow, _ := datastreams.PathwayFromContext(ExtractFromBase64Carrier(context.Background(), slow))
	gotFast, _ := datastreams.PathwayFromContext(ExtractFromBase64Carrier(context.Background(), fast))
	assert.NotEqual(t, uint64(0), expected.GetHash())
	assert.Equal(t, expected.GetHash(), gotSlow.GetHash())
	assert.Equal(t, expected.GetHash(), gotFast.GetHash())
}

// When the pathway header appears more than once, both paths must agree on which
// one wins (the last), so extraction is deterministic regardless of carrier type.
func TestExtractDuplicatePathwayHeaderLastWins(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	ctxA, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:kafka", "topic:a")
	ctxB, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "type:kafka", "topic:b")
	valA, valB := inject(t, ctxA), inject(t, ctxB)
	hs := []kv{{datastreams.PropagationKeyBase64, valA}, {datastreams.PropagationKeyBase64, valB}} // B is last

	expectedB, _ := datastreams.PathwayFromContext(ctxB)
	gotSlow, _ := datastreams.PathwayFromContext(ExtractFromBase64Carrier(context.Background(), &slowCarrier{hs: hs}))
	gotFast, _ := datastreams.PathwayFromContext(ExtractFromBase64Carrier(context.Background(), &fastCarrier{slowCarrier{hs: hs}}))
	assert.NotEqual(t, expectedB.GetHash(), datastreams.Pathway{}.GetHash())
	assert.Equal(t, expectedB.GetHash(), gotSlow.GetHash(), "fallback path: last duplicate wins")
	assert.Equal(t, expectedB.GetHash(), gotFast.GetHash(), "fast path: last duplicate wins")
}
