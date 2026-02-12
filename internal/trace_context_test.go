// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

func TestTraceTaskEndContext(t *testing.T) {
	if IsExecutionTraced(context.Background()) {
		t.Fatal("background context incorrectly marked as execution traced")
	}
	ctx := WithExecutionTraced(context.Background())
	if !IsExecutionTraced(ctx) {
		t.Fatal("context not marked as execution traced")
	}
	ctx = WithExecutionNotTraced(ctx)
	if IsExecutionTraced(ctx) {
		t.Fatal("context incorrectly marked as execution traced")
	}
}

func TestWithExecutionTracedGLSCleanup(t *testing.T) {
	t.Cleanup(orchestrion.MockGLS())

	ctx := WithExecutionTraced(context.Background())
	if got := IsExecutionTraced(ctx); got != true {
		t.Fatalf("IsExecutionTraced(WithExecutionTraced(ctx)) = %v, want true", got)
	}

	PopExecutionTraced()

	// After pop, the GLS stack no longer has the value, but the context
	// still holds it via context.WithValue. We check GLS via a fresh
	// WrapContext on a bare context to verify GLS cleanup.
	if got := IsExecutionTraced(orchestrion.WrapContext(nil)); got != false {
		t.Fatalf("IsExecutionTraced(WrapContext(nil)) after PopExecutionTraced() = %v, want false", got)
	}
}
