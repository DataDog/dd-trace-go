// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package proxy

import (
	"context"
	"sync"
	"testing"
)

func TestCloseAfterCloseBeforeResponseDoesNotFinalizeResponse(t *testing.T) {
	finalized := 0
	state := RequestState{
		Mu:      new(sync.Mutex),
		Context: context.Background(),
		State:   MessageTypeRequestHeaders,
		afterHandle: func() {
			finalized++
		},
	}

	state.CloseBeforeResponse()
	if err := state.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if finalized != 0 {
		t.Fatalf("afterHandle called %d times, want 0", finalized)
	}
}
