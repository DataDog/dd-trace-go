// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActiveLLMSpanFromContextFlag(t *testing.T) {
	// The flag is global and sticky; reset it here and restore it afterward
	// so test order does not matter.
	prev := activeLLMSpanUsed.Load()
	activeLLMSpanUsed.Store(false)
	t.Cleanup(func() { activeLLMSpanUsed.Store(prev) })

	span := &Span{}
	// A value stored without contextWithActiveLLMSpan is not seen while the flag is off.
	raw := context.WithValue(context.Background(), ctxKeyActiveLLMSpan{}, span)
	_, ok := ActiveLLMSpanFromContext(raw)
	assert.False(t, ok)

	ctx := contextWithActiveLLMSpan(context.Background(), span)
	got, ok := ActiveLLMSpanFromContext(ctx)
	assert.True(t, ok)
	assert.Same(t, span, got)
	_, ok = ActiveLLMSpanFromContext(context.Background())
	assert.False(t, ok)
}
