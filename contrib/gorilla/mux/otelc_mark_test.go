// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package mux

import (
	"context"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestTraceMark(t *testing.T) {
	router, other := mux.NewRouter(), mux.NewRouter()
	assert.False(t, TraceMarked(context.Background(), router))
	ctx := traceMark(context.Background(), router)
	assert.True(t, TraceMarked(ctx, router))
	assert.False(t, TraceMarked(ctx, other), "a different router must not be marked")
	assert.False(t, TraceMarked(ctx, nil))
}
