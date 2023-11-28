// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"context"
	"testing"
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
