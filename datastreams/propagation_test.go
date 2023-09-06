// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"

	"github.com/stretchr/testify/assert"
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
