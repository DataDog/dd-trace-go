// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package kafka

import (
	"context"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
)

func TestTraceMark(t *testing.T) {
	r, w := &kafka.Reader{}, &kafka.Writer{}

	t.Run("plain build", func(t *testing.T) {
		ctx := context.Background()
		assert.Equal(t, ctx, traceMark(ctx, r), "plain builds must not mark")
		assert.False(t, TraceMarked(traceMark(ctx, r), r))
	})

	t.Run("otelc build", func(t *testing.T) {
		orig := otelcEnabled
		otelcEnabled = func() bool { return true }
		defer func() { otelcEnabled = orig }()

		ctx := traceMark(context.Background(), r)
		assert.True(t, TraceMarked(ctx, r))
		assert.False(t, TraceMarked(ctx, &kafka.Reader{}), "the mark only covers its own reader")
		assert.False(t, TraceMarked(ctx, w))
		assert.True(t, TraceMarked(traceMark(context.Background(), w), w))
		//nolint:staticcheck // A nil context must not panic.
		assert.False(t, TraceMarked(nil, r))
	})
}
