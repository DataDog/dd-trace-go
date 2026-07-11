// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"runtime"
	"sync/atomic"
	"testing"
)

var orchestrionRetryProcessHybridParentRuns atomic.Int32
var orchestrionRetryProcessPureParentRuns atomic.Int32

func TestOrchestrionRetryProcessSelectedChild(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessInvalidConfigEnv) == "true" {
		t.Fatal("selected test ran despite invalid process retry child config")
	}
	if !orchestrionRetryProcessChild() {
		t.Skip("selected child fixture runs only in process retry child mode")
	}
}

func TestOrchestrionRetryProcessUnselectedChild(t *testing.T) {
	if orchestrionRetryProcessChild() {
		t.Fatal("unselected test ran in process retry child mode")
	}
}

func TestOrchestrionRetryProcessErrorChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("error child fixture runs only in process retry child mode")
	}
	t.Error("orchestrion error sentinel")
}

func TestOrchestrionRetryProcessSkipChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("skip child fixture runs only in process retry child mode")
	}
	t.Skip("orchestrion skip sentinel")
}

func TestOrchestrionRetryProcessSubtestThenTopLevelSkipChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("subtest/top-level skip child fixture runs only in process retry child mode")
	}
	t.Run("subtest", func(t *testing.T) {
		t.Skip("orchestrion subtest skip sentinel")
	})
	t.Skip("orchestrion top-level skip sentinel")
}

func TestOrchestrionRetryProcessSubtestErrorChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("subtest error child fixture runs only in process retry child mode")
	}
	t.Run("subtest", func(t *testing.T) {
		t.Error("orchestrion subtest error sentinel")
	})
}

func TestOrchestrionRetryProcessSubtestPanicChild(t *testing.T) {
	if !orchestrionRetryProcessChild() {
		t.Skip("subtest panic child fixture runs only in process retry child mode")
	}
	t.Run("subtest", func(*testing.T) {
		panic("orchestrion subtest panic sentinel")
	})
}

func TestOrchestrionRetryProcessHybridParentFixture(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessHybridParentEnv) != "true" && !orchestrionRetryProcessChild() {
		t.Skip("hybrid parent fixture runs only from its controller subprocess")
	}
	if orchestrionRetryProcessChild() {
		if orchestrionRetryProcessHybridParentRuns.Load() != 0 {
			t.Fatalf("hybrid child inherited parent run count: %d", orchestrionRetryProcessHybridParentRuns.Load())
		}
		return
	}
	if orchestrionRetryProcessHybridParentRuns.Add(1) == 1 {
		t.Fatal("first hybrid parent execution must fail to trigger process retry")
	}
	t.Fatalf("hybrid retry ran in the parent process with run count %d", orchestrionRetryProcessHybridParentRuns.Load())
}

func TestOrchestrionRetryProcessPureParentFixture(t *testing.T) {
	if orchestrionRetryProcessEnv(orchestrionRetryProcessPureParentEnv) != "true" {
		t.Skip("pure parent fixture runs only from its controller subprocess")
	}
	if orchestrionRetryProcessChild() {
		t.Fatal("pure Orchestrion parent unexpectedly launched a process retry child")
	}
	if orchestrionRetryProcessPureParentRuns.Add(1) == 1 {
		t.Fail()
		runtime.Goexit()
	}
}
